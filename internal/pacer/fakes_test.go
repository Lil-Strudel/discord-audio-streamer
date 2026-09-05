package pacer

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave"
)

// The disgo voice interfaces are wide and the sender touches four methods
// between them, so the doubles embed the interfaces. Anything the sender is not
// supposed to call panics rather than quietly returning a zero value.

type fakeUDP struct {
	voice.UDPConn

	mu     sync.Mutex
	writes [][]byte
	at     []time.Time
	err    error
}

func (f *fakeUDP) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.writes = append(f.writes, append([]byte(nil), p...))
	f.at = append(f.at, time.Now())
	return len(p), nil
}

func (f *fakeUDP) snapshot() ([][]byte, []time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.writes...), append([]time.Time(nil), f.at...)
}

func (f *fakeUDP) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func (f *fakeUDP) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

type fakeDave struct {
	godave.Session
	ready atomic.Bool
}

func (f *fakeDave) Ready() bool { return f.ready.Load() }

type fakeConn struct {
	voice.Conn

	udp  *fakeUDP
	dave *fakeDave

	mu       sync.Mutex
	speaking []voice.SpeakingFlags
}

func newFakeConn() *fakeConn {
	c := &fakeConn{udp: &fakeUDP{}, dave: &fakeDave{}}
	c.dave.ready.Store(true)
	return c
}

func (c *fakeConn) UDP() voice.UDPConn { return c.udp }

// Returning the field directly would hand back a non-nil interface wrapping a
// nil pointer, which is not what "no DAVE session" looks like from disgo.
func (c *fakeConn) DAVE() godave.Session {
	if c.dave == nil {
		return nil
	}
	return c.dave
}

func (c *fakeConn) SetSpeaking(_ context.Context, flags voice.SpeakingFlags) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.speaking = append(c.speaking, flags)
	return nil
}

func (c *fakeConn) speakingEvents() []voice.SpeakingFlags {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]voice.SpeakingFlags(nil), c.speaking...)
}

// staticProvider hands out the same frame forever, which is all the sender
// needs to be paced.
type staticProvider struct {
	frame  []byte
	closed atomic.Bool

	mu      sync.Mutex
	limit   int // when > 0, stop producing after this many frames
	count   int
	delay   time.Duration // injected stall on the first call
	stalled bool
}

func (p *staticProvider) ProvideOpusFrame() ([]byte, error) {
	p.mu.Lock()
	if p.delay > 0 && !p.stalled {
		p.stalled = true
		d := p.delay
		p.mu.Unlock()
		time.Sleep(d)
		p.mu.Lock()
	}
	p.count++
	over := p.limit > 0 && p.count > p.limit
	p.mu.Unlock()

	if over {
		return nil, nil
	}
	return p.frame, nil
}

func (p *staticProvider) Close() { p.closed.Store(true) }
