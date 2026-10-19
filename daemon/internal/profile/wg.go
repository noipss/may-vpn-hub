package profile

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

type Key [32]byte

// parseKey reads a 44-character base64 WireGuard key. Errors say what is wrong with it without
// ever repeating the key itself.
func parseKey(s string) (Key, error) {
	var k Key
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return k, fmt.Errorf("the key is empty — it was not filled in when the config was printed")
	case strings.HasPrefix(s, "$"):
		return k, fmt.Errorf("the key is a shell variable name, not a key — the config was printed without it")
	case len(s) != 44:
		return k, fmt.Errorf("the key has %d characters, a WireGuard key has 44 ending with \"=\" — it got cut or wrapped while copying", len(s))
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return k, fmt.Errorf("the key has characters that are not base64 — copy it again")
	}
	copy(k[:], b)
	return k, nil
}

type WG struct {
	PrivateKey Key
	Addresses  []netip.Prefix
	DNS        []netip.Addr
	MTU        int
	Peers      []Peer
	// AmneziaWG obfuscation, already lower-cased UAPI keys in a stable order (jc, jmin, … itime).
	AWG [][2]string
}

type Peer struct {
	PublicKey    Key
	PresharedKey *Key
	Host         string
	Port         uint16
	AllowedIPs   []netip.Prefix
	Keepalive    int
}

// FullTunnel: some peer routes 0.0.0.0/0 (or ::/0).
func (w *WG) FullTunnel() bool {
	for _, p := range w.Peers {
		for _, a := range p.AllowedIPs {
			if a.Bits() == 0 {
				return true
			}
		}
	}
	return false
}

func (w *WG) Wipe() {
	for i := range w.PrivateKey {
		w.PrivateKey[i] = 0
	}
	for _, p := range w.Peers {
		if p.PresharedKey != nil {
			for i := range p.PresharedKey {
				p.PresharedKey[i] = 0
			}
		}
	}
}

var awgKeys = []string{"jc", "jmin", "jmax", "s1", "s2", "h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5", "j1", "j2", "j3", "itime"}

// hooks run commands as root in wg-quick; we never execute them.
var hookKeys = map[string]bool{"preup": true, "postup": true, "predown": true, "postdown": true, "saveconfig": true, "table": true}

func parseWG(text string) (*Profile, error) {
	w := &WG{}
	p := &Profile{Kind: WireGuard, WG: w}
	awg := map[string]string{}
	section := ""
	var peer *Peer
	lines, off := fromTerminal(text)
	for i, raw := range lines {
		n := i + off
		line := strings.TrimSpace(raw)
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if section == "peer" {
				w.Peers = append(w.Peers, Peer{})
				peer = &w.Peers[len(w.Peers)-1]
			} else if section != "interface" {
				return nil, fmt.Errorf("line %d: unknown section [%s]", n+1, section)
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", n+1)
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		var err error
		switch section {
		case "interface":
			switch {
			case k == "privatekey":
				w.PrivateKey, err = parseKey(v)
			case k == "address":
				for _, a := range list(v) {
					var pf netip.Prefix
					if pf, err = parsePrefix(a); err != nil {
						break
					}
					w.Addresses = append(w.Addresses, pf)
				}
			case k == "dns":
				for _, a := range list(v) {
					if ip, e := netip.ParseAddr(a); e == nil {
						w.DNS = append(w.DNS, ip)
					}
				}
			case k == "mtu":
				w.MTU, err = strconv.Atoi(v)
				if err == nil && (w.MTU < 1280 || w.MTU > 9000) {
					err = fmt.Errorf("MTU out of range")
				}
			case k == "listenport", k == "fwmark":
			case hookKeys[k]:
				p.Ignored = append(p.Ignored, k)
			case contains(awgKeys, k):
				awg[k] = v
			case k == "s3" || k == "s4":
				return nil, fmt.Errorf("AmneziaWG 2.0 parameter %s is not supported yet", strings.ToUpper(k))
			default:
				p.Ignored = append(p.Ignored, k)
			}
		case "peer":
			switch k {
			case "publickey":
				peer.PublicKey, err = parseKey(v)
			case "presharedkey":
				var psk Key
				psk, err = parseKey(v)
				peer.PresharedKey = &psk
			case "endpoint":
				peer.Host, peer.Port, err = splitHostPort(v, 0)
			case "allowedips":
				for _, a := range list(v) {
					var pf netip.Prefix
					if pf, err = parsePrefix(a); err != nil {
						break
					}
					peer.AllowedIPs = append(peer.AllowedIPs, pf.Masked())
				}
			case "persistentkeepalive":
				if v != "off" {
					peer.Keepalive, err = strconv.Atoi(v)
				}
			default:
				p.Ignored = append(p.Ignored, k)
			}
		default:
			return nil, fmt.Errorf("line %d: %s is outside [Interface] / [Peer] — copy the config from [Interface] to the end", n+1, k)
		}
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %w", n+1, k, err)
		}
	}
	if w.PrivateKey == (Key{}) {
		return nil, fmt.Errorf("[Interface] has no PrivateKey")
	}
	if len(w.Addresses) == 0 {
		return nil, fmt.Errorf("[Interface] has no Address")
	}
	if len(w.Peers) == 0 {
		return nil, fmt.Errorf("no [Peer]")
	}
	for i, pr := range w.Peers {
		if pr.PublicKey == (Key{}) {
			return nil, fmt.Errorf("peer %d has no PublicKey", i+1)
		}
		if pr.Host == "" {
			return nil, fmt.Errorf("peer %d has no Endpoint", i+1)
		}
	}
	for _, k := range awgKeys {
		if v, ok := awg[k]; ok {
			w.AWG = append(w.AWG, [2]string{k, v})
			if v != "0" && v != "" {
				p.Kind = AmneziaWG
			}
		}
	}
	p.Host, p.Port = w.Peers[0].Host, w.Peers[0].Port
	return p, nil
}

// fromTerminal cuts the config out of whatever was copied around it from a terminal: commands
// and the prompt before the first [section], a prompt or blank lines after the last setting.
// It returns the kept lines and the index of the first one (for line numbers in errors).
func fromTerminal(text string) ([]string, int) {
	all := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	isSection := func(t string) bool { return strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") }
	start := -1
	for i, l := range all {
		if isSection(strings.TrimSpace(l)) {
			start = i
			break
		}
	}
	if start < 0 {
		return all, 0
	}
	end := len(all)
	for end > start+1 {
		t := strings.TrimSpace(all[end-1])
		if isSection(t) || strings.Contains(t, "=") || (t != "" && (t[0] == '#' || t[0] == ';')) {
			break
		}
		end--
	}
	return all[start:end], start
}

func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		return netip.ParsePrefix(s)
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func list(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
