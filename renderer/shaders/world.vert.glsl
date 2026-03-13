#version 430 core

layout(location = 0) in vec3 aPos;
layout(location = 1) in float aFaceIndex;
layout(location = 2) in vec2 aTexST;

uniform mat4 uMVP;

flat out uint vFaceIndex;
out vec2 vTexST;

void main() {
    gl_Position = uMVP * vec4(aPos, 1.0);
    vFaceIndex = uint(aFaceIndex);
    vTexST = aTexST;
}
