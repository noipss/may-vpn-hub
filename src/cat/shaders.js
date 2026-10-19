// GLSL for the scanline kitten.
// Pass A: raymarch the SDF kitten into a low-res "cell" buffer (lum, coverage, ink, ground shadow).
// Pass B: per scanline row, find the right edge of the silhouette (feeds the trailing line field).
// Pass C: full-res composite — pixel cells + horizontal lines + line field, transparent elsewhere.
window.CatShaders = (function () {
  const VERT = `#version 300 es
in vec2 aPos;
void main() { gl_Position = vec4(aPos, 0.0, 1.0); }`;

  const COMMON = `#version 300 es
precision highp float;
precision highp int;
`;

  const SCENE = COMMON + `
out vec4 outColor;
uniform ivec2 uCellO;
uniform float uCell;
uniform vec3 uCamP, uCamR, uCamU, uCamF;
uniform vec3 uProj;
uniform vec3 uTorsoC;
uniform mat3 uTorsoM;
uniform vec3 uTorsoS;
uniform vec3 uHeadC;
uniform mat3 uHeadM;
uniform vec3 uLeg[12];
uniform vec3 uTail[4];
uniform vec4 uFace;   // blink, tongue, earL, earR
uniform vec4 uMisc;   // happy, mouthOpen, -, -
uniform vec4 uBound;  // center, radius
uniform float uGround; // world y of the surface the cat stands on
uniform vec4 uAcc;    // sword amount, shield amount, -, -
uniform vec3 uSwordC, uShieldC;  // rigid body origins (sword: guard, shield: face centre)
uniform mat3 uSwordM, uShieldM;  // world -> local

const vec3 LIGHT = normalize(vec3(-0.8, 0.5, 0.35));
const vec3 SHADOW_DIR = normalize(vec3(-0.30, 1.0, 0.22));
const vec3 HEAD_R = vec3(1.01, 0.92, 0.97);

float hash3(vec3 p) {
  p = fract(p * 0.3183099 + 0.1);
  p *= 17.0;
  return fract(p.x * p.y * p.z * (p.x + p.y + p.z));
}
float vnoise(vec3 x) {
  vec3 i = floor(x), f = fract(x);
  f = f * f * (3.0 - 2.0 * f);
  return mix(mix(mix(hash3(i), hash3(i + vec3(1, 0, 0)), f.x),
                 mix(hash3(i + vec3(0, 1, 0)), hash3(i + vec3(1, 1, 0)), f.x), f.y),
             mix(mix(hash3(i + vec3(0, 0, 1)), hash3(i + vec3(1, 0, 1)), f.x),
                 mix(hash3(i + vec3(0, 1, 1)), hash3(i + vec3(1, 1, 1)), f.x), f.y), f.z);
}
float fur(vec3 p) { return vnoise(p) * 0.62 + vnoise(p * 2.3 + 7.1) * 0.38; }

float smin(float a, float b, float k) {
  float h = max(k - abs(a - b), 0.0) / k;
  return min(a, b) - h * h * k * 0.25;
}
float sdEll(vec3 p, vec3 r) {
  float k0 = length(p / r);
  float k1 = length(p / (r * r));
  return k0 * (k0 - 1.0) / k1;
}
float sdCap(vec3 p, vec3 a, vec3 b, float ra, float rb) {
  vec3 pa = p - a, ba = b - a;
  float h = clamp(dot(pa, ba) / dot(ba, ba), 0.0, 1.0);
  return length(pa - ba * h) - mix(ra, rb, h);
}

float ear(vec3 hl, float s, float tw) {
  vec3 q = hl - vec3(-0.06, 0.6, 0.52 * s);
  vec3 ax = normalize(vec3(-0.12 - tw * 0.45, 1.0, (0.42 + tw * 0.25) * s));
  q.x *= 1.35;
  return sdCap(q, vec3(0.0), ax * 0.56, 0.36, 0.06) * 0.74;
}

// parts: x body, y legs, z tail, w head(+ears)
vec4 parts(vec3 p) {
  vec3 tl = uTorsoM * (p - uTorsoC);
  vec3 ts = tl / uTorsoS;
  float body = sdEll(ts, vec3(0.96, 0.74, 0.72));
  body = smin(body, length(ts - vec3(-0.55, 0.05, 0.0)) - 0.66, 0.4);
  body = smin(body, length(ts - vec3(0.52, 0.0, 0.0)) - 0.66, 0.4);
  body *= min(uTorsoS.x, min(uTorsoS.y, uTorsoS.z));
  body -= 0.06 * (fur(tl * 2.6 + 3.1) - 0.4);
  float haunch = min(length(p - uLeg[6]) - 0.36, length(p - uLeg[9]) - 0.36);
  body = smin(body, haunch, 0.3);

  float legs = 1e9;
  for (int i = 0; i < 4; i++) {
    vec3 h = uLeg[i * 3], k = uLeg[i * 3 + 1], w = uLeg[i * 3 + 2];
    float l = min(sdCap(p, h, k, 0.23, 0.19), sdCap(p, k, w, 0.19, 0.165));
    l = smin(l, sdEll(p - w, vec3(0.2, 0.14, 0.2)), 0.08);
    legs = min(legs, l);
  }
  legs -= 0.02 * (fur(p * 4.0) - 0.4);

  float tail = min(min(sdCap(p, uTail[0], uTail[1], 0.2, 0.19), sdCap(p, uTail[1], uTail[2], 0.19, 0.17)),
                   sdCap(p, uTail[2], uTail[3], 0.17, 0.12));
  tail -= 0.06 * (fur(p * 3.2 + 1.7) - 0.3);

  vec3 hl = uHeadM * (p - uHeadC);
  float head = sdEll(hl, HEAD_R);
  head = smin(head, length(hl - vec3(0.30, -0.33, 0.45)) - 0.48, 0.3);
  head = smin(head, length(hl - vec3(0.30, -0.33, -0.45)) - 0.48, 0.3);
  head = smin(head, sdEll(hl - vec3(0.70, -0.42, 0.0), vec3(0.28, 0.22, 0.30)), 0.18);
  float face = smoothstep(0.35, 0.8, hl.x) * smoothstep(0.25, -0.2, hl.y);
  head -= (0.05 - 0.042 * face) * (fur(hl * 3.0 + 9.0) - 0.4);
  float ears = min(ear(hl, 1.0, uFace.z), ear(hl, -1.0, uFace.w));
  head = smin(head, ears, 0.12);
  if (uFace.y > 0.01) {
    vec3 m = vec3(0.80, -0.50, 0.0);
    head = min(head, sdCap(hl, m, m + normalize(vec3(0.75, -0.6, 0.0)) * 0.3 * uFace.y, 0.085, 0.1));
  }
  return vec4(body, legs, tail, head);
}

float sdBox(vec3 p, vec3 b) {
  vec3 q = abs(p) - b;
  return length(max(q, 0.0)) + min(max(q.x, max(q.y, q.z)), 0.0);
}

// sword, local frame: x = flat normal, y = along the blade (0 = guard), z = blade width
float swordSDF(vec3 p, out float kind) {
  vec3 q = uSwordM * (p - uSwordC);
  const float TIP = 1.55;
  float w = 0.13 * clamp((TIP - q.y) / 0.26, 0.0, 1.0);
  float blade = max(max(abs(q.z) - w, abs(q.x) - 0.036), max(0.08 - q.y, q.y - TIP)) * 0.75;
  float guard = sdBox(q - vec3(0.0, 0.05, 0.0), vec3(0.055, 0.04, 0.25)) - 0.015;
  float grip = sdCap(q, vec3(0.0, -0.3, 0.0), vec3(0.0, 0.02, 0.0), 0.048, 0.048);
  float pommel = length(q - vec3(0.0, -0.35, 0.0)) - 0.075;
  float dark = min(guard, min(grip, pommel));
  kind = dark < blade ? 1.0 : 0.0;
  // fuller: an ink groove down the middle of the blade
  if (blade <= dark && abs(q.z) < 0.016 && q.y > 0.16 && q.y < TIP - 0.32) kind = 1.0;
  return min(blade, dark);
}

// round shield, local frame: x = out of the face, y = up, z = across
float shieldSDF(vec3 p, out float kind) {
  vec3 q = uShieldM * (p - uShieldC);
  const float R = 0.5;
  float r = length(q.yz);
  float dome = 0.08 * (1.0 - min(r * r / (R * R), 1.0));
  float d = max(r - R, abs(q.x - dome * 0.5) - (0.035 + dome * 0.5)) * 0.85 - 0.01;
  kind = 0.0;
  if (r > R - 0.085) kind = 1.0;       // iron rim
  if (q.x > 0.0) {
    // paw emblem: a pad and four toes
    vec2 e = vec2(q.y, q.z);
    if (length((e - vec2(-0.1, 0.0)) / vec2(0.15, 0.19)) < 1.0) kind = 1.0;
    if (length(e - vec2(0.12, 0.2)) < 0.07 || length(e - vec2(0.12, -0.2)) < 0.07) kind = 1.0;
    if (length(e - vec2(0.21, 0.075)) < 0.07 || length(e - vec2(0.21, -0.075)) < 0.07) kind = 1.0;
  } else if (abs(q.z) < 0.05 && abs(q.y) < 0.3) kind = 1.0; // grip strap on the back
  return d;
}

// kind: 0 lit surface, 1 ink
float accSDF(vec3 p, out float kind) {
  kind = 0.0;
  float d = 1e9;
  float k;
  if (uAcc.x > 0.01) { float s = swordSDF(p, k); if (s < d) { d = s; kind = k; } }
  if (uAcc.y > 0.01) { float s = shieldSDF(p, k); if (s < d) { d = s; kind = k; } }
  return d;
}

float mapD(vec3 p) {
  vec4 q = parts(p);
  float d = smin(q.x, q.y, 0.16);
  d = smin(d, q.z, 0.12);
  d = smin(d, q.w, 0.16);
  if (uAcc.x + uAcc.y > 0.01) { float k; d = min(d, accSDF(p, k)); }
  return d;
}

vec3 calcNormal(vec3 p) {
  const vec2 e = vec2(1.0, -1.0) * 0.004;
  return normalize(e.xyy * mapD(p + e.xyy) + e.yyx * mapD(p + e.yyx) +
                   e.yxy * mapD(p + e.yxy) + e.xxx * mapD(p + e.xxx));
}
float calcAO(vec3 p, vec3 n) {
  float o = 0.0, sc = 1.0;
  for (int i = 1; i <= 4; i++) {
    float h = 0.09 * float(i);
    o += (h - mapD(p + n * h)) * sc;
    sc *= 0.7;
  }
  return clamp(1.0 - 1.5 * o, 0.0, 1.0);
}
float softShadow(vec3 ro, vec3 rd) {
  float res = 1.0, t = 0.04;
  for (int i = 0; i < 30; i++) {
    float h = mapD(ro + rd * t);
    res = min(res, 4.0 * h / t);
    t += clamp(h, 0.05, 0.35);
    if (res < 0.01 || t > 4.5) break;
  }
  return clamp(res, 0.0, 1.0);
}

// eye / nose / mouth decals on the head, in head-local space. returns ink: 1 black, 0.5 white
float faceInk(vec3 hl) {
  vec3 q = normalize(hl / HEAD_R);
  float ink = 0.0;
  for (int s = -1; s <= 1; s += 2) {
    vec3 e = normalize(vec3(0.70, -0.30, 0.60 * float(s)));
    vec3 u = normalize(cross(vec3(0.0, 1.0, 0.0), e)) * float(s);
    vec3 v = cross(e, u) * float(s);
    if (dot(q, e) < 0.8) continue;
    vec3 dq = q - e;
    float du = dot(dq, u), dv = dot(dq, v);
    float closed = max(uFace.x, uMisc.x);
    if (closed > 0.85) {
      // closed / happy eye: a short arch
      float arch = uMisc.x > 0.5 ? 0.05 * (1.0 - pow(du / 0.15, 2.0)) - 0.01 : -0.01;
      if (abs(du) < 0.15 && abs(dv - arch) < 0.026) ink = 1.0;
    } else {
      float ry = 0.19 * (1.0 - closed);
      float r = pow(du / 0.155, 2.0) + pow(dv / ry, 2.0);
      if (r < 1.0) {
        ink = 1.0;
        vec2 hp = vec2(du + 0.055, dv - 0.07);
        if (closed < 0.4 && abs(hp.x) < 0.03 && abs(hp.y) < 0.03) ink = 0.5;
      }
    }
  }
  // nose
  vec3 n0 = normalize(vec3(0.93, -0.36, 0.0));
  vec3 nu = vec3(0.0, 0.0, -1.0);
  vec3 nv = normalize(cross(n0, nu));
  if (dot(q, n0) < 0.85) return ink;
  vec3 dn = q - n0;
  float nx = dot(dn, nu), ny = dot(dn, nv);
  if (abs(nx) < 0.055 && ny > -0.035 && ny < 0.03) ink = 1.0;
  // philtrum + little "w" mouth
  if (abs(nx) < 0.016 && ny < -0.03 && ny > -0.10) ink = 1.0;
  float w = -0.10 - 0.035 * sin(clamp(abs(nx) / 0.11, 0.0, 1.0) * 3.14159);
  if (abs(nx) < 0.11 && abs(ny - w) < 0.016) ink = 1.0;
  if (uMisc.y > 0.05 && abs(nx) < 0.06 && ny < -0.09 && ny > -0.09 - 0.08 * uMisc.y) ink = 1.0;
  return ink;
}

void main() {
  vec2 cell = vec2(ivec2(gl_FragCoord.xy) + uCellO) + 0.5;
  vec2 dpx = cell * uCell;                   // design-space pixel (y down)
  vec3 rd = normalize(uCamF + uCamR * (dpx.x - uProj.x) / uProj.z - uCamU * (dpx.y - uProj.y) / uProj.z);
  vec3 ro = uCamP;

  float lum = 1.0, cov = 0.0, ink = 0.0, shadow = 0.0, mark = 0.0;

  // bounding sphere
  vec3 oc = ro - uBound.xyz;
  float b = dot(oc, rd);
  float c = dot(oc, oc) - uBound.w * uBound.w;
  float disc = b * b - c;
  // the floor only occludes / receives shadow when seen from above
  bool above = ro.y > uGround;
  float tg = (above && rd.y < 0.0) ? (uGround - ro.y) / rd.y : 1e9;
  bool hit = false;
  float t = 0.0;
  if (disc > 0.0) {
    float sq = sqrt(disc);
    t = max(-b - sq, 0.0);
    float tmax = min(-b + sq, tg);
    float minD = 1e9;
    vec3 pMin = ro;
    for (int i = 0; i < 110; i++) {
      vec3 p = ro + rd * t;
      float d = mapD(p);
      if (d < minD) { minD = d; pMin = p; }
      if (d < 0.0025) { hit = true; break; }
      t += d * 0.7;
      if (t > tmax) break;
    }
    if (hit) {
      vec3 p = ro + rd * t;
      vec3 n = calcNormal(p);
      vec4 q = parts(p);
      float ao = calcAO(p, n);
      float diff = clamp((dot(n, LIGHT) + 0.8) / 1.8, 0.0, 1.0);
      float rim = pow(1.0 - max(dot(n, -rd), 0.0), 2.2) * 0.4 * clamp(dot(n, LIGHT) + 0.5, 0.0, 1.0);
      lum = clamp(0.12 + 1.0 * diff * mix(1.0, ao, 0.8) + rim, 0.0, 1.0);
      cov = 1.0;
      float dm = min(min(q.x, q.y), q.z);
      float accKind = 0.0;
      float accD = uAcc.x + uAcc.y > 0.01 ? accSDF(p, accKind) : 1e9;
      bool onAcc = accD < min(dm, q.w) + 0.01;
      float fuzz = vnoise(p * 11.0) - 0.5;
      // socks: dark fur around every paw
      if (q.y < min(q.x, min(q.z, q.w)) + 0.03) {
        for (int i = 0; i < 4; i++)
          mark = max(mark, 1.0 - smoothstep(0.27, 0.37, length(p - uLeg[i * 3 + 2]) + 0.08 * fuzz));
      }
      // tail tip
      if (q.z < min(q.x, min(q.y, q.w)) + 0.03)
        mark = max(mark, 1.0 - smoothstep(0.22, 0.4, length(p - uTail[3]) + 0.08 * fuzz));
      if (onAcc) {
        ink = accKind;
        mark = 0.0;
      } else if (q.w < dm + 0.02) {
        vec3 hl = uHeadM * (p - uHeadC);
        // muzzle mask around the nose, fading out before the eyes
        vec3 mq = (hl - vec3(0.86, -0.46, 0.0)) / vec3(0.36, 0.3, 0.44);
        mark = max(mark, 1.0 - smoothstep(0.62, 1.12, length(mq) + 0.22 * fuzz));
        // inner ear
        for (int s = -1; s <= 1; s += 2) {
          vec3 eq = hl - vec3(-0.06, 0.6, 0.52 * float(s));
          if (eq.y > 0.14 && eq.x > 0.04 && ear(hl, float(s), s > 0 ? uFace.z : uFace.w) < 0.03)
            lum = min(lum, 0.35);
        }
        // tongue
        if (uFace.y > 0.01) {
          vec3 m = vec3(0.80, -0.50, 0.0);
          if (sdCap(hl, m, m + normalize(vec3(0.75, -0.6, 0.0)) * 0.3 * uFace.y, 0.085, 0.1) < 0.01)
            ink = 1.0;
        }
        if (ink < 0.5 && length(hl / HEAD_R) < 1.25 && hl.y < 0.5) ink = faceInk(hl);
      }
      lum *= 1.0 - 0.5 * mark;
    } else if (minD < 0.22) {
      vec3 nn = calcNormal(pMin);
      float lit = clamp(0.1 + 1.2 * dot(nn, LIGHT), 0.0, 1.0);
      float w = mix(0.04, 0.17, lit);
      cov = smoothstep(w, 0.0, minD) * (0.25 + 0.5 * vnoise(pMin * 8.0)) * mix(0.5, 1.0, lit);
      lum = 1.0;
    }
  }
  if (!hit && above && rd.y < 0.0) {
    vec3 g = ro + rd * tg;
    vec2 off = g.xz - uBound.xz;
    if (dot(off, off) < 16.0) {
      shadow = 1.0 - softShadow(g + vec3(0.0, 0.02, 0.0), SHADOW_DIR);
    }
  }
  // A channel: pigment on the cat (cov == 1), ground shadow elsewhere
  outColor = vec4(lum, cov, ink, hit ? mark : shadow);
}`;

  const ROWS = COMMON + `
out vec4 outColor;
uniform sampler2D uScene;
uniform ivec2 uRt;
uniform ivec2 uCellO;
uniform int uRowO;
void main() {
  int k = int(gl_FragCoord.x) + uRowO;
  int j = k * 2 - uCellO.y;
  int found = -1;
  if (j >= 0 && j + 1 < uRt.y) {
    for (int i = uRt.x - 1; i >= 0; i--) {
      float c = max(texelFetch(uScene, ivec2(i, j), 0).g, texelFetch(uScene, ivec2(i, j + 1), 0).g);
      if (c > 0.5) { found = i; break; }
    }
  }
  if (found < 0) outColor = vec4(0.0);
  else outColor = vec4(float(found % 256) / 255.0, float(found / 256) / 255.0, 1.0, 1.0);
}`;

  const COMPOSITE = COMMON + `
out vec4 outColor;
uniform sampler2D uScene;
uniform sampler2D uRows;
uniform vec2 uRes;
uniform float uDpr, uS, uP, uCell;
uniform vec2 uOff;
uniform ivec2 uCellO;
uniform ivec2 uRt;
uniform int uRowO, uRowN;
uniform float uTime, uIntro, uGlitch;
uniform vec4 uBtn;
uniform vec2 uCatX;
uniform float uGroundY;
uniform int uMode;      // 0 home (line field), 1 chat (compact, no field)
uniform float uLineK;   // line thickness scale
uniform float uK;       // floor-line length scale
uniform vec4 uClip;     // the cat is only drawn inside this rect (design px)
uniform vec4 uField;    // line field: top y, bottom y, base x, flow speed
uniform vec3 uTint;     // milk

float hash11(float n) { return fract(sin(n * 127.1 + 1.7) * 43758.5453); }
float hash21(vec2 p) { return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453); }
float vn1(float x) { float i = floor(x), f = fract(x); f = f * f * (3.0 - 2.0 * f); return mix(hash11(i), hash11(i + 1.0), f); }
float band(float y, float c, float th, float px) {
  float a = max(y - px * 0.5, c - th * 0.5);
  float b = min(y + px * 0.5, c + th * 0.5);
  return clamp((b - a) / px, 0.0, 1.0);
}
vec4 fetchS(int i, int j) {
  if (i < 0 || j < 0 || i >= uRt.x || j >= uRt.y) return vec4(1.0, 0.0, 0.0, 0.0);
  return texelFetch(uScene, ivec2(i, j), 0);
}

vec3 col; float alpha;
void over(vec3 c, float a) { col = c * a + col * (1.0 - a); alpha = a + alpha * (1.0 - a); }

void main() {
  vec2 fc = vec2(gl_FragCoord.x, uRes.y - gl_FragCoord.y);
  vec2 d = (fc / uDpr - uOff) / uS;
  float px = 1.0 / (uDpr * uS);
  float k = floor(d.y / uP);
  float yIn = d.y - k * uP;
  col = vec3(0.0); alpha = 0.0;

  // intro: each row slides in from the right and snaps into place
  float delay = 0.1 + hash11(k * 1.7) * 0.5 + clamp((k - 20.0) / 80.0, 0.0, 1.0) * 0.3;
  float pr = clamp((uIntro - delay) / 0.5, 0.0, 1.0);
  float shift = pow(1.0 - pr, 3.0) * (240.0 + 320.0 * hash11(k * 3.1));
  float vis = step(0.001, pr);
  float gt = floor(uTime * 12.0);
  float glitch = uGlitch * step(0.9, hash21(vec2(k, gt))) * (hash21(vec2(k + 9.0, gt)) - 0.5) * 18.0;
  float xs = d.x - shift - glitch;

  bool inBtn = d.x > uBtn.x - 5.0 && d.x < uBtn.z + 5.0 && d.y > uBtn.y - 5.0 && d.y < uBtn.w + 5.0;
  float lineC = uP * 0.5;

  // ---- line field on the right (behind the cat) ----
  if (d.x < uClip.x || d.y < uClip.y || d.x > uClip.z || d.y > uClip.w) { outColor = vec4(0.0); return; }

  if (uMode == 0 && d.y > uField.x && d.y < uField.y && vis > 0.0) {
    float seedT = floor(uTime * (0.15 + 0.35 * hash11(k * 5.3)) * uField.w + hash11(k) * 10.0);
    float h1 = hash21(vec2(k, seedT)), h2 = hash21(vec2(k + 31.0, seedT)), h3 = hash21(vec2(k + 57.0, seedT));
    float base = uField.z + 60.0 * (vn1(k * 0.21 + 4.0) - 0.5) + 26.0 * (h1 - 0.5);
    int ri = int(k) - uRowO;
    if (ri >= 0 && ri < uRowN) {
      vec4 re = texelFetch(uRows, ivec2(ri, 0), 0);
      if (re.b > 0.5) {
        float xr = (floor(re.r * 255.0 + 0.5) + floor(re.g * 255.0 + 0.5) * 256.0 + float(uCellO.x) + 1.0) * uCell;
        base = xr + 8.0 + 22.0 * h2;
      }
    }
    // fade the field in at its top and bottom edges
    float edge = smoothstep(uField.x, uField.x + 40.0, d.y) * smoothstep(uField.y, uField.y - 40.0, d.y);
    float x = xs;
    if (h3 > 0.3 + 0.5 * (1.0 - edge) && x > base) {
      float seg = floor((x - base) / 37.0);
      if (hash21(vec2(k * 3.0 + seg, seedT)) > 0.08) {
        float gray = h2 > 0.7 ? mix(0.45, 0.66, h1) : 0.1;
        over(vec3(gray), band(yIn, lineC, (h1 > 0.82 ? 2.2 : 1.7) * uLineK, px));
      }
    }
  }

  // ---- floor lines under the cat ----
  float fy = (d.y - uGroundY) / (66.0 * uK);
  if (fy > -0.12 && fy < 1.0 && !inBtn && vis > 0.0) {
    float seedT = floor(uTime * 0.4 + hash11(k * 2.1) * 7.0);
    float f1 = hash21(vec2(k, seedT + 1.0)), f2 = hash21(vec2(k + 11.0, seedT)), f3 = hash21(vec2(k + 23.0, seedT));
    float L = uCatX.x + (30.0 + 150.0 * f1 * f1) * uK;
    float R = uCatX.y + (110.0 + 260.0 * f2 * (1.0 - 0.6 * max(fy, 0.0))) * uK;
    if (f3 > 0.14 && xs > L && xs < R) {
      float gray = fy > 0.45 || f3 > 0.85 ? mix(0.4, 0.62, f1) : 0.07;
      over(vec3(gray), band(yIn, lineC, 1.9 * uLineK, px));
    }
  }

  // ---- cat ----
  int ci = int(floor(xs / uCell)) - uCellO.x;
  int cj = int(floor(d.y / uCell)) - uCellO.y;
  vec4 sc = fetchS(ci, cj);
  int rj = int(k) * 2 - uCellO.y;
  vec4 a = fetchS(ci, rj), b = fetchS(ci, rj + 1);
  float covR = max(a.g, b.g);
  float lumR = (a.r * a.g + b.r * b.g) / max(a.g + b.g, 1e-4);
  float shR = max(a.g < 0.999 ? a.a : 0.0, b.g < 0.999 ? b.a : 0.0);

  if (!inBtn && covR < 0.5) {
    float sa = smoothstep(0.08, 0.38, shR) * vis;
    over(vec3(0.05), sa * band(yIn, lineC, mix(1.6, 3.4, shR) * uLineK, px));
  }

  float cov = sc.g * vis;
  float tone = mix(0.70, 0.985, smoothstep(0.35, 0.9, sc.r));
  float pigment = sc.g > 0.999 ? sc.a : 0.0;
  tone = mix(tone, 0.27 + 0.22 * smoothstep(0.2, 0.55, sc.r), pigment);
  over(vec3(tone), cov);

  float D = 1.0 - lumR;
  float la = smoothstep(0.12, 0.22, D) * smoothstep(0.35, 0.6, covR) * vis;
  float th = mix(2.0, 3.5, smoothstep(0.3, 0.8, D)) * uLineK;
  over(vec3(0.02), la * band(yIn, lineC, th, px));

  if (vis > 0.0 && sc.g > 0.5) {
    if (sc.b > 0.75) over(vec3(0.015), 1.0);
    else if (sc.b > 0.25) over(vec3(0.99), 1.0);
  }
  outColor = vec4(col * uTint, alpha);
}`;

  return { VERT, SCENE, ROWS, COMPOSITE };
})();
