// Package profile parses connection keys: WireGuard / AmneziaWG .conf, OpenVPN .ovpn and
// vless:// vmess:// ss:// trojan:// hysteria2:// links. Parsing is pure — no I/O, no logging.
package profile

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

type Kind string

const (
	WireGuard   Kind = "wireguard"
	AmneziaWG   Kind = "amneziawg"
	VLESS       Kind = "vless"
	VMess       Kind = "vmess"
	Shadowsocks Kind = "shadowsocks"
	Trojan      Kind = "trojan"
	Hysteria2   Kind = "hysteria2"
	OpenVPN     Kind = "openvpn"
)

var Labels = map[Kind]string{
	WireGuard: "WireGuard", AmneziaWG: "AmneziaWG", VLESS: "VLESS", VMess: "VMess",
	Shadowsocks: "Shadowsocks", Trojan: "Trojan", Hysteria2: "Hysteria2", OpenVPN: "OpenVPN",
}

// Profile is one parsed key. Exactly one of WG / Proxy / OVPN is set.
type Profile struct {
	Kind    Kind
	Name    string
	Host    string
	Port    uint16
	Ignored []string // directives we refuse to act on (PostUp scripts, up/down hooks…)
	WG      *WG
	Proxy   *Proxy
	OVPN    *OVPN
}

// Summary is what the UI may see: no secrets.
type Summary struct {
	Kind    Kind     `json:"kind"`
	Label   string   `json:"label"`
	Name    string   `json:"name,omitempty"`
	Server  string   `json:"server"`
	Ignored []string `json:"ignored,omitempty"`
	Details []string `json:"details,omitempty"`
}

func (p *Profile) Summary() Summary {
	s := Summary{Kind: p.Kind, Label: Labels[p.Kind], Name: p.Name, Server: net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))), Ignored: p.Ignored}
	switch {
	case p.WG != nil:
		for _, a := range p.WG.Addresses {
			s.Details = append(s.Details, "address "+a.String())
		}
		if p.WG.FullTunnel() {
			s.Details = append(s.Details, "all traffic")
		}
		if p.Kind == AmneziaWG {
			s.Details = append(s.Details, "obfuscation on")
		}
	case p.Proxy != nil:
		if p.Proxy.Security != "" {
			s.Details = append(s.Details, p.Proxy.Security)
		}
		if p.Proxy.Transport != "" && p.Proxy.Transport != "tcp" {
			s.Details = append(s.Details, p.Proxy.Transport)
		}
	case p.OVPN != nil:
		s.Details = append(s.Details, p.OVPN.Remotes[0].Network)
	}
	return s
}

var ErrUnknown = errors.New("not a WireGuard / AmneziaWG config, OpenVPN profile or vless:// vmess:// ss:// trojan:// hysteria2:// link")

// Parse detects the format and parses it.
func Parse(text string) (*Profile, error) {
	t := strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	if t == "" {
		return nil, errors.New("empty key")
	}
	if i := strings.Index(t, "://"); i > 0 && i < 12 && !strings.ContainsAny(t[:i], " \n[") {
		return parseLink(t)
	}
	low := strings.ToLower(t)
	if strings.Contains(low, "[interface]") {
		return parseWG(t)
	}
	if strings.Contains(low, "\nremote ") || strings.HasPrefix(low, "remote ") || strings.Contains(low, "<ca>") {
		return parseOVPN(t)
	}
	return nil, ErrUnknown
}

func splitHostPort(hp string, def int) (string, uint16, error) {
	host, ps, err := net.SplitHostPort(hp)
	if err != nil {
		if def == 0 {
			return "", 0, fmt.Errorf("bad address %q", hp)
		}
		host, ps = strings.Trim(hp, "[]"), strconv.Itoa(def)
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("bad port in %q", hp)
	}
	if host == "" {
		return "", 0, fmt.Errorf("missing host in %q", hp)
	}
	return host, uint16(port), nil
}
