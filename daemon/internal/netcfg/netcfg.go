// Package netcfg changes the network around a tunnel: interface addresses, routes, the DNS
// override and the kill switch. Every change is recorded in memory and undone in reverse order by
// Undo. Nothing is written to disk. One implementation per OS (netcfg_<os>.go).
package netcfg

import (
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
	"sync"
)

// Runner executes a system tool; tests swap it for a recorder.
type Runner func(stdin string, name string, args ...string) (string, error)

func System(stdin string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Config applies changes and remembers how to revert them.
type Config struct {
	Run  Runner
	mu   sync.Mutex
	undo []func()
}

func New(run Runner) *Config {
	if run == nil {
		run = System
	}
	return &Config{Run: run}
}

func (c *Config) push(f func()) { c.mu.Lock(); c.undo = append(c.undo, f); c.mu.Unlock() }

// Undo reverts every recorded change, newest first. Errors are swallowed: we are tearing down.
func (c *Config) Undo() {
	c.mu.Lock()
	u := c.undo
	c.undo = nil
	c.mu.Unlock()
	for i := len(u) - 1; i >= 0; i-- {
		u[i]()
	}
}

// Gateway is the physical next hop for a family (the split routes leave it in place).
type Gateway struct {
	Addr  netip.Addr // invalid when the default route is a link (no next hop)
	Iface string
	Index int // interface index (Windows routes by it)
}

type Allow struct {
	Addr  netip.Addr
	Port  uint16
	Proto string // udp | tcp | both
}

func (a Allow) protos() []string {
	if a.Proto == "both" {
		return []string{"udp", "tcp"}
	}
	return []string{a.Proto}
}

// split turns a default route into two halves that win over the real default without replacing it.
func split(p netip.Prefix) []netip.Prefix {
	if p.Bits() != 0 {
		return []netip.Prefix{p}
	}
	if p.Addr().Is4() {
		return []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1")}
	}
	return []netip.Prefix{netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1")}
}

// tunnelPrefixes are interface names other VPNs use; a default route on one of them means
// another VPN is running (we refuse to nest: our kill switch would cut its packets).
var tunnelPrefixes = []string{"utun", "ipsec", "ppp", "tun", "tap", "wg", "nordlynx", "proton", "mullvad"}

func isTunnelName(iface string) bool {
	for _, p := range tunnelPrefixes {
		if strings.HasPrefix(strings.ToLower(iface), p) {
			return true
		}
	}
	return false
}

// PFRules (macOS): only loopback, the tunnel, the VPN server and DHCP may pass; everything else drops.
func PFRules(iface string, allow []Allow, lan bool) string {
	var b strings.Builder
	b.WriteString("pass quick on lo0 all no state\n")
	fmt.Fprintf(&b, "pass quick on %s all\n", iface)
	for _, a := range allow {
		fam := "inet"
		if a.Addr.Is6() {
			fam = "inet6"
		}
		for _, p := range a.protos() {
			fmt.Fprintf(&b, "pass out quick %s proto %s to %s port %d\n", fam, p, a.Addr, a.Port)
		}
	}
	b.WriteString("pass out quick inet proto udp from any port 68 to any port 67\n")
	b.WriteString("pass in quick inet proto udp from any port 67 to any port 68\n")
	b.WriteString("pass quick inet6 proto icmp6 all icmp6-type { neighbrsol, neighbradv, routersol, routeradv }\n")
	if lan {
		b.WriteString("pass quick inet from any to { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 224.0.0.0/4 }\n")
		b.WriteString("pass quick inet from { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 } to any\n")
		b.WriteString("pass quick inet6 from any to { fe80::/10, ff00::/8, fc00::/7 }\n")
	}
	b.WriteString("block drop quick all\n")
	return b.String()
}

// NftTable is ours alone; deleting it removes the whole kill switch.
const NftTable = "mayvpn"

// NftRules (Linux): the same policy as PFRules as one nftables table with drop policies.
func NftRules(iface string, allow []Allow, lan bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", NftTable)
	b.WriteString("  chain output {\n    type filter hook output priority 0; policy drop;\n")
	b.WriteString("    oifname \"lo\" accept\n")
	fmt.Fprintf(&b, "    oifname %q accept\n", iface)
	for _, a := range allow {
		fam := "ip"
		if a.Addr.Is6() {
			fam = "ip6"
		}
		for _, p := range a.protos() {
			fmt.Fprintf(&b, "    %s daddr %s %s dport %d accept\n", fam, a.Addr, p, a.Port)
		}
	}
	b.WriteString("    udp sport 68 udp dport 67 accept\n")
	b.WriteString("    icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert } accept\n")
	if lan {
		b.WriteString("    ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 224.0.0.0/4 } accept\n")
		b.WriteString("    ip6 daddr { fe80::/10, ff00::/8, fc00::/7 } accept\n")
	}
	b.WriteString("  }\n")
	b.WriteString("  chain input {\n    type filter hook input priority 0; policy drop;\n")
	b.WriteString("    iifname \"lo\" accept\n")
	fmt.Fprintf(&b, "    iifname %q accept\n", iface)
	b.WriteString("    ct state established,related accept\n")
	b.WriteString("    udp sport 67 udp dport 68 accept\n")
	b.WriteString("    icmpv6 type { nd-router-advert, nd-neighbor-solicit, nd-neighbor-advert } accept\n")
	if lan {
		b.WriteString("    ip saddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 } accept\n")
		b.WriteString("    ip6 saddr { fe80::/10, fc00::/7 } accept\n")
	}
	b.WriteString("  }\n}\n")
	return b.String()
}
