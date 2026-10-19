package profile

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Proxy is a vless / vmess / shadowsocks / trojan / hysteria2 server.
type Proxy struct {
	UUID     string // vless, vmess
	Password string // trojan, hysteria2, shadowsocks
	Method   string // shadowsocks cipher, vmess security
	Flow     string // vless (xtls-rprx-vision)
	AlterID  int    // vmess

	Security    string // "", "tls", "reality"
	SNI         string
	Fingerprint string // uTLS fingerprint
	ALPN        []string
	Insecure    bool
	RealityPub  string
	RealitySID  string

	Transport   string // tcp, ws, grpc, http, httpupgrade
	Path        string
	HostHeader  string
	ServiceName string

	Obfs         string // hysteria2: salamander
	ObfsPassword string
	UpMbps       int
	DownMbps     int
}

func parseLink(t string) (*Profile, error) {
	scheme := strings.ToLower(t[:strings.Index(t, "://")])
	switch scheme {
	case "vmess":
		return parseVMess(t)
	case "ss":
		return parseSS(t)
	}
	u, err := url.Parse(t)
	if err != nil {
		return nil, fmt.Errorf("bad link: %w", err)
	}
	p := &Profile{Name: u.Fragment, Proxy: &Proxy{}}
	x := p.Proxy
	q := u.Query()
	p.Host, p.Port, err = splitHostPort(u.Host, 443)
	if err != nil {
		return nil, err
	}
	secret := u.User.Username()
	if pw, ok := u.User.Password(); ok {
		secret += ":" + pw
	}
	if secret == "" {
		return nil, fmt.Errorf("%s link has no credentials", scheme)
	}
	x.SNI = first(q.Get("sni"), q.Get("peer"))
	x.Fingerprint = q.Get("fp")
	x.Insecure = truthy(q.Get("allowInsecure")) || truthy(q.Get("insecure"))
	if a := q.Get("alpn"); a != "" {
		x.ALPN = strings.Split(a, ",")
	}
	transport(x, q.Get("type"), q.Get("path"), q.Get("host"), q.Get("serviceName"))

	switch scheme {
	case "vless":
		p.Kind = VLESS
		x.UUID = secret
		x.Flow = q.Get("flow")
		x.Security = q.Get("security")
		if x.Security == "none" {
			x.Security = ""
		}
		if x.Security == "reality" {
			x.RealityPub, x.RealitySID = q.Get("pbk"), q.Get("sid")
			if x.RealityPub == "" {
				return nil, fmt.Errorf("reality link has no pbk (public key)")
			}
			if x.Fingerprint == "" {
				x.Fingerprint = "chrome"
			}
		}
	case "trojan":
		p.Kind = Trojan
		x.Password = secret
		x.Security = "tls"
		if s := q.Get("security"); s == "reality" {
			return nil, fmt.Errorf("trojan over reality is not supported")
		}
	case "hysteria2", "hy2":
		p.Kind = Hysteria2
		x.Password = secret
		x.Security = "tls"
		x.Obfs = q.Get("obfs")
		x.ObfsPassword = q.Get("obfs-password")
		if x.Obfs != "" && x.Obfs != "salamander" {
			return nil, fmt.Errorf("unknown hysteria2 obfs %q", x.Obfs)
		}
		x.UpMbps, _ = strconv.Atoi(q.Get("upmbps"))
		x.DownMbps, _ = strconv.Atoi(q.Get("downmbps"))
		x.Transport = ""
	default:
		return nil, fmt.Errorf("unsupported link type %s://", scheme)
	}
	if x.Security != "" && x.SNI == "" && !isIP(p.Host) {
		x.SNI = p.Host
	}
	return p, nil
}

func transport(x *Proxy, typ, path, host, service string) {
	switch typ {
	case "", "tcp", "raw":
		x.Transport = "tcp"
	case "ws", "grpc", "httpupgrade":
		x.Transport = typ
	case "http", "h2":
		x.Transport = "http"
	default:
		x.Transport = typ // rejected later by sing-box with a clear message
	}
	x.Path, x.HostHeader, x.ServiceName = path, host, service
	if x.Transport == "grpc" && x.ServiceName == "" {
		x.ServiceName = path
	}
}

// vmess://base64(json) — the v2rayN format.
func parseVMess(t string) (*Profile, error) {
	b, err := b64(t[len("vmess://"):])
	if err != nil {
		return nil, fmt.Errorf("vmess link is not base64 JSON")
	}
	var j map[string]any
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("vmess link is not base64 JSON")
	}
	s := func(k string) string {
		switch v := j[k].(type) {
		case string:
			return v
		case float64:
			return strconv.Itoa(int(v))
		}
		return ""
	}
	port, err := strconv.Atoi(s("port"))
	if err != nil || port < 1 || port > 65535 || s("add") == "" || s("id") == "" {
		return nil, fmt.Errorf("vmess link misses add / port / id")
	}
	p := &Profile{Kind: VMess, Name: s("ps"), Host: s("add"), Port: uint16(port), Proxy: &Proxy{}}
	x := p.Proxy
	x.UUID = s("id")
	x.AlterID, _ = strconv.Atoi(s("aid"))
	x.Method = first(s("scy"), "auto")
	if s("tls") == "tls" {
		x.Security = "tls"
		x.SNI = first(s("sni"), s("host"))
		x.Fingerprint = s("fp")
		if a := s("alpn"); a != "" {
			x.ALPN = strings.Split(a, ",")
		}
	}
	transport(x, s("net"), s("path"), s("host"), s("path"))
	return p, nil
}

// ss://base64(method:password)@host:port#name (SIP002), ss://method:password@host:port,
// or legacy ss://base64(method:password@host:port)#name.
func parseSS(t string) (*Profile, error) {
	rest := t[len("ss://"):]
	name := ""
	if i := strings.Index(rest, "#"); i >= 0 {
		name, _ = url.PathUnescape(rest[i+1:])
		rest = rest[:i]
	}
	if i := strings.Index(rest, "?"); i >= 0 {
		q, _ := url.ParseQuery(rest[i+1:])
		if pl := q.Get("plugin"); pl != "" {
			return nil, fmt.Errorf("shadowsocks plugins (%s) are not supported", strings.Split(pl, ";")[0])
		}
		rest = rest[:i]
	}
	rest = strings.TrimSuffix(rest, "/")
	var cred, hp string
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		cred, hp = rest[:at], rest[at+1:]
		if dec, err := b64(cred); err == nil && strings.Contains(string(dec), ":") {
			cred = string(dec)
		} else if un, err := url.PathUnescape(cred); err == nil {
			cred = un
		}
	} else {
		dec, err := b64(rest)
		if err != nil {
			return nil, fmt.Errorf("bad ss:// link")
		}
		at := strings.LastIndex(string(dec), "@")
		if at < 0 {
			return nil, fmt.Errorf("bad ss:// link")
		}
		cred, hp = string(dec[:at]), string(dec[at+1:])
	}
	method, pw, ok := strings.Cut(cred, ":")
	if !ok || method == "" || pw == "" {
		return nil, fmt.Errorf("ss:// link has no method:password")
	}
	host, port, err := splitHostPort(hp, 0)
	if err != nil {
		return nil, err
	}
	return &Profile{Kind: Shadowsocks, Name: name, Host: host, Port: port, Proxy: &Proxy{Method: strings.ToLower(method), Password: pw}}, nil
}

func b64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("not base64")
}

func first(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func truthy(s string) bool { return s == "1" || strings.EqualFold(s, "true") }

func isIP(h string) bool {
	_, err := parsePrefix(h)
	return err == nil
}
