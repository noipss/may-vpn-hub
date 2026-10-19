package session

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mayvpn/internal/driver"
	"mayvpn/internal/profile"
	"mayvpn/internal/tunnel"
)

type nopTun struct{}

func (nopTun) Name() string                          { return "utun9" }
func (nopTun) Counters() (uint64, uint64, time.Time) { return 0, 0, time.Time{} }
func (nopTun) Close() error                          { return nil }

type drv struct{ stopped atomic.Bool }

func (d *drv) Start(context.Context, *profile.Profile, driver.Prefs) (tunnel.Tunnel, func(), error) {
	return nopTun{}, func() { d.stopped.Store(true) }, nil
}

func TestTimeoutTearsDown(t *testing.T) {
	d := &drv{}
	s := New(d, func(context.Context) (time.Duration, error) { return 0, errors.New("silence") })
	s.Tick, s.ConnectTimeout = 20*time.Millisecond, 120*time.Millisecond
	ev, unsub := s.Subscribe()
	defer unsub()
	if err := s.Connect(&profile.Profile{Kind: profile.WireGuard}, driver.Prefs{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-ev:
			if e.Status != nil && e.Status.State == Idle {
				if !strings.Contains(e.Status.Error, "No answer") || !d.stopped.Load() {
					t.Fatalf("ended with %+v stopped=%v", e.Status, d.stopped.Load())
				}
				return
			}
		case <-deadline:
			t.Fatal("never gave up")
		}
	}
}

func TestDisconnectWhileConnecting(t *testing.T) {
	d := &drv{}
	s := New(d, func(ctx context.Context) (time.Duration, error) { <-ctx.Done(); return 0, ctx.Err() })
	s.Tick = 20 * time.Millisecond
	s.Connect(&profile.Profile{Kind: profile.VLESS}, driver.Prefs{})
	time.Sleep(50 * time.Millisecond)
	s.Disconnect()
	if st := s.Status(); st.State != Idle || st.Error != "" || !d.stopped.Load() {
		t.Fatalf("after disconnect: %+v stopped=%v", st, d.stopped.Load())
	}
}
