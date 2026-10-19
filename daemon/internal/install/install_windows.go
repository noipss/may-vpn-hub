package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"golang.zx2c4.com/wireguard/ipc/namedpipe"
)

const (
	Service = "MayVPN"
	Socket  = `\\.\pipe\mayvpn`
)

// Dir is %ProgramFiles%\MayVPN — only Administrators can write there, so nobody can swap the
// binary the service runs as SYSTEM.
func Dir() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "MayVPN")
}

func Binary() string { return filepath.Join(Dir(), "mayvpnd.exe") }

func elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// Install copies mayvpnd.exe and wintun.dll and registers an auto-start service. Needs UAC.
func Install(owner string) error {
	if !elevated() {
		return fmt.Errorf("install must run as Administrator")
	}
	if !strings.HasPrefix(owner, "S-1-") {
		return fmt.Errorf("install needs the app user's SID")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	removeService(m)
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	if err := copyFile(self, Binary()); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(filepath.Dir(self), "wintun.dll"), filepath.Join(Dir(), "wintun.dll")); err != nil {
		return fmt.Errorf("wintun.dll must sit next to mayvpnd.exe: %w", err)
	}
	s, err := m.CreateService(Service, Binary(), mgr.Config{
		DisplayName:  "May VPN helper",
		Description:  "Builds May VPN tunnels. Keeps no logs.",
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}, "serve", "--sid", owner)
	if err != nil {
		return err
	}
	defer s.Close()
	s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: time.Second}}, 60)
	if err := s.Start(); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		if c, err := namedpipe.DialTimeout(Socket, 100*time.Millisecond); err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the helper was installed but did not start")
}

// Uninstall stops the service (which restores the network) and deletes its folder.
func Uninstall() error {
	if !elevated() {
		return fmt.Errorf("uninstall must run as Administrator")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	removeService(m)
	return os.RemoveAll(Dir())
}

func removeService(m *mgr.Mgr) {
	s, err := m.OpenService(Service)
	if err != nil {
		return
	}
	defer s.Close()
	if st, err := s.Control(svc.Stop); err == nil {
		for i := 0; i < 50 && st.State != svc.Stopped; i++ {
			time.Sleep(100 * time.Millisecond)
			if st, err = s.Query(); err != nil {
				break
			}
		}
	}
	s.Delete()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
