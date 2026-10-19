// go vet (for all three OSes) + go test for this OS, with the helper's build tags.
const { execFileSync } = require('child_process');
const path = require('path');
const TAGS = 'with_gvisor,with_quic,with_utls,with_openvpn,badlinkname,tfogo_checklinkname0';

const cwd = path.join(__dirname, '..', 'daemon');
const run = (args, env = {}) => execFileSync('go', args, { cwd, stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0', ...env } });
for (const goos of ['darwin', 'linux', 'windows']) run(['vet', '-tags', TAGS, './...'], { GOOS: goos });
run(['test', '-tags', TAGS, '-ldflags=-checklinkname=0', './...']);
