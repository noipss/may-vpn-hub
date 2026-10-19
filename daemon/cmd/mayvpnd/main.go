// mayvpnd — May VPN's privileged helper. It never logs: errors go back over the socket only.
//
//	mayvpnd serve [--uid N] [--socket PATH]   run under launchd (root)
//	mayvpnd install --uid N                    copy self into /Library and load the launchd job (root)
//	mayvpnd uninstall                          stop the job and delete every file it owns (root)
//	mayvpnd parse < key                        print a key's summary as JSON (no secrets, no root)
//	mayvpnd version
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"mayvpn/internal/driver"
	"mayvpn/internal/install"
	"mayvpn/internal/netcfg"
	"mayvpn/internal/profile"
	"mayvpn/internal/server"
	"mayvpn/internal/session"
)

const Version = "0.1.0"

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
	uid := fs.Int("uid", -1, "user allowed to talk to the helper")
	sock := fs.String("socket", install.Socket, "socket path")
	if err := fs.Parse(args); err != nil {
		fail(err)
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
		if err := install.Install(*uid); err != nil {
			fail(err)
		}
	case "uninstall":
		if err := install.Uninstall(); err != nil {
			fail(err)
		}
	case "serve":
		serve(*sock, *uid)
	default:
		fail(fmt.Errorf("unknown command %q", cmd))
	}
}

func serve(sock string, uid int) {
	netcfg.Sweep(nil) // a previous run may have died with the kill switch loaded
	sess := session.New(&driver.Darwin{}, session.HTTPProbe(ProbeURL))
	ln, err := server.Listen(sock, uid)
	if err != nil {
		os.Exit(1)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-sig
		sess.Disconnect() // restores routes, DNS and pf before we go
		ln.Close()
		os.Remove(sock)
		os.Exit(0)
	}()
	(&server.Server{Sess: sess, Version: Version, AllowUID: uid}).Serve(ln)
}

// fail is for the interactive subcommands (install / uninstall / parse); serve stays silent.
func fail(err error) {
	fmt.Fprintln(os.Stderr, "mayvpnd:", err)
	os.Exit(1)
}
