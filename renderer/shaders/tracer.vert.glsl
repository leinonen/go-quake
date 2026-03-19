#version 430 core
layout(location = 0) in vec3  aPos;
layout(location = 1) in float aLife;
uniform mat4 uMVP;
out float vLife;
void main() {
    gl_Position = uMVP * vec4(aPos, 1.0);
    vLife = aLife;
}
