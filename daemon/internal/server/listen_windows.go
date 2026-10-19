package server

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/ipc/namedpipe"
)

// Listen opens the named pipe. Its ACL admits SYSTEM, Administrators and the owner SID only —
// Windows checks it on every open, so there is nothing left for peerOK to do.
func Listen(path, owner string) (net.Listener, error) {
	sddl := "O:SYD:P(A;;GA;;;SY)(A;;GA;;;BA)"
	if owner != "" {
		if !strings.HasPrefix(owner, "S-1-") || strings.ContainsAny(owner, "();") {
			return nil, fmt.Errorf("bad owner SID %q", owner)
		}
		sddl += "(A;;GA;;;" + owner + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	return (&namedpipe.ListenConfig{SecurityDescriptor: sd}).Listen(path)
}

func Cleanup(string) {}

func (s *Server) peerOK(net.Conn) bool { return true }
