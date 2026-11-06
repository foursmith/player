cbuffer Projection : register(b0) {
    float4 Camera;  // yaw, pitch, zoom, forward
    int4 Layout;    // mode, split, portrait, RGBA
    float4 Output;  // source aspect, output width, output height, FOV
    float4 Matrix;  // kr, kb, Y offset, Y scale
    float4 Chroma;  // UV offset, UV scale, active texture width, height
    float4 Flat;    // pan x, pan y, zoom
};
Texture2D<float4> Luma : register(t0);
Texture2D<float4> ChromaPlane : register(t1);
SamplerState LinearClamp : register(s0);

struct Vertex { float4 position : SV_POSITION; float2 uv : TEXCOORD0; };
Vertex vertexMain(uint id : SV_VertexID) {
    float2 uv = float2((id << 1) & 2, id & 2);
    Vertex v;
    v.position = float4(uv.x * 2 - 1, 1 - uv.y * 2, 0, 1);
    v.uv = uv;
    return v;
}
float3 rotateRay(float3 p, float yaw, float pitch) {
    float sy = sin(yaw), cy = cos(yaw), sx = sin(pitch), cx = cos(pitch);
    float y = cx * p.y - sx * p.z, z = sx * p.y + cx * p.z;
    return float3(cy * p.x + sy * z, y, -sy * p.x + cy * z);
}
float4 fragmentMain(Vertex v) : SV_TARGET {
    const float pi = 3.14159265359;
    float2 size = Output.yz;
    float2 source;
    bool visible = true;
    if (Layout.x == 6) {
        float ratio = (size.x / size.y) / Output.x;
        source = (v.uv - .5) * float2(max(1.0, ratio), max(1.0, 1 / ratio)) /
                 Flat.z + .5 - Flat.xy;
        if (any(source < 0) || any(source > 1)) return float4(0, 0, 0, 1);
    } else {
        float count = size.x > size.y ? min(float(Layout.y), max(1.0,
            floor(size.x / (size.y * (Layout.z != 0 ? 9.0 / 16.0 : 1.0)) + .5))) : 1;
        float2 uv = float2(frac(v.uv.x * count), v.uv.y);
        float t = tan(Output.w * pi / 360) / max(.01, Camera.z);
        float yaw = Camera.x * pi / 180, pitch = Camera.y * pi / 180;
        float3 ray = rotateRay(normalize(float3((uv.x * 2 - 1) * t * size.x / count / size.y,
                                              (1 - uv.y * 2) * t, -1)), yaw, pitch);
        float3 origin = rotateRay(float3(0, 0, -Camera.w), yaw, pitch);
        float b = dot(origin, ray), disc = b * b - dot(origin, origin) + 10000;
        float hit = -b + sqrt(max(0.0, disc));
        float3 q = (origin + ray * hit) / 100;
        visible = disc >= 0 && hit > 0;
        if ((Layout.x == 0 || Layout.x == 1 || Layout.x == 3 || Layout.x == 4) && q.z > 0) visible = false;
        if (Layout.x == 1 || Layout.x == 4) {
            float r = 1 - acos(clamp(q.z, -1.0, 1.0)) / pi;
            float phi = atan2(q.y, q.x);
            source = float2(.5 + r * cos(phi), .5 - r * sin(phi));
            if (Layout.x == 1) source.x *= .5;
        } else {
            float a = atan2(-q.x, -q.z);
            source = float2(.5 - a / (2 * pi), acos(clamp(q.y, -1.0, 1.0)) / pi);
            if (Layout.x == 0) source.x = .25 - a / (2 * pi);
            else if (Layout.x == 3) source.x = .5 - a / pi;
            else if (Layout.x == 5) source.y *= .5;
        }
    }
    if (!visible) return float4(.0784314, .0705882, .0588235, 1);
    source *= Chroma.zw;
    if (Layout.w != 0) return float4(Luma.Sample(LinearClamp, source).rgb, 1);
    float y = (Luma.Sample(LinearClamp, source).r - Matrix.z) * Matrix.w;
    float2 uv = (ChromaPlane.Sample(LinearClamp, source).rg - Chroma.x) * Chroma.y;
    float kr = Matrix.x, kb = Matrix.y, kg = 1 - kr - kb;
    float3 rgb = float3(y + 2 * (1 - kr) * uv.y,
                       y - 2 * kb * (1 - kb) / kg * uv.x - 2 * kr * (1 - kr) / kg * uv.y,
                       y + 2 * (1 - kb) * uv.x);
    return float4(saturate(rgb), 1);
}
