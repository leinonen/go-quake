#version 430 core
uniform float uTime;
in vec2 vUV;
out vec4 FragColor;
void main() {
    float wave = 0.05 * sin(vUV.x * 8.0 + uTime * 2.0) * sin(vUV.y * 6.0 + uTime * 1.5);
    FragColor = vec4(0.0, 0.25 + wave, 0.45 + wave, 0.35);
}
