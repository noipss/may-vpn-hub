//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func waitForStop(cleanup func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	<-sig
	cleanup()
}
