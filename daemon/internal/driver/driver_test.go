package driver

import (
	"strings"
	"testing"

	"mayvpn/internal/netcfg"
)

func gwRunner(iface string) netcfg.Runner {
	return func(stdin, name string, args ...string) (string, error) {
		return "   route to: default\n    gateway: 10.0.0.1\n  interface: " + iface + "\n", nil
	}
}

func TestForeignVPN(t *testing.T) {
	if err := ForeignVPN(netcfg.New(gwRunner("en0"))); err != nil {
		t.Fatalf("en0 is a normal uplink: %v", err)
	}
	for _, i := range []string{"utun6", "ipsec0", "ppp0"} {
		err := ForeignVPN(netcfg.New(gwRunner(i)))
		if err == nil || !strings.Contains(err.Error(), i) || !strings.Contains(err.Error(), "turn it off") {
			t.Fatalf("%s: want a clear refusal, got %v", i, err)
		}
	}
}
