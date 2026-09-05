package pacer

import (
	"math"
	"sync/atomic"
)

// Timing measurements are read by the UI while the pacing goroutine writes
// them, and Go has no atomic float, so they are stored as their bit patterns.

func floatBits(f float64) uint64 { return math.Float64bits(f) }

func storeFloat(dst *atomic.Uint64, f float64) { dst.Store(floatBits(f)) }

func loadFloat(src *atomic.Uint64) float64 { return math.Float64frombits(src.Load()) }
