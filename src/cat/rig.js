// May May's skeleton for May VPN: poses in "cat frame" (x = forward, y = up, z = cat's left),
// resolved into world-space uniforms for the SDF shader. Sword and shield are separate rigid
// bodies: held by a paw (cat frame) or flying free (world frame).
window.CatRig = (function () {
  // ---------- math ----------
  const add = (a, b) => [a[0] + b[0], a[1] + b[1], a[2] + b[2]];
  const sub = (a, b) => [a[0] - b[0], a[1] - b[1], a[2] - b[2]];
  const mul = (a, s) => [a[0] * s, a[1] * s, a[2] * s];
  const dot = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
  const len = (a) => Math.hypot(a[0], a[1], a[2]);
  const norm = (a) => mul(a, 1 / (len(a) || 1));
  const cross = (a, b) => [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]];
  const lerp = (a, b, t) => a + (b - a) * t;
  const clamp01 = (x) => Math.min(1, Math.max(0, x));
  const smooth = (x) => { x = clamp01(x); return x * x * (3 - 2 * x); };
  const easeIO = (x) => { x = clamp01(x); return x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2; };
  const range = (t, a, b) => clamp01((t - a) / (b - a));
  const frac = (x) => x - Math.floor(x);

  // row-major 3x3
  const mm = (A, B) => {
    const C = new Array(9);
    for (let r = 0; r < 3; r++) for (let c = 0; c < 3; c++)
      C[r * 3 + c] = A[r * 3] * B[c] + A[r * 3 + 1] * B[3 + c] + A[r * 3 + 2] * B[6 + c];
    return C;
  };
  const mv = (M, v) => [M[0] * v[0] + M[1] * v[1] + M[2] * v[2], M[3] * v[0] + M[4] * v[1] + M[5] * v[2], M[6] * v[0] + M[7] * v[1] + M[8] * v[2]];
  const tr = (M) => [M[0], M[3], M[6], M[1], M[4], M[7], M[2], M[5], M[8]];
  const rotY = (a) => { const c = Math.cos(a), s = Math.sin(a); return [c, 0, -s, 0, 1, 0, s, 0, c]; };
  const rotZ = (a) => { const c = Math.cos(a), s = Math.sin(a); return [c, -s, 0, s, c, 0, 0, 0, 1]; };
  const rotX = (a) => { const c = Math.cos(a), s = Math.sin(a); return [1, 0, 0, 0, c, -s, 0, s, c]; };
  // rotate v around unit axis k by angle a (Rodrigues)
  const rotAxis = (v, k, a) => {
    const c = Math.cos(a), s = Math.sin(a);
    return add(add(mul(v, c), mul(cross(k, v), s)), mul(k, dot(k, v) * (1 - c)));
  };

  // ---------- camera (design space 480x800) ----------
  function makeCamera({ P, T, f, cx, cy }) {
    const F = norm(sub(T, P));
    const R = norm(cross(F, [0, 1, 0]));
    const U = cross(R, F);
    return { P, F, R, U, f, cx, cy };
  }
  // world origin lands on design point (240, GROUND_Y); 1 unit ≈ 92px
  const GROUND_Y = 446;
  const CAM = (() => {
    const c = makeCamera({ P: [0, 2.4, 15], T: [0, 1.3, 0], f: 1380, cx: 240, cy: 0 });
    const v = sub([0, 0, 0], c.P);
    c.cy = GROUND_Y + (c.f * dot(v, c.U)) / dot(v, c.F);
    return c;
  })();
  function project(w, cam = CAM) {
    const v = sub(w, cam.P);
    const z = dot(v, cam.F);
    return [cam.cx + (cam.f * dot(v, cam.R)) / z, cam.cy - (cam.f * dot(v, cam.U)) / z, z];
  }

  // ---------- skeleton ----------
  const HIP = {
    FL: [0.58, -0.40, 0.34], FR: [0.58, -0.40, -0.34],
    BL: [-0.58, -0.36, 0.38], BR: [-0.58, -0.36, -0.38],
  };
  const LEGS = ['FL', 'FR', 'BL', 'BR'];
  const REST = {
    FL: [0.66, 0.14, 0.34], FR: [0.66, 0.14, -0.34],
    BL: [-0.60, 0.14, 0.38], BR: [-0.60, 0.14, -0.38],
  };
  const L1 = 0.42, L2 = 0.42;
  const tailFrom = (torso, rel) => rel.map((p) => add(torso, p));
  const TAIL_UP = [[-0.95, 0.1, 0], [-1.28, 0.36, 0], [-1.38, 0.72, 0.02], [-1.2, 1.0, 0.05]];
  const TAIL_RUN = [[-0.95, 0.12, 0], [-1.32, 0.3, 0], [-1.62, 0.5, 0], [-1.9, 0.62, 0]];

  function mix(a, b, t) {
    if (typeof a === 'number') return lerp(a, b, t);
    if (Array.isArray(a)) return a.map((v, i) => mix(v, b[i], t));
    const o = {};
    for (const k in a) o[k] = mix(a[k], b[k], t);
    return o;
  }
  function track(keys, t) {
    if (t <= keys[0][0]) return keys[0][1];
    for (let i = 0; i < keys.length - 1; i++) {
      const [t0, v0] = keys[i], [t1, v1] = keys[i + 1];
      if (t <= t1) return mix(v0, v1, smooth((t - t0) / (t1 - t0)));
    }
    return keys[keys.length - 1][1];
  }

  function stand() {
    const torso = [0, 1.34, 0];
    return {
      torso, pitch: 0, wig: 0, squash: [1, 1, 1],
      head: [0.91, 2.42, 0], hYaw: 0, hPitch: -0.04, hRoll: 0,
      paws: { FL: REST.FL, FR: REST.FR, BL: REST.BL, BR: REST.BR },
      tail: tailFrom(torso, TAIL_UP),
      earL: 0, earR: 0, blink: 0, happy: 0, tongue: 0, mouth: 0,
    };
  }

  // ---------- idle life ----------
  function blinkAt(t) {
    const period = 4.3;
    const ph = ((t % period) + period) % period;
    const cycle = Math.floor(t / period);
    let b = ph < 0.17 ? Math.sin((ph / 0.17) * Math.PI) : 0;
    if (cycle % 3 === 1 && ph > 0.3 && ph < 0.47) b = Math.max(b, Math.sin(((ph - 0.3) / 0.17) * Math.PI));
    return b;
  }
  function earTwitch(t, seed) {
    const period = 5.7 + seed;
    const ph = ((t + seed * 2.3) % period + period) % period;
    return ph < 0.28 ? Math.sin((ph / 0.28) * Math.PI) * 0.5 : 0;
  }
  function breathe(p, t, k = 1) {
    const b = Math.sin(t * Math.PI * 2 / 3.1) * k;
    p.squash = [p.squash[0], p.squash[1] * (1 + 0.016 * b), p.squash[2] * (1 + 0.01 * b)];
    p.head = add(p.head, [0, 0.012 * b, 0]);
  }

  // ---------- weapons ----------
  // Held: { paw: 'FR'|'FL', dir, side } in cat frame (+twist around dir). Free: { C, D, Z } world.
  const SWORD_REST = { dir: norm([0.3, 1, -0.42]), twist: 0 };
  const SHIELD_REST = { face: norm([0.55, 0.05, 1]), up: [0, 1, 0] };

  // ---------- poses ----------
  const YAW = 1.02; // three-quarter view, facing the viewer and a little to the right
  const TAIL_GUARD = [[-0.62, 0.0, 0.05], [-0.92, -0.36, 0.4], [-0.6, -0.62, 0.78], [-0.02, -0.66, 0.9]];

  function guardBase() {
    const torso = [-0.02, 1.0, 0];
    return {
      torso, pitch: 0.92, wig: 0, squash: [1, 1, 1],
      head: [0.44, 2.66, 0.02], hYaw: 0.3, hPitch: 0.02, hRoll: 0,
      paws: { FL: [0.6, 1.02, 0.78], FR: [0.62, 0.98, -0.72], BL: [0.34, 0.14, 0.5], BR: [0.34, 0.14, -0.5] },
      tail: tailFrom(torso, TAIL_GUARD),
      earL: 0, earR: 0, blink: 0, happy: 0, tongue: 0, mouth: 0,
    };
  }

  // off: stands guard; every 7.5s lifts the sword, looks at it and twirls it
  function guard(t) {
    const p = guardBase();
    breathe(p, t);
    p.blink = blinkAt(t);
    p.earL = earTwitch(t, 0.0);
    p.earR = earTwitch(t, 1.7);
    p.tail[3] = add(p.tail[3], [0.04 * Math.sin(t * 1.3), 0.05 * Math.max(0, Math.sin(t * 0.9)), 0.04 * Math.sin(t * 1.7)]);
    const sword = { paw: 'FR', dir: SWORD_REST.dir, twist: 0 };
    const k = ((t - 2.5) % 7.5 + 7.5) % 7.5; // first look 2.5s after the screen opens
    if (t > 2.5 && k < 3.2) {
      const up = smooth(range(k, 0, 0.6)) * (1 - smooth(range(k, 2.5, 3.2)));
      p.paws.FR = mix(p.paws.FR, [0.98, 1.6, -0.3], up);
      sword.dir = norm(mix(SWORD_REST.dir, [0.12, 1, 0.12], up));
      sword.twist = Math.PI * 4 * easeIO(range(k, 0.75, 2.2));
      p.hYaw = lerp(p.hYaw, -0.18, up);
      p.hPitch = lerp(p.hPitch, 0.2, up);
      p.hRoll = 0.12 * up;
      p.earL = lerp(p.earL, 0.25, up);
      p.earR = lerp(p.earR, 0.25, up);
      p.blink = k > 2.3 && k < 2.47 ? Math.sin(((k - 2.3) / 0.17) * Math.PI) : 0;
    }
    return { p, yaw: YAW, sword, shield: { paw: 'FL', ...SHIELD_REST } };
  }

  // connecting: swings the sword and blocks with the shield, on a loop
  const FIGHT_FR = [
    [0.0, [0.62, 0.98, -0.72]], [0.32, [0.36, 1.9, -0.95]], [0.46, [1.08, 1.45, -0.72]],
    [0.62, [0.95, 1.0, -0.82]], [1.0, [0.62, 0.98, -0.72]],
  ];
  const FIGHT_DIR = [
    [0.0, SWORD_REST.dir], [0.32, norm([0.05, 1, -0.55])], [0.46, norm([0.75, 0.25, -0.75])],
    [0.62, norm([0.42, -0.45, -0.8])], [1.0, SWORD_REST.dir],
  ];
  const FIGHT_FL = [[0.0, [0.6, 1.02, 0.78]], [0.36, [0.5, 1.08, 0.82]], [0.62, [1.08, 1.3, 0.5]], [0.82, [1.06, 1.28, 0.5]], [1.0, [0.6, 1.02, 0.78]]];
  function fight(t) {
    const p = guardBase();
    const u = frac(t / 1.35);
    p.paws.FR = track(FIGHT_FR, u);
    const dir = norm(track(FIGHT_DIR, u));
    p.paws.FL = track(FIGHT_FL, u);
    const swing = Math.sin(range(u, 0.3, 0.62) * Math.PI);
    const block = Math.sin(range(u, 0.55, 0.95) * Math.PI);
    p.pitch -= 0.14 * swing;
    p.torso = add(p.torso, [0.06 * swing, -0.03 * swing, 0]);
    p.head = add(p.head, [0.12 * swing + 0.05 * block, -0.06 * swing, 0]);
    p.hYaw = 0.12;
    p.hPitch = -0.12;
    p.earL = p.earR = -0.35;
    p.squash = [1 + 0.03 * swing, 1 - 0.04 * swing, 1];
    p.tail[3] = add(p.tail[3], [0, 0.15 * swing, 0.1 * Math.sin(t * 7)]);
    const face = norm(mix(SHIELD_REST.face, [1, 0.1, 0.35], block));
    return { p, yaw: YAW - 0.12 * swing, sword: { paw: 'FR', dir, twist: 0.3 * swing }, shield: { paw: 'FL', face, up: [0, 1, 0] } };
  }

  // connected: curled into a ball that turns like a globe; the tail flicks now and then
  const BALL_TAIL = [[-0.8, -0.1, 0.4], [-0.62, -0.55, 1.0], [0.15, -0.72, 1.22], [0.85, -0.7, 0.95]];
  function ballBase() {
    const torso = [-0.1, 1.0, 0];
    return {
      torso, pitch: -0.05, wig: 0, squash: [1.18, 1.3, 1.5],
      head: [0.66, 0.86, 0.5], hYaw: 1.0, hPitch: -0.5, hRoll: 0.45,
      paws: { FL: [0.72, 0.22, 0.72], FR: [0.78, 0.24, 0.32], BL: [-0.25, 0.16, 0.7], BR: [-0.4, 0.16, -0.5] },
      tail: tailFrom(torso, BALL_TAIL),
      earL: -0.45, earR: -0.45, blink: 1, happy: 1, tongue: 0, mouth: 0,
    };
  }
  const SPIN = 0.55; // rad/s
  function ball(t) {
    const p = ballBase();
    breathe(p, t * 0.8, 1.6);
    const k = ((t - 1.5) % 5.2 + 5.2) % 5.2;
    if (t > 1.5 && k < 1.1) {
      const e = Math.sin(range(k, 0, 1.1) * Math.PI);
      const sw = Math.sin(k * 11) * e;
      p.tail[3] = add(p.tail[3], [0.05 * e, 0.42 * e, 0.22 * sw]);
      p.tail[2] = add(p.tail[2], [0, 0.18 * e, 0.08 * sw]);
    }
    p.earL = -0.3 + earTwitch(t, 0.6) * 0.6;
    return { p, yaw: YAW + SPIN * t, lift: 0.55 + 0.06 * Math.sin(t * 1.7), sword: null, shield: null };
  }

  // ---------- run (loading screen) ----------
  const GAIT = { T: 0.46, L: 0.86, duty: 0.5, off: { FL: 0.0, FR: 0.12, BL: 0.52, BR: 0.64 } };
  const RUN_SPEED = GAIT.L / (GAIT.duty * GAIT.T);
  function runPhase(t, k) { return frac(t / GAIT.T + GAIT.off[k]); }
  function run(t) {
    const p = stand();
    const ph = t / GAIT.T;
    const bob = Math.sin(ph * Math.PI * 4);
    p.torso = add(p.torso, [0, 0.05 * bob - 0.04, 0]);
    p.pitch = 0.06 * Math.sin(ph * Math.PI * 2);
    p.head = add(p.head, [0.12, 0.03 * Math.sin(ph * Math.PI * 4 + 0.6) - 0.08, 0]);
    p.hPitch = -0.12 - p.pitch * 0.6;
    p.hYaw = 0.12;
    p.earL = p.earR = -0.28;
    p.squash = [1.03, 1 - 0.03 * bob, 1];
    p.tail = tailFrom(p.torso, TAIL_RUN).map((q, i) => add(q, [0, 0.07 * i * Math.sin(ph * Math.PI * 2 - i * 0.7), 0]));
    for (const k of LEGS) {
      const f = runPhase(t, k);
      const r = REST[k];
      if (f < GAIT.duty) {
        p.paws[k] = [r[0] + GAIT.L / 2 - GAIT.L * (f / GAIT.duty), 0.14, r[2]];
      } else {
        const s = (f - GAIT.duty) / (1 - GAIT.duty);
        p.paws[k] = [r[0] - GAIT.L / 2 + GAIT.L * smooth(s), 0.14 + 0.32 * Math.sin(s * Math.PI), r[2]];
      }
    }
    return { p, yaw: 0, sword: null, shield: null };
  }

  // ---------- resolve to world uniforms ----------
  function ik(hip, paw, hint) {
    let d = sub(paw, hip);
    let dist = len(d);
    const reach = (L1 + L2) * 0.995;
    if (dist > reach) { paw = add(hip, mul(d, reach / dist)); d = sub(paw, hip); dist = reach; }
    dist = Math.max(dist, 0.05);
    const dn = mul(d, 1 / dist);
    const a = (L1 * L1 - L2 * L2 + dist * dist) / (2 * dist);
    const h = Math.sqrt(Math.max(L1 * L1 - a * a, 0));
    const perp = norm(sub(hint, mul(dn, dot(hint, dn))));
    return { knee: add(add(hip, mul(dn, a)), mul(perp, h)), paw };
  }

  // a rigid body's frame → shader matrix (rows = local axes)
  const frameM = (X, Y, Z) => [...X, ...Y, ...Z];

  // sword world frame from a held descriptor: grip at the paw, blade along dir, flat side to camera
  function swordHeld(s, pawW, Ry, cam) {
    const D = norm(mv(Ry, s.dir));
    let Z = cross(D, cam.F);
    Z = len(Z) < 1e-3 ? [1, 0, 0] : norm(Z);
    Z = rotAxis(Z, D, s.twist || 0);
    return { C: add(pawW, mul(D, 0.08)), D, Z };
  }
  function shieldHeld(s, pawW, Ry) {
    const F = norm(mv(Ry, s.face));
    const Z = norm(cross(F, mv(Ry, s.up)));
    return { C: add(pawW, mul(F, 0.12)), F, Z };
  }

  // frame: { p, yaw, root?, sword, shield } (weapons: held descriptor | free world frame | null)
  function finalize(fr, cam = CAM) {
    const { p } = fr;
    const root = fr.root || [0, 0, 0];
    const yaw = fr.yaw;
    const Ry = rotY(yaw);
    const lifted = add(root, [0, fr.lift || 0, 0]);
    const toW = (c) => add(lifted, mv(Ry, c));
    const torsoW = toW(p.torso);
    const torsoM = mm(mm(rotZ(-p.pitch), rotY(-p.wig)), rotY(-yaw));
    const headW = toW(p.head);
    const headM = tr(mm(mm(mm(Ry, rotY(p.hYaw)), rotZ(p.hPitch)), rotX(p.hRoll)));

    const hipM = mm(rotY(p.wig), rotZ(p.pitch));
    const legs = [];
    for (const k of LEGS) {
      const hl = HIP[k];
      const hip = add(p.torso, mv(hipM, [hl[0], hl[1] * p.squash[1], hl[2]]));
      const hint = k[0] === 'F' ? [-0.7, -0.3, 0] : [0.6, 0.8, 0];
      const s = ik(hip, p.paws[k], hint);
      legs.push(toW(hip), toW(s.knee), toW(s.paw));
    }
    const tail = p.tail.map(toW);
    const pawW = { FL: legs[2], FR: legs[5] };

    let sw = null, sh = null;
    if (fr.sword) sw = fr.sword.D ? fr.sword : swordHeld(fr.sword, pawW[fr.sword.paw], Ry, cam);
    if (fr.shield) sh = fr.shield.F ? fr.shield : shieldHeld(fr.shield, pawW[fr.shield.paw], Ry);
    const swordAmt = sw ? (sw.amt != null ? sw.amt : 1) : 0;
    const shieldAmt = sh ? (sh.amt != null ? sh.amt : 1) : 0;
    const swordM = sw ? frameM(cross(sw.D, sw.Z), sw.D, sw.Z) : [1, 0, 0, 0, 1, 0, 0, 0, 1];
    const shieldM = sh ? frameM(sh.F, cross(sh.Z, sh.F), sh.Z) : [1, 0, 0, 0, 1, 0, 0, 0, 1];

    // bounding sphere around the cat and anything it holds or threw
    const center = add(torsoW, [0.1, 0.5, 0]);
    let radius = 2.6;
    if (sw) radius = Math.max(radius, len(sub(sw.C, center)) + 1.8);
    if (sh) radius = Math.max(radius, len(sub(sh.C, center)) + 0.8);

    const P = (w) => project(w, cam);
    const headS = P(headW);
    const torsoS = P(torsoW);
    const xs = [];
    for (const q of [torsoW, headW, ...legs, ...tail]) xs.push(P(q)[0]);
    const pxPerUnit = cam.f / headS[2];
    const catX = [Math.min(...xs) - 0.3 * pxPerUnit, Math.max(...xs) + 0.3 * pxPerUnit];
    const groundS = P([torsoW[0], root[1], torsoW[2]]);

    return {
      uniforms: {
        torsoC: torsoW, torsoM, torsoS: p.squash,
        headC: headW, headM,
        legs, tail,
        face: [p.blink, p.tongue, p.earL, p.earR],
        misc: [p.happy, p.mouth, 0, 0],
        bound: [...center, radius],
        ground: root[1],
        acc: [swordAmt, shieldAmt, 0, 0],
        swordC: sw ? sw.C : [0, -99, 0], swordM,
        shieldC: sh ? sh.C : [0, -99, 0], shieldM,
      },
      anchors: { head: headS, torso: torsoS, catX, groundY: groundS[1], pxPerUnit, paws: legs.filter((_, i) => i % 3 === 2).map(P) },
      world: { sword: sw, shield: sh, paws: legs.filter((_, i) => i % 3 === 2) },
      cam,
    };
  }

  return {
    CAM, GROUND_Y, project, finalize, mix, smooth, easeIO, range, clamp01, lerp, frac,
    add, sub, mul, len, norm, cross, rotAxis, mm, tr,
    guard, fight, ball, run, runPhase, GAIT, RUN_SPEED, YAW, SPIN, LEGS,
  };
})();
