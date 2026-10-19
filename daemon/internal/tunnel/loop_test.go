package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	awgtun "github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/crypto/curve25519"
	wgtun "golang.zx2c4.com/wireguard/tun"
	wgnet "golang.zx2c4.com/wireguard/tun/netstack"

	"mayvpn/internal/profile"
)

func keypair(t *testing.T) (priv, pub profile.Key) {
	if _, err := rand.Read(priv[:]); err != nil {
		t.Fatal(err)
	}
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	p, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	copy(pub[:], p)
	return
}

func freeUDP(t *testing.T) int {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

type side struct {
	tun   Tunnel
	stack *wgnet.Net
}

// pair builds a server and a client over 127.0.0.1 and checks a TCP echo through the tunnel.
func runLoop(t *testing.T, awg [][2]string, start func(addr netip.Addr, uapi []byte) side) {
	srvPriv, srvPub := keypair(t)
	cliPriv, cliPub := keypair(t)
	port := freeUDP(t)

	// server: hand-written UAPI (the VPS side)
	srvCfg := fmt.Sprintf("private_key=%s\nlisten_port=%d\n", hex.EncodeToString(srvPriv[:]), port)
	for _, kv := range awg {
		srvCfg += kv[0] + "=" + kv[1] + "\n"
	}
	srvCfg += fmt.Sprintf("public_key=%s\nallowed_ip=10.9.0.2/32\n", hex.EncodeToString(cliPub[:]))
	srv := start(netip.MustParseAddr("10.9.0.1"), []byte(srvCfg))
	defer srv.tun.Close()

	// client: our UAPI() from a parsed-profile shape
	w := &profile.WG{
		PrivateKey: cliPriv,
		Addresses:  []netip.Prefix{netip.MustParsePrefix("10.9.0.2/32")},
		AWG:        awg,
		Peers: []profile.Peer{{
			PublicKey: srvPub, Host: "127.0.0.1", Port: uint16(port), Keepalive: 1,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/24")},
		}},
	}
	cli := start(netip.MustParseAddr("10.9.0.2"), []byte(UAPI(w, []netip.AddrPort{netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port))}, awg != nil)))
	defer cli.tun.Close()

	ln, err := srv.stack.ListenTCPAddrPort(netip.MustParseAddrPort("10.9.0.1:7777"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		io.Copy(c, c)
		c.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := cli.stack.DialContextTCPAddrPort(ctx, netip.MustParseAddrPort("10.9.0.1:7777"))
	if err != nil {
		t.Fatal("dial through tunnel:", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	msg := []byte("meow through the tunnel")
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil || string(got) != string(msg) {
		t.Fatalf("echo: %q %v", got, err)
	}
	rx, tx, hs := cli.tun.Counters()
	if rx == 0 || tx == 0 || hs.IsZero() {
		t.Fatalf("counters rx=%d tx=%d handshake=%v", rx, tx, hs)
	}
}

func TestWireGuardLoop(t *testing.T) {
	runLoop(t, nil, func(addr netip.Addr, uapi []byte) side {
		tdev, tnet, err := wgnet.CreateNetTUN([]netip.Addr{addr}, nil, 1420)
		if err != nil {
			t.Fatal(err)
		}
		tu, err := StartWireGuard(tdev, uapi)
		if err != nil {
			t.Fatal(err)
		}
		return side{tu, tnet}
	})
}

func TestAmneziaWGLoop(t *testing.T) {
	obf := [][2]string{{"jc", "4"}, {"jmin", "40"}, {"jmax", "70"}, {"s1", "15"}, {"s2", "42"}, {"h1", "1234567"}, {"h2", "2345678"}, {"h3", "3456789"}, {"h4", "4567890"}}
	runLoop(t, obf, func(addr netip.Addr, uapi []byte) side {
		tdev, tnet, err := wgnet.CreateNetTUN([]netip.Addr{addr}, nil, 1420)
		if err != nil {
			t.Fatal(err)
		}
		tu, err := StartAmneziaWG(asAWG(tdev), uapi)
		if err != nil {
			t.Fatal(err)
		}
		return side{tu, tnet}
	})
}

func TestUAPIWipesInput(t *testing.T) {
	b := []byte("private_key=zz\n")
	wipe(b)
	for _, c := range b {
		if c != 0 {
			t.Fatal("not wiped")
		}
	}
}

// asAWG adapts wireguard-go's netstack TUN to amneziawg-go's identical interface (only the Event
// type differs); amneziawg-go's own netstack needs a gvisor release that does not build here.
type awgAdapter struct {
	wgtun.Device
	ev chan awgtun.Event
}

func asAWG(d wgtun.Device) awgtun.Device {
	a := &awgAdapter{Device: d, ev: make(chan awgtun.Event, 4)}
	go func() {
		for e := range d.Events() {
			a.ev <- awgtun.Event(e)
		}
		close(a.ev)
	}()
	return a
}

func (a *awgAdapter) Events() <-chan awgtun.Event { return a.ev }
