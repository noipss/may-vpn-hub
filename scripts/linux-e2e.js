// Linux end-to-end in Docker (needs Docker): builds the helper for Docker's CPU, then runs
// scripts/linux-e2e/run.sh (kernel WireGuard, routes, nftables kill switch, DNS, restore) and the
// Go unit tests inside a privileged container.
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const root = path.join(__dirname, '..');
const bench = path.join(__dirname, 'linux-e2e');
const daemon = path.join(root, 'daemon');
const TAGS = 'with_gvisor,with_quic,with_utls,with_openvpn,badlinkname,tfogo_checklinkname0';
const sh = (cmd, args, opts = {}) => execFileSync(cmd, args, { stdio: 'inherit', ...opts });

const arch = execFileSync('docker', ['info', '--format', '{{.Architecture}}']).toString().trim();
const goarch = /arm64|aarch64/.test(arch) ? 'arm64' : 'amd64';
const env = { ...process.env, GOOS: 'linux', GOARCH: goarch, CGO_ENABLED: '0' };

sh('go', ['build', '-trimpath', '-tags', TAGS, '-ldflags', '-s -w -buildid= -checklinkname=0', '-o', path.join(bench, 'mayvpnd'), './cmd/mayvpnd'], { cwd: daemon, env });
const tests = path.join(bench, 'tests');
fs.rmSync(tests, { recursive: true, force: true });
fs.mkdirSync(tests);
for (const p of ['netcfg', 'tunnel', 'server', 'session', 'profile', 'install']) {
  sh('go', ['test', '-c', '-tags', TAGS, '-ldflags=-checklinkname=0', '-o', path.join(tests, `${p}.test`), `./internal/${p}`], { cwd: daemon, env });
}
sh('docker', ['build', '-q', '-t', 'mayvpn-linux-e2e', bench], { stdio: ['ignore', 'ignore', 'inherit'] });
const inside = `cp /src/mayvpnd /src/run.sh /src/client.py /work/ && /work/run.sh || exit 1
cd /tmp
for t in /src/tests/*.test; do
  n=$(basename $t .test)
  if [ $n = server ]; then run="setpriv --reuid=65534 --regid=65534 --clear-groups $t"; else run=$t; fi
  if $run >/tmp/o 2>&1; then echo "PASS  go test $n"; else cat /tmp/o; exit 1; fi
done`;
sh('docker', ['run', '--rm', '--privileged', '-v', `${bench}:/src:ro`, 'mayvpn-linux-e2e', 'sh', '-c', inside]);
