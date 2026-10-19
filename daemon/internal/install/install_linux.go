package install

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	Dir    = "/usr/local/lib/mayvpn"
	Binary = Dir + "/mayvpnd"
	Unit   = "/etc/systemd/system/mayvpnd.service"
	Socket = "/run/mayvpn.sock"
	label  = "mayvpnd.service"
)

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Install copies the running binary, writes the unit and starts it. Must run as root (pkexec).
func Install(owner string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("install must run as root")
	}
	if uid, err := strconv.Atoi(owner); err != nil || uid <= 0 {
		return fmt.Errorf("install needs the app user's uid")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("May VPN needs systemd to run its helper")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	systemctl("stop", label) // fine if it was not there
	os.Remove(Socket)
	if err := copyFile(self, Binary, 0o755); err != nil {
		return err
	}
	if err := writeFile(Unit, []byte(UnitFor(Binary, owner)), 0o644); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", "--now", label); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(Socket); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the helper was installed but did not start (systemctl status %s)", label)
}

// Uninstall stops the unit (which restores the network on SIGTERM) and deletes its files.
func Uninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstall must run as root")
	}
	systemctl("disable", "--now", label)
	for _, f := range []string{Unit, Binary, Socket} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	os.Remove(Dir)
	return systemctl("daemon-reload")
}
