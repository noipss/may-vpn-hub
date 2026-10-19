// mayvpnd — May VPN's privileged helper. It never logs: errors go back over the socket only.
//
//	mayvpnd serve [--uid N | --sid SID] [--socket PATH]   run under launchd / systemd / Windows services
//	mayvpnd install --uid N | --sid SID [--errfile F]     copy self into the system and start it (admin)
//	mayvpnd uninstall [--errfile F]                       stop it and delete every file it owns (admin)
//	mayvpnd parse < key                        print a key's summary as JSON (no secrets, no root)
//	mayvpnd version
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"mayvpn/internal/driver"
	"mayvpn/internal/install"
	"mayvpn/internal/netcfg"
	"mayvpn/internal/profile"
	"mayvpn/internal/server"
	"mayvpn/internal/session"
)

const Version = "0.2.0"

// ProbeURL answers fast over plain HTTP from anywhere; we only time the round trip.
const ProbeURL = "http://1.1.1.1/cdn-cgi/trace"

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	uid := fs.Int("uid", -1, "user allowed to talk to the helper (macOS, Linux)")
	sid := fs.String("sid", "", "user allowed to talk to the helper (Windows)")
	sock := fs.String("socket", install.Socket, "socket path / pipe name")
	fs.StringVar(&errFile, "errfile", "", "write the error here too (elevated runs have no console)")
	if err := fs.Parse(args); err != nil {
		fail(err)
	}
	owner := *sid
	if owner == "" && *uid >= 0 {
		owner = strconv.Itoa(*uid)
	}
	switch cmd {
	case "version":
		fmt.Println(Version)
	case "parse":
		b, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		p, err := profile.Parse(string(b))
		if err != nil {
			json.NewEncoder(os.Stdout).Encode(map[string]string{"error": err.Error()})
			os.Exit(1)
		}
		json.NewEncoder(os.Stdout).Encode(p.Summary())
	case "install":
		if err := install.Install(owner); err != nil {
			fail(err)
		}
	case "uninstall":
		if err := install.Uninstall(); err != nil {
			fail(err)
		}
	case "serve":
		serve(*sock, owner)
	default:
		fail(fmt.Errorf("unknown command %q", cmd))
	}
}

func serve(sock, owner string) {
	netcfg.Sweep(nil) // a previous run may have died with the kill switch loaded
	sess := session.New(&driver.Driver{}, session.HTTPProbe(ProbeURL))
	ln, err := server.Listen(sock, owner)
	if err != nil {
		os.Exit(1)
	}
	go (&server.Server{Sess: sess, Version: Version, Owner: owner}).Serve(ln)
	// signals, or the Windows service manager; cleanup runs before we report "stopped"
	waitForStop(func() {
		sess.Disconnect() // restores routes, DNS and the firewall before we go
		ln.Close()
		server.Cleanup(sock)
	})
}

var errFile string

// fail is for the interactive subcommands (install / uninstall / parse); serve stays silent.
func fail(err error) {
	fmt.Fprintln(os.Stderr, "mayvpnd:", err)
	if errFile != "" {
		os.WriteFile(errFile, []byte(err.Error()), 0o600)
	}
	os.Exit(1)
}
