//go:build darwin

package netcfg

import (
	"bufio"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

const (
	ifconfig  = "/sbin/ifconfig"
	route     = "/sbin/route"
	scutil    = "/usr/sbin/scutil"
	pfctl     = "/sbin/pfctl"
	PFAnchor  = "com.apple/250.MayVPN"
	dnsCache1 = "/usr/bin/dscacheutil"
	killall   = "/usr/bin/killall"
)

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

// KillSwitchAfterTunnel: pf names the interface in its rules, so it can load before the tunnel.
const KillSwitchAfterTunnel = false

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
func (c *Config) Blackhole(_ string, p netip.Prefix) error {
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
func (c *Config) OverrideDNS(_ string, servers []netip.Addr) error {
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

var reToken = regexp.MustCompile(`Token\s*:\s*(\d+)`)

// KillSwitch loads the anchor and takes a pf enable reference (released on Undo).
func (c *Config) KillSwitch(iface string, allow []Allow, lan bool, _ []netip.Addr) error {
	if _, err := c.Run(PFRules(iface, allow, lan), pfctl, "-a", PFAnchor, "-f", "-"); err != nil {
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

// IsForeignTunnel: the default route sits on another VPN's interface.
func IsForeignTunnel(gw Gateway) bool { return isTunnelName(gw.Iface) }

// Sweep removes leftovers of a previous run that died without Undo (memory was lost with it).
func Sweep(run Runner) {
	if run == nil {
		run = System
	}
	run("", pfctl, "-a", PFAnchor, "-F", "all")
}
