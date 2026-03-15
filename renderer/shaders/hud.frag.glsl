#version 430 core

uniform sampler2D uHUDTex;

in vec2 vUV;
out vec4 FragColor;

void main() {
    vec4 col = texture(uHUDTex, vUV);
    if (col.a < 0.1) discard;
    FragColor = col;
}
