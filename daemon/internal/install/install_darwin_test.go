//go:build darwin

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlist(t *testing.T) {
	p := PlistFor(Label, Binary, 501)
	f := filepath.Join(t.TempDir(), "x.plist")
	os.WriteFile(f, []byte(p), 0o644)
	if out, err := exec.Command("/usr/bin/plutil", "-lint", f).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	for _, want := range []string{"<string>serve</string>", "<string>501</string>", "<key>StandardErrorPath</key><string>/dev/null</string>", Binary} {
		if !strings.Contains(p, want) {
			t.Fatalf("plist misses %s", want)
		}
	}
}
