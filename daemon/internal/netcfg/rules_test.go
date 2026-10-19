package netcfg

import (
	"net/netip"
	"strings"
	"testing"
)

func TestNftRules(t *testing.T) {
	allow := []Allow{{Addr: netip.MustParseAddr("203.0.113.7"), Port: 51820, Proto: "udp"}, {Addr: netip.MustParseAddr("2001:db8::1"), Port: 443, Proto: "both"}}
	r := NftRules("mayvpn0", allow, false)
	for _, want := range []string{
		"table inet mayvpn {", "policy drop;", `oifname "mayvpn0" accept`, `iifname "mayvpn0" accept`,
		"ip daddr 203.0.113.7 udp dport 51820 accept", "ip6 daddr 2001:db8::1 udp dport 443 accept",
		"ip6 daddr 2001:db8::1 tcp dport 443 accept", "udp sport 68 udp dport 67 accept", "ct state established,related accept",
	} {
		if !strings.Contains(r, want) {
			t.Fatalf("missing %q in\n%s", want, r)
		}
	}
	if strings.Contains(r, "192.168.0.0/16") {
		t.Fatal("LAN must be closed unless allowed")
	}
	if !strings.Contains(NftRules("mayvpn0", nil, true), "192.168.0.0/16") {
		t.Fatal("allowLan must open private ranges")
	}
}

func TestForeignTunnelNames(t *testing.T) {
	for name, want := range map[string]bool{"en0": false, "eth0": false, "wlp2s0": false, "utun6": true, "tun0": true, "wg0": true, "ppp0": true, "nordlynx": true} {
		if got := isTunnelName(name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
