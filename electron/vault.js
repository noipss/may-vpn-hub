// The only file May VPN keeps: connection keys + three preferences, encrypted with a key that
// lives in the macOS keychain (safeStorage). The renderer never sees key text — only summaries.
const fs = require('fs');
const path = require('path');

const KINDS = ['wireguard', 'amneziawg', 'vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria2', 'openvpn'];
const DEFAULTS = { v: 1, region: '', killSwitch: true, allowLan: false, active: null, keys: {} };

class Vault {
  // crypto: { encrypt(string) -> Buffer, decrypt(Buffer) -> string, available() -> bool }
  constructor(file, crypto) {
    this.file = file;
    this.crypto = crypto;
    this.data = { ...DEFAULTS, keys: {} };
    this.loadError = null;
  }

  load() {
    if (!fs.existsSync(this.file)) return this;
    try {
      const plain = this.crypto.decrypt(fs.readFileSync(this.file));
      this.data = { ...DEFAULTS, ...JSON.parse(plain) };
    } catch (e) {
      // keychain item gone or file damaged: start empty, never overwrite silently until the user imports
      this.loadError = 'Saved key could not be unlocked — import it again.';
    }
    return this;
  }

  save() {
    if (!this.crypto.available()) throw new Error('Keychain encryption is not available — the key cannot be stored safely.');
    fs.mkdirSync(path.dirname(this.file), { recursive: true, mode: 0o700 });
    const tmp = this.file + '.tmp';
    fs.writeFileSync(tmp, this.crypto.encrypt(JSON.stringify(this.data)), { mode: 0o600 });
    fs.renameSync(tmp, this.file);
    this.loadError = null;
  }

  // what the UI may see
  meta() {
    const d = this.data;
    const keys = {};
    for (const k of KINDS) if (d.keys[k]) keys[k] = d.keys[k].summary;
    return { region: d.region, killSwitch: d.killSwitch, allowLan: d.allowLan, active: this.active(), keys, error: this.loadError };
  }

  active() {
    const d = this.data;
    if (d.active && d.keys[d.active]) return d.active;
    return KINDS.find((k) => d.keys[k]) || null;
  }

  put(text, summary) {
    if (!KINDS.includes(summary.kind)) throw new Error(`Unknown key type ${summary.kind}`);
    this.data.keys[summary.kind] = { text, summary };
    this.data.active = summary.kind;
    if (!this.data.region && summary.name) this.data.region = summary.name.slice(0, 40);
    this.save();
  }

  key(kind) {
    const k = this.data.keys[kind];
    return k ? k.text : null;
  }

  remove(kind) {
    delete this.data.keys[kind];
    if (this.data.active === kind) this.data.active = null;
    this.save();
  }

  set(patch) {
    const d = this.data;
    if (typeof patch.region === 'string') d.region = patch.region.trim().slice(0, 40);
    if (typeof patch.killSwitch === 'boolean') d.killSwitch = patch.killSwitch;
    if (typeof patch.allowLan === 'boolean') d.allowLan = patch.allowLan;
    if (patch.active && d.keys[patch.active]) d.active = patch.active;
    this.save();
  }

  erase() {
    this.data = { ...DEFAULTS, keys: {} };
    try { fs.rmSync(this.file, { force: true }); } catch (_) {}
    try { fs.rmdirSync(path.dirname(this.file)); } catch (_) {}
  }
}

module.exports = { Vault, KINDS };
