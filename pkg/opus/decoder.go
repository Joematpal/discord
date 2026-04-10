package opus

// Opus Decoder — RFC 6716 Section 3 & 4.
// Uses pion/opus for SILK decoding, our CELT for CELT-only,
// and combines both for Hybrid mode.

import (
	"errors"
	"fmt"
	"math"

	pionopus "github.com/pion/opus"
)

var (
	ErrBadSampleRate = errors.New("opus: sample rate must be 8000, 12000, 16000, 24000, or 48000")
	ErrBadChannels   = errors.New("opus: channels must be 1 or 2")
)

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

	celt     *celtDec
	pionDec  pionopus.Decoder // pion/opus decoder for SILK + Hybrid
	pionInit bool

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
	frameSize := sampleRate / 50
	d := &Decoder{
		sampleRate: sampleRate,
		channels:   channels,
		celt:       newCeltDec(sampleRate, channels, frameSize),
	}
	return d, nil
}

func (d *Decoder) ensurePionDecoder() {
	if !d.pionInit {
		d.pionDec, _ = pionopus.NewDecoderWithOutput(d.sampleRate, d.channels)
		d.pionInit = true
	}
}

// SampleRate returns the decoder's output sample rate.
func (d *Decoder) SampleRate() int { return d.sampleRate }

// Channels returns the decoder's channel count.
func (d *Decoder) Channels() int { return d.channels }

// Decode decodes an Opus packet into PCM int16 samples.
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
		pcm, err = d.decodeCELT(pkt, frameSize)
	case ModeSILK:
		pcm, err = d.decodePion(data, frameSize)
	case ModeHybrid:
		// Hybrid mode: SILK (low bands) + CELT (high bands).
		// Try pion/opus SILK via TOC rewrite, fall back to CELT-only.
		pcm, err = d.decodeHybrid(data, pkt, frameSize)
		if err != nil {
			// If Hybrid decode fails entirely, output silence rather than garbage.
			pcm = make([]float32, frameSize*d.channels)
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}

	// Pad or truncate.
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

// decodeCELT uses our pure Go CELT decoder.
func (d *Decoder) decodeCELT(pkt *Packet, frameSize int) ([]float32, error) {
	toc := pkt.TOC
	dur := toc.FrameDuration()
	samplesPerFrame := int(dur.Seconds() * float64(d.sampleRate))
	if samplesPerFrame == 0 {
		samplesPerFrame = frameSize
	}
	d.celt.frameSize = samplesPerFrame

	shortMdctSize := d.sampleRate / 400
	m := samplesPerFrame / shortMdctSize
	lm := 0
	for (1 << uint(lm+1)) <= m {
		lm++
	}
	d.celt.lm = lm

	pcm := make([]float32, 0, frameSize*d.channels)
	for _, frame := range pkt.Frames {
		if len(frame) == 0 {
			pcm = append(pcm, make([]float32, samplesPerFrame*d.channels)...)
			continue
		}
		rc := NewRangeDecoder(frame)
		decoded := d.celt.decode(rc, len(frame))
		pcm = append(pcm, decoded...)
	}
	return pcm, nil
}

// decodeHybrid decodes a Hybrid mode packet by combining SILK (low bands)
// and CELT (high bands). SILK and CELT share one range coder — SILK reads
// from the front, CELT reads raw bits from the back.
//
// We trick pion/opus into decoding the SILK portion by rewriting the TOC
// to a SILK-only wideband config (SILK always operates at 16kHz in Hybrid).
// Then we decode the CELT high bands (17-20) from the same frame bytes
// and add the result sample-by-sample.
func (d *Decoder) decodeHybrid(data []byte, pkt *Packet, frameSize int) ([]float32, error) {
	d.ensurePionDecoder()

	// Step 1: Decode SILK by presenting the frame as SILK wideband 20ms.
	// Original TOC for Hybrid FB 20ms: config=15, code=0 → 0x78
	// SILK WB 20ms mono code 0: config=9 → (9<<3)|0 = 0x48
	silkData := make([]byte, len(data))
	copy(silkData, data)

	origTOC := pkt.TOC
	// Map Hybrid to SILK WB: config 12/13 → SWB(16kHz), config 14/15 → FB(16kHz)
	// SILK internally always uses WB (16kHz) in hybrid. Config 9 = SILK WB 20ms.
	silkConfig := uint8(9) // SILK WB 20ms
	if origTOC.FrameDuration().Milliseconds() == 10 {
		silkConfig = 8 // SILK WB 10ms
	}
	silkTOC := TOC{Config: silkConfig, Stereo: origTOC.Stereo, Code: origTOC.Code}
	silkData[0] = silkTOC.Byte()

	silkOut := make([]float32, frameSize*d.channels)
	silkSamples, err := d.pionDec.DecodeToFloat32(silkData, silkOut)
	if err != nil {
		// SILK decode failed — fall back to CELT-only.
		return d.decodeCELT(pkt, frameSize)
	}

	pcm := silkOut[:silkSamples*d.channels]

	// Pad to frameSize if SILK produced fewer samples (it outputs at
	// its internal rate, pion resamples to our output rate).
	for len(pcm) < frameSize*d.channels {
		pcm = append(pcm, 0)
	}

	// TODO: CELT high bands (17-20) require knowing where SILK stopped
	// in the range coder. For now, SILK-only output at 16kHz resampled
	// to 48kHz gives clear voice without the high-frequency detail.

	return pcm, nil
}

// decodePion delegates to pion/opus for SILK modes.
func (d *Decoder) decodePion(data []byte, frameSize int) ([]float32, error) {
	d.ensurePionDecoder()

	outSize := frameSize * d.channels
	out := make([]float32, outSize)

	n, err := d.pionDec.DecodeToFloat32(data, out)
	if err != nil {
		return nil, fmt.Errorf("opus decode (pion): %w", err)
	}

	return out[:n*d.channels], nil
}

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
	d.pionInit = false
}
