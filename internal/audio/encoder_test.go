package audio

import (
	"math"
	"testing"
)

// sineFrame produces one frame of a tone, so the encoder is given something
// with real spectral content rather than silence or a constant.
func sineFrame(hz float64, phase int) []int16 {
	f := make([]int16, SamplesPerFrame)
	for i := range SamplesPerChannel {
		t := float64(phase*SamplesPerChannel+i) / SampleRate
		v := int16(math.Sin(2*math.Pi*hz*t) * 0.5 * math.MaxInt16)
		f[i*Channels] = v
		f[i*Channels+1] = v
	}
	return f
}

func TestEncoderProducesPackets(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	for i := range 10 {
		pkt, err := enc.Encode(sineFrame(440, i))
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(pkt) == 0 {
			t.Fatalf("frame %d produced an empty packet", i)
		}
		if len(pkt) > MaxOpusFrameSize {
			t.Fatalf("frame %d produced %d bytes, over the %d byte bound", i, len(pkt), MaxOpusFrameSize)
		}
	}
}

func TestEncoderRejectsWrongFrameSize(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	if _, err := enc.Encode(make([]int16, SamplesPerFrame-2)); err == nil {
		t.Fatal("encoding a short frame succeeded, want an error")
	}
}

func TestEncoderBitrateRoundTrips(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{Bitrate: 128_000})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	got, err := enc.Bitrate()
	if err != nil {
		t.Fatalf("Bitrate: %v", err)
	}
	if got != 128_000 {
		t.Fatalf("Bitrate = %d, want 128000", got)
	}
}

func TestEncoderClampsBitrate(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{Bitrate: 1})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	if got, _ := enc.Bitrate(); got != MinBitrate {
		t.Fatalf("Bitrate = %d, want it clamped to %d", got, MinBitrate)
	}

	if err := enc.SetBitrate(10_000_000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if got, _ := enc.Bitrate(); got != MaxBitrate {
		t.Fatalf("Bitrate = %d, want it clamped to %d", got, MaxBitrate)
	}
}

// A higher bitrate must actually produce larger packets, which confirms the
// bitrate control reaches libopus rather than being silently ignored.
func TestEncoderBitrateAffectsPacketSize(t *testing.T) {
	measure := func(bitrate int) int {
		enc, err := NewEncoder(EncoderConfig{Bitrate: bitrate})
		if err != nil {
			t.Fatalf("NewEncoder(%d): %v", bitrate, err)
		}
		defer enc.Close()

		total := 0
		for i := range 50 {
			pkt, err := enc.Encode(sineFrame(440, i))
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			total += len(pkt)
		}
		return total
	}

	low, high := measure(MinBitrate), measure(MaxBitrate)
	if high <= low {
		t.Fatalf("%d bps produced %d bytes and %d bps produced %d; expected more bits to mean more bytes",
			MinBitrate, low, MaxBitrate, high)
	}
}

func TestEncoderWithForwardErrorCorrection(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{PacketLossPercent: 5})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	if _, err := enc.Encode(sineFrame(440, 0)); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func TestEncoderCloseIsIdempotentAndFailsLater(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.Close()
	enc.Close()

	if _, err := enc.Encode(sineFrame(440, 0)); err == nil {
		t.Fatal("encoding after Close succeeded, want an error")
	}
}

func TestVersionIsReported(t *testing.T) {
	if Version() == "" {
		t.Fatal("Version returned an empty string")
	}
	t.Logf("linked against %s", Version())
}
