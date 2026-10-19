package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"mayvpn/internal/driver"
	"mayvpn/internal/profile"
	"mayvpn/internal/session"
	"mayvpn/internal/tunnel"
)

type fakeTun struct{ rx atomic.Uint64 }

func (f *fakeTun) Name() string { return "utun99" }
func (f *fakeTun) Counters() (uint64, uint64, time.Time) {
	return f.rx.Add(125_000), 1000, time.Now()
}
func (f *fakeTun) Close() error { return nil }

type fakeDriver struct{ stopped atomic.Bool }

func (d *fakeDriver) Start(ctx context.Context, p *profile.Profile, prefs driver.Prefs) (tunnel.Tunnel, func(), error) {
	if !prefs.KillSwitch {
		return nil, nil, errors.New("test expects the kill switch on")
	}
	return &fakeTun{}, func() { d.stopped.Store(true) }, nil
}

func wgKey() string {
	k := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	return "[Interface]\nPrivateKey=" + k + "\nAddress=10.8.0.2/32\n[Peer]\nPublicKey=" + k + "\nEndpoint=203.0.113.7:51820\nAllowedIPs=0.0.0.0/0\n"
}

type client struct {
	c  net.Conn
	sc *bufio.Scanner
}

func (cl *client) send(t *testing.T, v any) {
	b, _ := json.Marshal(v)
	cl.c.Write(append(b, '\n'))
}

// next reads until a message matches.
func (cl *client) next(t *testing.T, match func(map[string]any) bool) map[string]any {
	t.Helper()
	cl.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for cl.sc.Scan() {
		var m map[string]any
		json.Unmarshal(cl.sc.Bytes(), &m)
		if match(m) {
			return m
		}
	}
	t.Fatal("no matching message:", cl.sc.Err())
	return nil
}

func start(t *testing.T, uid int) (string, *fakeDriver) {
	dir, _ := os.MkdirTemp("", "mv")
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	d := &fakeDriver{}
	sess := session.New(d, func(ctx context.Context) (time.Duration, error) { return 42 * time.Millisecond, nil })
	sess.Tick = 50 * time.Millisecond
	ln, err := Listen(path, uid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go (&Server{Sess: sess, Version: "test", AllowUID: uid}).Serve(ln)
	return path, d
}

func dial(t *testing.T, path string) *client {
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &client{c: c, sc: bufio.NewScanner(c)}
}

func TestFlow(t *testing.T) {
	path, d := start(t, os.Getuid())
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", st.Mode().Perm())
	}
	cl := dial(t, path)
	isID := func(id float64) func(map[string]any) bool {
		return func(m map[string]any) bool { return m["id"] == id }
	}

	cl.send(t, map[string]any{"id": 1, "op": "hello"})
	m := cl.next(t, isID(1))
	if m["version"] != "test" || m["status"].(map[string]any)["state"] != "idle" {
		t.Fatalf("hello %v", m)
	}

	cl.send(t, map[string]any{"id": 2, "op": "parse", "key": wgKey()})
	m = cl.next(t, isID(2))
	if m["summary"].(map[string]any)["label"] != "WireGuard" {
		t.Fatalf("parse %v", m)
	}

	cl.send(t, map[string]any{"id": 3, "op": "connect", "key": "garbage"})
	if m = cl.next(t, isID(3)); m["ok"] == true {
		t.Fatal("garbage key must fail")
	}

	cl.send(t, map[string]any{"id": 4, "op": "connect", "key": wgKey(), "prefs": map[string]any{"killSwitch": true}})
	cl.next(t, func(m map[string]any) bool {
		return m["ev"] == "status" && m["status"].(map[string]any)["state"] == "connecting"
	})
	m = cl.next(t, func(m map[string]any) bool {
		return m["ev"] == "status" && m["status"].(map[string]any)["state"] == "connected"
	})
	if m["status"].(map[string]any)["since"] == nil {
		t.Fatal("connected without since")
	}
	m = cl.next(t, func(m map[string]any) bool {
		return m["ev"] == "stats" && m["stats"].(map[string]any)["rxRate"].(float64) > 0
	})
	if m["stats"].(map[string]any)["ping"].(float64) != 42 {
		t.Fatalf("stats %v", m)
	}

	cl.send(t, map[string]any{"id": 5, "op": "connect", "key": wgKey(), "prefs": map[string]any{"killSwitch": true}})
	if m = cl.next(t, isID(5)); m["ok"] == true {
		t.Fatal("second connect must be refused")
	}

	cl.send(t, map[string]any{"id": 6, "op": "disconnect"})
	cl.next(t, isID(6))
	if !d.stopped.Load() {
		t.Fatal("disconnect must tear the network down before replying")
	}
	cl.send(t, map[string]any{"id": 7, "op": "status"})
	if m = cl.next(t, isID(7)); m["status"].(map[string]any)["state"] != "idle" {
		t.Fatalf("status after disconnect %v", m)
	}
}

func TestStrangerIsDropped(t *testing.T) {
	path, _ := start(t, os.Getuid()+12345)
	if os.Getuid() == 0 {
		t.Skip("root is always allowed")
	}
	cl := dial(t, path)
	cl.send(t, map[string]any{"id": 1, "op": "hello"})
	cl.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if cl.sc.Scan() {
		t.Fatalf("a different uid got an answer: %s", cl.sc.Text())
	}
}
