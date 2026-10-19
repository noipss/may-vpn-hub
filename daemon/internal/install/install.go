// Package install puts the helper into launchd and takes it out again. These two files are the
// only trace May VPN leaves in the system, and Uninstall removes both.
package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	Label  = "com.maymay.vpn.helper"
	Binary = "/Library/PrivilegedHelperTools/" + Label
	Plist  = "/Library/LaunchDaemons/" + Label + ".plist"
	Socket = "/var/run/" + "com.maymay.vpn.sock"
)

// PlistFor renders the launchd job. Output goes to /dev/null: the helper keeps no logs.
func PlistFor(uid int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
		<string>--uid</string>
		<string>%d</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>StandardOutPath</key><string>/dev/null</string>
	<key>StandardErrorPath</key><string>/dev/null</string>
	<key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`, Label, Binary, uid)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Install copies the running binary and (re)loads the job. Must run as root.
func Install(uid int) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("install must run as root")
	}
	if uid <= 0 {
		return fmt.Errorf("install needs the app user's uid")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	run("/bin/launchctl", "bootout", "system/"+Label) // fine if it was not loaded
	os.Remove(Socket)                                 // so the wait below only sees the new helper
	if err := copyFile(self, Binary, 0o755); err != nil {
		return err
	}
	if err := writeFile(Plist, []byte(PlistFor(uid)), 0o644); err != nil {
		return err
	}
	if err := run("/bin/launchctl", "bootstrap", "system", Plist); err != nil {
		// "Bootstrap failed: 5" usually means a half-loaded job: enable + kickstart instead
		run("/bin/launchctl", "enable", "system/"+Label)
		if err2 := run("/bin/launchctl", "kickstart", "-k", "system/"+Label); err2 != nil {
			return fmt.Errorf("launchd would not start the helper: %v", err)
		}
	}
	// installed is not the same as running: wait for the socket
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(Socket); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	out, _ := exec.Command("/bin/launchctl", "print", "system/"+Label).CombinedOutput()
	state := "unknown"
	for _, l := range strings.Split(string(out), "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "state =") || strings.HasPrefix(t, "last exit code =") {
			state = t
		}
	}
	return fmt.Errorf("the helper was installed but did not start (%s)", state)
}

// Uninstall stops the job (which restores the network on SIGTERM) and deletes both files.
func Uninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstall must run as root")
	}
	run("/bin/launchctl", "bootout", "system/"+Label)
	for _, f := range []string{Plist, Binary, Socket} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
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
	if err := os.Chown(tmp, 0, 0); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func writeFile(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chown(tmp, 0, 0); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
