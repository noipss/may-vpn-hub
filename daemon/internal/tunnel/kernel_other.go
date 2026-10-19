//go:build !linux

package tunnel

import (
	"errors"
	"net/netip"

	"mayvpn/internal/profile"
)

var ErrNoKernel = errors.New("no kernel WireGuard")

// StartKernelWG exists on Linux only; elsewhere wireguard-go is the engine.
func StartKernelWG(*profile.WG, []netip.AddrPort) (Tunnel, error) { return nil, ErrNoKernel }
