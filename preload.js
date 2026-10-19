const { contextBridge, ipcRenderer } = require('electron');

const listen = (channel) => (cb) => {
  const h = (_e, data) => cb(data);
  ipcRenderer.on(channel, h);
  return () => ipcRenderer.off(channel, h);
};
const call = (channel) => (...args) => ipcRenderer.invoke(channel, ...args);

contextBridge.exposeInMainWorld('vpn', {
  platform: process.platform,
  state: call('vpn:state'),
  connect: call('vpn:connect'),
  disconnect: call('vpn:disconnect'),
  importKey: call('vault:import'),
  importFile: call('vault:importFile'),
  removeKey: call('vault:remove'),
  setPrefs: call('vault:set'),
  installHelper: call('helper:install'),
  uninstallHelper: call('helper:uninstall'),
  erase: call('app:erase'),
  onState: listen('vpn:state'),
  onStats: listen('vpn:stats'),
});
