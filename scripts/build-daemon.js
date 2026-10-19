// Builds the Go helper for this machine, or for every installer target with --all / --target=win-x64.
// Output: daemon/bin/<mac|win|linux>-<x64|arm64>/mayvpnd[.exe] (+ wintun.dll on Windows) —
// named like electron-builder's ${os}-${arch}, so packaging picks the right one up.
const { execFileSync } = require('child_process');
const path = require('path');

const DAEMON = path.join(__dirname, '..', 'daemon');
const TAGS = 'with_gvisor,with_quic,with_utls,with_openvpn,badlinkname,tfogo_checklinkname0';
// -trimpath and -buildid= keep build-machine paths and ids out of the binary.
const LDFLAGS = '-s -w -buildid= -checklinkname=0';
const GOOS = { mac: 'darwin', win: 'windows', linux: 'linux' };
const GOARCH = { x64: 'amd64', arm64: 'arm64' };
const ALL = ['mac-arm64', 'mac-x64', 'win-x64', 'win-arm64', 'linux-x64', 'linux-arm64'];

const here = `${{ darwin: 'mac', win32: 'win', linux: 'linux' }[process.platform]}-${process.arch}`;
const arg = process.argv.find((a) => a.startsWith('--target='));
const targets = process.argv.includes('--all') ? ALL : arg ? arg.slice(9).split(',') : [here];

for (const t of targets) {
  const [os, arch] = t.split('-');
  if (!GOOS[os] || !GOARCH[arch]) throw new Error(`unknown target ${t}`);
  const out = path.join(DAEMON, 'bin', t);
  const exe = path.join(out, os === 'win' ? 'mayvpnd.exe' : 'mayvpnd');
  const env = { ...process.env, GOOS: GOOS[os], GOARCH: GOARCH[arch], CGO_ENABLED: '0' };
  process.stdout.write(`mayvpnd ${t} … `);
  execFileSync('go', ['build', '-trimpath', '-tags', TAGS, '-ldflags', LDFLAGS, '-o', exe, './cmd/mayvpnd'], { cwd: DAEMON, env, stdio: ['ignore', 'ignore', 'inherit'] });
  if (os === 'win') {
    // the signed Wintun driver DLL that wireguard-go / amneziawg-go load from next to the exe
    const host = { ...process.env, GOOS: '', GOARCH: '', CGO_ENABLED: '0' };
    execFileSync('go', ['run', './cmd/fetchwintun', GOARCH[arch], out, path.join(DAEMON, 'bin', '.cache')], { cwd: DAEMON, env: host, stdio: 'inherit' });
  }
  console.log('ok');
}

module.exports = { TAGS, here };
