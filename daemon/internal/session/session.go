// Package session is the one live connection: a state machine, byte rates and ping, all in
// memory. When it ends, everything about it is gone.
package session

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"mayvpn/internal/driver"
	"mayvpn/internal/profile"
	"mayvpn/internal/tunnel"
)

type State string

const (
	Idle          State = "idle"
	Connecting    State = "connecting"
	Connected     State = "connected"
	Disconnecting State = "disconnecting"
)

type Status struct {
	State State        `json:"state"`
	Kind  profile.Kind `json:"kind,omitempty"`
	Since int64        `json:"since,omitempty"` // unix ms when connected
	Error string       `json:"error,omitempty"` // why the last attempt ended
}

type Stats struct {
	Rx     uint64  `json:"rx"`
	Tx     uint64  `json:"tx"`
	RxRate float64 `json:"rxRate"` // bytes/s
	TxRate float64 `json:"txRate"`
	Ping   int     `json:"ping"` // ms, -1 unknown
}

type Starter interface {
	Start(ctx context.Context, p *profile.Profile, prefs driver.Prefs) (tunnel.Tunnel, func(), error)
}

// Prober measures one round trip through the tunnel; the first success means "connected".
type Prober func(ctx context.Context) (time.Duration, error)

type Event struct {
	Status *Status `json:"status,omitempty"`
	Stats  *Stats  `json:"stats,omitempty"`
}

type Session struct {
	Driver         Starter
	Probe          Prober
	ConnectTimeout time.Duration
	Tick           time.Duration
	PingEvery      int // ticks

	mu     sync.Mutex
	status Status
	cancel context.CancelFunc
	done   chan struct{}
	subs   map[chan Event]struct{}
}

func New(d Starter, probe Prober) *Session {
	return &Session{Driver: d, Probe: probe, ConnectTimeout: 25 * time.Second, Tick: time.Second, PingEvery: 3,
		status: Status{State: Idle}, subs: map[chan Event]struct{}{}}
}

func (s *Session) Status() Status { s.mu.Lock(); defer s.mu.Unlock(); return s.status }

func (s *Session) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
}

func (s *Session) emitLocked(e Event) {
	for ch := range s.subs {
		select {
		case ch <- e:
		default: // slow reader: drop stats rather than block the tunnel
		}
	}
}

func (s *Session) setLocked(st Status) {
	s.status = st
	c := st
	s.emitLocked(Event{Status: &c})
}

var ErrBusy = errors.New("already connected — disconnect first")

// Connect starts a connection in the background; progress arrives as events.
func (s *Session) Connect(p *profile.Profile, prefs driver.Prefs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State != Idle {
		return ErrBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	s.setLocked(Status{State: Connecting, Kind: p.Kind})
	go s.run(ctx, p, prefs, s.done)
	return nil
}

// Disconnect tears down and waits until the network is restored.
func (s *Session) Disconnect() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	if cancel == nil {
		s.mu.Unlock()
		return
	}
	if s.status.State != Idle {
		st := s.status
		st.State, st.Error = Disconnecting, ""
		s.setLocked(st)
	}
	s.mu.Unlock()
	cancel()
	<-done
}

func (s *Session) run(ctx context.Context, p *profile.Profile, prefs driver.Prefs, done chan struct{}) {
	var endErr error
	defer func() {
		s.mu.Lock()
		st := Status{State: Idle}
		if endErr != nil && ctx.Err() == nil {
			st.Error = endErr.Error()
		}
		s.cancel, s.done = nil, nil
		s.setLocked(st)
		s.mu.Unlock()
		close(done)
	}()

	t, stop, err := s.Driver.Start(ctx, p, prefs)
	if err != nil {
		endErr = err
		return
	}
	defer stop()

	tick := time.NewTicker(s.Tick)
	defer tick.Stop()
	deadline := time.Now().Add(s.ConnectTimeout)
	connected := false
	var lastRx, lastTx uint64
	var rxRate, txRate float64
	ping := -1
	n := 0
	var since int64
	var lastOK time.Time
	for {
		// probe: every tick until connected, then every PingEvery ticks
		if !connected || n%s.PingEvery == 0 {
			pctx, pc := context.WithTimeout(ctx, 4*time.Second)
			rtt, perr := s.Probe(pctx)
			pc()
			if ctx.Err() != nil {
				return
			}
			switch {
			case perr == nil:
				ping, lastOK = int(rtt.Milliseconds()), time.Now()
				if !connected {
					connected, since = true, time.Now().UnixMilli()
					rx, tx, _ := t.Counters()
					lastRx, lastTx = rx, tx
				}
				s.mu.Lock()
				if s.status.State == Connecting {
					s.setLocked(Status{State: Connected, Kind: p.Kind, Since: since})
				}
				s.mu.Unlock()
			case connected:
				ping = -1
				// the tunnel went quiet: show it as reconnecting (WireGuard keeps retrying on its own)
				s.mu.Lock()
				if time.Since(lastOK) > 12*time.Second && s.status.State == Connected {
					s.setLocked(Status{State: Connecting, Kind: p.Kind, Since: since})
				}
				s.mu.Unlock()
			case time.Now().After(deadline):
				endErr = errors.New("No answer through the tunnel — check the key and the server")
				return
			}
		}
		if connected {
			rx, tx, _ := t.Counters()
			sec := s.Tick.Seconds()
			// light smoothing so the number does not flicker
			rxRate = 0.5*rxRate + 0.5*float64(rx-min(rx, lastRx))/sec
			txRate = 0.5*txRate + 0.5*float64(tx-min(tx, lastTx))/sec
			lastRx, lastTx = rx, tx
			s.mu.Lock()
			s.emitLocked(Event{Stats: &Stats{Rx: rx, Tx: tx, RxRate: rxRate, TxRate: txRate, Ping: ping}})
			s.mu.Unlock()
		}
		n++
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// HTTPProbe times a request through the tunnel on a kept-alive connection, so after the first
// call the number is one round trip client → VPS → target.
func HTTPProbe(url string) Prober {
	tr := &http.Transport{
		Proxy:             nil,
		DialContext:       (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
		MaxIdleConns:      1,
		IdleConnTimeout:   30 * time.Second,
		DisableKeepAlives: false,
	}
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context) (time.Duration, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			tr.CloseIdleConnections()
			return 0, err
		}
		resp.Body.Close()
		return time.Since(start), nil
	}
}
