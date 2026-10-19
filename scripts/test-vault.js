// Vault: encrypted round trip, renderer-safe meta, erase.
const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { Vault } = require('../electron/vault');

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'vault-test-'));
const file = path.join(dir, 'May VPN', 'vault');
const xor = (b) => Buffer.from(b).map((c) => c ^ 0x5a);
const crypto = { available: () => true, encrypt: (s) => xor(Buffer.from(s)), decrypt: (b) => xor(b).toString() };

const v = new Vault(file, crypto).load();
assert.strictEqual(v.meta().active, null);
const conf = '[Interface]\nPrivateKey = SECRET\n';
v.put(conf, { kind: 'wireguard', label: 'WireGuard', server: 'vps:51820', name: 'Frankfurt' });
v.put('vless://u@h:443', { kind: 'vless', label: 'VLESS', server: 'h:443' });
v.set({ active: 'wireguard', killSwitch: false, region: '  DE · Frankfurt  ' });

const raw = fs.readFileSync(file);
assert(!raw.includes('SECRET'), 'key must not be stored in plain text');
assert.strictEqual(fs.statSync(file).mode & 0o777, 0o600);

const back = new Vault(file, crypto).load();
const m = back.meta();
assert.strictEqual(back.key('wireguard'), conf);
assert.strictEqual(m.active, 'wireguard');
assert.strictEqual(m.region, 'DE · Frankfurt');
assert.strictEqual(m.killSwitch, false);
assert.deepStrictEqual(Object.keys(m.keys).sort(), ['vless', 'wireguard']);
assert(!JSON.stringify(m).includes('SECRET'), 'meta must not leak key text');

back.remove('wireguard');
assert.strictEqual(back.meta().active, 'vless');

const broken = new Vault(file, { ...crypto, decrypt: () => { throw new Error('no keychain'); } }).load();
assert(broken.meta().error, 'unreadable vault is reported');

back.erase();
assert(!fs.existsSync(file) && !fs.existsSync(path.dirname(file)), 'erase leaves nothing');
assert.throws(() => new Vault(file, { ...crypto, available: () => false }).put('x', { kind: 'vless' }), /keychain/);
fs.rmSync(dir, { recursive: true, force: true });
console.log('vault OK');
