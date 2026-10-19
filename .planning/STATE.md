# State
- Phase 0 — done: agent moved to `may-attacker/` (its tests pass from there), `may-vpn/` created, plan written.
- Phase 1 — done: profile parser (WG, AWG, OpenVPN, vless/vmess/ss/trojan/hysteria2), PostUp & up/down hooks ignored. Tests green.
- Phase 2 — done: wireguard-go & amneziawg-go engines (netstack loopback: handshake + TCP echo + counters), sing-box config builder (every kind accepted by box.New). gvisor pinned to wireguard-go's version (amneziawg-go's pick does not build).
- Phase 3 — done: netcfg (utun addresses, split routes, server pin, v6 blackhole, scutil DNS override with restore, pf kill switch — rules pass `pfctl -n`), driver with rollback, session (connecting→connected on first probe through the tunnel, reconnecting after 12s silence, rates, ping), unix-socket server (0600, LOCAL_PEERCRED), launchd install/uninstall, `mayvpnd` CLI. Not verified: a live root run against a real VPS.
