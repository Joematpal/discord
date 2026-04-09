package opus

import "math"

// CELT IMDCT, overlap-add, and de-emphasis.
// RFC 6716 Section 4.3.8 and 4.3.9.

// imdct computes the inverse MDCT (type-IV DCT) of length N.
// Input: N frequency-domain coefficients.
// Output: 2*N time-domain samples.
func imdct(in []float32, n int) []float32 {
	out := make([]float32, 2*n)
	nf := float64(n)

	for t := 0; t < 2*n; t++ {
		sum := float64(0)
		for k := 0; k < n; k++ {
			sum += float64(in[k]) * math.Cos(math.Pi/nf*(float64(t)+0.5+nf/2)*(float64(k)+0.5))
		}
		out[t] = float32(sum / nf)
	}
	return out
}

// overlapAdd applies the MDCT window and overlap-adds with the previous frame.
// prev is the overlap buffer from the previous frame (length = overlap).
// imdctOut is the full IMDCT output (length = 2*frameSize).
// Returns the output PCM (length = frameSize) and updates prev in-place.
func overlapAdd(imdctOut []float32, prev []float32, frameSize, overlap int) []float32 {
	out := make([]float32, frameSize)

	// Overlap region: window current + window previous.
	for i := 0; i < overlap; i++ {
		w1 := mdctWindow120[i]
		w2 := mdctWindow120[overlap-1-i]
		out[i] = w1*imdctOut[i] + w2*prev[i]
	}

	// Pass-through region.
	for i := overlap; i < frameSize; i++ {
		out[i] = imdctOut[i]
	}

	// Save tail for next frame's overlap.
	for i := 0; i < overlap; i++ {
		prev[i] = imdctOut[frameSize+i]
	}

	return out
}

// deemphasis applies the de-emphasis IIR filter: y[n] = x[n] + coef * y[n-1].
// Returns the updated filter state (last output sample).
func deemphasis(pcm []float32, state float32) float32 {
	for i := range pcm {
		pcm[i] = pcm[i] + deemphCoef*state
		state = pcm[i]
	}
	return state
}

// denormalizeBands multiplies each band's normalized coefficients by its energy.
// bandE contains log2 energies; the spectrum X is modified in-place.
func denormalizeBands(x []float32, bandE []float32, nbBands, lm int) {
	m := 1 << uint(lm)
	for i := 0; i < nbBands; i++ {
		bandStart := eBands5ms[i] * m
		bandEnd := eBands5ms[i+1] * m
		if bandEnd > len(x) {
			bandEnd = len(x)
		}
		// Convert log2 energy to linear gain.
		gain := float32(math.Exp2(float64(bandE[i])))
		for k := bandStart; k < bandEnd; k++ {
			x[k] *= gain
		}
	}
}

// synthesize performs the full CELT synthesis chain:
// denormalize → IMDCT → overlap-add → de-emphasis.
func (st *celtDecState) synthesize(x []float32, bandE []float32, frameSize, nbBands, lm int) []float32 {
	// 1. Denormalize: scale spectrum by band energies.
	denormalizeBands(x, bandE, nbBands, lm)

	// 2. IMDCT. The MDCT size N = len(x) (spectral bins), output = 2*N.
	// frameSize = N + overlap for CELT, but we use the spectrum size.
	n := len(x)
	mdctOut := imdct(x, n)

	// 3. Overlap-add. Output is N samples; overlap is saved for next frame.
	pcm := overlapAdd(mdctOut, st.overlap, n, celtOverlap)

	// 4. De-emphasis.
	st.deemphState = deemphasis(pcm, st.deemphState)

	return pcm
}
