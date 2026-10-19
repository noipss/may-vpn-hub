// Talks to mayvpnd over its unix socket (JSON lines), installs / removes the launchd helper, and
// parses keys with the bundled binary (no root needed for that).
const net = require('net');
const fs = require('fs');
const { execFile } = require('child_process');
const { EventEmitter } = require('events');

const LABEL = 'com.maymay.vpn.helper';
const SOCKET = '/var/run/com.maymay.vpn.sock';
const PLIST = `/Library/LaunchDaemons/${LABEL}.plist`;

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
      const p = execFile(this.bin, args, { timeout: 10000, maxBuffer: 1 << 20 }, (err, stdout, stderr) => {
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

  // osascript shows the standard macOS password prompt; nothing else is elevated.
  admin(cmd) {
    const script = `do shell script ${JSON.stringify(cmd)} with administrator privileges`;
    return new Promise((resolve, reject) => {
      execFile('/usr/bin/osascript', ['-e', script], (err, _o, stderr) => {
        if (err) {
          if (/User canceled|-128/.test(stderr)) return reject(new Error('cancelled'));
          return reject(new Error((stderr || err.message).trim()));
        }
        resolve();
      });
    });
  }

  install() {
    const uid = process.getuid();
    return this.admin(`${shq(this.bin)} install --uid ${uid}`);
  }

  uninstall() { return this.admin(`${shq(this.bin)} uninstall`); }
}

const shq = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`;

module.exports = { Helper, SOCKET, LABEL };
