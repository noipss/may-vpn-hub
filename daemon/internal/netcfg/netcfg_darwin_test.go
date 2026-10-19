//go:build darwin

package netcfg

import (
	"net/netip"
	"os/exec"
	"strings"
	"testing"
)

type rec struct{ calls []string }

func (r *rec) run(stdin, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	if stdin != "" {
		line += " <<" + strings.ReplaceAll(strings.TrimSpace(stdin), "\n", "; ")
	}
	r.calls = append(r.calls, line)
	switch {
	case name == route && len(args) > 1 && args[1] == "get":
		return "   route to: default\ndestination: default\n    gateway: 192.168.1.1\n  interface: en0\n", nil
	case name == scutil && strings.HasPrefix(stdin, "show State:/Network/Global"):
		return "<dictionary> {\n  PrimaryInterface : en0\n  PrimaryService : 1234-ABCD\n}\n", nil
	case name == scutil && strings.HasPrefix(stdin, "show State:/Network/Service"):
		return "<dictionary> {\n  DomainName : home.lan\n  SearchDomains : <array> {\n    0 : home.lan\n  }\n  ServerAddresses : <array> {\n    0 : 192.168.1.1\n    1 : 8.8.8.8\n  }\n}\n", nil
	case name == pfctl && len(args) == 1 && args[0] == "-E":
		return "pf enabled\nToken : 7788\n", nil
	}
	return "", nil
}

func TestRoutesAndUndo(t *testing.T) {
	r := &rec{}
	c := New(r.run)
	if err := c.Address("utun5", netip.MustParsePrefix("10.8.0.2/32")); err != nil {
		t.Fatal(err)
	}
	gw, err := c.DefaultGateway(false)
	if err != nil || gw.Addr.String() != "192.168.1.1" || gw.Iface != "en0" {
		t.Fatalf("gateway %+v %v", gw, err)
	}
	c.PinHost(netip.MustParseAddr("203.0.113.7"), gw)
	c.RouteVia("utun5", netip.MustParsePrefix("0.0.0.0/0"))
	c.Blackhole("utun5", netip.MustParsePrefix("::/0"))
	r.calls = nil
	c.Undo()
	want := []string{
		"/sbin/route -q -n delete -inet6 8000::/1 ::1 -blackhole",
		"/sbin/route -q -n delete -inet6 ::/1 ::1 -blackhole",
		"/sbin/route -q -n delete -inet 128.0.0.0/1 -interface utun5",
		"/sbin/route -q -n delete -inet 0.0.0.0/1 -interface utun5",
		"/sbin/route -q -n delete -inet 203.0.113.7/32",
	}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("undo order:\n%s", strings.Join(r.calls, "\n"))
	}
	c.Undo()
	if len(r.calls) != len(want) {
		t.Fatal("second Undo must be a no-op")
	}
}

func TestDNSOverrideRestoresOriginal(t *testing.T) {
	r := &rec{}
	c := New(r.run)
	if err := c.OverrideDNS("utun5", []netip.Addr{netip.MustParseAddr("10.8.0.1")}); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(r.calls, "\n")
	if !strings.Contains(all, "d.add ServerAddresses * 10.8.0.1; set State:/Network/Service/1234-ABCD/DNS") {
		t.Fatalf("override missing:\n%s", all)
	}
	r.calls = nil
	c.Undo()
	all = strings.Join(r.calls, "\n")
	if !strings.Contains(all, "d.add ServerAddresses * 192.168.1.1 8.8.8.8; d.add SearchDomains * home.lan; d.add DomainName home.lan; set State:/Network/Service/1234-ABCD/DNS") {
		t.Fatalf("restore missing:\n%s", all)
	}
}

func TestKillSwitch(t *testing.T) {
	r := &rec{}
	c := New(r.run)
	allow := []Allow{{Addr: netip.MustParseAddr("203.0.113.7"), Port: 51820, Proto: "udp"}}
	rules := PFRules("utun5", allow, false)
	if err := c.KillSwitch("utun5", allow, false, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(rules), "block drop quick all") || !strings.Contains(rules, "pass out quick inet proto udp to 203.0.113.7 port 51820") {
		t.Fatalf("rules:\n%s", rules)
	}
	if strings.Contains(rules, "192.168.0.0/16") {
		t.Fatal("LAN must be closed unless allowed")
	}
	r.calls = nil
	c.Undo()
	if r.calls[0] != "/sbin/pfctl -X 7788" || r.calls[1] != "/sbin/pfctl -a com.apple/250.MayVPN -F all" {
		t.Fatalf("undo: %v", r.calls)
	}
}

// pfctl -n parses without loading; it needs /dev/pf, so it only runs where we can open it.
func TestPFSyntax(t *testing.T) {
	rules := PFRules("utun5", []Allow{{Addr: netip.MustParseAddr("203.0.113.7"), Port: 443, Proto: "both"}, {Addr: netip.MustParseAddr("2001:db8::1"), Port: 1, Proto: "udp"}}, true)
	cmd := exec.Command(pfctl, "-n", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "Permission denied") {
		t.Skip("pfctl -n needs root here")
	}
	if err != nil {
		t.Fatalf("pf rejected rules: %s\n%s", out, rules)
	}
}
