#version 430 core
layout(location = 0) in vec3  aPos;
layout(location = 1) in float aLife;
layout(location = 2) in float aStuck;
uniform mat4 uMVP;
out float vLife;
out float vStuck;
void main() {
    gl_Position  = uMVP * vec4(aPos, 1.0);
    float depth  = max(gl_Position.w, 1.0);
    gl_PointSize = clamp(800.0 / depth, 2.0, 16.0);
    vLife  = aLife;
    vStuck = aStuck;
}
