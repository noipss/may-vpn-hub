// Talks to mayvpnd over its socket / named pipe (JSON lines), installs / removes the system helper
// (launchd, systemd, Windows service) and parses keys with the bundled binary (no admin needed).
const net = require('net');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFile } = require('child_process');
const { EventEmitter } = require('events');

const LABEL = 'com.maymay.vpn.helper';
const WIN_DIR = path.join(process.env.ProgramFiles || 'C:\\Program Files', 'MayVPN');
// where each OS keeps the helper's endpoint and the file that says it is installed
const PLATFORM = {
  darwin: { socket: '/var/run/com.maymay.vpn.sock', marker: `/Library/LaunchDaemons/${LABEL}.plist` },
  linux: { socket: '/run/mayvpn.sock', marker: '/etc/systemd/system/mayvpnd.service' },
  win32: { socket: '\\\\.\\pipe\\mayvpn', marker: path.join(WIN_DIR, 'mayvpnd.exe') },
}[process.platform] || {};
const SOCKET = PLATFORM.socket;
const PLIST = PLATFORM.marker;
const EXE = process.platform === 'win32' ? 'mayvpnd.exe' : 'mayvpnd';

class Helper extends EventEmitter {
  // bin: path to the bundled mayvpnd; socket: override for tests
  constructor({ bin, socket = SOCKET, plist = PLIST } = {}) {
    super();
    this.bin = bin;
    this.socketPath = socket;
    this.plist = plist;
    this.sock = null;
    this.buf = '';
    this.seq = 0;
    this.waiting = new Map();
    this.version = null;
    this.connected = false;
    this.retry = null;
    this.closed = false;
  }

  installed() { return fs.existsSync(this.plist); }

  start() {
    this.closed = false;
    this.dial();
  }

  dial() {
    if (this.closed || this.sock) return;
    const s = net.createConnection(this.socketPath);
    this.sock = s;
    s.setEncoding('utf8');
    s.on('connect', async () => {
      this.connected = true;
      try {
        const r = await this.request({ op: 'hello' });
        this.version = r.version;
        this.emit('up', r);
      } catch (_) {}
    });
    s.on('data', (chunk) => {
      this.buf += chunk;
      let i;
      while ((i = this.buf.indexOf('\n')) >= 0) {
        const line = this.buf.slice(0, i);
        this.buf = this.buf.slice(i + 1);
        let m;
        try { m = JSON.parse(line); } catch (_) { continue; }
        if (m.ev) this.emit(m.ev, m[m.ev]);
        else if (this.waiting.has(m.id)) {
          const { resolve, reject } = this.waiting.get(m.id);
          this.waiting.delete(m.id);
          if (m.ok) resolve(m); else reject(new Error(m.error || 'helper error'));
        }
      }
    });
    const down = () => {
      if (this.sock !== s) return;
      this.sock = null;
      this.buf = '';
      const was = this.connected;
      this.connected = false;
      for (const { reject } of this.waiting.values()) reject(new Error('helper went away'));
      this.waiting.clear();
      if (was) this.emit('down');
      if (!this.closed) this.retry = setTimeout(() => this.dial(), 1200);
    };
    s.on('error', down);
    s.on('close', down);
  }

  stop() {
    this.closed = true;
    clearTimeout(this.retry);
    if (this.sock) this.sock.destroy();
  }

  request(msg, timeout = 30000) {
    if (!this.sock || !this.connected) return Promise.reject(new Error('helper is not running'));
    const id = ++this.seq;
    return new Promise((resolve, reject) => {
      const t = setTimeout(() => { this.waiting.delete(id); reject(new Error('helper did not answer')); }, timeout);
      this.waiting.set(id, {
        resolve: (v) => { clearTimeout(t); resolve(v); },
        reject: (e) => { clearTimeout(t); reject(e); },
      });
      this.sock.write(JSON.stringify({ id, ...msg }) + '\n');
    });
  }

  connect(key, prefs) { return this.request({ op: 'connect', key, prefs }); }
  disconnect() { return this.request({ op: 'disconnect' }); }
  status() { return this.request({ op: 'status' }); }

  // ---------- bundled binary ----------
  run(args, input) {
    return new Promise((resolve, reject) => {
      const p = execFile(this.bin, args, { timeout: 10000, maxBuffer: 1 << 20, windowsHide: true }, (err, stdout, stderr) => {
        if (err && !stdout) return reject(new Error((stderr || err.message).trim()));
        resolve(stdout);
      });
      if (input != null) p.stdin.end(input);
    });
  }

  async parse(text) {
    const out = await this.run(['parse'], text);
    const j = JSON.parse(out);
    if (j.error) throw new Error(j.error);
    return j;
  }

  bundledVersion() { return this.run(['version']).then((s) => s.trim()).catch(() => null); }

  // ---------- elevation, one way per OS ----------
  // macOS: osascript shows the standard password prompt; nothing else is elevated.
  osascript(cmd) {
    const script = `do shell script ${JSON.stringify(cmd)} with administrator privileges`;
    return new Promise((resolve, reject) => {
      execFile('/usr/bin/osascript', ['-e', script], (err, _o, stderr) => {
        if (err) {
          if (/User canceled|-128/.test(stderr)) return reject(new Error('cancelled'));
          // osascript wraps the tool's own message: "execution error: mayvpnd: … (1)"
          const msg = (stderr || err.message).trim().replace(/^.*execution error:\s*/s, '').replace(/\s*\(-?\d+\)$/, '');
          return reject(new Error(msg));
        }
        resolve();
      });
    });
  }

  // Linux: pkexec asks through the desktop's polkit agent. 126 = dismissed, 127 = not authorized.
  pkexec(bin, args) {
    return new Promise((resolve, reject) => {
      execFile('pkexec', [bin, ...args], (err, _o, stderr) => {
        if (!err) return resolve();
        if (err.code === 126) return reject(new Error('cancelled'));
        if (err.code === 'ENOENT') return reject(new Error('pkexec is missing — install polkit (policykit-1) to set up the helper'));
        if (err.code === 127 && !stderr.includes('mayvpnd:')) return reject(new Error('not authorized to install the helper'));
        reject(new Error((stderr || err.message).trim().replace(/^mayvpnd:\s*/, '')));
      });
    });
  }

  // Windows: UAC via PowerShell's RunAs; the elevated process has no console, so it reports
  // errors through a file we read back.
  uac(bin, args) {
    const errFile = path.join(os.tmpdir(), `mayvpn-err-${process.pid}-${Date.now()}.txt`);
    const ps = (v) => `'${String(v).replace(/'/g, "''")}'`;
    const list = [...args, '--errfile', errFile].map(ps).join(',');
    const cmd = `$p = Start-Process -FilePath ${ps(bin)} -ArgumentList ${list} -Verb RunAs -WindowStyle Hidden -Wait -PassThru; exit $p.ExitCode`;
    return new Promise((resolve, reject) => {
      execFile('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', cmd], { windowsHide: true }, (err, _o, stderr) => {
        let msg = '';
        try { msg = fs.readFileSync(errFile, 'utf8').trim(); fs.rmSync(errFile, { force: true }); } catch (_) {}
        if (!err) return resolve();
        if (/cancel/i.test(stderr)) return reject(new Error('cancelled'));
        reject(new Error(msg || (stderr || err.message).trim()));
      });
    });
  }

  // the current user's SID, which the Windows service lets through its pipe
  sid() {
    return new Promise((resolve, reject) => {
      execFile('whoami', ['/user', '/fo', 'csv', '/nh'], { windowsHide: true }, (err, out) => {
        const m = !err && out.match(/"(S-1-[0-9-]+)"/);
        return m ? resolve(m[1]) : reject(new Error('could not read your Windows account SID'));
      });
    });
  }

  // root / SYSTEM must not run a binary out of a protected or user-mounted place (~/Documents on
  // macOS, an AppImage's FUSE mount on Linux), so the elevated step always runs a private copy —
  // which then copies itself (and wintun.dll on Windows) into the system location.
  async withStagedBinary(fn) {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'mayvpn-inst-'));
    const bin = path.join(dir, EXE);
    try {
      fs.copyFileSync(this.bin, bin);
      fs.chmodSync(bin, 0o755);
      const dll = path.join(path.dirname(this.bin), 'wintun.dll');
      if (process.platform === 'win32' && fs.existsSync(dll)) fs.copyFileSync(dll, path.join(dir, 'wintun.dll'));
      return await fn(bin);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  }

  elevate(bin, args) {
    if (process.platform === 'darwin') return this.osascript([bin, ...args].map(shq).join(' '));
    if (process.platform === 'linux') return this.pkexec(bin, args);
    if (process.platform === 'win32') return this.uac(bin, args);
    return Promise.reject(new Error(`${process.platform} is not supported`));
  }

  async install() {
    const owner = process.platform === 'win32' ? ['--sid', await this.sid()] : ['--uid', String(process.getuid())];
    return this.withStagedBinary((bin) => this.elevate(bin, ['install', ...owner]));
  }

  uninstall() { return this.withStagedBinary((bin) => this.elevate(bin, ['uninstall'])); }
}

const shq = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`;

module.exports = { Helper, SOCKET, LABEL, EXE };
