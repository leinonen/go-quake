#version 430 core

flat in uint vFaceIndex;
in vec2 vTexST;
in vec2 vLightmapST;
in float vEyeDist;

// SSBO 3: visible face flags (same binding as compute shader)
layout(std430, binding = 3) readonly buffer VisibleFaces {
    uint visibleFaceFlags[];
};

// SSBO 4: per-face brightness from baked lightmaps
layout(std430, binding = 4) readonly buffer FaceBrightness {
    float faceBrightness[];
};

// SSBO 5: per-face atlas rect: x, y, w, h in pixels
layout(std430, binding = 5) readonly buffer FaceAtlasInfo {
    vec4 faceAtlas[];
};

uniform bool uUsePVS;
uniform uint uTotalFaces;
uniform sampler2D uAtlas;
uniform vec2 uAtlasSize;
uniform sampler2D uLightmap;
uniform float uTime;

out vec4 fragColor;

// --- Noise (shared by water) ---

float hash(vec2 p) {
    return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453);
}

float noise(vec2 p) {
    vec2 i = floor(p);
    vec2 f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    return mix(mix(hash(i),                 hash(i + vec2(1.0, 0.0)), f.x),
               mix(hash(i + vec2(0.0, 1.0)), hash(i + vec2(1.0, 1.0)), f.x), f.y);
}

float fbm(vec2 p) {
    float v = 0.0, a = 0.5;
    for (int i = 0; i < 5; i++) {
        v += a * noise(p);
        p  = p * 2.0 + vec2(1.7, 9.2);
        a *= 0.5;
    }
    return v;
}

// --- Procedural water ---

vec3 proceduralWater(vec2 texST) {
    vec2 base = texST / 64.0;

    // Quake-style sin warp: two offset wave fronts distort the UV before sampling
    float t = uTime;
    vec2 warp1 = vec2(sin(base.y * 2.3 + t * 1.1) * 0.06,
                      sin(base.x * 1.7 + t * 0.9) * 0.06);
    vec2 warp2 = vec2(sin(base.y * 3.7 - t * 0.7) * 0.04,
                      sin(base.x * 2.9 + t * 1.4) * 0.04);

    float w1 = fbm(base       + warp1);
    float w2 = fbm(base * 1.4 + warp2 + vec2(3.7, 1.3));
    float wave = (w1 + w2) * 0.5;

    // Deep murky void → sickly dark teal at surface
    vec3 deepColor    = vec3(0.01, 0.05, 0.04);
    vec3 surfaceColor = vec3(0.03, 0.20, 0.15);
    vec3 water = mix(deepColor, surfaceColor, smoothstep(0.3, 0.7, wave));

    // Sparse caustic glints at crests
    float crest = smoothstep(0.64, 0.76, wave);
    vec3 glint = vec3(0.25, 0.80, 0.60);
    water = mix(water, glint, crest * 0.55);

    // Subtle foam edge where two wave systems clash
    float clash = abs(w1 - w2);
    water += vec3(0.04, 0.12, 0.09) * smoothstep(0.08, 0.16, clash);

    return water;
}

// --- Procedural lava ---

vec3 proceduralLava(vec2 texST) {
    vec2 base = texST / 64.0;

    float t = uTime * 0.6;
    // Slow rolling warp — lava moves sluggishly
    vec2 warp1 = vec2(sin(base.y * 1.8 + t * 0.7) * 0.10,
                      sin(base.x * 1.4 + t * 0.5) * 0.10);
    vec2 warp2 = vec2(sin(base.y * 2.9 - t * 0.4) * 0.06,
                      sin(base.x * 2.3 + t * 0.8) * 0.06);

    float w1 = fbm(base       + warp1);
    float w2 = fbm(base * 1.3 + warp2 + vec2(5.1, 2.7));
    float wave = (w1 + w2) * 0.5;

    // Dark cooled crust → glowing molten orange-red
    vec3 crustColor  = vec3(0.05, 0.01, 0.00);
    vec3 hotColor    = vec3(0.85, 0.18, 0.01);
    vec3 lava = mix(crustColor, hotColor, smoothstep(0.30, 0.65, wave));

    // Bright yellow-white hotspot at the highest crests
    float crest = smoothstep(0.62, 0.78, wave);
    vec3 glow = vec3(1.0, 0.75, 0.10);
    lava = mix(lava, glow, crest * 0.70);

    // Add pulsing emissive shimmer so it looks self-lit
    float pulse = 0.5 + 0.5 * sin(uTime * 1.3 + w1 * 6.2);
    lava += vec3(0.08, 0.02, 0.00) * pulse * smoothstep(0.4, 0.6, wave);

    return lava;
}

// --- Procedural portal ---

vec3 proceduralPortal(vec2 texST) {
    vec2 base = texST / 64.0;
    float t = uTime;

    // Divergence-free (curl) domain warp: rotate the sampling coords using
    // perpendicular sine waves so the flow is rotational with no fixed centre.
    // This tiles seamlessly because sin/cos are smooth everywhere.
    float wx = sin(base.y * 2.1 - t * 1.1) * 0.20 + sin(base.y * 4.3 + t * 0.5) * 0.08;
    float wy = cos(base.x * 1.9 + t * 0.9) * 0.20 + cos(base.x * 3.8 - t * 0.6) * 0.08;
    vec2 swirl1 = base + vec2(wx, wy);

    float wx2 = cos(base.y * 3.2 + t * 0.7) * 0.14;
    float wy2 = sin(base.x * 2.7 - t * 1.3) * 0.14;
    vec2 swirl2 = base + vec2(wx2, wy2) + vec2(3.7, 1.5);

    float n1 = fbm(swirl1 * 1.8);
    float n2 = fbm(swirl2 * 2.1);
    float energy = (n1 + n2) * 0.5;

    // Pulsing brightness so it feels alive
    float pulse = 0.5 + 0.5 * sin(t * 1.8 + n1 * 5.0);

    // void black → electric cyan/blue → violet → white-hot
    vec3 col = mix(vec3(0.00, 0.00, 0.03), vec3(0.05, 0.25, 0.65), smoothstep(0.28, 0.55, energy));
    col      = mix(col, vec3(0.50, 0.15, 0.95),                     smoothstep(0.52, 0.70, energy));
    col      = mix(col, vec3(0.90, 0.85, 1.00),                     smoothstep(0.68, 0.82, energy) * pulse);

    // Crackling arcs where both FBM layers peak simultaneously
    float arc = smoothstep(0.72, 0.82, n1) * smoothstep(0.68, 0.78, n2);
    col += vec3(0.70, 0.50, 1.00) * arc * 0.7;

    return col;
}

// --- Main ---

void main() {
    if (uUsePVS && vFaceIndex < uTotalFaces) {
        if (visibleFaceFlags[vFaceIndex] == 0u) {
            discard;
        }
    }

    float b = (vFaceIndex < uTotalFaces) ? faceBrightness[vFaceIndex] : 1.0;

    // Sky faces (sentinel 2.0): let the skybox show through
    if (b >= 1.8 && b < 2.5) {
        discard;
    }

    // Water faces (sentinel 3.0)
    if (b >= 2.5 && b < 3.5) {
        fragColor = vec4(proceduralWater(vTexST), 1.0);
        return;
    }

    // Lava faces (sentinel 4.0)
    if (b >= 3.5 && b < 4.5) {
        fragColor = vec4(proceduralLava(vTexST), 1.0);
        return;
    }

    // Portal/teleporter faces (sentinel 5.0)
    if (b >= 4.5) {
        fragColor = vec4(proceduralPortal(vTexST), 1.0);
        return;
    }

    // Sample texture from atlas
    vec3 color = vec3(0.5);
    if (vFaceIndex < uTotalFaces) {
        vec4 ar = faceAtlas[vFaceIndex]; // atlasX, atlasY, texW, texH
        if (ar.z > 0.0 && ar.w > 0.0) {
            vec2 wrapped = fract(vTexST / ar.zw);
            vec2 atlasUV = (ar.xy + wrapped * ar.zw) / uAtlasSize;
            color = texture(uAtlas, atlasUV).rgb;
        }
    }

    // Slightly desaturate toward grey
    float luma = dot(color, vec3(0.299, 0.587, 0.114));
    color = mix(color, vec3(luma), 0.4);

    // Per-texel lightmap (Quake overbright: stored at half intensity, *2 restores range)
    vec3 lm = texture(uLightmap, vLightmapST).rgb;

    // Exponential fog (greyish)
    const vec3 fogColor = vec3(0.12, 0.12, 0.13);
    const float fogDensity = 0.0013;
    float fogFactor = exp(-fogDensity * vEyeDist);
    fogFactor = clamp(fogFactor, 0.0, 1.0);

    vec3 finalColor = mix(fogColor, color * lm * 2.0, fogFactor);
    fragColor = vec4(finalColor, 1.0);
}
