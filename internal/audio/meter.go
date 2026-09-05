package audio

import (
	"math"
	"sync/atomic"
)

// Meter tracks the level of the frames passing through the pipeline so the UI
// can draw a level indicator.
//
// Peak is held with a decay rather than reset per frame: at 50 frames a second a
// raw per-frame peak flickers too fast to read, while a decaying peak behaves
// like the peak-hold on a hardware meter.
type Meter struct {
	rms  atomic.Uint64 // float64 bits, 0..1
	peak atomic.Uint64 // float64 bits, 0..1
}

// peakDecay is the fraction of the held peak retained each frame. At 20 ms per
// frame this falls to roughly a tenth of full scale in about half a second.
const peakDecay = 0.91

// NewMeter returns a Meter reading silence.
func NewMeter() *Meter { return &Meter{} }

// Observe updates the meter from one frame.
func (m *Meter) Observe(pcm []int16) {
	if len(pcm) == 0 {
		return
	}

	var sumSquares float64
	var framePeak float64
	for _, s := range pcm {
		v := float64(s) / -math.MinInt16
		sumSquares += v * v
		if a := math.Abs(v); a > framePeak {
			framePeak = a
		}
	}

	m.rms.Store(math.Float64bits(math.Sqrt(sumSquares / float64(len(pcm)))))

	held := math.Float64frombits(m.peak.Load()) * peakDecay
	m.peak.Store(math.Float64bits(math.Max(held, framePeak)))
}

// Levels returns the current RMS and peak levels, both 0 to 1.
func (m *Meter) Levels() (rms float64, peak float64) {
	return math.Float64frombits(m.rms.Load()), math.Float64frombits(m.peak.Load())
}

// Reset returns the meter to silence.
func (m *Meter) Reset() {
	m.rms.Store(0)
	m.peak.Store(0)
}
