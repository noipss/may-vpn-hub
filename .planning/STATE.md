# State
- Phase 0 — done: agent moved to `may-attacker/` (its tests pass from there), `may-vpn/` created, plan written.
- Phase 1 — done: profile parser (WG, AWG, OpenVPN, vless/vmess/ss/trojan/hysteria2), PostUp & up/down hooks ignored. Tests green.
- Phase 2 — done: wireguard-go & amneziawg-go engines (netstack loopback: handshake + TCP echo + counters), sing-box config builder (every kind accepted by box.New). gvisor pinned to wireguard-go's version (amneziawg-go's pick does not build).
