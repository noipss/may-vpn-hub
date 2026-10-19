// Helper client against the real mayvpnd, unprivileged: hello, parse, and a connect that must
// fail cleanly (no root → no utun) instead of hanging.
const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');
const { Helper } = require('../electron/helper');

const { EXE } = require('../electron/helper');
const TARGET = `${{ darwin: 'mac', win32: 'win', linux: 'linux' }[process.platform]}-${process.arch}`;
const BIN = path.join(__dirname, '..', 'daemon', 'bin', TARGET, EXE);
const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'mv-'));
const sock = path.join(dir, 's.sock');
const d = spawn(BIN, ['serve', '--socket', sock, '--uid', String(process.getuid())], { stdio: ['ignore', 'pipe', 'pipe'] });
let daemonOut = '';
d.stdout.on('data', (b) => (daemonOut += b));
d.stderr.on('data', (b) => (daemonOut += b));

const key = '[Interface]\nPrivateKey = ' + Buffer.alloc(32, 7).toString('base64') + '\nAddress = 10.8.0.2/32\nPostUp = touch /tmp/pwned\n[Peer]\nPublicKey = ' +
  Buffer.alloc(32, 9).toString('base64') + '\nEndpoint = 127.0.0.1:51820\nAllowedIPs = 0.0.0.0/0\n';

(async () => {
  const h = new Helper({ bin: BIN, socket: sock, plist: path.join(dir, 'none.plist') });
  assert.strictEqual(h.installed(), false);
  const up = new Promise((r) => h.once('up', r));
  h.start();
  const hello = await up;
  assert.strictEqual(hello.version, await h.bundledVersion());
  assert.strictEqual(hello.status.state, 'idle');

  const s = await h.parse(key);
  assert.strictEqual(s.kind, 'wireguard');
  assert.deepStrictEqual(s.ignored, ['postup']);
  await assert.rejects(h.parse('nope'), /not a WireGuard/);

  const states = [];
  const ended = new Promise((r) => h.on('status', (st) => { states.push(st.state); if (st.state === 'idle') r(st); }));
  await h.connect(key, { killSwitch: true });
  const end = await ended;
  assert.deepStrictEqual(states, ['connecting', 'idle']);
  assert(end.error, 'failed connect reports why');
  console.log('  unprivileged connect ended with:', end.error);

  h.stop();
  d.kill('SIGTERM');
  await new Promise((r) => d.on('exit', r));
  assert.strictEqual(daemonOut, '', 'the helper must not print anything');
  assert(!fs.existsSync(sock), 'socket removed on exit');
  assert(!fs.existsSync('/tmp/pwned'), 'PostUp never runs');
  fs.rmSync(dir, { recursive: true, force: true });
  console.log('helper OK');
})().catch((e) => { d.kill(); console.error(e); process.exit(1); });
