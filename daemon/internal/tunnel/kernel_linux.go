package tunnel

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"mayvpn/internal/profile"
)

// ErrNoKernel: the kernel has no WireGuard module; the caller falls back to wireguard-go.
var ErrNoKernel = errors.New("no kernel WireGuard")

type kernelTunnel struct {
	c    *wgctrl.Client
	name string
}

// StartKernelWG creates a kernel WireGuard link (fastest path on Linux) and configures it over
// netlink. Addresses, routes and DNS are the caller's job, as for every engine.
func StartKernelWG(w *profile.WG, endpoints []netip.AddrPort) (Tunnel, error) {
	ip, err := exec.LookPath("ip")
	if err != nil {
		ip = "/usr/sbin/ip"
	}
	exec.Command(ip, "link", "del", "dev", TunName).Run() // stale link from a crashed run
	if out, err := exec.Command(ip, "link", "add", "dev", TunName, "type", "wireguard").CombinedOutput(); err != nil {
		if strings.Contains(string(out), "Unknown device type") || strings.Contains(string(out), "not supported") {
			return nil, ErrNoKernel
		}
		return nil, fmt.Errorf("ip link add: %s", strings.TrimSpace(string(out)))
	}
	del := func() { exec.Command(ip, "link", "del", "dev", TunName).Run() }
	c, err := wgctrl.New()
	if err != nil {
		del()
		return nil, err
	}
	cfg := wgtypes.Config{PrivateKey: (*wgtypes.Key)(&w.PrivateKey), ReplacePeers: true}
	for i, p := range w.Peers {
		pc := wgtypes.PeerConfig{
			PublicKey:         wgtypes.Key(p.PublicKey),
			Endpoint:          net.UDPAddrFromAddrPort(endpoints[i]),
			ReplaceAllowedIPs: true,
		}
		if p.PresharedKey != nil {
			k := wgtypes.Key(*p.PresharedKey)
			pc.PresharedKey = &k
		}
		if p.Keepalive > 0 {
			d := time.Duration(p.Keepalive) * time.Second
			pc.PersistentKeepaliveInterval = &d
		}
		for _, a := range p.AllowedIPs {
			pc.AllowedIPs = append(pc.AllowedIPs, net.IPNet{IP: a.Addr().AsSlice(), Mask: net.CIDRMask(a.Bits(), a.Addr().BitLen())})
		}
		cfg.Peers = append(cfg.Peers, pc)
	}
	if err := c.ConfigureDevice(TunName, cfg); err != nil {
		c.Close()
		del()
		return nil, fmt.Errorf("configure %s: %w", TunName, err)
	}
	return &kernelTunnel{c: c, name: TunName}, nil
}

func (t *kernelTunnel) Name() string { return t.name }

func (t *kernelTunnel) Counters() (rx, tx uint64, hs time.Time) {
	d, err := t.c.Device(t.name)
	if err != nil {
		return
	}
	for _, p := range d.Peers {
		rx += uint64(p.ReceiveBytes)
		tx += uint64(p.TransmitBytes)
		if p.LastHandshakeTime.After(hs) {
			hs = p.LastHandshakeTime
		}
	}
	return
}

func (t *kernelTunnel) Close() error {
	t.c.Close()
	ip, err := exec.LookPath("ip")
	if err != nil {
		ip = "/usr/sbin/ip"
	}
	return exec.Command(ip, "link", "del", "dev", t.name).Run()
}
