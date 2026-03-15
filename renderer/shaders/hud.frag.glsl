#version 430 core

uniform float uFrac;

in vec2 vUV;
out vec4 FragColor;

void main() {
    if (vUV.x > uFrac) discard;
    float r = 1.0 - uFrac;
    float g = uFrac;
    FragColor = vec4(r, g, 0.1, 1.0);
}
