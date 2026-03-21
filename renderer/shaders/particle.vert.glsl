#version 430 core
layout(location = 0) in vec3  aPos;
layout(location = 1) in float aLife;
layout(location = 2) in float aStuck;
layout(location = 3) in float aKind;
uniform mat4 uMVP;
out float vLife;
out float vStuck;
out float vKind;
void main() {
    gl_Position  = uMVP * vec4(aPos, 1.0);
    float depth  = max(gl_Position.w, 1.0);
    gl_PointSize = clamp(1200.0 / depth, 3.0, 22.0);
    vLife  = aLife;
    vStuck = aStuck;
    vKind  = aKind;
}
