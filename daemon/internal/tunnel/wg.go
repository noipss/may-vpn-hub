package tunnel

import (
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

type wgTunnel struct {
	dev  *device.Device
	name string
}

// StartWireGuard brings up wireguard-go on tdev (a real utun or a netstack TUN in tests).
func StartWireGuard(tdev tun.Device, uapi []byte) (Tunnel, error) {
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
	return &wgTunnel{dev: dev, name: name}, nil
}

// CreateUTUN opens the next free utun for wireguard-go.
func CreateUTUN(mtu int) (tun.Device, error) { return tun.CreateTUN("utun", mtu) }

func (t *wgTunnel) Name() string { return t.name }
func (t *wgTunnel) Counters() (uint64, uint64, time.Time) {
	s, err := t.dev.IpcGet()
	if err != nil {
		return 0, 0, time.Time{}
	}
	return parseGet(s)
}
func (t *wgTunnel) Close() error { t.dev.Close(); return nil }
