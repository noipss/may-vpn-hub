// Package netcfg changes the macOS network around a tunnel: interface addresses, routes,
// the DNS override (dynamic store, memory only) and the pf kill switch. Every change is
// recorded in memory and undone in reverse order by Undo. Nothing is written to disk.
package netcfg

import (
	"bufio"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
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

const (
	ifconfig  = "/sbin/ifconfig"
	route     = "/sbin/route"
	scutil    = "/usr/sbin/scutil"
	pfctl     = "/sbin/pfctl"
	PFAnchor  = "com.apple/250.MayVPN"
	dnsCache1 = "/usr/bin/dscacheutil"
	killall   = "/usr/bin/killall"
)

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

// ---------- interface & routes ----------

// Address assigns a tunnel address to a utun (point-to-point, like wg-quick on macOS).
func (c *Config) Address(iface string, p netip.Prefix) error {
	var err error
	if p.Addr().Is4() {
		_, err = c.Run("", ifconfig, iface, "inet", p.String(), p.Addr().String(), "alias")
	} else {
		_, err = c.Run("", ifconfig, iface, "inet6", p.String(), "alias")
	}
	return err
}

func (c *Config) Up(iface string, mtu int) error {
	if mtu > 0 {
		if _, err := c.Run("", ifconfig, iface, "mtu", fmt.Sprint(mtu)); err != nil {
			return err
		}
	}
	_, err := c.Run("", ifconfig, iface, "up")
	return err
}

func family(p netip.Prefix) string {
	if p.Addr().Is4() {
		return "-inet"
	}
	return "-inet6"
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

// RouteVia sends a prefix into the tunnel interface.
func (c *Config) RouteVia(iface string, p netip.Prefix) error {
	for _, h := range split(p) {
		h := h
		if _, err := c.Run("", route, "-q", "-n", "add", family(h), h.String(), "-interface", iface); err != nil {
			return err
		}
		c.push(func() { c.Run("", route, "-q", "-n", "delete", family(h), h.String(), "-interface", iface) })
	}
	return nil
}

// Blackhole drops a whole family (IPv6 when the tunnel has none — no leak around it).
func (c *Config) Blackhole(p netip.Prefix) error {
	lo := "127.0.0.1"
	if !p.Addr().Is4() {
		lo = "::1"
	}
	for _, h := range split(p) {
		h := h
		if _, err := c.Run("", route, "-q", "-n", "add", family(h), h.String(), lo, "-blackhole"); err != nil {
			return err
		}
		c.push(func() { c.Run("", route, "-q", "-n", "delete", family(h), h.String(), lo, "-blackhole") })
	}
	return nil
}

type Gateway struct {
	Addr  netip.Addr // invalid when the default route is a link (no next hop)
	Iface string
}

var reGW = regexp.MustCompile(`(?m)^\s*gateway:\s*(\S+)`)
var reIF = regexp.MustCompile(`(?m)^\s*interface:\s*(\S+)`)

// DefaultGateway is the physical next hop for a family (the split routes leave it in place).
func (c *Config) DefaultGateway(v6 bool) (Gateway, error) {
	args := []string{"-n", "get"}
	if v6 {
		args = append(args, "-inet6")
	}
	out, err := c.Run("", route, append(args, "default")...)
	if err != nil {
		return Gateway{}, err
	}
	var g Gateway
	if m := reIF.FindStringSubmatch(out); m != nil {
		g.Iface = m[1]
	}
	if m := reGW.FindStringSubmatch(out); m != nil {
		g.Addr, _ = netip.ParseAddr(strings.Split(m[1], "%")[0])
	}
	if g.Iface == "" {
		return g, fmt.Errorf("no default route")
	}
	return g, nil
}

// PinHost keeps traffic to the VPN server itself outside the tunnel.
func (c *Config) PinHost(ip netip.Addr, gw Gateway) error {
	h := netip.PrefixFrom(ip, ip.BitLen())
	args := []string{"-q", "-n", "add", family(h), h.String()}
	del := []string{"-q", "-n", "delete", family(h), h.String()}
	if gw.Addr.IsValid() {
		args = append(args, gw.Addr.String())
	} else {
		args = append(args, "-interface", gw.Iface)
	}
	if _, err := c.Run("", route, args...); err != nil {
		return err
	}
	c.push(func() { c.Run("", route, del...) })
	return nil
}

// ---------- DNS (dynamic store: memory only, gone after reboot) ----------

var rePrimary = regexp.MustCompile(`PrimaryService\s*:\s*(\S+)`)

// OverrideDNS points the primary service's resolver at servers and restores the original on Undo.
func (c *Config) OverrideDNS(servers []netip.Addr) error {
	out, err := c.Run("show State:/Network/Global/IPv4\n", scutil)
	if err != nil {
		return err
	}
	m := rePrimary.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("no primary network service")
	}
	key := "State:/Network/Service/" + m[1] + "/DNS"
	orig, _ := c.Run("show "+key+"\n", scutil)
	saved := parseDict(orig)

	addrs := make([]string, len(servers))
	for i, s := range servers {
		addrs[i] = s.String()
	}
	if _, err := c.Run(setScript(key, map[string][]string{"ServerAddresses": addrs}), scutil); err != nil {
		return err
	}
	c.flushDNS()
	c.push(func() {
		if len(saved) == 0 {
			c.Run("remove "+key+"\n", scutil)
		} else {
			c.Run(setScript(key, saved), scutil)
		}
		c.flushDNS()
	})
	return nil
}

func (c *Config) flushDNS() {
	c.Run("", dnsCache1, "-flushcache")
	c.Run("", killall, "-HUP", "mDNSResponder")
}

func setScript(key string, d map[string][]string) string {
	var b strings.Builder
	b.WriteString("d.init\n")
	for _, k := range []string{"ServerAddresses", "SearchDomains", "DomainName"} {
		v, ok := d[k]
		if !ok || len(v) == 0 {
			continue
		}
		if k == "DomainName" {
			fmt.Fprintf(&b, "d.add %s %s\n", k, v[0])
		} else {
			fmt.Fprintf(&b, "d.add %s * %s\n", k, strings.Join(v, " "))
		}
	}
	fmt.Fprintf(&b, "set %s\n", key)
	return b.String()
}

// parseDict reads the scutil "show" dump of a DNS dictionary (arrays + DomainName).
func parseDict(s string) map[string][]string {
	d := map[string][]string{}
	if !strings.Contains(s, "<dictionary>") {
		return d
	}
	sc := bufio.NewScanner(strings.NewReader(s))
	cur := ""
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasSuffix(l, ": <array> {"):
			cur = strings.TrimSpace(strings.TrimSuffix(l, ": <array> {"))
		case l == "}":
			cur = ""
		case cur != "":
			if _, v, ok := strings.Cut(l, " : "); ok {
				d[cur] = append(d[cur], v)
			}
		case strings.HasPrefix(l, "DomainName : "):
			d["DomainName"] = []string{strings.TrimPrefix(l, "DomainName : ")}
		}
	}
	return d
}

// ---------- kill switch (pf anchor) ----------

type Allow struct {
	Addr  netip.Addr
	Port  uint16
	Proto string // udp | tcp | both
}

// PFRules: only loopback, the tunnel, the VPN server and DHCP may pass; everything else drops.
func PFRules(iface string, allow []Allow, lan bool) string {
	var b strings.Builder
	b.WriteString("pass quick on lo0 all no state\n")
	fmt.Fprintf(&b, "pass quick on %s all\n", iface)
	for _, a := range allow {
		fam := "inet"
		if a.Addr.Is6() {
			fam = "inet6"
		}
		protos := []string{a.Proto}
		if a.Proto == "both" {
			protos = []string{"udp", "tcp"}
		}
		for _, p := range protos {
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

var reToken = regexp.MustCompile(`Token\s*:\s*(\d+)`)

// KillSwitch loads the anchor and takes a pf enable reference (released on Undo).
func (c *Config) KillSwitch(rules string) error {
	if _, err := c.Run(rules, pfctl, "-a", PFAnchor, "-f", "-"); err != nil {
		return err
	}
	c.push(func() { c.Run("", pfctl, "-a", PFAnchor, "-F", "all") })
	out, err := c.Run("", pfctl, "-E")
	if err != nil {
		return err
	}
	if m := reToken.FindStringSubmatch(out); m != nil {
		tok := m[1]
		c.push(func() { c.Run("", pfctl, "-X", tok) })
	}
	return nil
}

// Sweep removes leftovers of a previous run that died without Undo (memory was lost with it).
func Sweep(run Runner) {
	if run == nil {
		run = System
	}
	run("", pfctl, "-a", PFAnchor, "-F", "all")
}
