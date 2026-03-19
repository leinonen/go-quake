#version 430 core
in float vLife;
in float vStuck;
in float vKind;
out vec4 fragColor;
void main() {
    vec2  c    = gl_PointCoord * 2.0 - 1.0;
    float dist = dot(c, c);
    if (dist > 1.0) discard;
    float edge  = 1.0 - smoothstep(0.6, 1.0, dist);
    float alpha = edge * vLife;
    vec3 color;
    if (vKind < 0.5) {
        // Blood: fresh red → dried dark red
        vec3 fresh = vec3(0.6, 0.05, 0.05);
        vec3 dried = vec3(0.2, 0.01, 0.01);
        color = mix(fresh, dried, vStuck);
    } else {
        // Spark: bright orange → dark grey
        vec3 hot  = vec3(1.0, 0.6, 0.1);
        vec3 cool = vec3(0.3, 0.2, 0.05);
        color = mix(hot, cool, vStuck);
    }
    fragColor = vec4(color, alpha);
}
