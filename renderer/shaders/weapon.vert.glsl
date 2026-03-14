#version 430 core

layout(location=0) in vec3 aPos;
layout(location=1) in vec2 aTexST;

uniform mat4 uProj;
uniform mat4 uWeaponMat;

out vec2 vTexST;

void main() {
    gl_Position = uProj * uWeaponMat * vec4(aPos, 1.0);
    vTexST = aTexST;
}
