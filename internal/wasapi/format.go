// Package wasapi captures audio through the Windows Core Audio API.
//
// It exists because DirectShow — the only audio input ffmpeg has on Windows —
// can see capture endpoints and nothing else. The speakers can never appear in
// a DirectShow listing, which is why capturing desktop audio through ffmpeg
// requires a virtual audio cable to launder the output into an input.
//
// WASAPI has no such limit: a render endpoint can be opened in *loopback* mode,
// which is how screen-sharing applications record what you hear. Enumerating
// render and capture endpoints together therefore produces the full list of
// audio devices, virtual and physical alike, and every one of them can be
// streamed without installing anything.
//
// The format conversion lives in this file, deliberately free of any Windows
// dependency, so that the fiddliest part of the package can be tested on a
// machine that has no Core Audio at all.
package wasapi

import (
	"encoding/binary"
	"fmt"
	"math"
)

// The pipeline's fixed output format. These mirror audio.SampleRate and
// audio.Channels, which are not imported here only because that package needs
// cgo and this one is kept buildable without it. TestFormatMatchesPipeline
// asserts the two stay in step.
const (
	outSampleRate = 48000
	outChannels   = 2
)

// Format describes the PCM layout an endpoint hands us.
//
// In shared mode WASAPI speaks the audio engine's mix format, which is whatever
// the user picked in Windows sound settings. 32-bit float at 48 kHz stereo is
// the common case, but 44.1 kHz and 24-bit are both routine and 5.1 is not rare
// on a machine plugged into a receiver.
type Format struct {
	SampleRate    int
	Channels      int
	BitsPerSample int
	Float         bool
}

func (f Format) String() string {
	kind := "int"
	if f.Float {
		kind = "float"
	}
	return fmt.Sprintf("%d Hz, %d ch, %d-bit %s", f.SampleRate, f.Channels, f.BitsPerSample, kind)
}

// BytesPerFrame is one sample across every channel.
func (f Format) BytesPerFrame() int { return f.Channels * f.BitsPerSample / 8 }

func (f Format) validate() error {
	if f.SampleRate <= 0 || f.Channels <= 0 || f.BitsPerSample%8 != 0 {
		return fmt.Errorf("unusable audio format (%s)", f)
	}
	switch {
	case f.Float && (f.BitsPerSample == 32 || f.BitsPerSample == 64):
	case !f.Float && (f.BitsPerSample == 8 || f.BitsPerSample == 16 ||
		f.BitsPerSample == 24 || f.BitsPerSample == 32):
	default:
		return fmt.Errorf("unsupported audio format (%s)", f)
	}
	return nil
}

// Converter turns an endpoint's native PCM into the pipeline's format: signed
// 16-bit little-endian, 48 kHz, stereo, interleaved.
//
// Resampling is linear interpolation. That is not the finest resampler going,
// but the input is already 48 kHz on the overwhelming majority of machines —
// this path exists for the minority whose audio engine runs at 44.1 kHz — and
// carrying a resampling library for that would cost more than it returns.
type Converter struct {
	src         Format
	ratio       float64
	passthrough bool

	// pos is the next output sample's position, in source frames, relative to
	// the start of the block being converted. It is carried between calls and
	// may be negative, meaning the sample falls between the last frame of the
	// previous block and the first of this one.
	pos      float64
	prev     []float64
	havePrev bool

	frames []float64
}

// NewConverter builds a converter for an endpoint's format.
func NewConverter(src Format) (*Converter, error) {
	if err := src.validate(); err != nil {
		return nil, err
	}
	return &Converter{
		src:   src,
		ratio: float64(src.SampleRate) / float64(outSampleRate),
		passthrough: src.SampleRate == outSampleRate && src.Channels == outChannels &&
			src.BitsPerSample == 16 && !src.Float,
		prev: make([]float64, src.Channels),
	}, nil
}

// Passthrough reports whether the endpoint already speaks the pipeline's format
// and no conversion work is being done.
func (c *Converter) Passthrough() bool { return c.passthrough }

// Convert appends src, converted, to dst and returns the extended slice.
//
// A trailing partial frame in src is discarded rather than carried over: WASAPI
// hands out whole frames, so one appearing would mean the stream is already
// misaligned and holding onto the fragment would only propagate the damage.
func (c *Converter) Convert(src, dst []byte) []byte {
	bpf := c.src.BytesPerFrame()
	if n := len(src) % bpf; n != 0 {
		src = src[:len(src)-n]
	}
	n := len(src) / bpf
	if n == 0 {
		return dst
	}
	if c.passthrough {
		return append(dst, src...)
	}

	c.decode(src, n)
	ch := c.src.Channels
	at := func(i int) []float64 {
		if i < 0 {
			return c.prev
		}
		return c.frames[i*ch : (i+1)*ch]
	}

	// Without a previous block there is nothing to interpolate from, so the
	// first output sample lands exactly on the first input frame.
	if !c.havePrev {
		c.pos = 0
	}

	last := float64(n - 1)
	for c.pos <= last {
		i := int(math.Floor(c.pos))
		frac := c.pos - float64(i)

		// i can only reach n-1 when pos is exactly n-1, in which case frac is 0
		// and the second frame carries no weight.
		next := i + 1
		if next > n-1 {
			next = i
		}

		al, ar := downmix(at(i))
		bl, br := downmix(at(next))
		dst = appendSample(dst, al+(bl-al)*frac)
		dst = appendSample(dst, ar+(br-ar)*frac)

		c.pos += c.ratio
	}

	c.pos -= float64(n)
	c.prev = append(c.prev[:0], c.frames[(n-1)*ch:]...)
	c.havePrev = true
	return dst
}

func (c *Converter) decode(src []byte, n int) {
	need := n * c.src.Channels
	if cap(c.frames) < need {
		c.frames = make([]float64, need)
	}
	c.frames = c.frames[:need]

	stride := c.src.BitsPerSample / 8
	for i := range c.frames {
		c.frames[i] = c.sample(src[i*stride:])
	}
}

func (c *Converter) sample(b []byte) float64 {
	if c.src.Float {
		if c.src.BitsPerSample == 64 {
			return math.Float64frombits(binary.LittleEndian.Uint64(b))
		}
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	}

	switch c.src.BitsPerSample {
	case 8:
		// 8-bit PCM is the one unsigned format in the set.
		return (float64(b[0]) - 128) / 128
	case 16:
		return float64(int16(binary.LittleEndian.Uint16(b))) / 32768
	case 24:
		v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
		if v&0x800000 != 0 {
			v |= ^0xffffff
		}
		return float64(v) / 8388608
	default:
		return float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
	}
}

// downmix folds one source frame into a stereo pair.
func downmix(s []float64) (left, right float64) {
	switch {
	case len(s) == 1:
		return s[0], s[0]
	case len(s) < 6:
		return s[0], s[1]
	default:
		// Windows lays 5.1 and 7.1 out as front left, front right, centre, LFE,
		// then the surrounds. Folding the centre in at -3 dB is what matters
		// here: on a 5.1 setup dialogue lives in that channel, and taking only
		// the front pair would drop it entirely. LFE is left out because it is
		// not full range and adds nothing but rumble.
		const att = 0.7071
		return s[0] + att*s[2] + att*s[4], s[1] + att*s[2] + att*s[5]
	}
}

func appendSample(dst []byte, v float64) []byte {
	// Clamp rather than let the value wrap. A float source can legitimately peak
	// above full scale, and a downmix that sums three channels routinely will;
	// wrapping would turn either into loud noise.
	if v > 1 {
		v = 1
	} else if v < -1 {
		v = -1
	}
	return binary.LittleEndian.AppendUint16(dst, uint16(int16(v*32767)))
}

// Silence returns n sample-frames of silence in the pipeline's format.
func Silence(n int) []byte { return make([]byte, n*outChannels*2) }
