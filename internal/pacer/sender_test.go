package pacer

import (
	"net"
	"testing"
	"time"

	"github.com/disgoorg/disgo/voice"
)

func newSender(t *testing.T, conn *fakeConn, provider *staticProvider) *Sender {
	t.Helper()
	s := New(nil, provider, conn).(*Sender)
	t.Cleanup(s.Close)
	return s
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// The core requirement: error must not accumulate. A sleep-per-frame sender
// drifts by whatever each wake-up overshoots by, multiplied by the frame count;
// deadlines measured from a fixed anchor do not.
func TestSenderDoesNotAccumulateDrift(t *testing.T) {
	conn := newFakeConn()
	s := newSender(t, conn, &staticProvider{frame: []byte{1, 2, 3}})

	const want = 100 // two seconds of audio
	s.Open()
	if !waitFor(t, 10*time.Second, func() bool { return conn.udp.count() >= want }) {
		t.Fatalf("only %d frames sent in 10s, want %d", conn.udp.count(), want)
	}
	s.Close()

	_, at := conn.udp.snapshot()
	at = at[:want]

	elapsed := at[len(at)-1].Sub(at[0])
	ideal := time.Duration(len(at)-1) * frameDuration

	// Scheduling jitter is unavoidable, but total error over 100 frames must
	// stay in the low tens of milliseconds rather than growing with the count.
	diff := (elapsed - ideal).Abs()
	t.Logf("%d frames in %v (ideal %v), cumulative error %v, max lateness %.2fms",
		len(at), elapsed, ideal, diff, s.Stats().MaxLatenessMs)
	if diff > 60*time.Millisecond {
		t.Fatalf("%d frames took %v, ideal %v, off by %v", len(at), elapsed, ideal, diff)
	}

	// No single gap should be wildly off either.
	for i := 1; i < len(at); i++ {
		gap := at[i].Sub(at[i-1])
		if gap > 100*time.Millisecond {
			t.Fatalf("gap %d was %v, far beyond the %v cadence", i, gap, frameDuration)
		}
	}
}

// Frames must be withheld until the MLS handshake completes, because the DAVE
// session passes frames through unencrypted before then.
func TestSenderHoldsFramesUntilEncryptionIsReady(t *testing.T) {
	conn := newFakeConn()
	conn.dave.ready.Store(false)
	s := newSender(t, conn, &staticProvider{frame: []byte{1, 2, 3}})

	s.Open()

	if !waitFor(t, 2*time.Second, func() bool { return s.Stats().FramesHeld > 3 }) {
		t.Fatal("no frames were held while encryption was not ready")
	}
	if got := conn.udp.count(); got != 0 {
		t.Fatalf("%d frames were sent before encryption was established", got)
	}

	conn.dave.ready.Store(true)

	if !waitFor(t, 2*time.Second, func() bool { return conn.udp.count() > 3 }) {
		t.Fatal("no frames were sent after encryption became ready")
	}
}

// A nil DAVE session means encryption is not in use at all, which must not
// block audio outright.
func TestSenderSendsWhenThereIsNoDaveSession(t *testing.T) {
	conn := newFakeConn()
	conn.dave = nil
	s := newSender(t, conn, &staticProvider{frame: []byte{1, 2, 3}})

	s.Open()
	if !waitFor(t, 2*time.Second, func() bool { return conn.udp.count() > 3 }) {
		t.Fatal("no frames were sent without a DAVE session")
	}
}

// Discord uses a short run of silence frames to reset decoder interpolation, so
// the tail of a track does not smear into whatever plays next.
func TestSenderSpeakingLifecycle(t *testing.T) {
	conn := newFakeConn()
	provider := &staticProvider{frame: []byte{1, 2, 3}, limit: 5}
	s := newSender(t, conn, provider)

	s.Open()

	wantWrites := 5 + trailingSilenceFrames
	if !waitFor(t, 5*time.Second, func() bool {
		events := conn.speakingEvents()
		return len(events) >= 2
	}) {
		t.Fatalf("speaking state never settled; events=%v writes=%d", conn.speakingEvents(), conn.udp.count())
	}
	s.Close()

	events := conn.speakingEvents()
	if events[0] != voice.SpeakingFlagMicrophone {
		t.Errorf("first speaking event = %v, want microphone", events[0])
	}
	if events[len(events)-1] != voice.SpeakingFlagNone {
		t.Errorf("last speaking event = %v, want none", events[len(events)-1])
	}

	writes, _ := conn.udp.snapshot()
	if len(writes) < wantWrites {
		t.Fatalf("got %d writes, want at least %d (5 audio + %d silence)",
			len(writes), wantWrites, trailingSilenceFrames)
	}
	for i := 5; i < wantWrites; i++ {
		if string(writes[i]) != string(voice.SilenceAudioFrame) {
			t.Fatalf("write %d = %v, want the silence frame", i, writes[i])
		}
	}

	// Once stopped, it must not keep writing silence forever.
	settled := conn.udp.count()
	time.Sleep(200 * time.Millisecond)
	if got := conn.udp.count(); got != settled {
		t.Fatalf("sender kept writing after stopping: %d then %d", settled, got)
	}
}

// A machine that suspends, or a provider that stalls, leaves the loop far past
// its deadline. Catching up frame by frame would dump a backlog onto the
// channel, so the schedule is abandoned and restarted instead.
func TestSenderReanchorsAfterAStall(t *testing.T) {
	conn := newFakeConn()
	provider := &staticProvider{frame: []byte{1, 2, 3}, delay: 250 * time.Millisecond}
	s := newSender(t, conn, provider)

	s.Open()

	if !waitFor(t, 5*time.Second, func() bool { return s.Stats().Resyncs > 0 }) {
		t.Fatalf("no resync after a 250ms stall; stats=%+v", s.Stats())
	}

	// The backlog must not be flushed at once. Twelve frames' worth of stall
	// should not produce a burst of a dozen frames in the next few ms.
	before := conn.udp.count()
	time.Sleep(60 * time.Millisecond)
	if sent := conn.udp.count() - before; sent > 8 {
		t.Fatalf("sent %d frames in 60ms, expected about 3; the backlog was flushed", sent)
	}

	if s.Stats().MaxLatenessMs < 200 {
		t.Errorf("MaxLatenessMs = %v, expected the stall to be recorded", s.Stats().MaxLatenessMs)
	}
}

// A dead connection must stop the sender rather than log the same error fifty
// times a second forever.
func TestSenderStopsOnClosedConnection(t *testing.T) {
	conn := newFakeConn()
	s := newSender(t, conn, &staticProvider{frame: []byte{1, 2, 3}})

	s.Open()
	if !waitFor(t, 2*time.Second, func() bool { return conn.udp.count() > 2 }) {
		t.Fatal("sender never started")
	}

	conn.udp.failWith(net.ErrClosed)

	if !waitFor(t, 3*time.Second, func() bool {
		select {
		case <-s.done:
			return true
		default:
			return false
		}
	}) {
		t.Fatal("sender kept running after the connection closed")
	}
}

func TestSenderCloseReleasesProvider(t *testing.T) {
	conn := newFakeConn()
	provider := &staticProvider{frame: []byte{1, 2, 3}}
	s := New(nil, provider, conn).(*Sender)

	s.Open()
	waitFor(t, 2*time.Second, func() bool { return conn.udp.count() > 0 })

	s.Close()
	s.Close() // must be safe to repeat

	if !provider.closed.Load() {
		t.Fatal("Close did not release the frame provider")
	}
}

func TestCloseWithoutOpenIsSafe(t *testing.T) {
	provider := &staticProvider{frame: []byte{1}}
	New(nil, provider, newFakeConn()).Close()

	if !provider.closed.Load() {
		t.Fatal("Close did not release the frame provider")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	conn := newFakeConn()
	s := newSender(t, conn, &staticProvider{frame: []byte{1, 2, 3}})

	s.Open()
	s.Open()
	s.Open()

	time.Sleep(300 * time.Millisecond)
	s.Close()

	// Three concurrent loops would produce roughly three times the frames.
	if got := conn.udp.count(); got > 30 {
		t.Fatalf("%d frames in 300ms, expected about 15; Open started more than one loop", got)
	}
}
