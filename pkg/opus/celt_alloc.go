package opus

// CELT bit allocation — RFC 6716 Section 4.3.6.
// Distributes available bits among bands, determining pulses[] and fineQuant[].

// computeAllocation performs the full CELT bit allocation.
// Returns pulses per band, fine quantization bits, and fine priority flags.
func computeAllocation(rc *RangeDecoder, nbBands, lm, channels, total int,
	bandBoost []int) (pulses, fineQuant, finePriority []int) {

	pulses = make([]int, nbBands)
	fineQuant = make([]int, nbBands)
	finePriority = make([]int, nbBands)

	m := 1 << uint(lm)

	// Compute two allocation vectors from the static table using
	// interpolation based on available bitrate.
	lo := make([]int, nbBands)
	hi := make([]int, nbBands)

	// Find the two rows in bandAllocation that bracket our bitrate.
	totalBitsPerSample := total * 32 / (channels * m * 100) // rough bits/sample
	loRow := 0
	for loRow < 10 && bandAllocation[loRow+1][0] <= totalBitsPerSample {
		loRow++
	}
	hiRow := loRow + 1
	if hiRow > 10 {
		hiRow = 10
	}

	for i := 0; i < nbBands; i++ {
		n := channels * m * (eBands5ms[i+1] - eBands5ms[i])
		lo[i] = bandAllocation[loRow][i] * n >> 2
		hi[i] = bandAllocation[hiRow][i] * n >> 2
	}

	// Binary search for the interpolation factor.
	mid := make([]int, nbBands)
	loFrac := 0
	hiFrac := 1 << celtAllocSteps
	for step := 0; step < celtAllocSteps; step++ {
		midFrac := (loFrac + hiFrac) >> 1
		sum := 0
		for i := 0; i < nbBands; i++ {
			mid[i] = lo[i] + (midFrac*(hi[i]-lo[i]))>>(celtAllocSteps)
			sum += mid[i]
		}
		if sum > total<<celtBitRes {
			hiFrac = midFrac
		} else {
			loFrac = midFrac
		}
	}

	// Final interpolation.
	bitsLeft := total << celtBitRes
	for i := 0; i < nbBands; i++ {
		mid[i] = lo[i] + (loFrac*(hi[i]-lo[i]))>>(celtAllocSteps)
		bitsLeft -= mid[i]
	}

	// Distribute remaining bits to bands with most demand.
	for bitsLeft > 0 {
		bestBand := -1
		bestNeed := 0
		for i := 0; i < nbBands; i++ {
			n := channels * m * (eBands5ms[i+1] - eBands5ms[i])
			need := n*8 - mid[i]
			if need > bestNeed {
				bestNeed = need
				bestBand = i
			}
		}
		if bestBand < 0 {
			break
		}
		add := 8
		if add > bitsLeft {
			add = bitsLeft
		}
		mid[bestBand] += add
		bitsLeft -= add
	}

	// Convert bit allocations to pulses and fine quant bits.
	for i := 0; i < nbBands; i++ {
		n := channels * m * (eBands5ms[i+1] - eBands5ms[i])
		bits := mid[i]

		// Allocate fine energy bits first.
		fq := 0
		if bits >= n*8 {
			fq = maxFineBits
		} else if bits > 0 {
			fq = bits / (n * 8)
			if fq > maxFineBits {
				fq = maxFineBits
			}
		}
		fineQuant[i] = fq
		bits -= fq * n * 8

		// Remaining bits determine pulses (K).
		// K is roughly bits / (n * rate_per_pulse).
		// Simplified: use the number of bits to estimate K.
		if bits > 0 && n > 0 {
			k := bits / (n * 4)
			if k < 0 {
				k = 0
			}
			pulses[i] = k
		}

		// Fine priority: 0 = high priority, 1 = low.
		finePriority[i] = 0
		if fq > 0 && bits <= 0 {
			finePriority[i] = 1
		}
	}

	return
}

// decodeTFChanges decodes the time-frequency change flags per band.
func decodeTFChanges(rc *RangeDecoder, nbBands, lm int, isTransient bool) []int {
	tf := make([]int, nbBands)
	if lm == 0 {
		return tf
	}

	logp := uint(1)
	tfChanged := 0
	for i := 0; i < nbBands; i++ {
		tfChanged = rc.BitLogP(logp)
		if tfChanged != 0 {
			logp = 1
		} else {
			logp = 1
		}
		tf[i] = tfChanged
	}
	return tf
}
