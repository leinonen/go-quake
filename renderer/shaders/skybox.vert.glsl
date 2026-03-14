#version 430 core

layout(location = 0) in vec3 aPos;

uniform mat4 uMVP;

out vec3 vDir;

void main() {
    vDir = aPos;
    // Set z = w so depth is always 1.0 — skybox renders behind everything
    vec4 pos = uMVP * vec4(aPos, 1.0);
    gl_Position = pos.xyww;
}
