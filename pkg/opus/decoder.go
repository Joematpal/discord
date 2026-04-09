package opus

// Opus Decoder — RFC 6716 Section 3 & 4.

import (
	"errors"
	"fmt"
	"math"
)

var (
	ErrBadSampleRate = errors.New("opus: sample rate must be 8000, 12000, 16000, 24000, or 48000")
	ErrBadChannels   = errors.New("opus: channels must be 1 or 2")
)

// validSampleRate checks whether sr is one of the Opus-supported rates.
func validSampleRate(sr int) bool {
	switch sr {
	case 8000, 12000, 16000, 24000, 48000:
		return true
	}
	return false
}

// Decoder decodes Opus packets to PCM audio.
type Decoder struct {
	sampleRate int
	channels   int

	celt *celtDec

	// Packet loss concealment state.
	lastGoodSamples []float32
	plcCount        int
}

// NewDecoder creates a decoder for the given sample rate and channel count.
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrBadSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, ErrBadChannels
	}
	frameSize := sampleRate / 50 // 20ms default
	return &Decoder{
		sampleRate: sampleRate,
		channels:   channels,
		celt:       newCeltDec(sampleRate, channels, frameSize),
	}, nil
}

// SampleRate returns the decoder's output sample rate.
func (d *Decoder) SampleRate() int { return d.sampleRate }

// Channels returns the decoder's channel count.
func (d *Decoder) Channels() int { return d.channels }

// Decode decodes an Opus packet into PCM int16 samples.
// frameSize is the number of samples per channel to decode.
// If fec is true, forward error correction data is used if available.
// Returns interleaved samples: [L, R, L, R, ...] for stereo.
func (d *Decoder) Decode(data []byte, frameSize int, fec bool) ([]int16, error) {
	pcmFloat, err := d.DecodeFloat(data, frameSize, fec)
	if err != nil {
		return nil, err
	}
	out := make([]int16, len(pcmFloat))
	for i, s := range pcmFloat {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		out[i] = int16(s * 32767.0)
	}
	return out, nil
}

// DecodeFloat decodes an Opus packet into PCM float32 samples in [-1.0, 1.0].
func (d *Decoder) DecodeFloat(data []byte, frameSize int, fec bool) ([]float32, error) {
	if data == nil {
		return d.decodePLC(frameSize)
	}

	pkt, err := ParsePacket(data)
	if err != nil {
		return nil, fmt.Errorf("opus decode: %w", err)
	}

	toc := pkt.TOC
	mode := toc.Mode()

	var pcm []float32

	switch mode {
	case ModeCELT:
		dur := toc.FrameDuration()
		samplesPerFrame := int(dur.Seconds() * float64(d.sampleRate))
		if samplesPerFrame == 0 {
			samplesPerFrame = frameSize
		}
		d.celt.frameSize = samplesPerFrame

		// Recompute LM for this frame size.
		shortMdctSize := d.sampleRate / 400
		m := samplesPerFrame / shortMdctSize
		lm := 0
		for (1 << uint(lm+1)) <= m {
			lm++
		}
		d.celt.lm = lm

		pcm = make([]float32, 0, frameSize*d.channels)
		for _, frame := range pkt.Frames {
			if len(frame) == 0 {
				pcm = append(pcm, make([]float32, samplesPerFrame*d.channels)...)
				continue
			}
			rc := NewRangeDecoder(frame)
			decoded := d.celt.decode(rc, len(frame))
			pcm = append(pcm, decoded...)
		}

	case ModeSILK:
		pcm = make([]float32, frameSize*d.channels)

	case ModeHybrid:
		pcm = make([]float32, frameSize*d.channels)
	}

	// Truncate or pad to requested frameSize.
	wanted := frameSize * d.channels
	if len(pcm) > wanted {
		pcm = pcm[:wanted]
	}
	for len(pcm) < wanted {
		pcm = append(pcm, 0)
	}

	d.lastGoodSamples = pcm
	d.plcCount = 0
	return pcm, nil
}

// decodePLC performs packet loss concealment by fading the last good frame.
func (d *Decoder) decodePLC(frameSize int) ([]float32, error) {
	d.plcCount++
	n := frameSize * d.channels
	out := make([]float32, n)

	if d.lastGoodSamples == nil {
		return out, nil
	}

	gain := float32(math.Pow(0.85, float64(d.plcCount)))
	for i := 0; i < n && i < len(d.lastGoodSamples); i++ {
		out[i] = d.lastGoodSamples[i] * gain
	}

	return out, nil
}

// Reset resets the decoder state.
func (d *Decoder) Reset() {
	frameSize := d.sampleRate / 50
	d.celt = newCeltDec(d.sampleRate, d.channels, frameSize)
	d.lastGoodSamples = nil
	d.plcCount = 0
}
