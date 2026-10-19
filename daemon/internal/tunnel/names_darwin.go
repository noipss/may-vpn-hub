package tunnel

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// TunName: macOS hands out the next free utunN for "utun".
const TunName = "utun"

// SingTunName picks a high utun number nobody uses (sing-box needs a name up front).
func SingTunName() string {
	for i := 229; i < 260; i++ {
		n := fmt.Sprintf("utun%d", i)
		if _, err := net.InterfaceByName(n); err != nil {
			return n
		}
	}
	return "utun259"
}

// ifaceBytes reads interface byte counters from netstat (Ibytes / Obytes on the link row).
func ifaceBytes(name string) (rx, tx uint64) {
	out, err := exec.Command("/usr/sbin/netstat", "-I", name, "-b", "-n").Output()
	if err != nil {
		return 0, 0
	}
	return parseNetstat(string(out), name)
}

func parseNetstat(out, name string) (rx, tx uint64) {
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return
	}
	head := strings.Fields(lines[0])
	ib, ob := -1, -1
	for i, h := range head {
		switch h {
		case "Ibytes":
			ib = i
		case "Obytes":
			ob = i
		}
	}
	for _, l := range lines[1:] {
		f := strings.Fields(l)
		// the <Link#n> row has an empty Address column on utun, so count from the right
		if len(f) < 4 || f[0] != name || !strings.HasPrefix(f[2], "<Link") || ib < 0 || ob < 0 {
			continue
		}
		off := len(head) - len(f)
		rx, _ = strconv.ParseUint(f[ib-off], 10, 64)
		tx, _ = strconv.ParseUint(f[ob-off], 10, 64)
		return
	}
	return
}
