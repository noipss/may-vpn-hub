package tunnel

import (
	"time"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun"
)

type awgTunnel struct {
	dev  *device.Device
	name string
}

// StartAmneziaWG is StartWireGuard on the amneziawg-go fork (junk packets, padded and
// re-labelled handshakes against DPI).
func StartAmneziaWG(tdev tun.Device, uapi []byte) (Tunnel, error) {
	defer wipe(uapi)
	name, _ := tdev.Name()
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
	if err := dev.IpcSet(string(uapi)); err != nil {
		dev.Close()
		return nil, err
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, err
	}
	return &awgTunnel{dev: dev, name: name}, nil
}

func CreateAWGUTUN(mtu int) (tun.Device, error) { return tun.CreateTUN("utun", mtu) }

func (t *awgTunnel) Name() string { return t.name }
func (t *awgTunnel) Counters() (uint64, uint64, time.Time) {
	s, err := t.dev.IpcGet()
	if err != nil {
		return 0, 0, time.Time{}
	}
	return parseGet(s)
}
func (t *awgTunnel) Close() error { t.dev.Close(); return nil }
