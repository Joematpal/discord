package opus

// CELT energy decoding — Laplace-coded coarse energy + uniform fine energy.
// RFC 6716 Section 4.3.4 and 4.3.5.

const (
	laplaceMinP  = 1
	laplaceNMin  = 16
)

// ecLaplaceGetFreq1 computes the probability of the value ±1.
func ecLaplaceGetFreq1(fs0, decay int) int {
	ft := 32768 - laplaceMinP*(2*laplaceNMin) - fs0
	return (ft * (16384 - decay)) >> 15
}

// ecLaplaceDecode decodes a Laplace-distributed value from the range coder.
func ecLaplaceDecode(rc *RangeDecoder, fs, decay int) int {
	fm := rc.DecodeBin(15)
	fl := uint32(0)
	val := 0

	fsu := uint32(fs)
	if fm >= fsu {
		val = 1
		fl = fsu
		fsu = uint32(ecLaplaceGetFreq1(fs, decay) + laplaceMinP)

		for fsu > laplaceMinP && fm >= fl+2*fsu {
			fsu *= 2
			fl += fsu
			fsu = uint32(((int(fsu) - 2*laplaceMinP) * decay) >> 15)
			fsu += laplaceMinP
			val++
		}

		if fsu <= laplaceMinP {
			di := (fm - fl) >> 1
			val += int(di)
			fl += 2 * di * laplaceMinP
		}

		if fm < fl+fsu {
			val = -val
		} else {
			fl += fsu
		}
	}

	rc.Update(fl, min32(fl+fsu, 32768), 32768)
	return val
}

// decodeCoarseEnergy decodes coarse log-energy per band using Laplace coding.
// RFC 6716 Section 4.3.4.
func decodeCoarseEnergy(rc *RangeDecoder, nbBands, lm, channels int, intra bool,
	oldBandE []float32, prevLogE []float32) {

	var alpha, beta float32
	intraIdx := 0
	if intra {
		alpha = 0
		beta = betaIntra
		intraIdx = 1
	} else {
		alpha = predCoef[lm]
		beta = betaCoef[lm]
	}

	budget := rc.Tell()
	_ = budget

	for c := 0; c < channels; c++ {
		prev := float32(0)
		for i := 0; i < nbBands; i++ {
			var qi int
			available := int(rc.storage)*8 - rc.Tell()
			if available >= 15 {
				pi := 2 * i
				if pi > 40 {
					pi = 40
				}
				fs := eProbModel[lm][intraIdx][pi] << 7
				dc := eProbModel[lm][intraIdx][pi+1] << 6
				qi = ecLaplaceDecode(rc, fs, dc)
			} else if available >= 2 {
				qi = int(rc.Decode(3))
				rc.Update(uint32(qi), uint32(qi+1), 3)
				qi = (qi >> 1) ^ -(qi & 1) // zigzag decode
			} else if available >= 1 {
				qi = -int(rc.BitLogP(1))
			} else {
				qi = -1
			}

			q := float32(qi)
			idx := c*nbBands + i
			if oldBandE[idx] < -28.0 {
				oldBandE[idx] = -28.0
			}
			tmp := alpha*oldBandE[idx] + prev + q
			if tmp < -28.0 {
				tmp = -28.0
			} else if tmp > 28.0 {
				tmp = 28.0
			}
			oldBandE[idx] = tmp
			prev = prev + q - beta*q
		}
	}
}

// decodeFineEnergy decodes fine energy quantization.
// RFC 6716 Section 4.3.5.
func decodeFineEnergy(rc *RangeDecoder, nbBands, channels int,
	oldBandE []float32, fineQuant []int) {

	for i := 0; i < nbBands; i++ {
		if fineQuant[i] <= 0 {
			continue
		}
		for c := 0; c < channels; c++ {
			q2 := int(rc.Bits(uint(fineQuant[i])))
			offset := (float32(q2) + 0.5) * (1.0 / float32(int(1)<<uint(fineQuant[i]))) - 0.5
			oldBandE[c*nbBands+i] += offset
		}
	}
}

// decodeFineEnergyFinal uses remaining bits for fine energy refinement.
func decodeFineEnergyFinal(rc *RangeDecoder, nbBands, channels int,
	oldBandE []float32, fineQuant, finePriority []int, bitsLeft int) {

	for prio := 0; prio < 2; prio++ {
		for i := 0; i < nbBands; i++ {
			if fineQuant[i] >= maxFineBits || finePriority[i] != prio {
				continue
			}
			for c := 0; c < channels; c++ {
				if bitsLeft <= 0 {
					return
				}
				q2 := int(rc.Bits(1))
				offset := (float32(q2) - 0.5) / float32(int(1)<<uint(fineQuant[i]+1))
				oldBandE[c*nbBands+i] += offset
				bitsLeft--
			}
		}
	}
}
