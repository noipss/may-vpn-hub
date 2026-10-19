// Package tunnel runs the data plane: WireGuard (wireguard-go), AmneziaWG (amneziawg-go) and
// proxy protocols through an embedded sing-box. Nothing here logs.
package tunnel

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"mayvpn/internal/profile"
)

// Tunnel is a running data plane bound to one utun interface.
type Tunnel interface {
	Name() string
	// Counters returns bytes received / sent through the tunnel and the last handshake
	// (zero when the protocol has no such notion).
	Counters() (rx, tx uint64, handshake time.Time)
	Close() error
}

// UAPI renders a wg(8) "set" config for one interface. endpoints[i] is the resolved address of
// peer i. AmneziaWG keys are emitted only when awg is set (plain wireguard-go rejects them).
func UAPI(w *profile.WG, endpoints []netip.AddrPort, awg bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", hex.EncodeToString(w.PrivateKey[:]))
	if awg {
		for _, kv := range w.AWG {
			fmt.Fprintf(&b, "%s=%s\n", kv[0], kv[1])
		}
	}
	b.WriteString("replace_peers=true\n")
	for i, p := range w.Peers {
		fmt.Fprintf(&b, "public_key=%s\n", hex.EncodeToString(p.PublicKey[:]))
		if p.PresharedKey != nil {
			fmt.Fprintf(&b, "preshared_key=%s\n", hex.EncodeToString(p.PresharedKey[:]))
		}
		fmt.Fprintf(&b, "endpoint=%s\n", endpoints[i])
		if p.Keepalive > 0 {
			fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", p.Keepalive)
		}
		b.WriteString("replace_allowed_ips=true\n")
		for _, a := range p.AllowedIPs {
			fmt.Fprintf(&b, "allowed_ip=%s\n", a)
		}
	}
	return b.String()
}

// parseGet sums rx/tx over peers from a wg(8) "get" dump and returns the newest handshake.
func parseGet(s string) (rx, tx uint64, hs time.Time) {
	var sec, nsec int64
	flush := func() {
		if t := time.Unix(sec, nsec); sec > 0 && t.After(hs) {
			hs = t
		}
		sec, nsec = 0, 0
	}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		n, _ := strconv.ParseInt(v, 10, 64)
		switch k {
		case "public_key":
			flush()
		case "rx_bytes":
			rx += uint64(n)
		case "tx_bytes":
			tx += uint64(n)
		case "last_handshake_time_sec":
			sec = n
		case "last_handshake_time_nsec":
			nsec = n
		}
	}
	flush()
	return
}

// wipe overwrites a config string's backing copy we own; callers pass the []byte they built.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
