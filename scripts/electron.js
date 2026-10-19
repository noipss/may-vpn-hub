// Starts Electron with the given args on every OS (npm scripts can't "env -u" on Windows).
const { spawn } = require('child_process');
const electron = require('electron');

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE;
const p = spawn(electron, ['.', ...process.argv.slice(2)], { stdio: 'inherit', env, cwd: require('path').join(__dirname, '..') });
p.on('exit', (code) => process.exit(code ?? 0));
