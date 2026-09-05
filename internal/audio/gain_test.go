package audio

import (
	"math"
	"testing"
)

func constFrame(v int16) []int16 {
	f := make([]int16, SamplesPerFrame)
	for i := range f {
		f[i] = v
	}
	return f
}

func TestGainUnityLeavesSamplesUntouched(t *testing.T) {
	g := NewGain(100)
	frame := constFrame(12345)
	g.Apply(frame)
	for i, s := range frame {
		if s != 12345 {
			t.Fatalf("sample %d = %d, want 12345", i, s)
		}
	}
}

func TestGainScalesAfterRampSettles(t *testing.T) {
	// 50% on the perceptual curve is a 0.25 amplitude multiplier.
	g := NewGain(50)
	g.Apply(constFrame(0)) // let the ramp settle
	frame := constFrame(10000)
	g.Apply(frame)
	if got, want := frame[0], int16(2500); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func TestGainSilenceAtZero(t *testing.T) {
	g := NewGain(0)
	g.Apply(constFrame(0))
	frame := constFrame(math.MaxInt16)
	g.Apply(frame)
	for i, s := range frame {
		if s != 0 {
			t.Fatalf("sample %d = %d, want 0", i, s)
		}
	}
}

// Amplifying a loud signal must saturate. If it wrapped, a positive peak would
// come out as a large negative sample and a loud passage would turn to noise.
func TestGainSaturatesInsteadOfWrapping(t *testing.T) {
	g := NewGain(MaxVolumePercent)
	g.Apply(constFrame(0))

	loud := constFrame(math.MaxInt16)
	g.Apply(loud)
	for i, s := range loud {
		if s != math.MaxInt16 {
			t.Fatalf("positive sample %d = %d, want %d", i, s, math.MaxInt16)
		}
	}

	quiet := constFrame(math.MinInt16)
	g.Apply(quiet)
	for i, s := range quiet {
		if s != math.MinInt16 {
			t.Fatalf("negative sample %d = %d, want %d", i, s, math.MinInt16)
		}
	}
}

// A volume change must not step the waveform, or the slider clicks. Check that
// consecutive samples of a constant input never jump by more than a small
// fraction of the total change.
func TestGainRampsAcrossFrameWithoutSteps(t *testing.T) {
	g := NewGain(100)
	g.Apply(constFrame(0))

	g.SetPercent(0)
	frame := constFrame(20000)
	g.Apply(frame)

	if frame[0] < 19000 {
		t.Fatalf("ramp started at %d, expected to begin near the old gain", frame[0])
	}
	if frame[len(frame)-1] > 100 {
		t.Fatalf("ramp ended at %d, expected to reach the new gain", frame[len(frame)-1])
	}

	maxStep := 0
	for i := Channels; i < len(frame); i++ {
		if step := abs(int(frame[i]) - int(frame[i-Channels])); step > maxStep {
			maxStep = step
		}
	}
	if maxStep > 64 {
		t.Fatalf("largest sample-to-sample step was %d, expected a smooth ramp", maxStep)
	}
}

// Both channels of a stereo pair must be scaled by the same value, or the
// stereo image shifts while the slider moves.
func TestGainRampKeepsChannelsAligned(t *testing.T) {
	g := NewGain(100)
	g.Apply(constFrame(0))
	g.SetPercent(20)

	frame := constFrame(10000)
	g.Apply(frame)
	for i := 0; i < len(frame); i += Channels {
		if frame[i] != frame[i+1] {
			t.Fatalf("at position %d channels differ: %d vs %d", i/Channels, frame[i], frame[i+1])
		}
	}
}

func TestGainPercentRoundTrips(t *testing.T) {
	for _, want := range []float64{0, 25, 50, 100, MaxVolumePercent} {
		g := NewGain(want)
		if got := g.Percent(); math.Abs(got-want) > 0.001 {
			t.Fatalf("set %v, read back %v", want, got)
		}
	}
}

func TestGainClampsOutOfRangePercent(t *testing.T) {
	if got := NewGain(-20).Percent(); got != 0 {
		t.Fatalf("negative volume gave %v, want 0", got)
	}
	if got := NewGain(1000).Percent(); math.Abs(got-MaxVolumePercent) > 0.001 {
		t.Fatalf("oversized volume gave %v, want %v", got, MaxVolumePercent)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
