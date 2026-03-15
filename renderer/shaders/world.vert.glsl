#version 430 core

layout(location = 0) in vec3 aPos;
layout(location = 1) in float aFaceIndex;
layout(location = 2) in vec2 aTexST;
layout(location = 3) in vec2 aLightmapST;

uniform mat4 uMVP;
uniform vec3 uEntityOffset;

flat out uint vFaceIndex;
out vec2 vTexST;
out vec2 vLightmapST;
out float vEyeDist;

void main() {
    vec4 worldPos = vec4(aPos + uEntityOffset, 1.0);
    gl_Position = uMVP * worldPos;
    vFaceIndex = uint(aFaceIndex);
    vTexST = aTexST;
    vLightmapST = aLightmapST;
    vEyeDist = gl_Position.w; // clip-space w == eye-space z
}
