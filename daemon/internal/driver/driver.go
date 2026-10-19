// Package driver assembles a tunnel: engine + kill switch + addresses + routes + DNS, in an order
// that never lets traffic slip around the tunnel, with a full rollback on error. The OS-specific
// parts live in netcfg (macOS pf/scutil, Linux ip/nft/resolved, Windows winipcfg/WFP) and in the
// tunnel engines (Linux prefers kernel WireGuard).
package driver

import (
	"context"
	"errors"
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

// Driver works on every OS; Run is only used by the command-driven netcfg (macOS, Linux).
type Driver struct {
	Run      netcfg.Runner
	Resolver *net.Resolver
}

// Resolve picks one address for a host, IPv4 first (most VPS endpoints and every split route).
func (d *Driver) Resolve(ctx context.Context, host string) (netip.Addr, error) {
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
func (d *Driver) Start(ctx context.Context, p *profile.Profile, prefs Prefs) (tunnel.Tunnel, func(), error) {
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

func (d *Driver) startWG(ctx context.Context, p *profile.Profile, prefs Prefs) (_ tunnel.Tunnel, _ func(), err error) {
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
	var t tunnel.Tunnel
	switch {
	case p.Kind == profile.AmneziaWG:
		dev, e := tunnel.CreateAWGTUN(mtu)
		if e != nil {
			return nil, nil, fmt.Errorf("create tunnel interface: %w", e)
		}
		t, err = tunnel.StartAmneziaWG(dev, []byte(tunnel.UAPI(w, eps, true)))
	default:
		// kernel WireGuard where there is one (Linux), wireguard-go everywhere else
		if t, err = tunnel.StartKernelWG(w, eps); errors.Is(err, tunnel.ErrNoKernel) {
			dev, e := tunnel.CreateTUN(mtu)
			if e != nil {
				return nil, nil, fmt.Errorf("create tunnel interface: %w", e)
			}
			t, err = tunnel.StartWireGuard(dev, []byte(tunnel.UAPI(w, eps, false)))
		}
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
	dns := w.DNS
	if len(dns) == 0 && w.FullTunnel() {
		dns = FallbackDNS
	}

	if prefs.KillSwitch {
		allow := make([]netcfg.Allow, len(eps))
		for i, e := range eps {
			allow[i] = netcfg.Allow{Addr: e.Addr(), Port: e.Port(), Proto: "udp"}
		}
		if err = nc.KillSwitch(name, allow, prefs.AllowLAN, dns); err != nil {
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
	if err = nc.Up(name, mtu); err != nil {
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
		if err = nc.Blackhole(name, netip.MustParsePrefix("::/0")); err != nil {
			return nil, nil, err
		}
	}
	if len(dns) > 0 {
		if err = nc.OverrideDNS(name, dns); err != nil {
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
	if netcfg.IsForeignTunnel(gw) {
		name, _, _ := strings.Cut(gw.Iface, "|")
		return fmt.Errorf("another VPN is on (%s) — turn it off first, May VPN can't run inside it", name)
	}
	return nil
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

func (d *Driver) startSing(ctx context.Context, p *profile.Profile, prefs Prefs) (_ tunnel.Tunnel, _ func(), err error) {
	server, err := d.Resolve(ctx, p.Host)
	if err != nil {
		return nil, nil, err
	}
	name := tunnel.SingTunName()
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
	dns := []netip.Addr{netip.MustParseAddr(tunnel.SingTunDNS)}
	killSwitch := func() error {
		if !prefs.KillSwitch {
			return nil
		}
		allow := []netcfg.Allow{{Addr: server, Port: p.Port, Proto: "both"}}
		if err := nc.KillSwitch(name, allow, prefs.AllowLAN, dns); err != nil {
			return fmt.Errorf("kill switch: %w", err)
		}
		return nil
	}
	if !netcfg.KillSwitchAfterTunnel {
		if err = killSwitch(); err != nil {
			return nil, nil, err
		}
	}
	if t, err = tunnel.StartSingBox(cfg, name); err != nil {
		return nil, nil, err
	}
	if netcfg.KillSwitchAfterTunnel {
		if err = killSwitch(); err != nil {
			return nil, nil, err
		}
	}
	if err = nc.OverrideDNS(name, dns); err != nil {
		return nil, nil, fmt.Errorf("dns: %w", err)
	}
	return t, stop, nil
}
