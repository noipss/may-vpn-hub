// Package install puts the helper into the OS service manager (launchd, systemd, Windows
// services) and takes it out again. Those few files are the only trace May VPN leaves in the
// system, and Uninstall removes them.
package install

import "fmt"

// PlistFor renders the launchd job. Output goes to /dev/null: the helper keeps no logs.
func PlistFor(label, binary string, uid int) string {
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
`, label, binary, uid)
}

// UnitFor renders the systemd unit for Linux. Output goes nowhere: the helper keeps no logs.
func UnitFor(binary, uid string) string {
	return fmt.Sprintf(`[Unit]
Description=May VPN helper
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s serve --uid %s
Restart=always
RestartSec=1
StandardOutput=null
StandardError=null
LogLevelMax=0

[Install]
WantedBy=multi-user.target
`, binary, uid)
}
