package main

import (
	"os"
	"os/signal"

	"golang.org/x/sys/windows/svc"

	"mayvpn/internal/install"
)

// waitForStop returns when the service manager asks us to stop (or on Ctrl+C when run by hand).
func waitForStop(cleanup func()) {
	if is, _ := svc.IsWindowsService(); is {
		svc.Run(install.Service, handler{cleanup})
		return
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	cleanup()
}

type handler struct{ cleanup func() }

func (h handler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			s <- svc.Status{State: svc.StopPending}
			h.cleanup()
			return false, 0
		}
	}
	return false, 0
}
