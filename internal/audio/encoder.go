package audio

/*
#cgo pkg-config: opus
#include <opus/opus.h>
#include <stdlib.h>

// opus_encoder_ctl is variadic, which cgo cannot call directly. Both wrappers
// take the *_REQUEST constant rather than the OPUS_SET_* macro, because those
// macros expand to a comma expression that has no Go equivalent.
static int dasEncoderSetCtl(OpusEncoder *enc, int request, opus_int32 value) {
	return opus_encoder_ctl(enc, request, value);
}

static int dasEncoderGetCtl(OpusEncoder *enc, int request, opus_int32 *value) {
	return opus_encoder_ctl(enc, request, value);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Application tells the encoder what it is encoding, which selects how it
// trades bits between speech and general audio.
type Application int

const (
	// ApplicationAudio favours fidelity for music and mixed content. This is
	// what both of our audio paths want.
	ApplicationAudio Application = C.OPUS_APPLICATION_AUDIO

	// ApplicationVoIP favours speech intelligibility.
	ApplicationVoIP Application = C.OPUS_APPLICATION_VOIP
)

// Bitrate bounds. Discord accepts a wide range; below 48 kbps music audibly
// degrades and above 160 kbps there is nothing left to gain over a voice
// channel.
const (
	MinBitrate     = 48_000
	MaxBitrate     = 160_000
	DefaultBitrate = 96_000
)

// EncoderConfig configures a new [Encoder].
type EncoderConfig struct {
	// Application selects the encoder's tuning. Defaults to [ApplicationAudio].
	Application Application

	// Bitrate in bits per second. Defaults to [DefaultBitrate].
	Bitrate int

	// PacketLossPercent enables Opus's forward error correction and tells it how
	// much loss to expect. Zero disables FEC, which is right for file playback;
	// live capture sets a small value so a dropped packet degrades instead of
	// gapping.
	PacketLossPercent int
}

// Encoder encodes 20 ms PCM frames to Opus packets.
//
// libopus encoder state is not safe for concurrent use, and an Encoder must be
// used from a single goroutine. In this pipeline that is the pacer goroutine.
type Encoder struct {
	enc *C.OpusEncoder
	buf []byte
}

// NewEncoder creates an encoder for [SampleRate] and [Channels].
func NewEncoder(cfg EncoderConfig) (*Encoder, error) {
	if cfg.Application == 0 {
		cfg.Application = ApplicationAudio
	}
	if cfg.Bitrate == 0 {
		cfg.Bitrate = DefaultBitrate
	}

	var cErr C.int
	enc := C.opus_encoder_create(
		C.opus_int32(SampleRate),
		C.int(Channels),
		C.int(cfg.Application),
		&cErr,
	)
	if cErr != C.OPUS_OK {
		return nil, fmt.Errorf("create opus encoder: %w", opusError(cErr))
	}

	e := &Encoder{
		enc: enc,
		buf: make([]byte, MaxOpusFrameSize),
	}

	if err := e.SetBitrate(cfg.Bitrate); err != nil {
		e.Close()
		return nil, err
	}
	if cfg.PacketLossPercent > 0 {
		if err := e.setCtl(C.OPUS_SET_INBAND_FEC_REQUEST, 1); err != nil {
			e.Close()
			return nil, fmt.Errorf("enable inband FEC: %w", err)
		}
		if err := e.setCtl(C.OPUS_SET_PACKET_LOSS_PERC_REQUEST, C.opus_int32(cfg.PacketLossPercent)); err != nil {
			e.Close()
			return nil, fmt.Errorf("set packet loss percent: %w", err)
		}
	}

	return e, nil
}

// SetBitrate changes the target bitrate, clamped to [MinBitrate]..[MaxBitrate].
// It may be called between frames without recreating the encoder.
func (e *Encoder) SetBitrate(bps int) error {
	if bps < MinBitrate {
		bps = MinBitrate
	}
	if bps > MaxBitrate {
		bps = MaxBitrate
	}
	if err := e.setCtl(C.OPUS_SET_BITRATE_REQUEST, C.opus_int32(bps)); err != nil {
		return fmt.Errorf("set bitrate: %w", err)
	}
	return nil
}

// Bitrate returns the encoder's current target bitrate in bits per second.
func (e *Encoder) Bitrate() (int, error) {
	v, err := e.getCtl(C.OPUS_GET_BITRATE_REQUEST)
	if err != nil {
		return 0, fmt.Errorf("get bitrate: %w", err)
	}
	return int(v), nil
}

// Encode encodes one frame of exactly [SamplesPerFrame] interleaved samples.
//
// The returned slice is reused on the next call, and is handed straight to the
// voice connection's writer, which copies it before returning.
func (e *Encoder) Encode(pcm []int16) ([]byte, error) {
	if e.enc == nil {
		return nil, errors.New("opus: encoder is closed")
	}
	if len(pcm) != SamplesPerFrame {
		return nil, fmt.Errorf("opus: got %d samples, want %d", len(pcm), SamplesPerFrame)
	}

	n := C.opus_encode(
		e.enc,
		(*C.opus_int16)(unsafe.Pointer(&pcm[0])),
		C.int(SamplesPerChannel),
		(*C.uchar)(unsafe.Pointer(&e.buf[0])),
		C.opus_int32(len(e.buf)),
	)
	if n < 0 {
		return nil, fmt.Errorf("opus encode: %w", opusError(n))
	}
	return e.buf[:n], nil
}

// Close frees the encoder. It is safe to call more than once.
func (e *Encoder) Close() {
	if e.enc == nil {
		return
	}
	C.opus_encoder_destroy(e.enc)
	e.enc = nil
}

func (e *Encoder) setCtl(request C.int, value C.opus_int32) error {
	if e.enc == nil {
		return errors.New("opus: encoder is closed")
	}
	if code := C.dasEncoderSetCtl(e.enc, request, value); code != C.OPUS_OK {
		return opusError(code)
	}
	return nil
}

func (e *Encoder) getCtl(request C.int) (C.opus_int32, error) {
	if e.enc == nil {
		return 0, errors.New("opus: encoder is closed")
	}
	var value C.opus_int32
	if code := C.dasEncoderGetCtl(e.enc, request, &value); code != C.OPUS_OK {
		return 0, opusError(code)
	}
	return value, nil
}

// Version returns the libopus version the binary is linked against, which is
// worth logging when diagnosing a build.
func Version() string {
	return C.GoString(C.opus_get_version_string())
}

func opusError(code C.int) error {
	return fmt.Errorf("libopus: %s (%d)", C.GoString(C.opus_strerror(code)), int(code))
}
