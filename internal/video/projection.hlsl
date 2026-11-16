cbuffer Projection : register(b0) {
    float4 Camera;  // yaw, pitch, zoom, forward
    int4 Layout;    // mode, split, portrait, RGBA
    float4 Output;  // source aspect, output width, output height, FOV
    float4 Matrix;  // kr, kb, Y offset, Y scale
    float4 Chroma;  // UV offset, UV scale, active texture width, height
    float4 Flat;    // pan x, pan y, zoom, ambient (-2 off, -1 current frame, 0 history/update alpha)
};
Texture2D<float4> Luma : register(t0);
Texture2D<float4> ChromaPlane : register(t1);
Texture2D<float4> Ambient : register(t2);
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
float3 sampleVideo(float2 source) {
    source *= Chroma.zw;
    if (Layout.w != 0) return Luma.Sample(LinearClamp, source).rgb;
    float y = (Luma.Sample(LinearClamp, source).r - Matrix.z) * Matrix.w;
    float2 uv = (ChromaPlane.Sample(LinearClamp, source).rg - Chroma.x) * Chroma.y;
    float kr = Matrix.x, kb = Matrix.y, kg = 1 - kr - kb;
    float3 rgb = float3(y + 2 * (1 - kr) * uv.y,
                       y - 2 * kb * (1 - kb) / kg * uv.x - 2 * kr * (1 - kr) / kg * uv.y,
                       y + 2 * (1 - kb) * uv.x);
    return saturate(rgb);
}

float4 ambientLight(float2 source, float2 pixel) {
    float2 metric = float2(Output.x, 1) / min(Output.x, 1.0);
    float ratio = (Output.y / Output.z) / Output.x;
    float2 extent = float2(max(1.0, ratio), max(1.0, 1 / ratio)) / Flat.z;
    float2 before = (.5 * extent - .5 + Flat.xy) * metric;
    float2 after = (.5 * extent - .5 - Flat.xy) * metric;
    float2 spread = max(float2(.000001, .000001), float2(source.x < .5 ? before.x : after.x,
                                                         source.y < .5 ? before.y : after.y));
    float2 outside = (source - clamp(source, 0.0, 1.0)) * metric;
    float distance = length(outside);
    float reach = clamp(length(outside / spread), 0.0, 1.0);
    float transition = smoothstep(0.0, .15, distance);
    float fade = (1 - .22 * transition) * (1 - .78 * reach * reach * (3 - 2 * reach));
    // Follow the nearest edge instead of compressing the whole frame toward
    // its center. Blur grows with distance, keeping local color near the image.
    float2 center = clamp(source, 0.0, 1.0) * .92 + .04;
    float blur = .085 + .07 * reach;
    float3 light = 0;
    float total = 0;
    for (int y = 0; y < 7; y++) {
        for (int x = 0; x < 7; x++) {
            float2 uv = (float2(x, y) + .5) / 7;
            float2 delta = (uv - center) / blur;
            float weight = exp(-dot(delta, delta) / 2);
            light += (Flat.w < 0 ? sampleVideo(uv) : Ambient.SampleLevel(LinearClamp, uv, 0).rgb) * weight;
            total += weight;
        }
    }
    light /= total;
    float luminance = dot(light, float3(.2126, .7152, .0722));
    float gain = 1 + .35 * transition * (1 - max(light.r, max(light.g, light.b)));
    light = clamp((luminance + (light - luminance) * (1 + .25 * transition)) * gain, 0.0, 1.0);
    light *= fade;
    // Stable spatial dither, below one 8-bit step and shared by all channels.
    float noise = frac(52.9829189 * frac(dot(floor(pixel), float2(.06711056, .00583715)))) - .5;
    return float4(saturate(light + noise / 255), 1);
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
        if (any(source < 0) || any(source > 1)) {
            if (Flat.w == -2) return float4(0, 0, 0, 1);
            return ambientLight(source, v.position.xy);
        }
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
    return float4(sampleVideo(source), 1);
}

float4 fragmentAmbient(Vertex v) : SV_TARGET {
    float3 current = 0;
    for (int y = -1; y <= 1; y++) {
        for (int x = -1; x <= 1; x++) {
            current += sampleVideo(v.uv + float2(x, y) / 21) / 9;
        }
    }
    if (Flat.w >= 1) return float4(current, 1);
    return float4(lerp(Ambient.SampleLevel(LinearClamp, v.uv, 0).rgb, current, Flat.w), 1);
}
