//go:build linux

package netcfg

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
)

// Tools are looked up on PATH: distros disagree on /sbin vs /usr/sbin.
var (
	ipBin  = look("ip")
	nftBin = look("nft")
	resolv = look("resolvectl")
)

func look(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, d := range []string{"/usr/sbin/", "/sbin/", "/usr/bin/", "/bin/"} {
		if _, err := os.Stat(d + name); err == nil {
			return d + name
		}
	}
	return name
}

// KillSwitchAfterTunnel: nftables matches interfaces by name, so it loads before the tunnel exists.
const KillSwitchAfterTunnel = false

func fam(p netip.Prefix) string {
	if p.Addr().Is4() {
		return "-4"
	}
	return "-6"
}

func (c *Config) Address(iface string, p netip.Prefix) error {
	_, err := c.Run("", ipBin, fam(p), "address", "add", p.String(), "dev", iface)
	return err
}

func (c *Config) Up(iface string, mtu int) error {
	args := []string{"link", "set", "dev", iface}
	if mtu > 0 {
		args = append(args, "mtu", fmt.Sprint(mtu))
	}
	_, err := c.Run("", ipBin, append(args, "up")...)
	return err
}

func (c *Config) RouteVia(iface string, p netip.Prefix) error {
	for _, h := range split(p) {
		h := h
		if _, err := c.Run("", ipBin, fam(h), "route", "add", h.String(), "dev", iface); err != nil {
			return err
		}
		c.push(func() { c.Run("", ipBin, fam(h), "route", "del", h.String(), "dev", iface) })
	}
	return nil
}

func (c *Config) Blackhole(_ string, p netip.Prefix) error {
	for _, h := range split(p) {
		h := h
		if _, err := c.Run("", ipBin, fam(h), "route", "add", "blackhole", h.String()); err != nil {
			return err
		}
		c.push(func() { c.Run("", ipBin, fam(h), "route", "del", "blackhole", h.String()) })
	}
	return nil
}

// DefaultGateway parses "default via 192.168.1.1 dev wlan0 proto dhcp metric 600" (lowest metric
// wins, which is the first line ip prints).
func (c *Config) DefaultGateway(v6 bool) (Gateway, error) {
	f := "-4"
	if v6 {
		f = "-6"
	}
	out, err := c.Run("", ipBin, f, "route", "show", "default")
	if err != nil {
		return Gateway{}, err
	}
	return parseDefault(out)
}

func parseDefault(out string) (Gateway, error) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "default" {
			continue
		}
		var g Gateway
		for i := 1; i+1 < len(f); i++ {
			switch f[i] {
			case "via":
				g.Addr, _ = netip.ParseAddr(f[i+1])
			case "dev":
				g.Iface = f[i+1]
			}
		}
		if g.Iface != "" {
			return g, nil
		}
	}
	return Gateway{}, fmt.Errorf("no default route")
}

func (c *Config) PinHost(ip netip.Addr, gw Gateway) error {
	h := netip.PrefixFrom(ip, ip.BitLen())
	args := []string{fam(h), "route", "add", h.String()}
	if gw.Addr.IsValid() {
		args = append(args, "via", gw.Addr.String())
	}
	args = append(args, "dev", gw.Iface)
	if _, err := c.Run("", ipBin, args...); err != nil {
		return err
	}
	c.push(func() { c.Run("", ipBin, fam(h), "route", "del", h.String()) })
	return nil
}

// OverrideDNS: with systemd-resolved, per-link DNS on the tunnel that takes every domain ("~.") —
// it vanishes with the link. Without it, /etc/resolv.conf is replaced and restored byte for byte.
func (c *Config) OverrideDNS(iface string, servers []netip.Addr) error {
	addrs := make([]string, len(servers))
	for i, s := range servers {
		addrs[i] = s.String()
	}
	if _, err := c.Run("", resolv, "status"); err == nil {
		if _, err := c.Run("", resolv, append([]string{"dns", iface}, addrs...)...); err != nil {
			return err
		}
		c.push(func() { c.Run("", resolv, "revert", iface) })
		if _, err := c.Run("", resolv, "domain", iface, "~."); err != nil {
			return err
		}
		c.Run("", resolv, "default-route", iface, "true") // older systemd lacks it; "~." already routes everything
		c.Run("", resolv, "flush-caches")
		return nil
	}
	return c.overrideResolvConf(addrs)
}

const resolvConf = "/etc/resolv.conf"

func (c *Config) overrideResolvConf(addrs []string) error {
	link, linkErr := os.Readlink(resolvConf)
	orig, readErr := os.ReadFile(resolvConf)
	var b strings.Builder
	b.WriteString("# May VPN — restored when the tunnel closes\n")
	for _, a := range addrs {
		fmt.Fprintf(&b, "nameserver %s\n", a)
	}
	// replace (not write through) a symlink, so its target is untouched
	tmp := resolvConf + ".mayvpn"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, resolvConf); err != nil {
		// a bind-mounted resolv.conf (containers, some immutable setups) cannot be replaced:
		// write it in place instead; undo puts the original bytes back the same way
		os.Remove(tmp)
		if readErr != nil {
			return err
		}
		if err := os.WriteFile(resolvConf, []byte(b.String()), 0o644); err != nil {
			return err
		}
		c.push(func() { os.WriteFile(resolvConf, orig, 0o644) })
		return nil
	}
	c.push(func() {
		switch {
		case linkErr == nil:
			os.Remove(resolvConf)
			os.Symlink(link, resolvConf)
		case readErr == nil:
			os.WriteFile(resolvConf, orig, 0o644)
		}
	})
	return nil
}

func (c *Config) KillSwitch(iface string, allow []Allow, lan bool, _ []netip.Addr) error {
	c.Run("", nftBin, "delete", "table", "inet", NftTable) // a stale one from a crashed run
	if _, err := c.Run(NftRules(iface, allow, lan), nftBin, "-f", "-"); err != nil {
		return err
	}
	c.push(func() { c.Run("", nftBin, "delete", "table", "inet", NftTable) })
	return nil
}

func IsForeignTunnel(gw Gateway) bool { return isTunnelName(gw.Iface) }

// Sweep removes leftovers of a previous run that died without Undo.
func Sweep(run Runner) {
	if run == nil {
		run = System
	}
	run("", nftBin, "delete", "table", "inet", NftTable)
}
