/**
 * WebGL2 fragment-shader plasma orb — ported from the reference
 * implementation at `.planning/sketches/001-immersive-orb-stage/index.html`
 * (variant A, the locked "shader nebula" winner). A single fullscreen
 * triangle; the orb is a noise-warped rim with a bloom halo and hot core,
 * driven entirely by uniforms (GPU-bound, near-zero main-thread CPU cost —
 * leaves headroom for WebRTC audio decode within the latency budget).
 */

export const ORB_VERTEX_SHADER_SOURCE = `#version 300 es
in vec2 p;
void main() {
  gl_Position = vec4(p, 0.0, 1.0);
}
`;

export const ORB_FRAGMENT_SHADER_SOURCE = `#version 300 es
precision highp float;
uniform vec2 uRes;
uniform float uTime;
uniform float uAmp;
uniform vec3 uColor;
out vec4 fragColor;

float hash(vec3 p) {
  p = fract(p * 0.3183099 + 0.1);
  p *= 17.0;
  return fract(p.x * p.y * p.z * (p.x + p.y + p.z));
}

float noise(vec3 x) {
  vec3 i = floor(x);
  vec3 f = fract(x);
  f = f * f * (3.0 - 2.0 * f);
  return mix(
    mix(mix(hash(i + vec3(0, 0, 0)), hash(i + vec3(1, 0, 0)), f.x),
        mix(hash(i + vec3(0, 1, 0)), hash(i + vec3(1, 1, 0)), f.x), f.y),
    mix(mix(hash(i + vec3(0, 0, 1)), hash(i + vec3(1, 0, 1)), f.x),
        mix(hash(i + vec3(0, 1, 1)), hash(i + vec3(1, 1, 1)), f.x), f.y), f.z);
}

float fbm(vec3 p) {
  float v = 0.0;
  float a = 0.5;
  for (int i = 0; i < 5; i++) {
    v += a * noise(p);
    p *= 2.03;
    a *= 0.5;
  }
  return v;
}

void main() {
  vec2 uv = (gl_FragCoord.xy - 0.5 * uRes) / min(uRes.x, uRes.y);
  float t = uTime * 0.25;
  float r = length(uv);

  // Domain-warped radius so the rim breathes organically.
  vec3 q = vec3(uv * 2.2, t);
  float warp = fbm(q + fbm(q + t) * 0.6);
  float base = 0.34 + uAmp * 0.13; // radius pulse +-8-14% (amplitude-driven)
  float edge = base + (warp - 0.5) * (0.10 + uAmp * 0.12); // noisy rim

  float core = smoothstep(edge, edge - 0.14, r);
  float bloom = exp(-max(r - edge, 0.0) * (7.0 - uAmp * 2.5));
  float inner = smoothstep(0.0, edge, edge - r) * 0.6;

  vec3 col = uColor * (core * 1.0 + inner * 0.7) + uColor * bloom * (0.55 + uAmp * 0.6);
  col += vec3(1.0) * core * smoothstep(0.16, 0.0, r) * (0.10 + uAmp * 0.25); // hot center
  col *= 0.82 + 0.30 * fbm(vec3(uv * 5.0, t * 2.0)); // subtle noise texture inside the orb
  col *= smoothstep(1.15, 0.2, r); // vignette into the stage

  fragColor = vec4(col, 1.0);
}
`;

export interface OrbShaderProgram {
  /**
   * Draws one frame. Sets the GL viewport to `width`/`height` (device-pixel
   * canvas size) itself — the caller only needs to resize the backing
   * `<canvas>` element before calling `draw`.
   */
  draw(
    width: number,
    height: number,
    timeSec: number,
    amplitude: number,
    color: readonly [number, number, number],
  ): void;
}

function compileShader(gl: WebGL2RenderingContext, type: number, source: string): WebGLShader | null {
  const shader = gl.createShader(type);
  if (!shader) return null;
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    console.error("klanker-voice orb shader compile error:", gl.getShaderInfoLog(shader));
    gl.deleteShader(shader);
    return null;
  }
  return shader;
}

/**
 * Compiles + links the plasma-orb program and returns a `draw()` closure
 * over its uniforms/buffer. Returns `null` on compile/link failure — the
 * caller (`OrbCanvas`) treats that identically to "no WebGL2 support" and
 * swaps to the 2D fallback.
 */
export function createOrbShaderProgram(gl: WebGL2RenderingContext): OrbShaderProgram | null {
  const vertexShader = compileShader(gl, gl.VERTEX_SHADER, ORB_VERTEX_SHADER_SOURCE);
  const fragmentShader = compileShader(gl, gl.FRAGMENT_SHADER, ORB_FRAGMENT_SHADER_SOURCE);
  if (!vertexShader || !fragmentShader) return null;

  const program = gl.createProgram();
  if (!program) return null;
  gl.attachShader(program, vertexShader);
  gl.attachShader(program, fragmentShader);
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    console.error("klanker-voice orb shader link error:", gl.getProgramInfoLog(program));
    return null;
  }
  gl.useProgram(program);

  // One fullscreen triangle (covers the viewport, no separate quad/index buffer).
  const buffer = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
  const positionLoc = gl.getAttribLocation(program, "p");
  gl.enableVertexAttribArray(positionLoc);
  gl.vertexAttribPointer(positionLoc, 2, gl.FLOAT, false, 0, 0);

  const uRes = gl.getUniformLocation(program, "uRes");
  const uTime = gl.getUniformLocation(program, "uTime");
  const uAmp = gl.getUniformLocation(program, "uAmp");
  const uColor = gl.getUniformLocation(program, "uColor");

  return {
    draw(width, height, timeSec, amplitude, color) {
      gl.viewport(0, 0, width, height);
      gl.useProgram(program);
      gl.uniform2f(uRes, width, height);
      gl.uniform1f(uTime, timeSec);
      gl.uniform1f(uAmp, amplitude);
      gl.uniform3f(uColor, color[0], color[1], color[2]);
      gl.drawArrays(gl.TRIANGLES, 0, 3);
    },
  };
}
