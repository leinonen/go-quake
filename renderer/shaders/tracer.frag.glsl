#version 430 core
in float vLife;
out vec4 fragColor;
void main() {
    // Bright yellow-white at muzzle end, fades to transparent
    vec3 color = mix(vec3(1.0, 0.9, 0.5), vec3(1.0, 1.0, 1.0), vLife);
    fragColor = vec4(color, vLife * 0.85);
}
