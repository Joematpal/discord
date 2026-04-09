package opus

// CELT codec internals (RFC 6716 Section 4.3).
// CELT operates on the MDCT spectrum, dividing it into critical bands
// and coding energy + shape per band.

import (
	"math"
)

// CELT band structure for different frame sizes at 48 kHz.
// eBands defines the starting MDCT bin (in units of the short-block size)
// for each band, plus a sentinel for the end.
// RFC 6716 Table 73.
var celtBands48k = [22]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 10,
	12, 14, 16, 20, 24, 28, 34, 40, 48, 60, 78, 100,
}

const (
	celtMaxBands = 21 // number of bands for 48 kHz
	celtOverlap  = 120
)

// celtFrameSize returns the MDCT frame size for the given sample rate and
// frame duration in samples.
func celtFrameSize(sampleRate, frameSamples int) int {
	return frameSamples
}

// celtShortBlockSize returns the short block MDCT size.
func celtShortBlockSize(frameSize int) int {
	return frameSize / 8
}

// celtState holds the CELT decoder/encoder state for one channel.
type celtState struct {
	// Previous MDCT output for overlap-add.
	overlap []float64

	// Previous band energies for prediction.
	prevEnergy  [celtMaxBands]float64
	prevEnergy2 [celtMaxBands]float64

	// De-emphasis filter state.
	deemphState float64
}

func newCELTState(frameSize int) *celtState {
	return &celtState{
		overlap: make([]float64, celtOverlap),
	}
}

// celtDecoder decodes a single CELT frame from the range coder.
type celtDecoder struct {
	sampleRate int
	channels   int
	frameSize  int // samples per channel per frame
	states     []*celtState
}

func newCELTDecoder(sampleRate, channels, frameSize int) *celtDecoder {
	d := &celtDecoder{
		sampleRate: sampleRate,
		channels:   channels,
		frameSize:  frameSize,
		states:     make([]*celtState, channels),
	}
	for i := range d.states {
		d.states[i] = newCELTState(frameSize)
	}
	return d
}

// decodeCELTFrame decodes one CELT frame, returning interleaved PCM samples.
func (d *celtDecoder) decode(rc *RangeDecoder, frameLen int) []float64 {
	nbBands := celtMaxBands
	isTransient := false

	// 1. Silence flag (RFC 6716 Section 4.3.1).
	if rc.BitLogP(15) == 1 {
		// Silence: output zeros.
		rc.BitLogP(1) // consume the post-filter bit
		return make([]float64, d.frameSize*d.channels)
	}

	// 2. Post-filter (RFC 6716 Section 4.3.2).
	hasPostFilter := rc.BitLogP(1) == 1
	if hasPostFilter {
		// Decode post-filter parameters.
		octave := rc.Uint(6)
		period := (16 << octave) + int(rc.Bits(uint(4+octave)))
		_ = period
		gain := float64(rc.Bits(3)) / 8.0
		_ = gain
		if rc.Bits(1) == 1 {
			// Decode tapset.
			rc.Bits(2)
		}
	}

	// 3. Transient flag (RFC 6716 Section 4.3.3).
	if rc.BitLogP(3) == 1 {
		isTransient = true
	}
	_ = isTransient

	// 4. Coarse energy (RFC 6716 Section 4.3.4).
	// Intra-frame flag.
	intra := rc.BitLogP(3)
	coarseEnergy := d.decodeCoarseEnergy(rc, nbBands, intra)

	// 5. Fine energy bits (RFC 6716 Section 4.3.5).
	fineQuantBits := d.decodeTFChanges(rc, nbBands)
	fineEnergy := d.decodeFineEnergy(rc, nbBands, fineQuantBits)

	// 6. Combine coarse + fine energy.
	bandEnergy := make([]float64, nbBands*d.channels)
	for c := 0; c < d.channels; c++ {
		for i := 0; i < nbBands; i++ {
			idx := c*nbBands + i
			logE := coarseEnergy[idx] + fineEnergy[idx]
			bandEnergy[idx] = math.Exp2(logE)
		}
	}

	// 7. Synthesize output from band energies.
	// Full PVQ decode + IMDCT is complex; for now, generate shaped noise
	// scaled by the decoded energies (produces audible output for testing).
	return d.synthesize(bandEnergy)
}

// decodeCoarseEnergy decodes coarse log-energy per band using Laplace coding.
func (d *celtDecoder) decodeCoarseEnergy(rc *RangeDecoder, nbBands, intra int) []float64 {
	energy := make([]float64, nbBands*d.channels)

	// Alpha and beta for prediction (RFC 6716 Section 4.3.4).
	var alpha, beta float64
	if intra != 0 {
		alpha = 0
		beta = 1.0 - (4915.0 / 32768.0)
	} else {
		alpha = 29440.0 / 32768.0
		beta = 1.0 - (30720.0 / 32768.0)
	}

	for c := 0; c < d.channels; c++ {
		prev := 0.0
		for i := 0; i < nbBands; i++ {
			idx := c*nbBands + i
			pred := alpha*d.states[c].prevEnergy[i] + prev
			qi := d.decLaplace(rc, i, intra)
			q := float64(qi)
			energy[idx] = pred + q
			prev = energy[idx] - pred
			prev = prev * beta
		}
	}

	return energy
}

// decLaplace decodes a Laplace-distributed value from the range coder.
// This is a simplified version of ec_laplace_decode.
func (d *celtDecoder) decLaplace(rc *RangeDecoder, band, intra int) int {
	// Simplified: decode using uniform distribution bounded by typical range.
	// Full implementation uses the Laplace CDF tables.
	fs := rc.DecodeBin(15)

	// Convert frequency to signed integer.
	// The CDF is symmetric around 0.
	center := uint32(1 << 14) // midpoint
	var val int
	if fs >= center {
		val = int((fs - center) >> 10)
	} else {
		val = -int((center - fs) >> 10)
	}

	// Clamp to reasonable range.
	if val > 15 {
		val = 15
	}
	if val < -15 {
		val = -15
	}

	rc.Update(fs, fs+1, 1<<15)
	return val
}

// decodeTFChanges decodes the time-frequency change flags and returns
// the number of fine energy bits per band.
func (d *celtDecoder) decodeTFChanges(rc *RangeDecoder, nbBands int) []int {
	bits := make([]int, nbBands)
	// Simplified: allocate 0 fine bits. Full implementation uses the
	// bit allocation logic from RFC 6716 Section 4.3.5.
	return bits
}

// decodeFineEnergy decodes fine energy quantization.
func (d *celtDecoder) decodeFineEnergy(rc *RangeDecoder, nbBands int, fineBits []int) []float64 {
	energy := make([]float64, nbBands*d.channels)
	for c := 0; c < d.channels; c++ {
		for i := 0; i < nbBands; i++ {
			if fineBits[i] > 0 {
				q := rc.Bits(uint(fineBits[i]))
				energy[c*nbBands+i] = (float64(q) + 0.5) * (1.0 / float64(uint(1)<<uint(fineBits[i]))) * 2.0
				energy[c*nbBands+i] -= 1.0
			}
		}
	}
	return energy
}

// synthesize produces PCM samples from band energies.
// This is a placeholder that generates shaped noise; a full implementation
// would perform PVQ decode → denormalize → IMDCT → overlap-add → de-emphasis.
func (d *celtDecoder) synthesize(bandEnergy []float64) []float64 {
	out := make([]float64, d.frameSize*d.channels)

	nbBands := celtMaxBands
	binsPerBand := d.frameSize / celtBands48k[nbBands]

	// Generate frequency-domain noise shaped by band energies,
	// then do a naive inverse transform.
	spectrum := make([]float64, d.frameSize)
	rng := uint32(0xDEADBEEF)
	for i := 0; i < nbBands; i++ {
		start := celtBands48k[i] * binsPerBand
		end := celtBands48k[i+1] * binsPerBand
		if end > d.frameSize {
			end = d.frameSize
		}
		energy := bandEnergy[i]
		norm := energy / math.Sqrt(float64(end-start))
		for k := start; k < end; k++ {
			// Simple PRNG for deterministic noise.
			rng = rng*1664525 + 1013904223
			if rng&0x80000000 != 0 {
				spectrum[k] = norm
			} else {
				spectrum[k] = -norm
			}
		}
	}

	// Naive inverse DCT-IV (IMDCT substitute).
	n := float64(d.frameSize)
	for t := 0; t < d.frameSize; t++ {
		sum := 0.0
		for k := 0; k < d.frameSize; k++ {
			sum += spectrum[k] * math.Cos(math.Pi/n*(float64(t)+0.5)*(float64(k)+0.5))
		}
		out[t] = sum * (2.0 / n)
	}

	return out
}

// celtEncoder encodes one CELT frame.
type celtEncoder struct {
	sampleRate int
	channels   int
	frameSize  int
	states     []*celtState
}

func newCELTEncoder(sampleRate, channels, frameSize int) *celtEncoder {
	e := &celtEncoder{
		sampleRate: sampleRate,
		channels:   channels,
		frameSize:  frameSize,
		states:     make([]*celtState, channels),
	}
	for i := range e.states {
		e.states[i] = newCELTState(frameSize)
	}
	return e
}

// encode encodes interleaved PCM into the range coder.
func (e *celtEncoder) encode(rc *RangeEncoder, pcm []float64, bitBudget int) {
	nbBands := celtMaxBands

	// 1. Silence detection.
	silent := true
	for _, s := range pcm {
		if s != 0 {
			silent = false
			break
		}
	}
	if silent {
		rc.BitLogP(1, 15) // silence flag = 1
		rc.BitLogP(0, 1)  // post-filter bit
		return
	}
	rc.BitLogP(0, 15) // not silence

	// 2. No post-filter.
	rc.BitLogP(0, 1)

	// 3. No transient.
	rc.BitLogP(0, 3)

	// 4. Coarse energy.
	bandEnergy := e.analyzeBands(pcm)
	rc.BitLogP(0, 3) // intra = 0
	e.encodeCoarseEnergy(rc, bandEnergy, nbBands)

	// 5. Fine energy (0 bits per band for simplicity).

	// 6. Remaining bits would encode PVQ shape coefficients.
	// Pad remaining bits.
	remaining := bitBudget - rc.Tell()
	for remaining > 8 {
		rc.Bits(0, 8)
		remaining -= 8
	}
	if remaining > 0 {
		rc.Bits(0, uint(remaining))
	}
}

// analyzeBands computes log2 energy per critical band from time-domain PCM.
func (e *celtEncoder) analyzeBands(pcm []float64) []float64 {
	nbBands := celtMaxBands
	energy := make([]float64, nbBands*e.channels)
	binsPerBand := e.frameSize / celtBands48k[nbBands]

	for c := 0; c < e.channels; c++ {
		for i := 0; i < nbBands; i++ {
			start := celtBands48k[i] * binsPerBand
			end := celtBands48k[i+1] * binsPerBand
			if end > e.frameSize {
				end = e.frameSize
			}
			sum := 0.0
			for k := start; k < end; k++ {
				idx := k*e.channels + c
				if idx < len(pcm) {
					sum += pcm[idx] * pcm[idx]
				}
			}
			if sum < 1e-30 {
				sum = 1e-30
			}
			energy[c*nbBands+i] = math.Log2(math.Sqrt(sum / float64(end-start)))
		}
	}
	return energy
}

// encodeCoarseEnergy encodes coarse band energies using Laplace coding.
func (e *celtEncoder) encodeCoarseEnergy(rc *RangeEncoder, bandEnergy []float64, nbBands int) {
	beta := 1.0 - (30720.0 / 32768.0)
	alpha := 29440.0 / 32768.0

	for c := 0; c < e.channels; c++ {
		prev := 0.0
		for i := 0; i < nbBands; i++ {
			idx := c*nbBands + i
			pred := alpha*e.states[c].prevEnergy[i] + prev
			q := int(math.Round(bandEnergy[idx] - pred))
			if q > 15 {
				q = 15
			}
			if q < -15 {
				q = -15
			}

			// Encode as frequency in [0, 2^15).
			center := uint32(1 << 14)
			var fs uint32
			if q >= 0 {
				fs = center + uint32(q)<<10
			} else {
				fs = center - uint32(-q)<<10
			}
			if fs >= (1 << 15) {
				fs = (1 << 15) - 1
			}
			rc.EncodeBin(fs, fs+1, 15)

			energy := pred + float64(q)
			prev = (energy - pred) * beta
		}
	}
}
