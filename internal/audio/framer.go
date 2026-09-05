package audio

import (
	"encoding/binary"
	"io"
)

// Framer reads a raw s16le stream and hands back whole 20 ms frames.
//
// Pipes deliver arbitrary chunk sizes, so a naive Read would routinely return a
// partial frame and desynchronise the stream by a few samples, which is audible
// as a click. io.ReadFull is used to always assemble a complete frame before
// returning one.
type Framer struct {
	r       io.Reader
	rawBuf  []byte
	pcmBuf  []int16
	partial bool
}

// NewFramer returns a Framer reading from r, which must produce interleaved
// little-endian int16 samples at [SampleRate] with [Channels] channels.
func NewFramer(r io.Reader) *Framer {
	return &Framer{
		r:      r,
		rawBuf: make([]byte, BytesPerFrame),
		pcmBuf: make([]int16, SamplesPerFrame),
	}
}

// ReadFrame returns the next complete frame. The returned slice is reused across
// calls, so callers that need to retain it must copy it.
//
// At the end of the stream a trailing partial frame is zero-padded and returned
// with a nil error; the following call returns io.EOF. That keeps the final
// fraction of a second of a track from being silently truncated.
func (f *Framer) ReadFrame() ([]int16, error) {
	if f.partial {
		return nil, io.EOF
	}

	n, err := io.ReadFull(f.r, f.rawBuf)
	switch {
	case err == nil:
	case err == io.ErrUnexpectedEOF:
		// Pad the tail of the final frame with silence.
		clear(f.rawBuf[n:])
		f.partial = true
	default:
		return nil, err
	}

	for i := range f.pcmBuf {
		f.pcmBuf[i] = int16(binary.LittleEndian.Uint16(f.rawBuf[i*2:]))
	}
	return f.pcmBuf, nil
}
