// --demo: a pretend helper with the same surface as Helper, for UI work and screenshots.
// Nothing touches the network.
const { EventEmitter } = require('events');

class DemoHelper extends EventEmitter {
  constructor() {
    super();
    this.connected = true;
    this.version = 'demo';
    this.state = { state: 'idle' };
    this.timers = [];
  }

  installed() { return true; }
  start() { setTimeout(() => this.emit('up', { version: 'demo', status: this.state }), 50); }
  stop() { this.clear(); }
  clear() { for (const t of this.timers) clearTimeout(t), clearInterval(t); this.timers = []; }
  bundledVersion() { return Promise.resolve('demo'); }
  install() { return Promise.resolve(); }
  uninstall() { return Promise.resolve(); }

  set(st) { this.state = st; this.emit('status', st); }

  async parse(text) {
    const t = text.trim();
    const m = t.match(/^(\w+):\/\//);
    const kind = m ? { vless: 'vless', vmess: 'vmess', ss: 'shadowsocks', trojan: 'trojan', hysteria2: 'hysteria2', hy2: 'hysteria2' }[m[1]] : /\[interface\]/i.test(t) ? (/^\s*jc\s*=/im.test(t) ? 'amneziawg' : 'wireguard') : /^remote /m.test(t) ? 'openvpn' : null;
    if (!kind) throw new Error('not a WireGuard / AmneziaWG config, OpenVPN profile or vless:// vmess:// ss:// trojan:// hysteria2:// link');
    const labels = { wireguard: 'WireGuard', amneziawg: 'AmneziaWG', vless: 'VLESS', vmess: 'VMess', shadowsocks: 'Shadowsocks', trojan: 'Trojan', hysteria2: 'Hysteria2', openvpn: 'OpenVPN' };
    return { kind, label: labels[kind], server: 'demo.vps:51820', name: 'Frankfurt' };
  }

  async connect(_key, _prefs) {
    if (this.state.state !== 'idle') throw new Error('already connected — disconnect first');
    this.set({ state: 'connecting', kind: 'wireguard' });
    this.timers.push(setTimeout(() => {
      const since = Date.now();
      this.set({ state: 'connected', kind: 'wireguard', since });
      let rx = 0, tx = 0, rr = 0, tr = 0;
      this.timers.push(setInterval(() => {
        const nr = 180000 + Math.random() * 2400000 * (Math.random() < 0.3 ? 1 : 0.2);
        const nt = 20000 + Math.random() * 180000;
        rr = rr * 0.5 + nr * 0.5; tr = tr * 0.5 + nt * 0.5;
        rx += nr; tx += nt;
        this.emit('stats', { rx, tx, rxRate: rr, txRate: tr, ping: Math.round(38 + Math.random() * 9) });
      }, 1000));
    }, 2600));
    return { ok: true };
  }

  async disconnect() {
    this.clear();
    this.set({ ...this.state, state: 'disconnecting' });
    await new Promise((r) => setTimeout(r, 500));
    this.set({ state: 'idle' });
    return { ok: true };
  }

  async status() { return { status: this.state }; }
}

module.exports = { DemoHelper };
