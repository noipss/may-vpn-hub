package profile

import (
	"fmt"
	"strconv"
	"strings"
)

type OVPNRemote struct {
	Host    string
	Port    uint16
	Network string // udp | tcp
}

// OVPN holds what sing-box's openvpn-client endpoint needs. Certificates must be inline.
type OVPN struct {
	Remotes      []OVPNRemote
	CA           string
	Cert         string
	Key          string
	WrapType     string // tls_auth | tls_crypt | tls_crypt_v2
	WrapKey      string
	KeyDirection string // server | client (tls-auth)
	DataCiphers  []string
	Fallback     string
	Auth         string
	Username     string
	Password     string
	VerifyName   string
	VerifyType   string
	TLSMin       string
	Compression  string
	BlockIPv6    bool
}

// directives that run programs or touch the filesystem; never honoured.
var ovpnHooks = map[string]bool{
	"up": true, "down": true, "route-up": true, "route-pre-down": true, "ipchange": true, "tls-verify": true,
	"client-connect": true, "learn-address": true, "script-security": true, "plugin": true, "log": true,
	"log-append": true, "status": true, "writepid": true, "daemon": true, "management": true,
}

func parseOVPN(text string) (*Profile, error) {
	o := &OVPN{}
	p := &Profile{Kind: OpenVPN, OVPN: o}
	defProto, defPort := "udp", 1194
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	type pending struct{ host, port, proto string }
	var remotes []pending
	keyDirection := ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		// inline blocks: <ca> … </ca>
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") && !strings.HasPrefix(line, "</") {
			tag := line[1 : len(line)-1]
			var body []string
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "</"+tag+">"; i++ {
				body = append(body, lines[i])
			}
			if i >= len(lines) {
				return nil, fmt.Errorf("<%s> is not closed", tag)
			}
			b := strings.TrimSpace(strings.Join(body, "\n"))
			switch tag {
			case "ca":
				o.CA = b
			case "cert":
				o.Cert = b
			case "key":
				o.Key = b
			case "tls-auth":
				o.WrapType, o.WrapKey = "tls_auth", b
			case "tls-crypt":
				o.WrapType, o.WrapKey = "tls_crypt", b
			case "tls-crypt-v2":
				o.WrapType, o.WrapKey = "tls_crypt_v2", b
			case "auth-user-pass":
				up := strings.SplitN(b, "\n", 2)
				if len(up) == 2 {
					o.Username, o.Password = strings.TrimSpace(up[0]), strings.TrimSpace(up[1])
				}
			default:
				p.Ignored = append(p.Ignored, "<"+tag+">")
			}
			continue
		}
		f := strings.Fields(line)
		k, args := strings.ToLower(f[0]), f[1:]
		arg := func(n int) string {
			if n < len(args) {
				return args[n]
			}
			return ""
		}
		switch k {
		case "remote":
			remotes = append(remotes, pending{arg(0), arg(1), arg(2)})
		case "proto":
			defProto = arg(0)
		case "port", "rport":
			if n, err := strconv.Atoi(arg(0)); err == nil {
				defPort = n
			}
		case "ca", "cert", "key", "tls-auth", "tls-crypt", "tls-crypt-v2", "pkcs12", "auth-user-pass":
			if k == "tls-auth" && arg(1) != "" {
				keyDirection = arg(1)
			}
			if arg(0) != "" && arg(0) != "[inline]" {
				return nil, fmt.Errorf("%s points to a file (%s) — export the profile with inline certificates", k, arg(0))
			}
		case "key-direction":
			keyDirection = arg(0)
		case "data-ciphers", "ncp-ciphers":
			o.DataCiphers = strings.Split(arg(0), ":")
		case "cipher", "data-ciphers-fallback":
			o.Fallback = arg(0)
		case "auth":
			o.Auth = arg(0)
		case "verify-x509-name":
			o.VerifyName, o.VerifyType = arg(0), arg(1)
		case "tls-version-min":
			o.TLSMin = arg(0)
		case "compress":
			o.Compression = first(arg(0), "stub")
		case "comp-lzo":
			if arg(0) != "no" {
				o.Compression = "lzo"
			}
		case "block-ipv6":
			o.BlockIPv6 = true
		default:
			if ovpnHooks[k] {
				p.Ignored = append(p.Ignored, k)
			}
		}
	}
	if len(remotes) == 0 {
		return nil, fmt.Errorf("OpenVPN profile has no remote")
	}
	for _, r := range remotes {
		port := defPort
		if r.port != "" {
			n, err := strconv.Atoi(r.port)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("bad remote port %q", r.port)
			}
			port = n
		}
		network := strings.ToLower(first(r.proto, defProto))
		switch {
		case strings.HasPrefix(network, "tcp"):
			network = "tcp"
		case strings.HasPrefix(network, "udp"):
			network = "udp"
		default:
			return nil, fmt.Errorf("unknown proto %q", network)
		}
		o.Remotes = append(o.Remotes, OVPNRemote{Host: r.host, Port: uint16(port), Network: network})
	}
	if o.CA == "" {
		return nil, fmt.Errorf("OpenVPN profile has no inline <ca>")
	}
	if (o.Cert == "") != (o.Key == "") {
		return nil, fmt.Errorf("OpenVPN profile has <cert> without <key> (or the other way round)")
	}
	if o.Cert == "" && o.Username == "" {
		return nil, fmt.Errorf("OpenVPN profile needs a client certificate or an inline <auth-user-pass>")
	}
	switch keyDirection {
	case "0":
		o.KeyDirection = "server"
	case "1":
		o.KeyDirection = "client"
	}
	p.Host, p.Port = o.Remotes[0].Host, o.Remotes[0].Port
	return p, nil
}
