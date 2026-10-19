//go:build windows

package netcfg

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/firewall"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// KillSwitchAfterTunnel: WFP permits the tunnel by its LUID, which exists only once the adapter does.
const KillSwitchAfterTunnel = true

func luidOf(iface string) (winipcfg.LUID, error) {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return 0, fmt.Errorf("interface %s: %w", iface, err)
	}
	return winipcfg.LUIDFromIndex(uint32(ifc.Index))
}

func afOf(a netip.Addr) winipcfg.AddressFamily {
	if a.Is4() {
		return windows.AF_INET
	}
	return windows.AF_INET6
}

func (c *Config) Address(iface string, p netip.Prefix) error {
	luid, err := luidOf(iface)
	if err != nil {
		return err
	}
	return luid.AddIPAddress(p)
}

// Up sets the MTU and a top-priority metric, so Windows prefers the tunnel's DNS too.
func (c *Config) Up(iface string, mtu int) error {
	luid, err := luidOf(iface)
	if err != nil {
		return err
	}
	for _, f := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		row, err := luid.IPInterface(f)
		if err != nil {
			continue // the family may be disabled on the adapter
		}
		if mtu > 0 {
			row.NLMTU = uint32(mtu)
		}
		row.UseAutomaticMetric = false
		row.Metric = 0
		row.Set()
	}
	return nil
}

func (c *Config) RouteVia(iface string, p netip.Prefix) error {
	luid, err := luidOf(iface)
	if err != nil {
		return err
	}
	for _, h := range split(p) {
		h := h
		next := netip.IPv4Unspecified()
		if h.Addr().Is6() {
			next = netip.IPv6Unspecified()
		}
		if err := luid.AddRoute(h, next, 0); err != nil {
			return fmt.Errorf("route %s: %w", h, err)
		}
		c.push(func() { luid.DeleteRoute(h, next) })
	}
	return nil
}

// Blackhole: Windows has no blackhole routes; sending the family into the tunnel (which has no
// address for it) drops it just the same and never lets it out around the tunnel.
func (c *Config) Blackhole(iface string, p netip.Prefix) error { return c.RouteVia(iface, p) }

func (c *Config) DefaultGateway(v6 bool) (Gateway, error) {
	f := winipcfg.AddressFamily(windows.AF_INET)
	if v6 {
		f = windows.AF_INET6
	}
	rows, err := winipcfg.GetIPForwardTable2(f)
	if err != nil {
		return Gateway{}, err
	}
	best := -1
	var bestMetric uint32
	for i := range rows {
		r := &rows[i]
		if r.DestinationPrefix.PrefixLength != 0 {
			continue
		}
		ifRow, err := r.InterfaceLUID.Interface()
		if err != nil || ifRow.OperStatus != winipcfg.IfOperStatusUp {
			continue
		}
		ipIf, err := r.InterfaceLUID.IPInterface(f)
		metric := r.Metric
		if err == nil {
			metric += ipIf.Metric
		}
		if best < 0 || metric < bestMetric {
			best, bestMetric = i, metric
		}
	}
	if best < 0 {
		return Gateway{}, fmt.Errorf("no default route")
	}
	r := &rows[best]
	ifRow, _ := r.InterfaceLUID.Interface()
	g := Gateway{Addr: r.NextHop.Addr(), Index: int(r.InterfaceIndex)}
	if ifRow != nil {
		g.Iface = ifRow.Alias() + "|" + ifRow.Description()
	}
	if g.Addr.IsUnspecified() {
		g.Addr = netip.Addr{}
	}
	return g, nil
}

func (c *Config) PinHost(ip netip.Addr, gw Gateway) error {
	luid, err := winipcfg.LUIDFromIndex(uint32(gw.Index))
	if err != nil {
		return err
	}
	h := netip.PrefixFrom(ip, ip.BitLen())
	next := gw.Addr
	if !next.IsValid() {
		next = netip.IPv4Unspecified()
		if ip.Is6() {
			next = netip.IPv6Unspecified()
		}
	}
	if err := luid.AddRoute(h, next, 0); err != nil {
		return err
	}
	c.push(func() { luid.DeleteRoute(h, next) })
	return nil
}

func (c *Config) OverrideDNS(iface string, servers []netip.Addr) error {
	luid, err := luidOf(iface)
	if err != nil {
		return err
	}
	byFam := map[winipcfg.AddressFamily][]netip.Addr{}
	for _, s := range servers {
		byFam[afOf(s)] = append(byFam[afOf(s)], s)
	}
	for f, list := range byFam {
		f := f
		if err := luid.SetDNS(f, list, nil); err != nil {
			return err
		}
		c.push(func() { luid.FlushDNS(f) })
	}
	return nil
}

// KillSwitch: a dynamic WFP session that permits this process (our encrypted packets), loopback,
// the tunnel, DHCP and NDP, restricts DNS to the tunnel's servers and blocks everything else.
// Windows removes dynamic sessions when the process exits, so a crash cannot leave it behind.
// LAN exceptions are not offered by WFP here: allowLan is ignored on Windows.
func (c *Config) KillSwitch(iface string, _ []Allow, _ bool, dns []netip.Addr) error {
	luid, err := luidOf(iface)
	if err != nil {
		return err
	}
	if err := firewall.EnableFirewall(uint64(luid), false, dns); err != nil {
		return err
	}
	c.push(firewall.DisableFirewall)
	return nil
}

// IsForeignTunnel looks at the adapter's name and description (Wintun, TAP, other VPN drivers).
func IsForeignTunnel(gw Gateway) bool {
	low := strings.ToLower(gw.Iface)
	for _, w := range []string{"wintun", "tap-windows", "tap adapter", "wireguard", "openvpn", "vpn", "tunnel"} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// Sweep: nothing survives a crash on Windows — WFP sessions are dynamic and routes die with the adapter.
func Sweep(Runner) {}
