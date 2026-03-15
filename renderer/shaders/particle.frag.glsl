#version 430 core
in float vLife;
in float vStuck;
out vec4 fragColor;
void main() {
    vec2  c    = gl_PointCoord * 2.0 - 1.0;
    float dist = dot(c, c);
    if (dist > 1.0) discard;
    float edge  = 1.0 - smoothstep(0.6, 1.0, dist);
    float alpha = edge * vLife;
    // Fresh blood = bright red; stuck/dried = dark
    vec3 fresh  = vec3(0.6, 0.05, 0.05);
    vec3 dried  = vec3(0.2, 0.01, 0.01);
    vec3 color  = mix(fresh, dried, vStuck);
    fragColor   = vec4(color, alpha);
}
