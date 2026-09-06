//go:build windows

package wasapi

import (
	"errors"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// pollInterval is the fallback rate at which the capture thread looks for
	// new audio when the device does not report a sensible period of its own.
	pollInterval = 5 * time.Millisecond

	// idleGap is how long the endpoint must go without delivering a packet
	// before we start manufacturing silence. See Capture.drain.
	idleGap = 40 * time.Millisecond

	// minBufferMs is the smallest engine-side buffer worth asking for. This is
	// not the stream's latency — that is set by how promptly we drain — it is
	// only the headroom a scheduling hiccup gets before samples are lost.
	minBufferMs = 100
)

// Capture is a running capture of one endpoint. It reads as the pipeline's PCM
// format: signed 16-bit little-endian, 48 kHz, stereo.
type Capture struct {
	chunks chan []byte
	stop   chan struct{}
	done   chan struct{}

	stopOnce sync.Once
	pending  []byte

	mu  sync.Mutex
	err error

	dropped atomic.Int64

	format   Format
	loopback bool
}

// Open starts capturing from the endpoint with the given id.
//
// A render endpoint is opened in loopback mode, which is what makes streaming
// desktop audio possible without a virtual cable: Windows hands us the same mix
// it is sending to the speakers.
func Open(deviceID string, bufferMs int) (*Capture, error) {
	if deviceID == "" {
		return nil, errors.New("no capture device selected")
	}

	c := &Capture{
		// Around a second of audio. The pipeline keeps its own, much smaller,
		// ring buffer and that is what governs latency; this one only has to
		// cover the gap between two of its reads.
		chunks: make(chan []byte, 64),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}

	ready := make(chan error, 1)
	go c.run(deviceID, bufferMs, ready)

	if err := <-ready; err != nil {
		<-c.done
		return nil, err
	}
	return c, nil
}

// Format reports the endpoint's native format, before conversion.
func (c *Capture) Format() Format { return c.format }

// IsLoopback reports whether this is a render endpoint being recorded.
func (c *Capture) IsLoopback() bool { return c.loopback }

// Dropped counts chunks discarded because the reader fell behind.
func (c *Capture) Dropped() int64 { return c.dropped.Load() }

// Read returns raw PCM. It blocks until audio is available.
func (c *Capture) Read(b []byte) (int, error) {
	for len(c.pending) == 0 {
		chunk, ok := <-c.chunks
		if !ok {
			if err := c.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		c.pending = chunk
	}
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// Close stops the capture and waits for the device to be released.
func (c *Capture) Close() error {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done

	// Drain whatever the capture thread had already queued, so a blocked Read
	// elsewhere sees the closed channel rather than waiting forever.
	for range c.chunks { //nolint:revive // draining
	}
	return nil
}

// Wait blocks until the capture has finished and reports why it stopped.
func (c *Capture) Wait() error {
	<-c.done
	return c.Err()
}

// Err reports the failure that ended the capture, if any.
func (c *Capture) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Capture) setErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
}

func (c *Capture) run(deviceID string, bufferMs int, ready chan<- error) {
	// close(done) is registered first so that it runs last: a reader that sees
	// done closed must already have seen chunks closed.
	defer close(c.done)
	defer close(c.chunks)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	uninit, err := comInit()
	if err != nil {
		ready <- err
		return
	}
	defer uninit()

	s, err := openSession(deviceID, bufferMs)
	if err != nil {
		ready <- err
		return
	}
	defer s.close()

	c.format, c.loopback = s.format, s.loopback
	ready <- nil

	slog.Info("capturing audio device",
		slog.String("format", s.format.String()),
		slog.Bool("loopback", s.loopback),
		slog.Bool("converting", !s.conv.Passthrough()),
		slog.Duration("poll", s.period))

	if err := c.drain(s); err != nil {
		slog.Error("audio capture stopped", slog.Any("err", err))
		c.setErr(err)
	}
}

// drain is the capture loop. It polls the endpoint, converts whatever it finds,
// and manufactures silence to cover the stretches where an endpoint delivers
// nothing at all.
//
// That last part is what loopback capture requires: a render endpoint playing
// nothing produces no packets, not silent ones. Without this the pipeline would
// starve every time the music stopped and the stream would look, in the stream
// health readout, like it was constantly failing.
func (c *Capture) drain(s *session) error {
	ticker := time.NewTicker(s.period)
	defer ticker.Stop()

	var (
		out        []byte
		epoch      = time.Now()
		lastPacket = epoch
		produced   int64 // sample-frames handed to the pipeline since epoch
	)

	for {
		select {
		case <-c.stop:
			return nil
		case <-ticker.C:
		}

		out = out[:0]
		frames, err := s.read(&out)
		if err != nil {
			return err
		}
		if frames > 0 {
			lastPacket = time.Now()
		}
		produced += int64(len(out) / (outChannels * 2))

		if time.Since(lastPacket) > idleGap {
			want := int64(time.Since(epoch).Seconds() * outSampleRate)
			if gap := want - produced; gap > 0 {
				out = append(out, Silence(int(gap))...)
				produced = want
			}
		}

		if len(out) > 0 {
			c.push(out)
		}
	}
}

func (c *Capture) push(data []byte) {
	chunk := make([]byte, len(data))
	copy(chunk, data)

	select {
	case c.chunks <- chunk:
		return
	default:
	}

	// The reader has fallen behind. Drop the oldest chunk rather than block:
	// blocking here would stop us draining the audio engine, and the driver
	// would then discard samples somewhere we cannot see or count it.
	select {
	case <-c.chunks:
		c.dropped.Add(1)
	default:
	}
	select {
	case c.chunks <- chunk:
	default:
		c.dropped.Add(1)
	}
}

// --------------------------------------------------------------- session

type session struct {
	device  unsafe.Pointer
	client  unsafe.Pointer
	capture unsafe.Pointer
	started bool

	conv      *Converter
	format    Format
	loopback  bool
	period    time.Duration
	srcStride int

	zeros []byte
}

func openSession(deviceID string, bufferMs int) (*session, error) {
	enum, err := newDeviceEnumerator()
	if err != nil {
		return nil, err
	}
	defer release(enum)

	id, err := windows.UTF16PtrFromString(deviceID)
	if err != nil {
		return nil, errors.New("the saved device id is not usable")
	}

	s := &session{}
	ok := false
	defer func() {
		if !ok {
			s.close()
		}
	}()

	if hr := call(enum, 5, uintptr(unsafe.Pointer(id)), uintptr(unsafe.Pointer(&s.device))); !hr.ok() {
		return nil, hr.errorf("open the selected audio device")
	}

	if s.loopback, err = isRenderEndpoint(s.device); err != nil {
		return nil, err
	}
	if err := s.initialize(bufferMs); err != nil {
		return nil, err
	}

	if s.conv, err = NewConverter(s.format); err != nil {
		return nil, err
	}
	s.srcStride = s.format.BytesPerFrame()

	if hr := call(s.client, 14, uintptr(unsafe.Pointer(&iidIAudioCaptureClient)), uintptr(unsafe.Pointer(&s.capture))); !hr.ok() {
		return nil, hr.errorf("open the capture stream")
	}

	s.period = devicePeriod(s.client)

	if hr := call(s.client, 10); !hr.ok() {
		return nil, hr.errorf("start the audio stream")
	}
	s.started = true

	ok = true
	return s, nil
}

// initialize opens an IAudioClient on the device and negotiates a format.
//
// The first attempt takes the audio engine's own mix format and converts it in
// Go. That is the path every loopback recorder takes, so it is the one most
// likely to behave, and the conversion it needs is covered by tests.
//
// The second asks the engine to hand over the pipeline's format directly, which
// Windows 10 will do. It is only a fallback because a device whose mix format we
// cannot decode is the one case where it helps, and a silent stream would be far
// harder for a user to report than an outright failure to start.
//
// Each attempt gets a fresh client: an IAudioClient whose Initialize has failed
// is not documented to be reusable.
func (s *session) initialize(bufferMs int) error {
	flags := uint32(0)
	if s.loopback {
		flags |= streamFlagsLoopback
	}

	if bufferMs < minBufferMs {
		bufferMs = minBufferMs
	}
	// REFERENCE_TIME counts 100 ns ticks. It is an int64 passed by value, which
	// fits in a single argument only because this is built for amd64.
	buffer := uintptr(int64(bufferMs) * 10_000)

	attempt := func(extra uint32, w *waveFormatEx) (unsafe.Pointer, hresult) {
		var client unsafe.Pointer
		if hr := call(s.device, 3, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0,
			uintptr(unsafe.Pointer(&client))); !hr.ok() {
			return nil, hr
		}
		hr := call(client, 3, audclntShareModeShared, uintptr(flags|extra), buffer, 0,
			uintptr(unsafe.Pointer(w)), 0)
		if !hr.ok() {
			release(client)
			return nil, hr
		}
		return client, 0
	}

	failure := errors.New("the device did not report a usable audio format")

	if mixPtr, err := mixFormat(s.device); err != nil {
		failure = err
	} else {
		defer coTaskMemFree(unsafe.Pointer(mixPtr))

		if mix, err := formatOf(mixPtr); err != nil {
			failure = err
		} else if client, hr := attempt(0, mixPtr); hr.ok() {
			s.client, s.format = client, mix
			return nil
		} else {
			failure = hr.errorf("open the audio device")
		}
	}

	pipelineFormat := waveFormatEx{
		FormatTag:      waveFormatTagPCM,
		Channels:       outChannels,
		SamplesPerSec:  outSampleRate,
		AvgBytesPerSec: outSampleRate * outChannels * 2,
		BlockAlign:     outChannels * 2,
		BitsPerSample:  16,
	}

	client, hr := attempt(streamFlagsAutoConvertPCM|streamFlagsSRCDefaultQuality, &pipelineFormat)
	if !hr.ok() {
		return failure
	}
	s.client = client
	s.format = Format{SampleRate: outSampleRate, Channels: outChannels, BitsPerSample: 16}
	return nil
}

// mixFormat reads the format the audio engine is running the endpoint at. It
// needs an IAudioClient of its own, since the one the caller ends up using may
// not exist yet.
func mixFormat(device unsafe.Pointer) (*waveFormatEx, error) {
	var client unsafe.Pointer
	if hr := call(device, 3, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0,
		uintptr(unsafe.Pointer(&client))); !hr.ok() {
		return nil, hr.errorf("activate the audio device")
	}
	defer release(client)

	var w *waveFormatEx
	if hr := call(client, 8, uintptr(unsafe.Pointer(&w))); !hr.ok() {
		return nil, hr.errorf("read the device's audio format")
	}
	return w, nil
}

// devicePeriod picks how often to poll for audio: half the endpoint's own period,
// so a poll can never straddle two of them.
func devicePeriod(client unsafe.Pointer) time.Duration {
	var defaultPeriod, minPeriod int64
	if hr := call(client, 9, uintptr(unsafe.Pointer(&defaultPeriod)), uintptr(unsafe.Pointer(&minPeriod))); hr.ok() {
		if p := time.Duration(defaultPeriod*100) / 2; p >= 2*time.Millisecond && p <= 20*time.Millisecond {
			return p
		}
	}
	return pollInterval
}

// read drains every packet the endpoint currently has, appending converted PCM
// to out, and returns how many source frames it consumed.
func (s *session) read(out *[]byte) (int, error) {
	total := 0
	for {
		var packet uint32
		if hr := call(s.capture, 5, uintptr(unsafe.Pointer(&packet))); !hr.ok() {
			return total, hr.errorf("read from the audio device")
		}
		if packet == 0 {
			return total, nil
		}

		var (
			data   *byte
			frames uint32
			flags  uint32
		)
		if hr := call(s.capture, 3,
			uintptr(unsafe.Pointer(&data)),
			uintptr(unsafe.Pointer(&frames)),
			uintptr(unsafe.Pointer(&flags)),
			0, 0,
		); !hr.ok() {
			return total, hr.errorf("read from the audio device")
		}

		if frames > 0 {
			n := int(frames) * s.srcStride
			if flags&bufferFlagsSilent != 0 || data == nil {
				// A silent stretch is signalled with a flag rather than written
				// out, and the buffer's contents are undefined. Feed the
				// converter real zeros so its resampler state stays continuous.
				if cap(s.zeros) < n {
					s.zeros = make([]byte, n)
				}
				s.zeros = s.zeros[:n]
				clear(s.zeros)
				*out = s.conv.Convert(s.zeros, *out)
			} else {
				*out = s.conv.Convert(unsafe.Slice(data, n), *out)
			}
			total += int(frames)
		}

		if hr := call(s.capture, 4, uintptr(frames)); !hr.ok() {
			return total, hr.errorf("release the audio buffer")
		}
	}
}

func (s *session) close() {
	if s.started {
		call(s.client, 11) // IAudioClient::Stop
		s.started = false
	}
	release(s.capture)
	release(s.client)
	release(s.device)
	s.capture, s.client, s.device = nil, nil, nil
}
