# Phase 9 — Windows & Linux

## Grill Me
- **Q: Linux engine?** Kernel WireGuard via netlink (wgctrl), the fastest; falls back to wireguard-go
  when the module is missing. AmneziaWG stays userspace (amneziawg-go), proxies on sing-box.
- **Q: Windows engine?** wireguard-go / amneziawg-go on Wintun (`wintun.dll` shipped next to the helper,
  signed by WireGuard LLC); addresses, routes and DNS through `winipcfg`; kill switch through WFP
  (wireguard-windows' firewall: a dynamic session — it disappears with the process, no traces).
  sing-box brings its own Wintun.
- **Q: Privileges?** Same model as macOS: a system helper installed once.
  Linux — systemd unit, elevation with `pkexec`; Windows — a Windows service, elevation via UAC.
- **Q: IPC?** Unix socket on macOS/Linux (peer uid checked: LOCAL_PEERCRED / SO_PEERCRED);
  named pipe `\\.\pipe\mayvpn` on Windows with a DACL for the installing user, SYSTEM and Administrators.
- **Q: DNS?** macOS scutil override; Linux systemd-resolved per-link (`resolvectl`, gone with the link)
  with `/etc/resolv.conf` fallback; Windows per-interface DNS + WFP blocks DNS to other servers.
- **Q: Kill switch?** macOS pf anchor; Linux nftables table `inet mayvpn` (deleted on undo); Windows WFP.
- **Q: Logs?** Linux unit: stdout/stderr to null; Windows service writes no event log.
- **Q: Deliverable?** Installers with electron-builder: Windows NSIS .exe, Linux .deb + AppImage, macOS .dmg.
- **Q: Verification?** macOS native; Linux in Docker (privileged container: kernel WG, nft, routes);
  Windows: cross-build + `go vet` + unit tests that compile for windows — not run live (no Windows box).

## Tasks
1. Daemon: per-OS netcfg / engine / server listener / installer behind build tags; common driver logic.
2. Electron: platform layer (helper path, pipe, elevation, window chrome, safeStorage backend on Linux).
3. Build: cross-compile script (Node, no make), wintun.dll fetch + checksum, electron-builder config.
4. Verify: Go tests (darwin + linux in Docker), windows vet/build, installers built, UI shots.
