package opus

// Opus Encoder — RFC 6716 Section 2.

import (
	"errors"
)

// Application specifies the encoder's intended use case.
type Application int

const (
	// AppVoIP optimizes for speech intelligibility.
	AppVoIP Application = 2048
	// AppAudio optimizes for faithfully reproducing the input.
	AppAudio Application = 2049
	// AppLowDelay minimizes codec latency.
	AppLowDelay Application = 2051
)

func (a Application) String() string {
	switch a {
	case AppVoIP:
		return "VoIP"
	case AppAudio:
		return "Audio"
	case AppLowDelay:
		return "LowDelay"
	default:
		return "Unknown"
	}
}

var ErrBadApplication = errors.New("opus: invalid application type")

// Encoder encodes PCM audio to Opus packets.
type Encoder struct {
	sampleRate  int
	channels    int
	application Application

	bitrate    int // target bitrate in bits/s
	complexity int // 0–10

	celt *celtEnc
}

// NewEncoder creates an encoder with the given sample rate, channels, and application.
func NewEncoder(sampleRate, channels int, app Application) (*Encoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrBadSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, ErrBadChannels
	}
	switch app {
	case AppVoIP, AppAudio, AppLowDelay:
	default:
		return nil, ErrBadApplication
	}

	frameSize := sampleRate / 50 // 20ms default
	return &Encoder{
		sampleRate:  sampleRate,
		channels:    channels,
		application: app,
		bitrate:     64000,
		complexity:  5,
		celt:        newCeltEnc(sampleRate, channels, frameSize),
	}, nil
}

// SampleRate returns the encoder's input sample rate.
func (e *Encoder) SampleRate() int { return e.sampleRate }

// Channels returns the encoder's channel count.
func (e *Encoder) Channels() int { return e.channels }

// Application returns the encoder's application mode.
func (e *Encoder) Application() Application { return e.application }

// SetBitrate sets the target bitrate in bits/s (500–512000).
func (e *Encoder) SetBitrate(bps int) error {
	if bps < 500 || bps > 512000 {
		return errors.New("opus: bitrate out of range [500, 512000]")
	}
	e.bitrate = bps
	return nil
}

// Bitrate returns the current target bitrate.
func (e *Encoder) Bitrate() int { return e.bitrate }

// SetComplexity sets the encoding complexity (0–10).
func (e *Encoder) SetComplexity(c int) error {
	if c < 0 || c > 10 {
		return errors.New("opus: complexity out of range [0, 10]")
	}
	e.complexity = c
	return nil
}

// Complexity returns the current encoding complexity.
func (e *Encoder) Complexity() int { return e.complexity }

// Encode encodes PCM int16 samples into an Opus packet.
// pcm contains interleaved samples ([L, R, L, R, ...] for stereo).
// frameSize is the number of samples per channel (must match a valid Opus
// frame duration: 2.5, 5, 10, 20, 40, or 60 ms).
func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	pcmFloat := make([]float64, len(pcm))
	for i, s := range pcm {
		pcmFloat[i] = float64(s) / 32768.0
	}
	return e.encodeFloat64(pcmFloat, frameSize)
}

// EncodeFloat encodes PCM float32 samples in [-1.0, 1.0] into an Opus packet.
func (e *Encoder) EncodeFloat(pcm []float32, frameSize int) ([]byte, error) {
	pcm64 := make([]float64, len(pcm))
	for i, s := range pcm {
		pcm64[i] = float64(s)
	}
	return e.encodeFloat64(pcm64, frameSize)
}

func (e *Encoder) encodeFloat64(pcm []float64, frameSize int) ([]byte, error) {
	expected := frameSize * e.channels
	if len(pcm) < expected {
		return nil, errors.New("opus: pcm buffer too small for frame size")
	}

	e.celt.frameSize = frameSize

	// Determine packet size from bitrate and frame duration.
	frameDurSec := float64(frameSize) / float64(e.sampleRate)
	packetBytes := int(float64(e.bitrate)*frameDurSec/8.0 + 0.5)
	if packetBytes < 2 {
		packetBytes = 2
	}
	if packetBytes > 1275 {
		packetBytes = 1275
	}

	// Silence detection.
	silent := true
	for _, s := range pcm[:expected] {
		if s != 0 {
			silent = false
			break
		}
	}
	if silent {
		return SilenceFrame, nil
	}

	// Build packet: TOC byte + CELT frame.
	// Choose CELT fullband config based on frame duration.
	config := e.chooseConfig(frameSize)
	toc := TOC{Config: config, Stereo: e.channels == 2, Code: 0}

	// Allocate range encoder for the frame payload.
	frameBytes := packetBytes - 1 // minus TOC byte
	if frameBytes < 1 {
		frameBytes = 1
	}
	rc := NewRangeEncoder(frameBytes)
	bitBudget := frameBytes * 8
	e.celt.encode(rc, pcm[:expected], bitBudget)
	frameData := rc.Done()

	out := make([]byte, 1+len(frameData))
	out[0] = toc.Byte()
	copy(out[1:], frameData)
	return out, nil
}

// chooseConfig selects the CELT configuration number for the given frame size.
func (e *Encoder) chooseConfig(frameSize int) uint8 {
	// CELT fullband (config 28–31): 2.5, 5, 10, 20 ms.
	switch frameSize {
	case e.sampleRate * 5 / 2000: // 2.5 ms
		return 28
	case e.sampleRate * 5 / 1000: // 5 ms
		return 29
	case e.sampleRate * 10 / 1000: // 10 ms
		return 30
	default: // 20 ms
		return 31
	}
}

// Reset resets the encoder state.
func (e *Encoder) Reset() {
	frameSize := e.sampleRate / 50
	e.celt = newCeltEnc(e.sampleRate, e.channels, frameSize)
}
