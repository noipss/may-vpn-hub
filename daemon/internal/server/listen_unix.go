//go:build !windows

package server

import (
	"net"
	"os"
	"strconv"
	"syscall"
)

// Listen creates the socket owned by the owner uid with mode 0600 (replacing a stale one).
func Listen(path, owner string) (net.Listener, error) {
	os.Remove(path)
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if uid, err := strconv.Atoi(owner); err == nil && uid >= 0 && os.Getuid() == 0 {
		if err := os.Chown(path, uid, 0); err != nil {
			ln.Close()
			return nil, err
		}
	}
	return ln, nil
}

// Cleanup removes the socket file when the helper stops.
func Cleanup(path string) { os.Remove(path) }

// peerOK: only the owner (or root) may talk to us, whatever the file mode says.
func (s *Server) peerOK(c net.Conn) bool {
	if s.Owner == "" {
		return true
	}
	want, err := strconv.Atoi(s.Owner)
	if err != nil {
		return false
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	uid := -1
	raw.Control(func(fd uintptr) { uid = peerUID(int(fd)) })
	return uid == want || uid == 0
}
