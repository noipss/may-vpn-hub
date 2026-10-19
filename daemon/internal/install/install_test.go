package install

import (
	"strings"
	"testing"
)

func TestUnit(t *testing.T) {
	u := UnitFor("/usr/local/lib/mayvpn/mayvpnd", "1000")
	for _, want := range []string{"ExecStart=/usr/local/lib/mayvpn/mayvpnd serve --uid 1000", "StandardOutput=null", "StandardError=null", "Restart=always", "WantedBy=multi-user.target"} {
		if !strings.Contains(u, want) {
			t.Fatalf("unit misses %q:\n%s", want, u)
		}
	}
}
