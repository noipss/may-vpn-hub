// Package server speaks newline-delimited JSON over a unix socket to the May VPN app.
//
//	→ {"id":1,"op":"hello"}                          ← {"id":1,"ok":true,"version":"…","status":{…}}
//	→ {"id":2,"op":"connect","key":"…","prefs":{…}}  ← {"id":2,"ok":true}
//	→ {"id":3,"op":"disconnect"}                     ← {"id":3,"ok":true}   (after the network is restored)
//	→ {"id":4,"op":"parse","key":"…"}                ← {"id":4,"ok":true,"summary":{…}}
//	← {"ev":"status","status":{…}}  ← {"ev":"stats","stats":{…}}   pushed to every client
package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"

	"mayvpn/internal/driver"
	"mayvpn/internal/profile"
	"mayvpn/internal/session"
)

type Server struct {
	Sess     *session.Session
	Version  string
	AllowUID int // the user who installed the helper; -1 = anyone who can open the socket
}

type request struct {
	ID    int          `json:"id"`
	Op    string       `json:"op"`
	Key   string       `json:"key,omitempty"`
	Prefs driver.Prefs `json:"prefs"`
}

type reply struct {
	ID      int              `json:"id"`
	OK      bool             `json:"ok"`
	Error   string           `json:"error,omitempty"`
	Version string           `json:"version,omitempty"`
	Status  *session.Status  `json:"status,omitempty"`
	Summary *profile.Summary `json:"summary,omitempty"`
}

type push struct {
	Ev     string          `json:"ev"`
	Status *session.Status `json:"status,omitempty"`
	Stats  *session.Stats  `json:"stats,omitempty"`
}

// Listen creates the socket owned by uid with mode 0600 (replacing a stale one).
func Listen(path string, uid int) (net.Listener, error) {
	os.Remove(path)
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if uid >= 0 && os.Getuid() == 0 {
		if err := os.Chown(path, uid, 0); err != nil {
			ln.Close()
			return nil, err
		}
	}
	return ln, nil
}

func (s *Server) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if !s.peerOK(c) {
			c.Close()
			continue
		}
		go s.handle(c)
	}
}

func (s *Server) peerOK(c net.Conn) bool {
	if s.AllowUID < 0 {
		return true
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
	raw.Control(func(fd uintptr) {
		if cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED); err == nil {
			uid = int(cred.Uid)
		}
	})
	return uid == s.AllowUID || uid == 0
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	var wmu sync.Mutex
	enc := json.NewEncoder(c)
	send := func(v any) {
		wmu.Lock()
		enc.Encode(v)
		wmu.Unlock()
	}
	events, unsub := s.Sess.Subscribe()
	defer unsub()
	go func() {
		for e := range events {
			if e.Status != nil {
				send(push{Ev: "status", Status: e.Status})
			} else {
				send(push{Ev: "stats", Stats: e.Stats})
			}
		}
	}()

	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			send(reply{OK: false, Error: "bad request"})
			continue
		}
		go func(req request) { send(s.do(req)) }(req)
	}
}

func (s *Server) do(req request) reply {
	r := reply{ID: req.ID, OK: true}
	switch req.Op {
	case "hello", "status":
		st := s.Sess.Status()
		r.Version, r.Status = s.Version, &st
	case "parse":
		p, err := profile.Parse(req.Key)
		if err != nil {
			return reply{ID: req.ID, Error: err.Error()}
		}
		sum := p.Summary()
		r.Summary = &sum
	case "connect":
		p, err := profile.Parse(req.Key)
		if err != nil {
			return reply{ID: req.ID, Error: err.Error()}
		}
		if err := s.Sess.Connect(p, req.Prefs); err != nil {
			return reply{ID: req.ID, Error: err.Error()}
		}
	case "disconnect":
		s.Sess.Disconnect()
	default:
		return reply{ID: req.ID, Error: "unknown op " + req.Op}
	}
	return r
}
