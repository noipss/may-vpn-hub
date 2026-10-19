// Package driver assembles a tunnel on macOS: engine + kill switch + addresses + routes + DNS,
// in an order that never lets traffic slip around the tunnel, with a full rollback on error.
package driver

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"mayvpn/internal/netcfg"
	"mayvpn/internal/profile"
	"mayvpn/internal/tunnel"
)

type Prefs struct {
	KillSwitch bool `json:"killSwitch"`
	AllowLAN   bool `json:"allowLan"`
}

var FallbackDNS = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.0.0.1")}

type Darwin struct {
	Run      netcfg.Runner
	Resolver *net.Resolver
}

// Resolve picks one address for a host, IPv4 first (most VPS endpoints and every split route).
func (d *Darwin) Resolve(ctx context.Context, host string) (netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip, nil
	}
	r := d.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	ips, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return netip.Addr{}, fmt.Errorf("cannot resolve %s", host)
	}
	for _, ip := range ips {
		if ip.Unmap().Is4() {
			return ip.Unmap(), nil
		}
	}
	return ips[0], nil
}

// Start brings the tunnel up. The returned stop func undoes everything.
func (d *Darwin) Start(ctx context.Context, p *profile.Profile, prefs Prefs) (tunnel.Tunnel, func(), error) {
	if err := ForeignVPN(netcfg.New(d.Run)); err != nil {
		return nil, nil, err
	}
	switch p.Kind {
	case profile.WireGuard, profile.AmneziaWG:
		return d.startWG(ctx, p, prefs)
	default:
		return d.startSing(ctx, p, prefs)
	}
}

func (d *Darwin) startWG(ctx context.Context, p *profile.Profile, prefs Prefs) (_ tunnel.Tunnel, _ func(), err error) {
	w := p.WG
	defer w.Wipe()
	eps := make([]netip.AddrPort, len(w.Peers))
	for i, pr := range w.Peers {
		ip, err := d.Resolve(ctx, pr.Host)
		if err != nil {
			return nil, nil, err
		}
		eps[i] = netip.AddrPortFrom(ip, pr.Port)
	}
	mtu := w.MTU
	if mtu == 0 {
		mtu = 1420
	}
	uapi := []byte(tunnel.UAPI(w, eps, p.Kind == profile.AmneziaWG))
	var t tunnel.Tunnel
	if p.Kind == profile.AmneziaWG {
		dev, e := tunnel.CreateAWGUTUN(mtu)
		if e != nil {
			return nil, nil, fmt.Errorf("create utun: %w", e)
		}
		t, err = tunnel.StartAmneziaWG(dev, uapi)
	} else {
		dev, e := tunnel.CreateUTUN(mtu)
		if e != nil {
			return nil, nil, fmt.Errorf("create utun: %w", e)
		}
		t, err = tunnel.StartWireGuard(dev, uapi)
	}
	if err != nil {
		return nil, nil, err
	}
	nc := netcfg.New(d.Run)
	stop := func() { nc.Undo(); t.Close() }
	defer func() {
		if err != nil {
			stop()
		}
	}()
	name := t.Name()

	if prefs.KillSwitch {
		allow := make([]netcfg.Allow, len(eps))
		for i, e := range eps {
			allow[i] = netcfg.Allow{Addr: e.Addr(), Port: e.Port(), Proto: "udp"}
		}
		if err = nc.KillSwitch(netcfg.PFRules(name, allow, prefs.AllowLAN)); err != nil {
			return nil, nil, fmt.Errorf("kill switch: %w", err)
		}
	}
	has6 := false
	for _, a := range w.Addresses {
		if err = nc.Address(name, a); err != nil {
			return nil, nil, err
		}
		has6 = has6 || a.Addr().Is6()
	}
	if err = nc.Up(name, 0); err != nil {
		return nil, nil, err
	}
	// keep the server itself reachable outside the tunnel
	for _, e := range eps {
		if !covered(w, e.Addr()) {
			continue
		}
		gw, gerr := nc.DefaultGateway(e.Addr().Is6())
		if gerr != nil {
			err = gerr
			return nil, nil, err
		}
		if err = nc.PinHost(e.Addr(), gw); err != nil {
			return nil, nil, err
		}
	}
	full4 := false
	for _, pr := range w.Peers {
		for _, a := range pr.AllowedIPs {
			if a.Addr().Is6() && !has6 {
				continue
			}
			if err = nc.RouteVia(name, a); err != nil {
				return nil, nil, err
			}
			full4 = full4 || (a.Bits() == 0 && a.Addr().Is4())
		}
	}
	if full4 && !has6 {
		if err = nc.Blackhole(netip.MustParsePrefix("::/0")); err != nil {
			return nil, nil, err
		}
	}
	dns := w.DNS
	if len(dns) == 0 && w.FullTunnel() {
		dns = FallbackDNS
	}
	if len(dns) > 0 {
		if err = nc.OverrideDNS(dns); err != nil {
			return nil, nil, fmt.Errorf("dns: %w", err)
		}
	}
	return t, stop, nil
}

// ForeignVPN refuses to start inside another VPN: its tunnel owns the default route, so our
// server would be reached through it and the kill switch would cut that VPN's own traffic.
func ForeignVPN(nc *netcfg.Config) error {
	gw, err := nc.DefaultGateway(false)
	if err != nil {
		return nil // no default route: the resolve / dial step reports that more clearly
	}
	if isTunnel(gw.Iface) {
		return fmt.Errorf("another VPN is on (%s) — turn it off first, May VPN can't run inside it", gw.Iface)
	}
	return nil
}

func isTunnel(iface string) bool {
	for _, p := range []string{"utun", "ipsec", "ppp", "tun", "tap", "wg"} {
		if strings.HasPrefix(iface, p) {
			return true
		}
	}
	return false
}

func covered(w *profile.WG, ip netip.Addr) bool {
	for _, pr := range w.Peers {
		for _, a := range pr.AllowedIPs {
			if a.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// freeUTUN picks a high utun number nobody uses, for sing-box (it needs a name up front).
func freeUTUN() string {
	for i := 229; i < 260; i++ {
		n := fmt.Sprintf("utun%d", i)
		if _, err := net.InterfaceByName(n); err != nil {
			return n
		}
	}
	return "utun259"
}

func (d *Darwin) startSing(ctx context.Context, p *profile.Profile, prefs Prefs) (_ tunnel.Tunnel, _ func(), err error) {
	server, err := d.Resolve(ctx, p.Host)
	if err != nil {
		return nil, nil, err
	}
	name := freeUTUN()
	cfg, err := tunnel.SingBoxConfig(p, server, name)
	if err != nil {
		return nil, nil, err
	}
	nc := netcfg.New(d.Run)
	var t tunnel.Tunnel
	stop := func() {
		nc.Undo()
		if t != nil {
			t.Close()
		}
	}
	defer func() {
		if err != nil {
			stop()
		}
	}()
	if prefs.KillSwitch {
		allow := []netcfg.Allow{{Addr: server, Port: p.Port, Proto: "both"}}
		if err = nc.KillSwitch(netcfg.PFRules(name, allow, prefs.AllowLAN)); err != nil {
			return nil, nil, fmt.Errorf("kill switch: %w", err)
		}
	}
	if t, err = tunnel.StartSingBox(cfg, name); err != nil {
		return nil, nil, err
	}
	if err = nc.OverrideDNS([]netip.Addr{netip.MustParseAddr(tunnel.SingTunDNS)}); err != nil {
		return nil, nil, fmt.Errorf("dns: %w", err)
	}
	return t, stop, nil
}
