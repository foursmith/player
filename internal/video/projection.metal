#include <metal_stdlib>
using namespace metal;

struct Params {
    float yaw, pitch, zoom, forward;
    int mode, split, portrait, rgba;
    float aspect, width, height, fov;
    float kr, kb, yoffset, yscale, uvoffset, uvscale;
    float flat_x, flat_y, flat_zoom;
};

struct Vertex {
    float4 position [[position]];
    float2 uv;
};

vertex Vertex vertexMain(uint id [[vertex_id]]) {
    float2 uv = float2((id << 1) & 2, id & 2);
    return {float4(uv * 2 - 1, 0, 1), float2(uv.x, 1 - uv.y)};
}

float3 rotateRay(float3 p, float yaw, float pitch) {
    float sy = sin(yaw), cy = cos(yaw), sx = sin(pitch), cx = cos(pitch);
    float y = cx * p.y - sx * p.z, z = sx * p.y + cx * p.z;
    return float3(cy * p.x + sy * z, y, -sy * p.x + cy * z);
}

fragment float4 fragmentMain(
    Vertex in [[stage_in]], constant Params &p [[buffer(0)]],
    texture2d<float> ytex [[texture(0)]], texture2d<float> uvtex [[texture(1)]]) {
    constexpr sampler s(coord::normalized, address::clamp_to_edge, filter::linear);
    float2 outSize = float2(p.width, p.height);
    float2 source;
    bool visible = true;
    if (p.mode == 6) {
        float ratio = (outSize.x / outSize.y) / p.aspect;
        source = (in.uv - .5) * float2(max(1.0, ratio), max(1.0, 1 / ratio)) /
                 p.flat_zoom + .5 - float2(p.flat_x, p.flat_y);
        visible = all(source >= 0) && all(source <= 1);
        if (!visible) return float4(0, 0, 0, 1);
    } else {
        float count = outSize.x > outSize.y
            ? min(float(p.split), max(1.0, round(outSize.x /
                (outSize.y * (p.portrait != 0 ? 9.0 / 16.0 : 1.0))))) : 1;
        float2 uv = float2(fract(in.uv.x * count), in.uv.y);
        float aspect = outSize.x / count / outSize.y;
        float t = tan(p.fov * .01745329252 / 2) / max(.01, p.zoom);
        float yaw = p.yaw * .01745329252, pitch = p.pitch * .01745329252;
        float3 ray = rotateRay(normalize(float3((uv.x * 2 - 1) * t * aspect,
                                              (1 - uv.y * 2) * t, -1)), yaw, pitch);
        float3 origin = rotateRay(float3(0, 0, -p.forward), yaw, pitch);
        float b = dot(origin, ray), disc = b * b - dot(origin, origin) + 10000;
        float hit = -b + sqrt(max(0.0, disc));
        float3 q = (origin + ray * hit) / 100;
        visible = disc >= 0 && hit > 0;
        bool isHalf = p.mode == 0 || p.mode == 1 || p.mode == 3 || p.mode == 4;
        if (isHalf && q.z > 0) visible = false;
        if (p.mode == 1 || p.mode == 4) {
            float r = 1 - acos(clamp(q.z, -1.0, 1.0)) / M_PI_F;
            float phi = atan2(q.y, q.x);
            source = float2(.5 + r * cos(phi), .5 - r * sin(phi));
            if (p.mode == 1) source.x *= .5;
        } else {
            float a = atan2(-q.x, -q.z);
            source = float2(.5 - a / (2 * M_PI_F), acos(clamp(q.y, -1.0, 1.0)) / M_PI_F);
            if (p.mode == 0) source.x = .25 - a / (2 * M_PI_F);
            else if (p.mode == 3) source.x = .5 - a / M_PI_F;
            else if (p.mode == 5) source.y *= .5;
        }
    }
    if (!visible) return float4(.0784314, .0705882, .0588235, 1);
    if (p.rgba != 0) return float4(ytex.sample(s, source).rgb, 1);
    float y = (ytex.sample(s, source).r - p.yoffset) * p.yscale;
    float2 cbcr = (uvtex.sample(s, source).rg - float2(p.uvoffset)) * p.uvscale;
    float kg = 1 - p.kr - p.kb;
    float3 rgb = float3(y + 2 * (1 - p.kr) * cbcr.y,
                       y - 2 * p.kb * (1 - p.kb) / kg * cbcr.x -
                           2 * p.kr * (1 - p.kr) / kg * cbcr.y,
                       y + 2 * (1 - p.kb) * cbcr.x);
    return float4(clamp(rgb, 0.0, 1.0), 1);
}
