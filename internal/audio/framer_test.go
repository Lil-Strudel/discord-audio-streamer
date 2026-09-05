package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// chunkedReader hands back at most n bytes per Read, imitating a pipe that
// delivers data in chunks unrelated to our frame size.
type chunkedReader struct {
	data []byte
	n    int
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	size := min(min(c.n, len(p)), len(c.data))
	copy(p, c.data[:size])
	c.data = c.data[size:]
	return size, nil
}

func pcmBytes(samples []int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}

func TestFramerReassemblesFramesFromPartialReads(t *testing.T) {
	const frames = 3
	want := make([]int16, frames*SamplesPerFrame)
	for i := range want {
		want[i] = int16(i - 1000)
	}

	// A chunk size that divides neither the frame size nor the total.
	f := NewFramer(&chunkedReader{data: pcmBytes(want), n: 777})

	for i := range frames {
		got, err := f.ReadFrame()
		if err != nil {
			t.Fatalf("frame %d: unexpected error: %v", i, err)
		}
		expect := want[i*SamplesPerFrame : (i+1)*SamplesPerFrame]
		if !bytes.Equal(pcmBytes(got), pcmBytes(expect)) {
			t.Fatalf("frame %d does not match input", i)
		}
	}

	if _, err := f.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("after last frame: got %v, want io.EOF", err)
	}
}

func TestFramerPadsTrailingPartialFrame(t *testing.T) {
	// One full frame plus 100 samples of a second one.
	const tail = 100
	in := make([]int16, SamplesPerFrame+tail)
	for i := range in {
		in[i] = 1234
	}

	f := NewFramer(bytes.NewReader(pcmBytes(in)))

	if _, err := f.ReadFrame(); err != nil {
		t.Fatalf("first frame: %v", err)
	}

	got, err := f.ReadFrame()
	if err != nil {
		t.Fatalf("padded frame: %v", err)
	}
	if len(got) != SamplesPerFrame {
		t.Fatalf("padded frame has %d samples, want %d", len(got), SamplesPerFrame)
	}
	for i := range tail {
		if got[i] != 1234 {
			t.Fatalf("sample %d = %d, want 1234", i, got[i])
		}
	}
	for i := tail; i < SamplesPerFrame; i++ {
		if got[i] != 0 {
			t.Fatalf("padding sample %d = %d, want 0", i, got[i])
		}
	}

	if _, err := f.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("after padded frame: got %v, want io.EOF", err)
	}
}

func TestFramerPropagatesReadErrors(t *testing.T) {
	sentinel := errors.New("boom")
	f := NewFramer(errReader{sentinel})
	if _, err := f.ReadFrame(); !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want %v", err, sentinel)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
