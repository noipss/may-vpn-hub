const { app, BrowserWindow, ipcMain, safeStorage, dialog, session } = require('electron');
const path = require('path');
const fs = require('fs');
const os = require('os');
const { execFile } = require('child_process');
const { Vault } = require('./electron/vault');
const { Helper } = require('./electron/helper');
const { DemoHelper } = require('./electron/demo');

// QA:
//   electron . --demo                                         fake helper, temp vault
//   electron . --demo --script=steps.json --out=./shots       scripted captures
//     steps: [{ "js": "..." } | { "wait": ms } | { "shot": "name" }]
const arg = (name) => {
  const hit = process.argv.find((a) => a.startsWith(`--${name}=`));
  return hit ? hit.slice(name.length + 3) : null;
};
const demo = process.argv.includes('--demo');
const script = arg('script');
const offscreen = !!script;
const outDir = arg('out') || path.join(__dirname, 'shots');
const [W, H] = (arg('size') || '480x800').split('x').map(Number);

app.setName('May VPN');

// ---------- no traces: Chromium's profile lives in a temp dir that is wiped on exit ----------
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'mayvpn-'));
for (const k of ['userData', 'sessionData', 'crashDumps', 'logs']) {
  try { app.setPath(k, path.join(scratch, k)); } catch (_) {}
}
app.commandLine.appendSwitch('disable-http-cache');
app.commandLine.appendSwitch('disable-breakpad');
function wipeScratch() { try { fs.rmSync(scratch, { recursive: true, force: true }); } catch (_) {} }
process.on('exit', wipeScratch);

const vaultFile = demo ? path.join(scratch, 'vault') : path.join(app.getPath('appData'), 'May VPN', 'vault');
const vault = new Vault(vaultFile, {
  available: () => safeStorage.isEncryptionAvailable(),
  encrypt: (s) => safeStorage.encryptString(s),
  decrypt: (b) => safeStorage.decryptString(b),
});

const BIN = path.join(__dirname, 'daemon', 'bin', 'mayvpnd');
const helper = demo ? new DemoHelper() : new Helper({ bin: BIN });

let win = null;
let status = { state: 'idle' };
let stats = null;
let bundled = null;

const send = (ch, data) => { if (win && !win.isDestroyed()) win.webContents.send(ch, data); };

function snapshot() {
  return {
    demo,
    helper: {
      installed: helper.installed(),
      running: helper.connected,
      version: helper.version,
      outdated: !!(helper.connected && bundled && helper.version && helper.version !== bundled),
      binary: demo || fs.existsSync(BIN),
    },
    status,
    stats,
    vault: vault.meta(),
  };
}
const pushState = () => send('vpn:state', snapshot());

helper.on('up', (r) => { if (r && r.status) status = r.status; pushState(); });
helper.on('down', () => { status = { state: 'idle', error: status.state !== 'idle' ? 'The helper stopped.' : undefined }; stats = null; pushState(); });
helper.on('status', (s) => {
  status = s;
  if (s.state === 'idle') stats = null;
  pushState();
});
helper.on('stats', (s) => { stats = s; send('vpn:stats', s); });

// ---------- IPC ----------
ipcMain.handle('vpn:state', () => snapshot());

ipcMain.handle('vpn:connect', async () => {
  const kind = vault.active();
  if (!kind) return { error: 'nokey' };
  if (!helper.installed()) return { error: 'nohelper' };
  if (!helper.connected) return { error: 'Helper is starting — try again in a second.' };
  const { killSwitch, allowLan } = vault.data;
  try {
    await helper.connect(vault.key(kind), { killSwitch, allowLan });
    return { ok: true };
  } catch (e) { return { error: e.message }; }
});

ipcMain.handle('vpn:disconnect', async () => {
  try { await helper.disconnect(); return { ok: true }; } catch (e) { return { error: e.message }; }
});

async function importKey(text) {
  text = String(text || '').trim();
  if (!text) return { error: 'Paste a key first.' };
  if (text.length > 256 * 1024) return { error: 'That is too big to be a key.' };
  try {
    const summary = await helper.parse(text);
    vault.put(text, summary);
    pushState();
    return { ok: true, summary };
  } catch (e) { return { error: e.message }; }
}
ipcMain.handle('vault:import', (_e, text) => importKey(text));
ipcMain.handle('vault:importFile', async () => {
  const r = await dialog.showOpenDialog(win, {
    title: 'Choose a key from your VPS',
    properties: ['openFile'],
    filters: [{ name: 'VPN keys', extensions: ['conf', 'ovpn', 'txt'] }, { name: 'All files', extensions: ['*'] }],
  });
  if (r.canceled || !r.filePaths[0]) return { cancelled: true };
  const st = fs.statSync(r.filePaths[0]);
  if (st.size > 256 * 1024) return { error: 'That is too big to be a key.' };
  return importKey(fs.readFileSync(r.filePaths[0], 'utf8'));
});
ipcMain.handle('vault:remove', (_e, kind) => {
  try { vault.remove(kind); } catch (e) { return { error: e.message }; }
  pushState();
  return { ok: true };
});
ipcMain.handle('vault:set', (_e, patch) => {
  try { vault.set(patch || {}); } catch (e) { return { error: e.message }; }
  pushState();
  return { ok: true };
});

ipcMain.handle('helper:install', async () => {
  try {
    await helper.install();
    helper.stop();
    helper.start();
    pushState();
    return { ok: true };
  } catch (e) { return { error: e.message }; }
});
ipcMain.handle('helper:uninstall', async () => {
  try {
    if (status.state !== 'idle') await helper.disconnect().catch(() => {});
    await helper.uninstall();
    pushState();
    return { ok: true };
  } catch (e) { return { error: e.message }; }
});

// Erase everything: tunnel down, helper gone, vault file + its keychain item deleted.
ipcMain.handle('app:erase', async () => {
  try {
    if (status.state !== 'idle') await helper.disconnect().catch(() => {});
    if (helper.installed()) await helper.uninstall();
  } catch (e) { return { error: e.message }; }
  vault.erase();
  if (!demo) {
    await new Promise((r) => execFile('/usr/bin/security', ['delete-generic-password', '-s', `${app.getName()} Safe Storage`], () => r()));
  }
  pushState();
  return { ok: true };
});

// ---------- window ----------
function createWindow() {
  win = new BrowserWindow({
    width: W,
    height: H,
    minWidth: 380,
    minHeight: 633,
    useContentSize: true,
    title: 'May VPN',
    backgroundColor: '#F2EBDD',
    titleBarStyle: 'hiddenInset',
    trafficLightPosition: { x: 16, y: 16 },
    show: !offscreen,
    webPreferences: {
      contextIsolation: true,
      sandbox: true,
      preload: path.join(__dirname, 'preload.js'),
      offscreen,
      spellcheck: false,
      partition: 'mayvpn', // no "persist:" → in-memory session
    },
  });
  win.setAspectRatio(W / H);
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  win.webContents.on('will-navigate', (e) => e.preventDefault());
  win.loadFile(path.join(__dirname, 'src', 'index.html'), script ? { query: { capture: '1' } } : undefined);
  if (script) runScript(win, JSON.parse(fs.readFileSync(script, 'utf8')));
  if (process.argv.includes('--fps')) {
    setTimeout(async () => {
      const a = await win.webContents.executeJavaScript('({...window.__fps()})');
      setTimeout(async () => {
        const b = await win.webContents.executeJavaScript('({...window.__fps()})');
        console.log('fps', (b.n - a.n) / 5, 'worst frame ms', b.worst.toFixed(1));
        app.quit();
      }, 5000);
    }, 4000);
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function capture(name) {
  const img = await win.webContents.capturePage();
  const file = path.join(outDir, `${name}.png`);
  fs.writeFileSync(file, img.resize({ width: W, height: H }).toPNG());
  console.log('saved', file);
}
async function runScript(w, steps) {
  await new Promise((r) => w.webContents.once('did-finish-load', r));
  await sleep(800);
  fs.mkdirSync(outDir, { recursive: true });
  for (const s of steps) {
    if (s.wait) await sleep(s.wait);
    if (s.js) {
      const res = await w.webContents.executeJavaScript(s.js);
      if (res !== undefined) console.log('js:', JSON.stringify(res));
    }
    if (s.shot) await capture(s.shot);
  }
  app.quit();
}

app.whenReady().then(async () => {
  // deny every permission request (camera, notifications, geolocation…) — a VPN UI needs none
  session.fromPartition('mayvpn').setPermissionRequestHandler((_wc, _p, cb) => cb(false));
  vault.load();
  bundled = await helper.bundledVersion();
  helper.start();
  createWindow();
});

// Memory-only life: quitting the app ends the connection first.
let quitting = false;
app.on('before-quit', (e) => {
  if (quitting) return;
  if (status.state !== 'idle' && helper.connected) {
    e.preventDefault();
    quitting = true;
    Promise.race([helper.disconnect().catch(() => {}), sleep(5000)]).then(() => app.quit());
  }
});
app.on('will-quit', () => { helper.stop(); wipeScratch(); });
app.on('window-all-closed', () => app.quit());
