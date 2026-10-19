// wgprobe (dev tool): "genkey" prints a client keypair + PSK; "probe <conf>" brings the config up in
// userspace (netstack, no root) and checks exit IP, latency and download speed through it.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"mayvpn/internal/profile"
	"mayvpn/internal/tunnel"
)

func main() {
	switch os.Args[1] {
	case "genkey":
		var priv [32]byte
		rand.Read(priv[:])
		priv[0] &= 248
		priv[31] = (priv[31] & 127) | 64
		pub, _ := curve25519.X25519(priv[:], curve25519.Basepoint)
		psk := make([]byte, 32)
		rand.Read(psk)
		e := base64.StdEncoding.EncodeToString
		fmt.Printf("%s %s %s\n", e(priv[:]), e(pub), e(psk))
	case "probe":
		b, _ := os.ReadFile(os.Args[2])
		p, err := profile.Parse(string(b))
		if err != nil {
			fail(err)
		}
		w := p.WG
		ep := netip.AddrPortFrom(netip.MustParseAddr(p.Host), p.Port)
		var addrs []netip.Addr
		for _, a := range w.Addresses {
			addrs = append(addrs, a.Addr())
		}
		mtu := w.MTU
		if mtu == 0 {
			mtu = 1420
		}
		tdev, tnet, err := netstack.CreateNetTUN(addrs, []netip.Addr{netip.MustParseAddr("1.1.1.1")}, mtu)
		if err != nil {
			fail(err)
		}
		t, err := tunnel.StartWireGuard(tdev, []byte(tunnel.UAPI(w, []netip.AddrPort{ep}, false)))
		if err != nil {
			fail(err)
		}
		defer t.Close()
		client := &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, n, a string) (net.Conn, error) { return tnet.DialContext(ctx, n, a) }}}
		// exit IP (first request also performs the handshake)
		start := time.Now()
		r, err := client.Get("http://1.1.1.1/cdn-cgi/trace")
		if err != nil {
			fail(fmt.Errorf("no traffic through the tunnel: %w", err))
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		for _, l := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(l, "ip=") || strings.HasPrefix(l, "colo=") {
				fmt.Println("exit", l)
			}
		}
		fmt.Printf("first request (incl. handshake): %d ms\n", time.Since(start).Milliseconds())
		var best time.Duration = 1 << 62
		for i := 0; i < 5; i++ {
			s := time.Now()
			r, err := client.Head("http://1.1.1.1/cdn-cgi/trace")
			if err == nil {
				r.Body.Close()
				if d := time.Since(s); d < best {
					best = d
				}
			}
		}
		fmt.Printf("ping through tunnel (HTTP RTT, best of 5): %d ms\n", best.Milliseconds())
		s := time.Now()
		r, err = client.Get("http://proof.ovh.net/files/100Mb.dat")
		if err == nil {
			n, _ := io.Copy(io.Discard, io.LimitReader(r.Body, 12_500_000))
			r.Body.Close()
			fmt.Printf("download through tunnel: %.1f Mbit/s (%d MB, userspace netstack)\n", float64(n)*8/1e6/time.Since(s).Seconds(), n/1_000_000)
		}
		rx, tx, hs := t.Counters()
		fmt.Printf("handshake %s ago, rx %d tx %d\n", time.Since(hs).Round(time.Second), rx, tx)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, "FAIL:", err); os.Exit(1) }
