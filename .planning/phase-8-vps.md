# Phase 8 — our VPS (77.91.123.91, Ubuntu 22.04, kernel WireGuard)

## Grill Me
- **Q: Is the server broken?** No. wg-quick@wg0 enabled/active, forwarding + MASQUERADE + port 51820 fine,
  peer 10.66.66.2 (the previous admin's device) online with GBs of traffic. Nothing to rebuild.
- **Q: Why did May VPN fail on this Mac?** Two reasons: (1) no key for this Mac — the server only has
  public keys; the one client key lives on the other device; (2) another VPN (Happ, utun6) owns the
  default route, so WireGuard UDP is carried inside it (measured: 495 ms, 30 Mbit/s) and May VPN's kill
  switch would cut Happ's own packets.
- **Q: New protocol for speed?** No. Kernel WireGuard is the fastest option; the box (Ryzen 9 5950X,
  2 vCPU, ~360/270 Mbit/s, 22 ms to 1.1.1.1) is not the bottleneck. DPI not an issue → no AmneziaWG.
- **Q: What actually speeds it up?** Not chaining VPNs; MSS clamping so TCP never fragments on
  small-MTU paths. BBR / buffer tweaks don't touch forwarded traffic — skipped on purpose.

## Done
- Peer 10.66.66.3 for May VPN on the Mac: keypair + PSK generated on the Mac, server got the public
  key only; added live (`wg set`) + persisted, other client untouched. Verified end-to-end in
  userspace (exit IP = VPS, FRA).
- TCPMSS clamp-to-pmtu both directions on wg0 (live + PostUp/PostDown).
- Logs RAM-only: journald Storage=volatile, /var/log/journal removed, wtmp/btmp/lastlog → /dev/null,
  no root shell history.
- Root password rotated; stored in the Mac's Keychain ("VPS 77.91.123.91 root (May VPN)"); old one
  rejected; key login from this Mac works.
- May VPN: refuses to start inside another VPN with a clear message (helper 0.1.2); `cmd/wgprobe`
  dev tool tests a .conf in userspace without root.
