package tunnel

import (
	"net"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// TunName is the Wintun adapter name; wintun.dll must sit next to mayvpnd.exe.
const TunName = "MayVPN"

func SingTunName() string { return "MayVPN" }

func ifaceBytes(name string) (rx, tx uint64) {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return 0, 0
	}
	luid, err := winipcfg.LUIDFromIndex(uint32(ifc.Index))
	if err != nil {
		return 0, 0
	}
	row, err := luid.Interface()
	if err != nil {
		return 0, 0
	}
	return row.InOctets, row.OutOctets
}
