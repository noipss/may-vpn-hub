(function () {
  const Rig = window.CatRig;
  const $ = (id) => document.getElementById(id);
  const canvas = $('scene');
  const stage = $('stage');
  const renderer = window.CatRenderer(canvas, { pitch: 6.6 });
  const capture = new URLSearchParams(location.search).has('capture');
  const api = window.vpn;

  // ---------- layout: 480x800 design space, scaled to fit ----------
  const DW = 480, DH = 800;
  let view;
  function layout() {
    const W = window.innerWidth, H = window.innerHeight;
    const s = Math.min(W / DW, H / DH);
    const ox = (W - DW * s) / 2, oy = (H - DH * s) / 2;
    view = { W, H, dpr: window.devicePixelRatio || 1, s, ox, oy };
    stage.style.transform = `translate(${ox}px, ${oy}px) scale(${s})`;
    renderer.resize(view);
  }
  layout();
  window.addEventListener('resize', layout);

  // ---------- pixel art ----------
  // a circle on a 4px grid: fill disc, 1-cell ink ring, a 1-cell drop shadow
  function pixelDisc(cells, cell, rOut, rIn, offset = 0) {
    const out = { fill: [], ring: [], shadow: [] };
    const c = cells / 2;
    for (let j = 0; j < cells; j++) for (let i = 0; i < cells; i++) {
      const d = Math.hypot(i + 0.5 - c, j + 0.5 - c);
      const r = `<rect x="${i * cell + offset}" y="${j * cell + offset}" width="${cell}" height="${cell}"/>`;
      if (d < rIn) out.fill.push(r);
      else if (d < rOut) out.ring.push(r);
    }
    return out;
  }
  // paw print on a 1-unit grid: '#' = pad
  const PAW = [
    '...##...##...',
    '..####.####..',
    '..####.####..',
    '#..##...##..#',
    '##.........##',
    '###..###..###',
    '.#..#####..#.',
    '...#######...',
    '..#########..',
    '..#########..',
    '...#######...',
    '....##.##....',
  ];
  function pawRects(u, ox, oy, stripes) {
    const rs = [];
    PAW.forEach((row, j) => {
      for (let i = 0; i < row.length; i++) {
        if (row[i] !== '#') continue;
        // stripes: draw each cell as a horizontal scanline (the cat's own drawing style)
        if (stripes) rs.push(`<rect x="${ox + i * u}" y="${oy + j * u}" width="${u}" height="${Math.max(1, u * 0.62)}"/>`);
        else rs.push(`<rect x="${ox + i * u}" y="${oy + j * u}" width="${u}" height="${u}"/>`);
      }
    });
    return rs.join('');
  }
  (function drawPaw() {
    const disc = pixelDisc(34, 4, 16.6, 15.4);
    const shadow = pixelDisc(34, 4, 16.6, 0, 4);
    $('pawFace').setAttribute('viewBox', '0 0 140 140');
    $('pawFace').setAttribute('width', '140');
    $('pawFace').setAttribute('height', '140');
    $('pawFace').innerHTML =
      `<g class="shadow">${shadow.ring.join('')}</g><g class="fill">${disc.fill.join('')}</g><g class="ring">${disc.ring.join('')}</g>` +
      `<g class="pad">${pawRects(5, 68 - 6.5 * 5, 66 - 6 * 5, false)}</g>`;
    // dashed outer ring for "working…": every other arc segment of a bigger circle
    const segs = [];
    const C = 84;
    for (let j = 0; j < 42; j++) for (let i = 0; i < 42; i++) {
      const x = i + 0.5 - 21, y = j + 0.5 - 21, d = Math.hypot(x, y);
      if (d < 20.6 && d > 19.5 && Math.floor(((Math.atan2(y, x) + Math.PI) / (2 * Math.PI)) * 16) % 2 === 0)
        segs.push(`<rect x="${i * 4}" y="${j * 4}" width="4" height="4"/>`);
    }
    $('pawSpin').innerHTML = `<g fill="#0B0906">${segs.join('')}</g>`;
    $('pawSpin').style.transformOrigin = `${C}px ${C}px`;
  })();
  const PRINT_SVG = `<svg viewBox="0 0 13 12" width="15" height="14" fill="#0B0906">${pawRects(1, 0, 0, true)}</svg>`;

  // ---------- clock ----------
  let tStart = performance.now();
  let frozen = null;
  const now = () => (frozen != null ? frozen : (performance.now() - tStart) / 1000);

  // ---------- cat state machine ----------
  // modes: run (loader) · guard (off) · fight (connecting) · throw → ball (connected) · uncurl → guard|fight
  const THROW_D = 1.9, UNCURL_D = 1.55, FADE = 0.32;
  const M = { mode: 'run', t0: 0, from: null, fromT: 0, next: null, spin0: 0 };
  let revealT = null;   // when the loader handed over
  let last = null;      // last evaluated frame (cat frame pose + yaw)

  function setMode(mode, t, opts = {}) {
    if (M.mode === 'ball') M.spin0 = Rig.SPIN * (t - M.t0);
    M.from = opts.fade === false || !last ? null : { p: last.p, yaw: last.yaw, lift: last.lift || 0 };
    M.fromT = t;
    M.mode = mode;
    M.t0 = t;
    M.next = opts.next || null;
  }

  // the cat follows the connection state with whatever sequence gets her there
  function want(state, t) {
    const m = M.mode;
    const target = state === 'connected' ? 'ball' : state === 'connecting' ? 'fight' : 'guard';
    if (m === 'run') return;
    if (target === 'ball') {
      if (m === 'guard' || m === 'fight') setMode('throw', t);
      else if (m === 'uncurl') setMode('ball', t, { fade: true });
    } else if (m === 'ball' || m === 'throw') {
      setMode('uncurl', t, { fade: false, next: target });
    } else if (m === 'uncurl') {
      M.next = target;
    } else if (m !== target) {
      setMode(target, t);
    }
  }

  const sinceReveal = (t) => (revealT == null ? 0 : t - revealT);

  // weapons: windup + release → free flight (world frame), deterministic in u
  function throwSeq(u, t) {
    const g = Rig.guard(sinceReveal(t));
    const wind = Rig.guard(0);
    wind.p.paws.FR = [0.22, 2.0, -0.55];
    wind.p.paws.FL = [0.6, 1.25, 0.6];
    wind.p.pitch = 0.98;
    wind.p.hPitch = 0.12;
    wind.p.earL = wind.p.earR = -0.2;
    wind.sword = { paw: 'FR', dir: Rig.norm([-0.6, 0.75, -0.25]), twist: 0 };
    wind.shield = { paw: 'FL', face: Rig.norm([0.55, 0.25, 0.8]), up: [0, 1, 0] };
    const follow = Rig.guard(0);
    follow.p.paws.FR = [1.15, 1.25, -0.3];
    follow.p.paws.FL = [1.1, 1.35, 0.5];
    follow.p.pitch = 0.82;
    const ball = Rig.ball(0);

    let p;
    if (u < 0.32) p = Rig.mix(g.p, wind.p, Rig.easeIO(u / 0.32));
    else if (u < 0.62) p = Rig.mix(wind.p, follow.p, Rig.easeIO((u - 0.32) / 0.3));
    else p = Rig.mix(follow.p, ball.p, Rig.easeIO(Rig.range(u, 0.62, 1.75)));
    if (u > 1.3) { p.blink = 1; p.happy = 1; }

    const fr = { p, yaw: Rig.YAW, lift: ball.lift * Rig.easeIO(Rig.range(u, 0.9, 1.9)) };
    const SW_T = 0.32, SH_T = 0.42;
    const rel = Rig.finalize({ ...wind, p: Rig.mix(g.p, wind.p, 1) }).world; // frames at release
    if (u < SW_T) fr.sword = { paw: 'FR', dir: Rig.norm(Rig.mix(Rig.guard(0).sword.dir, wind.sword.dir, Rig.easeIO(u / SW_T))), twist: 0 };
    else if (u - SW_T < 1.3) {
      const dt = u - SW_T;
      const s0 = rel.sword;
      const C = Rig.add(Rig.add(s0.C, Rig.mul([2.4, 7.2, 2.8], dt)), [0, -8 * dt * dt, 0]);
      const a = -15 * dt;
      fr.sword = { C, D: Rig.rotAxis(s0.D, s0.Z, a), Z: s0.Z };
    }
    if (u < SH_T) fr.shield = { paw: 'FL', face: Rig.norm(Rig.mix(Rig.guard(0).shield.face, wind.shield.face, Rig.easeIO(u / SH_T))), up: [0, 1, 0] };
    else if (u - SH_T < 1.3) {
      const dt = u - SH_T;
      const s0 = rel.shield;
      const C = Rig.add(Rig.add(s0.C, Rig.mul([-2.6, 6.4, 2.2], dt)), [0, -8 * dt * dt, 0]);
      const ax = Rig.norm([0.25, 1, 0.3]);
      fr.shield = { C, F: Rig.rotAxis(s0.F, ax, 11 * dt), Z: Rig.rotAxis(s0.Z, ax, 11 * dt) };
    }
    return fr;
  }

  // uncurl back to guard; sword and shield drop from the sky into the paws
  function uncurlSeq(u, t) {
    const wrap = ((M.spin0 % (2 * Math.PI)) + 3 * Math.PI) % (2 * Math.PI) - Math.PI;
    const g = Rig.guard(sinceReveal(t));
    const b = Rig.ball(0);
    const k = Rig.easeIO(Rig.range(u, 0.05, 0.85));
    const p = Rig.mix(b.p, g.p, k);
    if (u < 0.5) { p.blink = 1; p.happy = 1; } else { p.happy = 0; }
    const land = Math.sin(Rig.range(u, 1.02, 1.3) * Math.PI);
    p.paws.FR = Rig.add(p.paws.FR, [0, -0.1 * land, 0]);
    p.paws.FL = Rig.add(p.paws.FL, [0, -0.08 * land, 0]);
    p.earL = p.earR = 0.3 * land;
    const fr = { p, yaw: Rig.YAW + wrap * (1 - Rig.easeIO(Rig.range(u, 0, 0.8))), lift: b.lift * (1 - Rig.easeIO(Rig.range(u, 0, 0.6))) };
    if (u > 0.4) {
      const f = Rig.range(u, 0.4, 1.02);
      const drop = 6.5 * (1 - f) * (1 - f);
      fr.sword = { paw: 'FR', dir: g.sword.dir, twist: 0, drop, tumble: (1 - f) * 9 };
      fr.shield = { paw: 'FL', face: g.shield.face, up: [0, 1, 0], drop: drop * 1.08, tumble: (1 - f) * 7 };
    }
    return fr;
  }

  // resolve "drop/tumble" held weapons into free frames once the paw positions are known
  function dropWeapons(fr) {
    const sw = fr.sword, sh = fr.shield;
    if (!(sw && sw.drop != null) && !(sh && sh.drop != null)) return fr;
    const held = Rig.finalize(fr).world;
    const o = { ...fr };
    if (sw && sw.drop != null) {
      const s = held.sword;
      o.sword = { C: Rig.add(s.C, [0, sw.drop, 0]), D: Rig.rotAxis(s.D, s.Z, sw.tumble), Z: s.Z };
    }
    if (sh && sh.drop != null) {
      const s = held.shield;
      o.shield = { C: Rig.add(s.C, [0, sh.drop, 0]), F: Rig.rotAxis(s.F, s.Z, sh.tumble), Z: s.Z };
    }
    return o;
  }

  // ---------- loader: May May runs across and leaves paw prints ----------
  const RUN_FROM = -3.6, RUN_TO = 3.8;
  const RUN_D = (RUN_TO - RUN_FROM) / Rig.RUN_SPEED;
  let runPhasePrev = null;
  const prints = $('prints');
  function spawnPrint(sx, sy) {
    const el = document.createElement('div');
    el.className = 'print';
    el.style.left = `${sx}px`;
    el.style.top = `${sy}px`;
    el.style.transform = 'rotate(90deg)';
    el.innerHTML = PRINT_SVG;
    prints.appendChild(el);
    if (!capture) setTimeout(() => el.remove(), 2400);
  }

  function evaluate(t) {
    let fr;
    const u = t - M.t0;
    switch (M.mode) {
      case 'run': {
        const lt = t % (RUN_D + 0.5);
        fr = Rig.run(lt);
        fr.root = [RUN_FROM + Rig.RUN_SPEED * lt, 0, 0.3];
        break;
      }
      case 'guard': fr = Rig.guard(sinceReveal(t)); break;
      case 'fight': fr = Rig.fight(u); break;
      case 'throw':
        fr = throwSeq(u, t);
        if (u >= THROW_D) { setMode('ball', t, { fade: false }); fr = Rig.ball(0); }
        break;
      case 'ball': fr = Rig.ball(u); break;
      case 'uncurl':
        fr = uncurlSeq(u, t);
        if (u >= UNCURL_D) { const n = M.next || 'guard'; setMode(n, t, { fade: n !== 'guard' }); }
        break;
    }
    if (M.from) {
      const k = (t - M.fromT) / FADE;
      if (k >= 1) M.from = null;
      else fr = { ...fr, p: Rig.mix(M.from.p, fr.p, Rig.smooth(k)), yaw: Rig.lerp(M.from.yaw, fr.yaw, Rig.smooth(k)), lift: Rig.lerp(M.from.lift, fr.lift || 0, Rig.smooth(k)) };
    }
    last = { p: fr.p, yaw: fr.yaw, lift: fr.lift || 0 };
    return dropWeapons(fr);
  }

  // ---------- connection state ----------
  let S = null;            // last snapshot from main
  let stats = null;
  const LABELS = { wireguard: 'WireGuard', amneziawg: 'AmneziaWG', vless: 'VLESS', vmess: 'VMess', trojan: 'Trojan', shadowsocks: 'Shadowsocks', hysteria2: 'Hysteria2', openvpn: 'OpenVPN' };
  const ORDER = Object.keys(LABELS);
  const state = () => (S && S.status ? S.status.state : 'idle');

  const STATE_TEXT = { idle: 'Not protected', connecting: 'Connecting…', connected: 'Protected', disconnecting: 'Disconnecting…' };
  let lastError = null;

  function applyState(snap) {
    const prev = state();
    S = snap;
    const st = state();
    document.body.classList.remove('s-idle', 's-connecting', 's-connected', 's-disconnecting');
    document.body.classList.add(`s-${st}`);
    const reconnecting = st === 'connecting' && S.status.since;
    $('stateText').textContent = reconnecting ? 'Reconnecting…' : STATE_TEXT[st] || st;
    $('paw').setAttribute('aria-label', st === 'idle' ? 'Connect' : 'Disconnect');
    if (st === 'idle') stats = null;
    const v = S.vault;
    const active = v.active;
    $('protoLabel').textContent = active ? LABELS[active] : 'No key';
    $('protoChip').disabled = st !== 'idle';
    if (st !== prev && revealT != null) want(st, now());
    if (S.status.error && S.status.error !== lastError) toast(S.status.error);
    lastError = S.status.error || null;
    if (v.error) toast(v.error);
    renderStats();
    renderKeys();
  }

  function fmtRate(bps) {
    const bits = bps * 8;
    if (bits >= 1e9) return `${(bits / 1e9).toFixed(1)} Gb/s`;
    if (bits >= 1e6) return `${(bits / 1e6).toFixed(bits >= 1e8 ? 0 : 1)} Mb/s`;
    return `${Math.round(bits / 1e3)} Kb/s`;
  }
  function fmtTime(ms) {
    const s = Math.max(0, Math.floor(ms / 1000));
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = s % 60;
    const p = (n) => String(n).padStart(2, '0');
    return h ? `${h}:${p(m)}:${p(x)}` : `${p(m)}:${p(x)}`;
  }
  let clockNow = () => Date.now();
  function renderStats() {
    if (!S) return;
    const on = state() === 'connected';
    const region = S.vault.region;
    $('sRegion').textContent = region || 'Set region';
    $('sRegion').classList.toggle('dim', !region);
    $('sPing').textContent = on && stats && stats.ping >= 0 ? `${stats.ping} ms` : '—';
    $('sSpeed').innerHTML = on && stats ? `<b>↓ ${fmtRate(stats.rxRate)}</b><small>↑ ${fmtRate(stats.txRate)}</small>` : '<b>—</b>';
    $('sTime').textContent = on && S.status.since ? fmtTime(clockNow() - S.status.since) : '—';
  }

  let toastTimer = null;
  function toast(text) {
    const el = $('toast');
    el.textContent = text;
    el.title = text;
    el.hidden = false;
    clearTimeout(toastTimer);
    if (!capture) toastTimer = setTimeout(() => (el.hidden = true), 5200);
  }

  // ---------- actions ----------
  $('paw').addEventListener('click', async () => {
    if (revealT == null) return;
    const st = state();
    if (st === 'disconnecting') return;
    if (st === 'connecting' || st === 'connected') { await api.disconnect(); return; }
    const r = await api.connect();
    if (r.error === 'nokey') { openSheet('keysSheet'); note('Import a key from your VPS first.', true); }
    else if (r.error === 'nohelper') openSheet('installSheet');
    else if (r.error) toast(r.error);
  });

  $('protoChip').addEventListener('click', (e) => {
    e.stopPropagation();
    const menu = $('protoMenu');
    if (!menu.hidden) { menu.hidden = true; return; }
    const v = S.vault;
    const items = ORDER.filter((k) => v.keys[k]).map((k) =>
      `<button type="button" data-kind="${k}" class="${k === v.active ? 'on' : ''}"><span>${LABELS[k]}</span><small>${k === 'wireguard' ? 'main' : ''}</small></button>`);
    items.push('<button type="button" data-add><span>Add key…</span><small>⌘K</small></button>');
    menu.innerHTML = items.join('');
    menu.hidden = false;
  });
  $('protoMenu').addEventListener('click', async (e) => {
    const b = e.target.closest('button');
    if (!b) return;
    $('protoMenu').hidden = true;
    if (b.dataset.add != null) openSheet('keysSheet');
    else await api.setPrefs({ active: b.dataset.kind });
  });
  document.addEventListener('click', (e) => { if (!e.target.closest('#protoMenu')) $('protoMenu').hidden = true; });

  // ---------- sheets ----------
  function openSheet(id) {
    for (const s of document.querySelectorAll('.sheet')) s.hidden = s.id !== id;
    $('scrim').hidden = false;
    if (id === 'keysSheet') { renderKeys(); note(''); }
  }
  function closeSheets() {
    for (const s of document.querySelectorAll('.sheet')) s.hidden = true;
    $('scrim').hidden = true;
  }
  $('keysBtn').addEventListener('click', () => ($('keysSheet').hidden ? openSheet('keysSheet') : closeSheets()));
  $('scrim').addEventListener('click', closeSheets);
  for (const b of document.querySelectorAll('[data-close]')) b.addEventListener('click', closeSheets);
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeSheets();
    if (e.metaKey && e.key.toLowerCase() === 'k') { e.preventDefault(); openSheet('keysSheet'); }
  });

  function note(text, err) {
    const el = $('importNote');
    el.textContent = text;
    el.hidden = !text;
    el.classList.toggle('err', !!err);
  }
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

  function renderKeys() {
    if (!S) return;
    const v = S.vault;
    $('protoList').innerHTML = ORDER.map((k) => {
      const s = v.keys[k];
      const cls = [s ? 'ready' : '', k === v.active ? 'active' : ''].join(' ');
      const right = s ? `<button type="button" data-use="${k}">use</button><button type="button" data-remove="${k}">remove</button>` : '';
      return `<li class="${cls}"><span class="dot"></span><span class="name">${LABELS[k]}${s ? `<small>${esc(s.server)}</small>` : k === 'wireguard' ? '<small>main</small>' : ''}</span>${right}</li>`;
    }).join('');
    if (document.activeElement !== $('region')) $('region').value = v.region || '';
    $('killSwitch').checked = !!v.killSwitch;
    $('allowLan').checked = !!v.allowLan;
    $('allowLan').disabled = !v.killSwitch;
    const h = S.helper;
    $('helperText').textContent = S.demo ? 'Demo mode' : !h.binary ? 'Not built — run npm run build:daemon' : h.installed ? (h.running ? `Running · v${h.version}${h.outdated ? ' (update ready)' : ''}` : 'Installed · starting…') : 'Not installed';
    $('helperBtn').textContent = h.installed && !h.outdated ? 'Remove' : h.outdated ? 'Update' : 'Install';
    $('helperBtn').disabled = S.demo || !h.binary;
  }

  async function doImport(promise) {
    note('Checking…');
    const r = await promise;
    if (r.cancelled) return note('');
    if (r.error) return note(r.error, true);
    $('keyText').value = '';
    const ign = r.summary.ignored && r.summary.ignored.length ? ` Ignored for safety: ${r.summary.ignored.join(', ')}.` : '';
    note(`${r.summary.label} key for ${r.summary.server} saved.${ign}`);
  }
  $('importBtn').addEventListener('click', () => doImport(api.importKey($('keyText').value)));
  $('fileBtn').addEventListener('click', () => doImport(api.importFile()));
  $('protoList').addEventListener('click', async (e) => {
    const b = e.target.closest('button');
    if (!b) return;
    if (b.dataset.use) await api.setPrefs({ active: b.dataset.use });
    if (b.dataset.remove) await api.removeKey(b.dataset.remove);
  });
  $('region').addEventListener('change', () => api.setPrefs({ region: $('region').value }));
  $('killSwitch').addEventListener('change', () => api.setPrefs({ killSwitch: $('killSwitch').checked }));
  $('allowLan').addEventListener('change', () => api.setPrefs({ allowLan: $('allowLan').checked }));
  $('helperBtn').addEventListener('click', async () => {
    const h = S.helper;
    const r = h.installed && !h.outdated ? await api.uninstallHelper() : await api.installHelper();
    if (r.error && r.error !== 'cancelled') note(r.error, true);
  });
  $('installBtn').addEventListener('click', async () => {
    $('installBtn').disabled = true;
    const r = await api.installHelper();
    $('installBtn').disabled = false;
    if (r.error) { if (r.error !== 'cancelled') toast(r.error); return; }
    closeSheets();
    toast('Guard post is up. Tap the paw.');
  });
  $('eraseBtn').addEventListener('click', async () => {
    if (!confirm('Erase everything? This removes your keys, the helper and every setting. May VPN will be as if it was never here.')) return;
    const r = await api.erase();
    if (r.error) note(r.error, true); else note('Everything is gone.');
  });

  api.onState(applyState);
  api.onStats((s) => { stats = s; renderStats(); });
  let ready = false;
  api.state().then((s) => { applyState(s); ready = true; });

  // ---------- reveal ----------
  function reveal(t) {
    revealT = t;
    document.body.classList.remove('loading');
    if (!capture) setTimeout(() => (prints.innerHTML = ''), 2400);
    const st = state();
    M.mode = st === 'connected' ? 'ball' : st === 'connecting' ? 'fight' : 'guard';
    M.t0 = t;
    M.from = null;
  }

  // ---------- frame ----------
  let lastSec = -1;
  let live = false; // capture mode normally freezes the clock; __vpn.live() runs it for scripted flows
  function frame() {
    const t = now();
    if (revealT == null && ready && t > Math.max(2.2, RUN_D * 0.86) && (!capture || live)) reveal(t);
    const fr = evaluate(t);
    const f = Rig.finalize(fr);

    if (M.mode === 'run') {
      const lt = t % (RUN_D + 0.5);
      for (let i = 0; i < 4; i++) {
        const k = Rig.LEGS[i];
        const ph = Rig.runPhase(lt, k);
        if (runPhasePrev && ph < runPhasePrev[i] && lt < RUN_D) {
          const w = f.world.paws[i];
          const s = Rig.project([w[0] + 0.05, 0, w[2]]);
          spawnPrint(s[0], s[1]);
        }
        (runPhasePrev = runPhasePrev || [])[i] = ph;
      }
    }

    const st = state();
    const flow = st === 'connected' ? 2.6 : st === 'connecting' ? 1.6 : 1;
    const intro = revealT == null ? 10 : t - revealT;
    renderer.render(f, t, {
      intro,
      btn: [-1, -1, -1, -1],
      mode: M.mode === 'run' ? 1 : 0,
      field: [96, 432, 372, flow],
    });
    const sec = Math.floor(Date.now() / 1000);
    if (sec !== lastSec) { lastSec = sec; renderStats(); }
  }

  const fps = { n: 0, worst: 0, last: 0 };
  window.__fps = () => fps;
  function loop() {
    const tn = performance.now();
    if (fps.last) { fps.n++; fps.worst = Math.max(fps.worst, tn - fps.last); }
    fps.last = tn;
    frame();
    requestAnimationFrame(loop);
  }

  // ---------- QA hooks (scripted captures) ----------
  window.__vpn = {
    // freeze the clock at t seconds of the *current* mode (or absolute when mode is given)
    at(mode, u, st) {
      if (st) applyState({ ...S, status: st === 'connected' ? { state: st, kind: 'wireguard', since: Date.now() - 754000 } : { state: st } });
      if (mode === 'run') { revealT = null; document.body.classList.add('loading'); M.mode = 'run'; runPhasePrev = null; frozen = u; frame(); return; }
      if (revealT == null) reveal(0);
      document.body.classList.remove('loading');
      M.mode = mode; M.t0 = 0; M.from = null; M.next = 'guard'; M.spin0 = 1.3;
      revealT = mode === 'guard' ? 0 : -10;
      frozen = u; frame();
    },
    runPrints(upTo) { // replay the loader up to a time so its prints are on screen
      prints.innerHTML = ''; runPhasePrev = null; revealT = null; M.mode = 'run';
      document.body.classList.add('loading');
      for (let x = 0; x <= upTo; x += 1 / 60) { frozen = x; frame(); }
    },
    stats(s) { stats = s; renderStats(); },
    clock(ms) { clockNow = () => ms; },
    open: openSheet,
    close: closeSheets,
    toast,
    live() { frozen = null; tStart = performance.now(); prints.innerHTML = ''; runPhasePrev = null; if (!live) { live = true; loop(); } },
  };
  if (capture) { frozen = 0.5; frame(); } else loop();
})();
