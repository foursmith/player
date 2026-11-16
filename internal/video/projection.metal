#include <metal_stdlib>
using namespace metal;

struct Params {
    float yaw, pitch, zoom, forward;
    int mode, split, portrait, rgba;
    float aspect, width, height, fov;
    float kr, kb, yoffset, yscale, uvoffset, uvscale;
    float flat_x, flat_y, flat_zoom, ambient_mix; // -2 off, -1 current frame, 0 history; positive for updates
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

float3 sampleVideo(float2 source, constant Params &p,
                   texture2d<float> ytex, texture2d<float> uvtex) {
    constexpr sampler s(coord::normalized, address::clamp_to_edge, filter::linear);
    if (p.rgba != 0) return ytex.sample(s, source).rgb;
    float y = (ytex.sample(s, source).r - p.yoffset) * p.yscale;
    float2 cbcr = (uvtex.sample(s, source).rg - float2(p.uvoffset)) * p.uvscale;
    float kg = 1 - p.kr - p.kb;
    float3 rgb = float3(y + 2 * (1 - p.kr) * cbcr.y,
                       y - 2 * p.kb * (1 - p.kb) / kg * cbcr.x -
                           2 * p.kr * (1 - p.kr) / kg * cbcr.y,
                       y + 2 * (1 - p.kb) * cbcr.x);
    return clamp(rgb, 0.0, 1.0);
}

// Broad edge-colored light and an eased outer shadow, evaluated
// only in the bars. Source-relative geometry follows flat zoom and pan.
float4 ambientLight(float2 source, float2 pixel, constant Params &p,
                    texture2d<float> ytex, texture2d<float> uvtex, texture2d<float> ambient) {
    constexpr sampler s(coord::normalized, address::clamp_to_edge, filter::linear);
    float2 metric = float2(p.aspect, 1) / min(p.aspect, 1.0);
    float ratio = (p.width / p.height) / p.aspect;
    float2 extent = float2(max(1.0, ratio), max(1.0, 1 / ratio)) / p.flat_zoom;
    float2 pan = float2(p.flat_x, p.flat_y);
    float2 before = (.5 * extent - .5 + pan) * metric;
    float2 after = (.5 * extent - .5 - pan) * metric;
    // Fit each bar through to the window edge; keep the field colored there.
    float2 spread = max(float2(.000001), select(before, after, source >= .5));
    float2 outside = (source - clamp(source, 0.0, 1.0)) * metric;
    float distance = length(outside);
    float reach = clamp(length(outside / spread), 0.0, 1.0);
    float transition = smoothstep(0.0, .15, distance);
    float fade = (1 - .22 * transition) * (1 - .78 * reach * reach * (3 - 2 * reach));
    // Follow the nearest edge instead of compressing the whole frame toward
    // its center. Blur grows with distance, keeping local color near the image.
    float2 center = clamp(source, 0.0, 1.0) * .92 + .04;
    float blur = .085 + .07 * reach;
    float3 light = float3(0);
    float total = 0;
    for (int y = 0; y < 7; y++) {
        for (int x = 0; x < 7; x++) {
            float2 uv = (float2(x, y) + .5) / 7;
            float2 delta = (uv - center) / blur;
            float weight = exp(-dot(delta, delta) / 2);
            light += (p.ambient_mix < 0 ? sampleVideo(uv, p, ytex, uvtex) : ambient.sample(s, uv).rgb) * weight;
            total += weight;
        }
    }
    light /= total;
    float luminance = dot(light, float3(.2126, .7152, .0722));
    float gain = 1 + .35 * transition * (1 - max(light.r, max(light.g, light.b)));
    light = clamp((luminance + (light - luminance) * (1 + .25 * transition)) * gain, 0.0, 1.0);
    light *= fade;
    // Static, achromatic sub-LSB dither breaks 8-bit gradient contours without
    // adding temporal noise or changing the picture/history color samples.
    float noise = fract(52.9829189 * fract(dot(floor(pixel), float2(.06711056, .00583715)))) - .5;
    return float4(clamp(light + noise / 255, 0.0, 1.0), 1);
}

fragment float4 fragmentMain(
    Vertex in [[stage_in]], constant Params &p [[buffer(0)]],
    texture2d<float> ytex [[texture(0)]], texture2d<float> uvtex [[texture(1)]],
    texture2d<float> ambient [[texture(2)]]) {
    float2 outSize = float2(p.width, p.height);
    float2 source;
    bool visible = true;
    if (p.mode == 6) {
        float ratio = (outSize.x / outSize.y) / p.aspect;
        source = (in.uv - .5) * float2(max(1.0, ratio), max(1.0, 1 / ratio)) /
                 p.flat_zoom + .5 - float2(p.flat_x, p.flat_y);
        visible = all(source >= 0) && all(source <= 1);
        if (!visible && p.ambient_mix == -2) return float4(0, 0, 0, 1);
        if (!visible) return ambientLight(source, in.position.xy, p, ytex, uvtex, ambient);
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
    return float4(sampleVideo(source, p, ytex, uvtex), 1);
}

fragment float4 fragmentAmbient(
    Vertex in [[stage_in]], constant Params &p [[buffer(0)]],
    texture2d<float> ytex [[texture(0)]], texture2d<float> uvtex [[texture(1)]],
    texture2d<float> previous [[texture(2)]]) {
    constexpr sampler s(coord::normalized, address::clamp_to_edge, filter::linear);
    // Area samples keep fine moving details from flickering in a sparse field.
    float3 current = float3(0);
    for (int y = -1; y <= 1; y++) {
        for (int x = -1; x <= 1; x++) {
            current += sampleVideo(in.uv + float2(x, y) / 21, p, ytex, uvtex) / 9;
        }
    }
    if (p.ambient_mix >= 1) return float4(current, 1);
    return float4(mix(previous.sample(s, in.uv).rgb, current, p.ambient_mix), 1);
}
