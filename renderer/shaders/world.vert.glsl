#version 430 core

layout(location = 0) in vec3 aPos;
layout(location = 1) in float aFaceIndex;

uniform mat4 uMVP;

flat out uint vFaceIndex;

void main() {
    gl_Position = uMVP * vec4(aPos, 1.0);
    vFaceIndex = uint(aFaceIndex);
}
