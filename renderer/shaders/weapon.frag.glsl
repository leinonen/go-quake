#version 430 core

in vec2 vTexST;

uniform sampler2D uTex;

out vec4 fragColor;

void main() {
    vec3 color = texture(uTex, vTexST).rgb;

    // Match world shader: desaturate toward grey
    float luma = dot(color, vec3(0.299, 0.587, 0.114));
    color = mix(color, vec3(luma), 0.4);

    // Fixed ambient dim (no BSP lightmap for MDL)
    color *= 0.72;

    fragColor = vec4(color, 1.0);
}
