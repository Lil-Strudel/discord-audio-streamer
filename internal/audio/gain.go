package audio

import (
	"math"
	"sync/atomic"
)

// MaxVolumePercent is the highest volume the UI may request. Above 100% the
// signal is amplified, which is useful for quiet source material but will clip
// loud material.
const MaxVolumePercent = 150.0

// Gain scales the amplitude of PCM frames in place.
//
// The target is set from the UI thread while frames are processed on the audio
// thread, so it is held in an atomic. Rather than snapping to a new value at a
// frame boundary (which steps the waveform and produces an audible click, the
// classic "zipper noise" of a dragged slider), each frame ramps linearly from
// the previous gain to the new one across its 960 sample positions.
type Gain struct {
	target atomic.Uint64 // float64 bits
	prev   float64       // audio thread only
}

// NewGain returns a Gain set to the given volume percentage.
func NewGain(percent float64) *Gain {
	g := &Gain{}
	g.SetPercent(percent)
	g.prev = amplitudeFor(percent)
	return g
}

// SetPercent sets the target volume, 0 to [MaxVolumePercent]. Safe to call from
// any goroutine. The change takes effect over the next frame.
func (g *Gain) SetPercent(percent float64) {
	g.target.Store(math.Float64bits(amplitudeFor(percent)))
}

// Percent returns the current target volume as a percentage.
func (g *Gain) Percent() float64 {
	return math.Sqrt(g.amplitude()) * 100
}

func (g *Gain) amplitude() float64 {
	return math.Float64frombits(g.target.Load())
}

// amplitudeFor maps a volume percentage to a linear amplitude multiplier.
//
// Loudness is perceived roughly logarithmically, so a linear mapping makes the
// bottom of a slider feel dead and the top feel like it does nothing. Squaring
// the fraction spreads the useful range across the travel of the slider.
func amplitudeFor(percent float64) float64 {
	percent = math.Max(0, math.Min(MaxVolumePercent, percent))
	frac := percent / 100
	return frac * frac
}

// Apply scales pcm in place, ramping from the gain used for the previous frame
// to the current target across the frame.
func (g *Gain) Apply(pcm []int16) {
	from, to := g.prev, g.amplitude()
	g.prev = to

	// Unity in and unity out: leave the samples untouched.
	if from == 1 && to == 1 {
		return
	}

	if from == to {
		for i, s := range pcm {
			pcm[i] = clampToInt16(float64(s) * to)
		}
		return
	}

	// Ramp per sample position rather than per sample, so the two channels of a
	// stereo pair are always scaled identically and the stereo image is stable.
	positions := len(pcm) / Channels
	if positions == 0 {
		return
	}
	step := (to - from) / float64(positions)
	gain := from
	for i := 0; i < positions; i++ {
		base := i * Channels
		for c := 0; c < Channels; c++ {
			pcm[base+c] = clampToInt16(float64(pcm[base+c]) * gain)
		}
		gain += step
	}
}

// clampToInt16 saturates instead of wrapping. Letting an amplified sample
// overflow int16 would turn a loud passage into a full-scale sign flip, which is
// far worse than the soft distortion of clipping.
func clampToInt16(v float64) int16 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}
