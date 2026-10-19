package netcfg

import (
	"net/netip"
	"strings"
	"testing"
)

func mustPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }

func TestParseDefault(t *testing.T) {
	g, err := parseDefault("default via 192.168.1.1 dev wlp2s0 proto dhcp src 192.168.1.20 metric 600\ndefault via 10.0.0.1 dev eth1 metric 700\n")
	if err != nil || g.Addr.String() != "192.168.1.1" || g.Iface != "wlp2s0" {
		t.Fatalf("%+v %v", g, err)
	}
	g, err = parseDefault("default dev ppp0 scope link\n")
	if err != nil || g.Addr.IsValid() || g.Iface != "ppp0" {
		t.Fatalf("link route: %+v %v", g, err)
	}
	if _, err := parseDefault(""); err == nil {
		t.Fatal("no default route must fail")
	}
}

type rec struct{ calls []string }

func (r *rec) run(stdin, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

func TestLinuxUndo(t *testing.T) {
	r := &rec{}
	c := New(r.run)
	c.RouteVia("mayvpn0", mustPrefix("0.0.0.0/0"))
	c.KillSwitch("mayvpn0", nil, false, nil)
	r.calls = nil
	c.Undo()
	got := strings.Join(r.calls, "\n")
	if !strings.Contains(got, "delete table inet mayvpn") || !strings.Contains(got, "route del 128.0.0.0/1 dev mayvpn0") {
		t.Fatalf("undo:\n%s", got)
	}
}
