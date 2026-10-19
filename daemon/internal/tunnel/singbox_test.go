package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"mayvpn/internal/profile"
)

// a throwaway self-signed cert, only so the OpenVPN TLS block parses
const testCA = `-----BEGIN CERTIFICATE-----
MIIBejCCASGgAwIBAgIUQ7oVbV2l0Wn3e8m2l3XCWxEJ9yIwCgYIKoZIzj0EAwIw
EjEQMA4GA1UEAwwHbWF5LXZwbjAeFw0yNjAxMDEwMDAwMDBaFw0zNjAxMDEwMDAw
MDBaMBIxEDAOBgNVBAMMB21heS12cG4wWTATBgcqhkjOPQIBBggqhkjOPQMBBwNC
AARPw2y6mJr0b2E0Xk0h0hZ0c6z9rjT9V8k6V0nV6xQ2l2rGQ0a5o1kqE4m8hQxk
mU0cH8tGQ5wDqk0Y4Y2v4V9Ko1MwUTAdBgNVHQ4EFgQU2Zt0xD7j0YQ0aJpQ8nQH
o6V6c0kwHwYDVR0jBBgwFoAU2Zt0xD7j0YQ0aJpQ8nQHo6V6c0kwDwYDVR0TAQH/
BAUwAwEB/zAKBggqhkjOPQQDAgNHADBEAiAQ7yq7O9i8n1o1p2YQqQyD7X8kVJ9T
x2rj9Rk7Q0QJ2AIgGQ6ZpWb3m7H6aP1VrWg0bq2gT4Ue1n0p8M3vF4bZ0aE=
-----END CERTIFICATE-----`

func TestSingBoxConfigs(t *testing.T) {
	links := []string{
		"vless://11111111-2222-3333-4444-555555555555@vps.example.com:443?type=tcp&security=reality&pbk=" +
			"Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw&sid=ab12&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#v",
		"vless://11111111-2222-3333-4444-555555555555@vps.example.com:443?type=ws&security=tls&path=%2Fws&host=cdn.example.com",
		"vless://11111111-2222-3333-4444-555555555555@vps.example.com:443?type=grpc&security=tls&serviceName=g",
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"vps.example.com","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":"0","net":"ws","path":"/v","tls":"tls"}`)),
		"trojan://secret@vps.example.com:443?sni=vps.example.com",
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pass")) + "@vps.example.com:8388",
		"ss://2022-blake3-aes-128-gcm:YWJjZGVmZ2hpamtsbW5vcA%3D%3D@vps.example.com:443",
		"hysteria2://pw@vps.example.com:8443?obfs=salamander&obfs-password=oo&sni=a.b",
		"client\nproto udp\nremote vps.example.com 1194\nremote vps2.example.com 443 tcp\ndata-ciphers AES-256-GCM\n<ca>\n" + testCA + "\n</ca>\n<auth-user-pass>\nmay\nsecret\n</auth-user-pass>\n<tls-crypt>\n-----BEGIN OpenVPN Static key V1-----\n" + strings.Repeat("0123456789abcdef0123456789abcdef\n", 16) + "-----END OpenVPN Static key V1-----\n</tls-crypt>\n",
	}
	for _, l := range links {
		p, err := profile.Parse(l)
		if err != nil {
			t.Fatalf("%s: %v", l[:20], err)
		}
		cfg, err := SingBoxConfig(p, netip.MustParseAddr("203.0.113.7"), "utun229")
		if err != nil {
			t.Fatalf("%s: %v", p.Kind, err)
		}
		var m map[string]any
		json.Unmarshal(cfg, &m)
		if m["log"].(map[string]any)["disabled"] != true {
			t.Fatal("logging must be disabled")
		}
		if !strings.Contains(string(cfg), `"203.0.113.7"`) {
			t.Fatalf("%s: server must be the pre-resolved IP: %s", p.Kind, cfg)
		}
		b, err := newBox(context.Background(), cfg)
		if err != nil {
			t.Fatalf("%s rejected by sing-box: %v\n%s", p.Kind, err, cfg)
		}
		b.Close()
	}
}

func TestSingBoxRejectsWireGuard(t *testing.T) {
	p := &profile.Profile{Kind: profile.WireGuard, WG: &profile.WG{}}
	if _, err := SingBoxConfig(p, netip.MustParseAddr("1.1.1.1"), "utun229"); err == nil {
		t.Fatal("wireguard must not go through sing-box")
	}
}

func TestParseNetstat(t *testing.T) {
	out := `Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
utun7      1380  <Link#15>                            9     0       1234        2     0        200     0
utun7      1380  fe80::2a6b: fe80:f::2a6b:d8eb        0     -          0        2     -        200     -
`
	rx, tx := parseNetstat(out, "utun7")
	if rx != 1234 || tx != 200 {
		t.Fatalf("rx=%d tx=%d", rx, tx)
	}
}
