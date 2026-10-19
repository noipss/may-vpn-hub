package tunnel

import (
	"os"
	"strconv"
	"strings"
)

// TunName is our interface on Linux (kernel WireGuard link or TUN device).
const TunName = "mayvpn0"

func SingTunName() string { return "mayvpn1" }

func ifaceBytes(name string) (rx, tx uint64) {
	read := func(f string) uint64 {
		b, _ := os.ReadFile("/sys/class/net/" + name + "/statistics/" + f)
		n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		return n
	}
	return read("rx_bytes"), read("tx_bytes")
}
