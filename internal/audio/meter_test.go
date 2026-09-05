package audio

import (
	"math"
	"testing"
)

func TestMeterReadsSilenceAsZero(t *testing.T) {
	m := NewMeter()
	m.Observe(constFrame(0))
	rms, peak := m.Levels()
	if rms != 0 || peak != 0 {
		t.Fatalf("silence gave rms=%v peak=%v, want 0 and 0", rms, peak)
	}
}

func TestMeterReadsFullScaleAsOne(t *testing.T) {
	m := NewMeter()
	m.Observe(constFrame(math.MinInt16))
	rms, peak := m.Levels()
	if math.Abs(rms-1) > 0.001 {
		t.Fatalf("rms = %v, want 1", rms)
	}
	if math.Abs(peak-1) > 0.001 {
		t.Fatalf("peak = %v, want 1", peak)
	}
}

// A raw per-frame peak flickers at 50 Hz and is unreadable, so the held peak
// decays gradually once the signal stops.
func TestMeterPeakDecaysGradually(t *testing.T) {
	m := NewMeter()
	m.Observe(constFrame(math.MinInt16))

	_, first := m.Levels()
	m.Observe(constFrame(0))
	_, second := m.Levels()

	if second >= first {
		t.Fatalf("peak did not decay: %v then %v", first, second)
	}
	if second < 0.5 {
		t.Fatalf("peak fell to %v in one frame, too fast to read", second)
	}

	for range 50 {
		m.Observe(constFrame(0))
	}
	if _, eventually := m.Levels(); eventually > 0.05 {
		t.Fatalf("peak settled at %v after a second of silence, want near 0", eventually)
	}
}

func TestMeterReset(t *testing.T) {
	m := NewMeter()
	m.Observe(constFrame(math.MinInt16))
	m.Reset()
	if rms, peak := m.Levels(); rms != 0 || peak != 0 {
		t.Fatalf("after Reset rms=%v peak=%v, want 0 and 0", rms, peak)
	}
}

func TestMeterIgnoresEmptyFrame(t *testing.T) {
	m := NewMeter()
	m.Observe(constFrame(math.MinInt16))
	before, _ := m.Levels()
	m.Observe(nil)
	if after, _ := m.Levels(); after != before {
		t.Fatalf("empty frame changed the reading from %v to %v", before, after)
	}
}
