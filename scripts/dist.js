// Installers: node scripts/dist.js [mac] [win] [linux] [--x64] [--arm64]  (default: this OS, this CPU)
// Builds the helper for each target first, then electron-builder packs it into resources/daemon.
const { execFileSync } = require('child_process');
const path = require('path');

const root = path.join(__dirname, '..');
const args = process.argv.slice(2);
const here = { darwin: 'mac', win32: 'win', linux: 'linux' }[process.platform];
const oses = args.filter((a) => ['mac', 'win', 'linux'].includes(a));
const arches = args.filter((a) => a === '--x64' || a === '--arm64').map((a) => a.slice(2));
const plan = oses.length ? oses : [here];
const cpus = arches.length ? arches : [process.arch];
const targets = { mac: ['dmg'], win: ['nsis'], linux: ['deb', 'AppImage'] };

for (const os of plan) {
  execFileSync(process.execPath, [path.join(__dirname, 'build-daemon.js'), `--target=${cpus.map((c) => `${os}-${c}`).join(',')}`], { stdio: 'inherit', cwd: root });
  const bin = path.join(root, 'node_modules', '.bin', process.platform === 'win32' ? 'electron-builder.cmd' : 'electron-builder');
  execFileSync(bin, [`--${os}`, ...targets[os], ...cpus.map((c) => `--${c}`), '--publish', 'never'], { stdio: 'inherit', cwd: root });
}
