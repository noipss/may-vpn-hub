package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"

	"mayvpn/internal/profile"
)

// Inside the sing-box TUN: 172.19.0.1 is us, 172.19.0.2 is the DNS address it hijacks.
const (
	SingTunAddr = "172.19.0.1/30"
	SingTunDNS  = "172.19.0.2"
)

type obj = map[string]any

// SingBoxConfig renders the whole sing-box config for one proxy profile. server is the
// pre-resolved address of p.Host (so no DNS query leaves outside the tunnel later).
func SingBoxConfig(p *profile.Profile, server netip.Addr, tunName string) ([]byte, error) {
	var out obj
	var endpoint obj
	switch {
	case p.Proxy != nil:
		o, err := proxyOutbound(p, server)
		if err != nil {
			return nil, err
		}
		out = o
	case p.OVPN != nil:
		endpoint = ovpnEndpoint(p, server)
	default:
		return nil, fmt.Errorf("%s does not run on sing-box", p.Kind)
	}
	cfg := obj{
		"log": obj{"disabled": true},
		"dns": obj{
			"servers": []obj{{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "proxy"}},
			"final":   "remote",
		},
		"inbounds": []obj{{
			"type": "tun", "tag": "tun", "interface_name": tunName,
			"address": []string{SingTunAddr, "fdfe:dcba:9876::1/126"},
			"mtu":     1500, "auto_route": true, "strict_route": true, "stack": "system",
			"dns_address": []string{SingTunDNS},
		}},
		"outbounds": []obj{{"type": "direct", "tag": "direct"}},
		"route": obj{
			"rules": []obj{
				{"action": "sniff"},
				{"protocol": "dns", "action": "hijack-dns"},
			},
			"final":                 "proxy",
			"auto_detect_interface": true,
		},
	}
	if out != nil {
		cfg["outbounds"] = append([]obj{out}, cfg["outbounds"].([]obj)...)
	}
	if endpoint != nil {
		cfg["endpoints"] = []obj{endpoint}
	}
	return json.Marshal(cfg)
}

func tlsBlock(x *profile.Proxy) obj {
	if x.Security == "" {
		return nil
	}
	t := obj{"enabled": true}
	if x.SNI != "" {
		t["server_name"] = x.SNI
	}
	if x.Insecure {
		t["insecure"] = true
	}
	if len(x.ALPN) > 0 {
		t["alpn"] = x.ALPN
	}
	if x.Fingerprint != "" {
		t["utls"] = obj{"enabled": true, "fingerprint": x.Fingerprint}
	}
	if x.Security == "reality" {
		r := obj{"enabled": true, "public_key": x.RealityPub}
		if x.RealitySID != "" {
			r["short_id"] = x.RealitySID
		}
		t["reality"] = r
	}
	return t
}

func transportBlock(x *profile.Proxy) obj {
	switch x.Transport {
	case "", "tcp":
		return nil
	case "ws":
		t := obj{"type": "ws", "path": first(x.Path, "/")}
		if x.HostHeader != "" {
			t["headers"] = obj{"Host": x.HostHeader}
		}
		return t
	case "grpc":
		return obj{"type": "grpc", "service_name": x.ServiceName}
	case "httpupgrade":
		return obj{"type": "httpupgrade", "path": first(x.Path, "/"), "host": x.HostHeader}
	case "http":
		t := obj{"type": "http", "path": first(x.Path, "/")}
		if x.HostHeader != "" {
			t["host"] = []string{x.HostHeader}
		}
		return t
	}
	return obj{"type": x.Transport}
}

func proxyOutbound(p *profile.Profile, server netip.Addr) (obj, error) {
	x := p.Proxy
	o := obj{"tag": "proxy", "server": server.String(), "server_port": p.Port}
	switch p.Kind {
	case profile.VLESS:
		o["type"], o["uuid"] = "vless", x.UUID
		if x.Flow != "" {
			o["flow"] = x.Flow
		}
	case profile.VMess:
		o["type"], o["uuid"], o["security"], o["alter_id"] = "vmess", x.UUID, x.Method, x.AlterID
	case profile.Trojan:
		o["type"], o["password"] = "trojan", x.Password
	case profile.Shadowsocks:
		o["type"], o["method"], o["password"] = "shadowsocks", x.Method, x.Password
	case profile.Hysteria2:
		o["type"], o["password"] = "hysteria2", x.Password
		if x.Obfs != "" {
			o["obfs"] = obj{"type": x.Obfs, "password": x.ObfsPassword}
		}
		if x.UpMbps > 0 {
			o["up_mbps"] = x.UpMbps
		}
		if x.DownMbps > 0 {
			o["down_mbps"] = x.DownMbps
		}
	default:
		return nil, fmt.Errorf("unsupported proxy %s", p.Kind)
	}
	if t := tlsBlock(x); t != nil {
		if t["server_name"] == nil && server.String() != p.Host {
			t["server_name"] = p.Host
		}
		o["tls"] = t
	}
	if t := transportBlock(x); t != nil {
		o["transport"] = t
	}
	return o, nil
}

func ovpnEndpoint(p *profile.Profile, server netip.Addr) obj {
	o := p.OVPN
	tls := obj{"certificate": o.CA}
	if o.Cert != "" {
		tls["client_certificate"], tls["client_key"] = o.Cert, o.Key
	}
	if o.WrapType != "" {
		w := obj{"type": o.WrapType, "key": o.WrapKey}
		if o.WrapType == "tls_auth" && o.KeyDirection != "" {
			w["direction"] = o.KeyDirection
		}
		tls["control_wrap"] = w
	}
	if o.VerifyName != "" {
		tls["server_name"] = o.VerifyName
		switch o.VerifyType {
		case "name-prefix":
			tls["server_name_type"] = "name-prefix"
		case "subject":
			tls["server_name_type"] = "subject"
		default:
			tls["server_name_type"] = "name"
		}
	}
	if o.TLSMin != "" && o.TLSMin != "1.0" && o.TLSMin != "1.1" {
		tls["version_min"] = o.TLSMin
	}
	// only the first remote is pinned to the resolved address; the rest stay as written
	servers := []obj{}
	for i, r := range o.Remotes {
		host := r.Host
		if i == 0 {
			host = server.String()
		}
		servers = append(servers, obj{"server": host, "server_port": r.Port, "network": r.Network})
	}
	e := obj{"type": "openvpn-client", "tag": "proxy", "servers": servers, "tls": tls, "block_ipv6": o.BlockIPv6}
	if len(o.DataCiphers) > 0 {
		e["data_ciphers"] = o.DataCiphers
	}
	if o.Fallback != "" {
		e["data_ciphers_fallback"] = o.Fallback
	}
	if o.Auth != "" {
		e["auth"] = o.Auth
	}
	if o.Username != "" {
		e["username"], e["password"] = o.Username, o.Password
	}
	switch o.Compression {
	case "":
	case "lzo":
		e["compression_lzo"] = "adaptive"
		e["allow_compression"] = "asym"
	default:
		e["compression"] = o.Compression
		e["allow_compression"] = "asym"
	}
	return e
}

func first(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// newBox runs the config through sing-box's own decoder and constructor (no Start), so
// schema mistakes surface as errors before any network change.
func newBox(ctx context.Context, cfg []byte) (*box.Box, error) {
	ctx = include.Context(ctx)
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, cfg)
	if err != nil {
		return nil, err
	}
	return box.New(box.Options{Context: ctx, Options: opts})
}

type singTunnel struct {
	box    *box.Box
	cancel context.CancelFunc
	name   string
}

// StartSingBox builds and starts sing-box with a TUN named tunName (auto_route on).
func StartSingBox(cfg []byte, tunName string) (Tunnel, error) {
	defer wipe(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	b, err := newBox(ctx, cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := b.Start(); err != nil {
		b.Close()
		cancel()
		return nil, err
	}
	return &singTunnel{box: b, cancel: cancel, name: tunName}, nil
}

func (t *singTunnel) Name() string { return t.name }
func (t *singTunnel) Counters() (uint64, uint64, time.Time) {
	rx, tx := ifaceBytes(t.name)
	return rx, tx, time.Time{}
}
func (t *singTunnel) Close() error {
	err := t.box.Close()
	t.cancel()
	return err
}
