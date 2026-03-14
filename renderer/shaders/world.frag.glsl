#version 430 core

flat in uint vFaceIndex;
in vec2 vTexST;
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

out vec4 fragColor;

void main() {
    if (uUsePVS && vFaceIndex < uTotalFaces) {
        if (visibleFaceFlags[vFaceIndex] == 0u) {
            discard;
        }
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

    // Apply lightmap brightness
    float b = (vFaceIndex < uTotalFaces) ? faceBrightness[vFaceIndex] : 1.0;
    float lightFactor;
    if (b >= 1.5) {
        lightFactor = 1.0; // sky or water: no lightmap dimming
    } else {
        lightFactor = pow(b, 0.75); // mild gamma lift for linear lightmaps
    }

    // Exponential fog (greyish)
    const vec3 fogColor = vec3(0.12, 0.12, 0.13);
    const float fogDensity = 0.0013;
    float fogFactor = exp(-fogDensity * vEyeDist);
    fogFactor = clamp(fogFactor, 0.0, 1.0);

    vec3 finalColor = mix(fogColor, color * lightFactor, fogFactor);
    fragColor = vec4(finalColor, 1.0);
}
