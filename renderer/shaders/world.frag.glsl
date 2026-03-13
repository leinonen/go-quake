#version 430 core

flat in uint vFaceIndex;

// SSBO 3: visible face flags (same binding as compute shader)
layout(std430, binding = 3) readonly buffer VisibleFaces {
    uint visibleFaceFlags[];
};

// SSBO 4: per-face brightness from baked lightmaps
layout(std430, binding = 4) readonly buffer FaceBrightness {
    float faceBrightness[];
};

uniform bool uUsePVS;
uniform uint uTotalFaces;

out vec4 fragColor;

void main() {
    if (uUsePVS && vFaceIndex < uTotalFaces) {
        if (visibleFaceFlags[vFaceIndex] == 0u) {
            discard;
        }
    }
    float b = (vFaceIndex < uTotalFaces) ? faceBrightness[vFaceIndex] : 1.0;
    if (b >= 2.5) {
        // Water: deep blue
        fragColor = vec4(0.05, 0.2, 0.55, 1.0);
    } else if (b >= 1.5) {
        // Sky: light blue
        fragColor = vec4(0.35, 0.55, 0.85, 1.0);
    } else {
        b = pow(b, 0.75); // mild gamma lift — Quake lightmaps are linear, dark on modern displays
        fragColor = vec4(vec3(b), 1.0);
    }
}
