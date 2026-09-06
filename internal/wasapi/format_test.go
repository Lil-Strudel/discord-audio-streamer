package wasapi

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
)

// The package hardcodes the pipeline's format rather than importing it, so that
// it can be built for Windows without cgo. This is the guard on that shortcut.
func TestFormatMatchesPipeline(t *testing.T) {
	if outSampleRate != audio.SampleRate || outChannels != audio.Channels {
		t.Fatalf("wasapi output is %d Hz/%d ch but the pipeline wants %d Hz/%d ch",
			outSampleRate, outChannels, audio.SampleRate, audio.Channels)
	}
}

func floatBytes(samples ...float32) []byte {
	b := make([]byte, 0, len(samples)*4)
	for _, s := range samples {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(s))
	}
	return b
}

func int16At(t *testing.T, b []byte, i int) int16 {
	t.Helper()
	if (i+1)*2 > len(b) {
		t.Fatalf("sample %d is past the end of %d bytes", i, len(b))
	}
	return int16(binary.LittleEndian.Uint16(b[i*2:]))
}

func newConv(t *testing.T, f Format) *Converter {
	t.Helper()
	c, err := NewConverter(f)
	if err != nil {
		t.Fatalf("NewConverter(%s): %v", f, err)
	}
	return c
}

// The overwhelmingly common mix format. Getting this one wrong would be
// audible on every machine.
func TestConvertFloat32Stereo48k(t *testing.T) {
	c := newConv(t, Format{SampleRate: 48000, Channels: 2, BitsPerSample: 32, Float: true})
	if c.Passthrough() {
		t.Fatal("float input was treated as already being 16-bit PCM")
	}

	out := c.Convert(floatBytes(0, 1, -1, 0.5), nil)
	if len(out) != 8 {
		t.Fatalf("got %d bytes, want 8 (2 frames of stereo int16)", len(out))
	}
	if got := int16At(t, out, 0); got != 0 {
		t.Errorf("silence became %d", got)
	}
	if got := int16At(t, out, 1); got != 32767 {
		t.Errorf("full scale became %d, want 32767", got)
	}
	if got := int16At(t, out, 2); got != -32767 {
		t.Errorf("negative full scale became %d", got)
	}
	if got := int16At(t, out, 3); got < 16000 || got > 16600 {
		t.Errorf("half scale became %d, want roughly 16384", got)
	}
}

// A float source may peak above full scale; wrapping would turn a loud passage
// into noise.
func TestConvertClampsInsteadOfWrapping(t *testing.T) {
	c := newConv(t, Format{SampleRate: 48000, Channels: 2, BitsPerSample: 32, Float: true})
	out := c.Convert(floatBytes(4, -4), nil)
	if got := int16At(t, out, 0); got != 32767 {
		t.Errorf("+4.0 became %d, want a clamp to 32767", got)
	}
	if got := int16At(t, out, 1); got != -32767 {
		t.Errorf("-4.0 became %d, want a clamp to -32767", got)
	}
}

func TestConvertPassesThroughMatchingFormat(t *testing.T) {
	c := newConv(t, Format{SampleRate: 48000, Channels: 2, BitsPerSample: 16})
	if !c.Passthrough() {
		t.Fatal("48 kHz stereo int16 is the pipeline format and should not be converted")
	}
	in := []byte{1, 2, 3, 4}
	if out := c.Convert(in, nil); string(out) != string(in) {
		t.Fatalf("got %v, want the input unchanged", out)
	}
}

func TestConvertDuplicatesMonoToStereo(t *testing.T) {
	c := newConv(t, Format{SampleRate: 48000, Channels: 1, BitsPerSample: 32, Float: true})
	out := c.Convert(floatBytes(0.5, -0.5), nil)
	if len(out) != 8 {
		t.Fatalf("got %d bytes, want 2 mono samples widened to stereo", len(out))
	}
	if int16At(t, out, 0) != int16At(t, out, 1) {
		t.Error("mono was not duplicated across both channels")
	}
	if int16At(t, out, 2) != int16At(t, out, 3) {
		t.Error("mono was not duplicated across both channels")
	}
}

// Dialogue lives in the centre channel on a 5.1 setup, so a downmix that keeps
// only the front pair would silently drop it.
func TestConvertFoldsCentreChannelIntoBoth(t *testing.T) {
	c := newConv(t, Format{SampleRate: 48000, Channels: 6, BitsPerSample: 32, Float: true})
	// front L, front R, centre, LFE, surround L, surround R
	out := c.Convert(floatBytes(0, 0, 0.5, 0, 0, 0), nil)
	l, r := int16At(t, out, 0), int16At(t, out, 1)
	if l < 10000 || r < 10000 {
		t.Fatalf("centre-only content produced L=%d R=%d, want it audible in both", l, r)
	}
	if l != r {
		t.Errorf("centre channel was not shared evenly: L=%d R=%d", l, r)
	}
}

func TestConvertResamples44100To48000(t *testing.T) {
	const in = 4410
	c := newConv(t, Format{SampleRate: 44100, Channels: 2, BitsPerSample: 16})

	src := make([]byte, 0, in*4)
	for i := range in {
		// A slow ramp, so interpolation error stays measurable.
		v := int16(i * 4)
		src = binary.LittleEndian.AppendUint16(src, uint16(v))
		src = binary.LittleEndian.AppendUint16(src, uint16(v))
	}

	out := c.Convert(src, nil)
	frames := len(out) / 4
	if frames < 4750 || frames > 4850 {
		t.Fatalf("100 ms of 44.1 kHz produced %d frames, want roughly 4800", frames)
	}
	// The ramp must survive: first sample near zero, last near the input's end.
	if got := int16At(t, out, 0); got != 0 {
		t.Errorf("first sample = %d, want 0", got)
	}
	if got := int16At(t, out, (frames-1)*2); got < 17000 {
		t.Errorf("last sample = %d, want the ramp to reach roughly 17600", got)
	}
}

// Resampling carries a fractional position between calls; if it reset each time,
// the output would gain or lose samples on every buffer boundary.
func TestConvertKeepsRateAcrossManyBlocks(t *testing.T) {
	c := newConv(t, Format{SampleRate: 44100, Channels: 2, BitsPerSample: 16})

	block := make([]byte, 441*4) // 10 ms
	total := 0
	for range 100 { // one second
		total += len(c.Convert(block, nil)) / 4
	}
	if total < 47900 || total > 48100 {
		t.Fatalf("one second of 44.1 kHz became %d frames, want roughly 48000", total)
	}
}

func TestConvertDiscardsTrailingPartialFrame(t *testing.T) {
	c := newConv(t, Format{SampleRate: 48000, Channels: 2, BitsPerSample: 16})
	if out := c.Convert([]byte{1, 2, 3, 4, 5}, nil); len(out) != 4 {
		t.Fatalf("got %d bytes, want the dangling byte dropped", len(out))
	}
}

func TestConvertDecodes24BitPCM(t *testing.T) {
	c := newConv(t, Format{SampleRate: 48000, Channels: 2, BitsPerSample: 24})
	// +0.5 and -0.5 of full scale, little-endian 24-bit.
	out := c.Convert([]byte{0x00, 0x00, 0x40, 0x00, 0x00, 0xc0}, nil)
	if got := int16At(t, out, 0); got < 16000 || got > 16600 {
		t.Errorf("+0.5 became %d, want roughly 16384", got)
	}
	if got := int16At(t, out, 1); got > -16000 || got < -16600 {
		t.Errorf("-0.5 became %d, want roughly -16384", got)
	}
}

func TestNewConverterRejectsNonsense(t *testing.T) {
	for _, f := range []Format{
		{SampleRate: 0, Channels: 2, BitsPerSample: 16},
		{SampleRate: 48000, Channels: 0, BitsPerSample: 16},
		{SampleRate: 48000, Channels: 2, BitsPerSample: 12},
		{SampleRate: 48000, Channels: 2, BitsPerSample: 16, Float: true},
	} {
		if _, err := NewConverter(f); err == nil {
			t.Errorf("NewConverter(%s) accepted an unusable format", f)
		}
	}
}

func TestSilenceIsSilent(t *testing.T) {
	s := Silence(960)
	if len(s) != 3840 {
		t.Fatalf("960 frames became %d bytes, want 3840", len(s))
	}
	for _, b := range s {
		if b != 0 {
			t.Fatal("silence is not zeroed")
		}
	}
}
