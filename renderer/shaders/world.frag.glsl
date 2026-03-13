#version 430 core

flat in uint vFaceIndex;

// SSBO 3: visible face flags (same binding as compute shader)
layout(std430, binding = 3) readonly buffer VisibleFaces {
    uint visibleFaceFlags[];
};

uniform bool uUsePVS;
uniform uint uTotalFaces;
uniform vec3 uFaceColor;

out vec4 fragColor;

// Simple hash for per-face color variety
vec3 faceColor(uint idx) {
    uint h = idx * 1664525u + 1013904223u;
    float r = float((h >> 16u) & 0xFFu) / 255.0;
    float g = float((h >> 8u)  & 0xFFu) / 255.0;
    float b = float( h         & 0xFFu) / 255.0;
    return vec3(r * 0.6 + 0.2, g * 0.6 + 0.2, b * 0.6 + 0.2);
}

void main() {
    if (uUsePVS && vFaceIndex < uTotalFaces) {
        if (visibleFaceFlags[vFaceIndex] == 0u) {
            discard;
        }
    }
    fragColor = vec4(faceColor(vFaceIndex), 1.0);
}
