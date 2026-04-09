package opus

import "math"

// CELT decoder — RFC 6716 Section 4.3.
// Full decode pipeline: energy → bit allocation → PVQ → IMDCT → de-emphasis.

// celtDecState holds per-channel CELT decoder state.
type celtDecState struct {
	overlap     []float32 // overlap buffer for IMDCT overlap-add
	prevBandE   []float32 // previous frame's band energies (for prediction)
	deemphState float32   // de-emphasis filter state
}

func newCeltDecState() *celtDecState {
	return &celtDecState{
		overlap:   make([]float32, celtOverlap),
		prevBandE: make([]float32, celtMaxBands),
	}
}

// celtDec is the CELT decoder.
type celtDec struct {
	sampleRate int
	channels   int
	frameSize  int
	lm         int // log2(M) where M = frameSize / shortMdctSize
	states     []*celtDecState
}

func newCeltDec(sampleRate, channels, frameSize int) *celtDec {
	// Compute LM from frame size.
	// shortMdctSize = sampleRate / 400 (2.5ms)
	shortMdctSize := sampleRate / 400
	m := frameSize / shortMdctSize
	lm := 0
	for (1 << uint(lm+1)) <= m {
		lm++
	}

	d := &celtDec{
		sampleRate: sampleRate,
		channels:   channels,
		frameSize:  frameSize,
		lm:         lm,
		states:     make([]*celtDecState, channels),
	}
	for i := range d.states {
		d.states[i] = newCeltDecState()
	}
	return d
}

// decode decodes one CELT frame, returning interleaved PCM samples.
func (d *celtDec) decode(rc *RangeDecoder, frameLen int) []float32 {
	nbBands := celtMaxBands
	lm := d.lm

	// --- 1. Silence flag (Section 4.3.1) ---
	silence := false
	if rc.Tell() <= 1 {
		if rc.BitLogP(15) == 1 {
			silence = true
		}
	}

	if silence {
		return make([]float32, d.frameSize*d.channels)
	}

	// --- 2. Post-filter (Section 4.3.2) ---
	hasPostFilter := rc.BitLogP(1) == 1
	if hasPostFilter {
		octave := rc.Uint(6)
		_ = (16 << octave) + int(rc.Bits(uint(4+int(octave)))) - 1 // period
		_ = float32(rc.Bits(3)+1) * 0.09375                        // gain
		if rc.Bits(1) == 1 {
			rc.Uint(3) // tapset via icdf
		}
	}

	// --- 3. Transient flag (Section 4.3.3) ---
	isTransient := false
	if lm > 0 {
		isTransient = rc.BitLogP(3) == 1
	}

	// --- 4. Intra flag ---
	intra := rc.BitLogP(3) == 1

	// --- 5. Coarse energy decode (Section 4.3.4) ---
	bandE := make([]float32, nbBands*d.channels)
	for c := 0; c < d.channels; c++ {
		for i := 0; i < nbBands; i++ {
			bandE[c*nbBands+i] = d.states[c].prevBandE[i]
		}
	}
	decodeCoarseEnergy(rc, nbBands, lm, d.channels, intra, bandE, nil)

	// --- 6. TF change flags ---
	_ = decodeTFChanges(rc, nbBands, lm, isTransient)

	// --- 7. Spread mode ---
	spread := spreadNormal
	if rc.Tell()+4 <= int(rc.storage)*8 {
		spread = int(decodeICDF(rc, spreadICDF[:]))
	}

	// --- 8. Bit allocation (Section 4.3.6) ---
	totalBits := int(rc.storage)*8 - rc.Tell()
	pulses, fineQuant, finePriority := computeAllocation(rc, nbBands, lm, d.channels, totalBits/8, nil)

	// --- 9. Fine energy (Section 4.3.5) ---
	decodeFineEnergy(rc, nbBands, d.channels, bandE, fineQuant)

	// --- 10. PVQ spectral decode (Section 4.3.7) ---
	m := 1 << uint(lm)
	specSize := eBands5ms[nbBands] * m
	x := make([]float32, specSize)
	for i := 0; i < nbBands; i++ {
		bandStart := eBands5ms[i] * m
		bandEnd := eBands5ms[i+1] * m
		n := bandEnd - bandStart
		k := pulses[i]

		if k > 0 && n > 0 {
			// Compute gain for this band from energy.
			gain := float32(math.Exp2(float64(bandE[i])))
			bandX := algUnquant(rc, n, k, spread, gain)
			copy(x[bandStart:bandEnd], bandX)
		} else if n > 0 {
			// Folding: fill with noise from a previous band, scaled by energy.
			gain := float32(math.Exp2(float64(bandE[i])))
			seed := uint32(bandStart)
			for j := bandStart; j < bandEnd; j++ {
				seed = seed*1664525 + 1013904223
				if seed&0x80000000 != 0 {
					x[j] = gain / float32(math.Sqrt(float64(n)))
				} else {
					x[j] = -gain / float32(math.Sqrt(float64(n)))
				}
			}
		}
	}

	// --- 11. Fine energy finalization ---
	bitsLeft := int(rc.storage)*8 - rc.Tell()
	decodeFineEnergyFinal(rc, nbBands, d.channels, bandE, fineQuant, finePriority, bitsLeft)

	// --- 12. Save band energies for next frame ---
	for c := 0; c < d.channels; c++ {
		for i := 0; i < nbBands; i++ {
			d.states[c].prevBandE[i] = bandE[c*nbBands+i]
		}
	}

	// --- 13. Synthesis: IMDCT + overlap-add + de-emphasis ---
	// For mono, synthesize directly. For stereo, interleave.
	if d.channels == 1 {
		return d.states[0].synthesize(x, bandE, d.frameSize, nbBands, lm)
	}

	// Stereo: split spectrum, synthesize each channel, interleave.
	left := make([]float32, specSize)
	right := make([]float32, specSize)
	copy(left, x) // simplified: use same spectrum for both (real impl does M/S)
	copy(right, x)

	pcmL := d.states[0].synthesize(left, bandE[:nbBands], d.frameSize, nbBands, lm)
	pcmR := d.states[1].synthesize(right, bandE[nbBands:], d.frameSize, nbBands, lm)

	out := make([]float32, d.frameSize*2)
	for i := 0; i < d.frameSize; i++ {
		out[2*i] = pcmL[i]
		out[2*i+1] = pcmR[i]
	}
	return out
}

// decodeICDF decodes using an inverse CDF table (cumulative probabilities).
func decodeICDF(rc *RangeDecoder, icdf []uint) int {
	ft := uint32(icdf[0]) + 1
	s := rc.Decode(ft)
	for i := len(icdf) - 1; i >= 0; i-- {
		if s < uint32(icdf[i]) {
			rc.Update(uint32(icdf[i]), ft, ft)
			return i
		}
		ft = uint32(icdf[i])
	}
	rc.Update(0, uint32(icdf[0]), uint32(icdf[0])+1)
	return 0
}

// celtEnc is the CELT encoder (simplified, unchanged from before).
type celtEnc struct {
	sampleRate int
	channels   int
	frameSize  int
}

func newCeltEnc(sampleRate, channels, frameSize int) *celtEnc {
	return &celtEnc{sampleRate: sampleRate, channels: channels, frameSize: frameSize}
}

// encode encodes interleaved PCM into the range coder.
func (e *celtEnc) encode(rc *RangeEncoder, pcm []float64, bitBudget int) {
	// Silence detection.
	silent := true
	for _, s := range pcm {
		if s != 0 {
			silent = false
			break
		}
	}
	if silent {
		rc.BitLogP(1, 15)
		rc.BitLogP(0, 1)
		return
	}
	rc.BitLogP(0, 15)
	rc.BitLogP(0, 1)
	rc.BitLogP(0, 3) // no transient
	rc.BitLogP(0, 3) // intra = 0

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
