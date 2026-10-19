# May VPN

A no-logs VPN client for macOS. May May — the scanline kitten from May Attacker — guards the
door on a milky background: she stands with her sword and shield, fights while the tunnel comes
up, throws both away once you are protected and curls into a ball that turns like a globe.

WireGuard is the main protocol. AmneziaWG, VLESS (Reality / WS / gRPC), VMess, Trojan,
Shadowsocks, Hysteria2 and OpenVPN run too.

```bash
npm install
npm start            # builds the Go helper (needs Go 1.26) and opens the app
npm run demo         # UI with a pretend helper — no root, no network changes
npm test             # Go tests (incl. WireGuard/AmneziaWG loopback) + vault + helper protocol
npm run shots        # frozen frames of every cat state → ./shots
```

## How it works

```
Electron UI (renderer, sandboxed)          ── IPC ──  main.js
  cat: WebGL2 SDF + scanlines                          vault (safeStorage) · Helper client
                                                              │ JSON lines, /var/run/com.maymay.vpn.sock (0600, peer uid checked)
                                                       mayvpnd (Go, root, launchd)
                                                         profile → driver → engine + netcfg
                                                         wireguard-go · amneziawg-go · sing-box
```

- **Keys.** Keys → paste or choose a file: a WireGuard / AmneziaWG `.conf`, an `.ovpn` with
  inline certificates, or a `vless:// vmess:// ss:// trojan:// hysteria2://` link. One slot per
  protocol, one VPS. The region is a label you type in; nothing is looked up online.
- **Helper.** The first connect asks to install a small root helper (macOS password prompt):
  `/Library/PrivilegedHelperTools/com.maymay.vpn.helper` + `/Library/LaunchDaemons/com.maymay.vpn.helper.plist`.
  Keys → Guard post → Remove takes both out again.
- **Connected** means a request actually went through the tunnel (`http://1.1.1.1` over a
  kept-alive connection — that round trip is also the ping), not just that an interface came up.
  If the tunnel goes quiet for 12 s the state returns to "Reconnecting…".

## No logs, no traces

- The helper writes nothing: stdout/stderr go to `/dev/null`, wireguard-go's logger is silent,
  sing-box runs with `log.disabled`. Errors only travel back over the socket.
- No database. Session state, byte counters, the original DNS settings live in the helper's
  memory and die with the connection.
- `PostUp`/`PreUp`/`PostDown` lines and OpenVPN `up`/`down`/`script-security` are ignored —
  a key can never run a command as root.
- The Electron profile (cache, local storage, crash dumps) lives in a temp folder deleted on
  exit; the browser session is in-memory; every permission request is denied.
- The only file on disk is `~/Library/Application Support/May VPN/vault`, encrypted with a key
  from your keychain. The page never sees key text, only summaries.
- Quitting the app disconnects first.
- **Erase everything** disconnects, removes the helper, deletes the vault and its keychain item.

What stays outside our control: launchd's own one-line "service started/exited" notes in the
unified system log, and macOS swap (encrypted by the OS).

## Leak protection

- **Kill switch** (on by default): a pf anchor `com.apple/250.MayVPN` lets through only
  loopback, the tunnel interface, the VPN server's port and DHCP; LAN is optional.
- **DNS** goes through the tunnel: the primary service's resolver is overridden in the dynamic
  store (memory only) and restored on disconnect.
- **IPv6**: when the tunnel has no IPv6, `::/1` and `8000::/1` are blackholed.
- **Routes**: split defaults (`0/1`, `128/1`) so the real default route stays for the server's
  host route; everything is undone in reverse order.

## Structure

- `main.js`, `preload.js` — window, IPC, temp profile, quit = disconnect
- `electron/vault.js`, `electron/helper.js`, `electron/demo.js`
- `src/app.js` — layout, paw button, cat state machine (run · guard · fight · throw → ball · uncurl), loader prints, sheets
- `src/cat/{shaders,rig,renderer}.js` — the kitten, sword and shield SDFs, poses
- `daemon/` — Go module `mayvpn`: `cmd/mayvpnd`, `internal/{profile,tunnel,driver,netcfg,session,server,install}`
- `.planning/` — GSD plan (PROJECT, ROADMAP, STATE)

QA: `electron . --demo --script=steps.json --out=dir` with steps `{ "js" } | { "wait" } | { "shot" }`;
`window.__vpn.at(mode, t, state)` freezes a cat state, `window.__vpn.live()` runs the clock.
