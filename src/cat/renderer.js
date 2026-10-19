// WebGL2 driver for the three passes in shaders.js.
// opts: { pitch: scanline pitch in design px, mode: 0 with line field | 1 cat + floor only }
window.CatRenderer = function (canvas, opts = {}) {
  const gl = canvas.getContext('webgl2', { alpha: true, premultipliedAlpha: true, antialias: false, preserveDrawingBuffer: true });
  if (!gl) throw new Error('WebGL2 unavailable');
  const S = window.CatShaders;
  const PITCH = opts.pitch || 9.25; // scanline pitch, design px (9.25 measured on the reference)
  const CELL = PITCH / 2;           // pixel cell, design px
  const MODE = opts.mode || 0;

  function compile(type, src) {
    const sh = gl.createShader(type);
    gl.shaderSource(sh, src);
    gl.compileShader(sh);
    if (!gl.getShaderParameter(sh, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(sh));
    return sh;
  }
  function program(fs) {
    const p = gl.createProgram();
    gl.attachShader(p, compile(gl.VERTEX_SHADER, S.VERT));
    gl.attachShader(p, compile(gl.FRAGMENT_SHADER, fs));
    gl.bindAttribLocation(p, 0, 'aPos');
    gl.linkProgram(p);
    if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(p));
    const u = {};
    const n = gl.getProgramParameter(p, gl.ACTIVE_UNIFORMS);
    for (let i = 0; i < n; i++) {
      const info = gl.getActiveUniform(p, i);
      const name = info.name.replace(/\[0\]$/, '');
      u[name] = gl.getUniformLocation(p, info.name);
    }
    return { p, u };
  }
  const progScene = program(S.SCENE);
  const progRows = program(S.ROWS);
  const progComp = program(S.COMPOSITE);

  const vao = gl.createVertexArray();
  gl.bindVertexArray(vao);
  const vbo = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, vbo);
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
  gl.enableVertexAttribArray(0);
  gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);

  function target(w, h) {
    const tex = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, tex);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, null);
    for (const [k, v] of [[gl.TEXTURE_MIN_FILTER, gl.NEAREST], [gl.TEXTURE_MAG_FILTER, gl.NEAREST], [gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE], [gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE]])
      gl.texParameteri(gl.TEXTURE_2D, k, v);
    const fb = gl.createFramebuffer();
    gl.bindFramebuffer(gl.FRAMEBUFFER, fb);
    gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, tex, 0);
    return { tex, fb, w, h };
  }

  let L = null, rtScene = null, rtRows = null;

  function resize(view) {
    // view: { W, H, dpr, s, ox, oy } — window CSS size, device ratio, design scale and offset
    canvas.width = Math.round(view.W * view.dpr);
    canvas.height = Math.round(view.H * view.dpr);
    const dx0 = -view.ox / view.s, dx1 = (view.W - view.ox) / view.s;
    const dy0 = -view.oy / view.s, dy1 = (view.H - view.oy) / view.s;
    const i0 = Math.floor(dx0 / CELL), i1 = Math.ceil(dx1 / CELL);
    const k0 = Math.floor(dy0 / PITCH), k1 = Math.ceil(dy1 / PITCH);
    L = { ...view, i0, j0: k0 * 2, rtW: i1 - i0, rtH: (k1 - k0) * 2, k0, rowN: k1 - k0 };
    if (rtScene) { gl.deleteTexture(rtScene.tex); gl.deleteFramebuffer(rtScene.fb); gl.deleteTexture(rtRows.tex); gl.deleteFramebuffer(rtRows.fb); }
    rtScene = target(L.rtW, L.rtH);
    rtRows = target(L.rowN, 1);
  }

  const v3 = (loc, v) => loc && gl.uniform3f(loc, v[0], v[1], v[2]);

  function render(frame, time, extra) {
    const U = frame.uniforms;
    const A = frame.anchors;
    const CAM = frame.cam;
    gl.bindVertexArray(vao);
    gl.disable(gl.BLEND);

    // Pass A — scene cells
    let { p, u } = progScene;
    gl.useProgram(p);
    gl.bindFramebuffer(gl.FRAMEBUFFER, rtScene.fb);
    gl.viewport(0, 0, rtScene.w, rtScene.h);
    gl.uniform2i(u.uCellO, L.i0, L.j0);
    gl.uniform1f(u.uCell, CELL);
    v3(u.uCamP, CAM.P); v3(u.uCamR, CAM.R); v3(u.uCamU, CAM.U); v3(u.uCamF, CAM.F);
    gl.uniform3f(u.uProj, CAM.cx, CAM.cy, CAM.f);
    v3(u.uTorsoC, U.torsoC);
    gl.uniformMatrix3fv(u.uTorsoM, false, window.CatRig.tr(U.torsoM));
    v3(u.uTorsoS, U.torsoS);
    v3(u.uHeadC, U.headC);
    gl.uniformMatrix3fv(u.uHeadM, false, window.CatRig.tr(U.headM));
    gl.uniform3fv(u.uLeg, U.legs.flat());
    gl.uniform3fv(u.uTail, U.tail.flat());
    gl.uniform4fv(u.uFace, U.face);
    gl.uniform4fv(u.uMisc, U.misc);
    gl.uniform4fv(u.uBound, U.bound);
    gl.uniform1f(u.uGround, U.ground);
    gl.uniform4fv(u.uAcc, U.acc);
    v3(u.uSwordC, U.swordC); v3(u.uShieldC, U.shieldC);
    gl.uniformMatrix3fv(u.uSwordM, false, window.CatRig.tr(U.swordM));
    gl.uniformMatrix3fv(u.uShieldM, false, window.CatRig.tr(U.shieldM));
    gl.drawArrays(gl.TRIANGLES, 0, 3);

    // Pass B — silhouette right edge per row
    ({ p, u } = progRows);
    gl.useProgram(p);
    gl.bindFramebuffer(gl.FRAMEBUFFER, rtRows.fb);
    gl.viewport(0, 0, rtRows.w, 1);
    gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D, rtScene.tex);
    gl.uniform1i(u.uScene, 0);
    gl.uniform2i(u.uRt, L.rtW, L.rtH);
    gl.uniform2i(u.uCellO, L.i0, L.j0);
    gl.uniform1i(u.uRowO, L.k0);
    gl.drawArrays(gl.TRIANGLES, 0, 3);

    // Pass C — composite
    ({ p, u } = progComp);
    gl.useProgram(p);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    gl.viewport(0, 0, canvas.width, canvas.height);
    gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D, rtScene.tex);
    gl.activeTexture(gl.TEXTURE1);
    gl.bindTexture(gl.TEXTURE_2D, rtRows.tex);
    gl.uniform1i(u.uScene, 0);
    gl.uniform1i(u.uRows, 1);
    gl.uniform2f(u.uRes, canvas.width, canvas.height);
    gl.uniform1f(u.uDpr, canvas.width / L.W);
    gl.uniform1f(u.uS, L.s);
    gl.uniform1f(u.uP, PITCH);
    gl.uniform1f(u.uCell, CELL);
    gl.uniform2f(u.uOff, L.ox, L.oy);
    gl.uniform2i(u.uCellO, L.i0, L.j0);
    gl.uniform2i(u.uRt, L.rtW, L.rtH);
    gl.uniform1i(u.uRowO, L.k0);
    gl.uniform1i(u.uRowN, L.rowN);
    gl.uniform1f(u.uTime, time);
    gl.uniform1f(u.uIntro, extra.intro);
    gl.uniform1f(u.uGlitch, extra.glitch || 0);
    gl.uniform4fv(u.uBtn, extra.btn);
    gl.uniform2fv(u.uCatX, A.catX);
    gl.uniform1f(u.uGroundY, A.groundY);
    gl.uniform1i(u.uMode, extra.mode != null ? extra.mode : MODE);
    gl.uniform4fv(u.uField, extra.field || [110, 440, 400, 1]);
    gl.uniform3fv(u.uTint, extra.tint || [1, 0.972, 0.918]);
    gl.uniform1f(u.uLineK, PITCH / 9.25);
    gl.uniform1f(u.uK, A.pxPerUnit / 165);
    gl.uniform4fv(u.uClip, extra.clip || [-1e6, -1e6, 1e6, 1e6]);
    gl.clearColor(0, 0, 0, 0);
    gl.clear(gl.COLOR_BUFFER_BIT);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
  }

  return { resize, render, PITCH, CELL };
};
