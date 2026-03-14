#version 430 core

in vec3 vDir;

uniform float uTime;

out vec4 fragColor;

// --- Noise ---

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

// --- Sky ---

vec3 skyboxColor(vec3 dir) {
    dir = normalize(dir);
    float elev = dir.z; // +1 = zenith, 0 = horizon, -1 = nadir (Quake Z-up)

    // Project direction onto a sky plane so clouds recede toward the horizon.
    // Clamp elevation to avoid blowup right at the horizon line.
    float h    = max(elev, 0.05);
    vec2  base = dir.xy / h * 0.22;

    // Background: slow dark churning void
    vec2  uv1  = base * 0.5 + vec2(uTime * 0.008, uTime * 0.004);
    float bg   = fbm(uv1);
    vec3  bgColor = mix(vec3(0.02, 0.01, 0.02), vec3(0.18, 0.03, 0.08), bg);

    // Mid layer: rolling crimson masses
    vec2  uv2  = base * 0.9 + vec2(-uTime * 0.015, uTime * 0.006);
    float mid  = fbm(uv2);
    vec3  midColor = mix(vec3(0.10, 0.01, 0.04), vec3(0.45, 0.05, 0.10), mid);
    bgColor = mix(bgColor, midColor, smoothstep(0.35, 0.65, mid) * 0.7);

    // Cloud veins: fast ember-orange to magenta
    vec2  uv3  = base * 1.4 + vec2(uTime * 0.032, uTime * 0.011);
    float cn   = fbm(uv3);
    float mask = smoothstep(0.48, 0.66, cn);
    vec3  cloudColor = mix(vec3(0.6, 0.08, 0.05), vec3(0.90, 0.18, 0.30), cn);
    vec3  sky  = mix(bgColor, cloudColor, mask);

    // Ember glow band at the horizon
    float horizonGlow = smoothstep(0.35, 0.0, abs(elev));
    sky += vec3(0.28, 0.04, 0.01) * horizonGlow;

    // Fade to void below horizon
    float above = smoothstep(-0.12, 0.10, elev);
    sky = mix(vec3(0.01, 0.005, 0.01), sky, above);

    return sky;
}

void main() {
    fragColor = vec4(skyboxColor(vDir), 1.0);
}
