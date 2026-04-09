package opus

import "math"

// PVQ (Pyramid Vector Quantization) decoder — RFC 6716 Section 4.3.7.
// Decodes a codeword index into a unit-norm vector of dimension N with K pulses.

// pvqU computes the number of unsigned PVQ codewords U(n, k).
// U(n,k) = number of vectors in Z^n with L1 norm exactly k and last component >= 0.
// Recurrence: U(n,k) = U(n-1,k) + U(n,k-1) + U(n-1,k-1)
func pvqU(n, k int) uint64 {
	if n <= 0 || k <= 0 {
		return 0
	}
	if k == 1 {
		return uint64(n)
	}
	if n == 1 {
		return 1
	}
	// Use dynamic programming row by row.
	// u[j] = U(current_row, j)
	row := make([]uint64, k+2)
	for j := 1; j <= k+1; j++ {
		row[j] = uint64(2*j - 1) // U(2, j) = 2j-1
	}
	for i := 3; i <= n; i++ {
		// Sweep in-place from right to left.
		for j := k + 1; j >= 1; j-- {
			row[j] = row[j] + row[j-1] + row[j-1]
			// Actually: U(i,j) = U(i-1,j) + U(i,j-1) + U(i-1,j-1)
			// But sweeping from right: row[j] currently holds U(i-1,j),
			// row[j-1] was already updated to U(i,j-1).
			// This needs to be done more carefully.
		}
	}
	// The simple DP above doesn't work correctly for the recurrence.
	// Use the proper approach with two rows.
	return pvqUSlow(n, k)
}

// pvqUSlow computes U(n,k) for the CELT PVQ enumeration.
// U(n,k) counts vectors where the first nonzero element is positive.
// Base cases: U(0,k)=0, U(n,0)=0, U(2,k)=2k-1 for k>=1.
// Recurrence: U(n,k) = U(n-1,k) + U(n,k-1) + U(n-1,k-1)
func pvqUSlow(n, k int) uint64 {
	if n <= 0 || k <= 0 {
		return 0
	}
	if n == 1 {
		return 1
	}

	// prev[j] = U(i-1=1, j) for the first row (n=1).
	prev := make([]uint64, k+2)
	curr := make([]uint64, k+2)
	for j := 1; j <= k+1; j++ {
		prev[j] = 1 // U(1, k) = 1 for k >= 1
	}

	// Build rows from n=2 upward.
	for i := 2; i <= n; i++ {
		curr[0] = 0
		for j := 1; j <= k+1; j++ {
			// U(i,j) = U(i-1,j) + U(i,j-1) + U(i-1,j-1)
			curr[j] = prev[j] + curr[j-1] + prev[j-1]
		}
		prev, curr = curr, prev
	}
	return prev[k]
}

// pvqV computes the total number of signed PVQ codewords V(n, k).
// V(n,k) = U(n,k) + U(n,k+1)
func pvqV(n, k int) uint64 {
	return pvqUSlow(n, k) + pvqUSlow(n, k+1)
}

// cwrsi decodes a PVQ codeword index into a pulse vector y[].
// Returns the sum of squares (Ryy).
// n = dimension, k = number of pulses, i = codeword index.
func cwrsi(n, k int, idx uint64, y []int) int {
	ryy := 0

	for n > 2 {
		// Determine sign: if idx >= U(n, k+1), the value is negative.
		p := pvqUSlow(n, k+1)
		s := 0
		if idx >= p {
			s = -1
			idx -= p
		}

		// Find the pulse count for this dimension.
		// Search for the largest k' such that U(n, k') <= idx.
		k0 := k
		for k > 0 && pvqUSlow(n, k) > idx {
			k--
		}
		idx -= pvqUSlow(n, k)

		// Output value with sign.
		val := (k0 - k + s) ^ s // XOR with s=-1 negates
		y[len(y)-n] = val
		ryy += val * val
		n--
	}

	// n == 2 case.
	{
		p := uint64(2*k + 1)
		s := 0
		if idx >= p {
			s = -1
			idx -= p
		}
		k0 := k
		k = int((idx + 1) >> 1)
		if k > 0 {
			idx -= uint64(2*k - 1)
		}
		val := (k0 - k + s) ^ s
		y[len(y)-2] = val
		ryy += val * val
	}

	// n == 1 case (last element).
	{
		s := 0
		if idx > 0 {
			s = -1
		}
		val := (k + s) ^ s
		y[len(y)-1] = val
		ryy += val * val
	}

	return ryy
}

// decodePulses decodes a PVQ vector from the range coder.
func decodePulses(rc *RangeDecoder, n, k int) ([]int, int) {
	y := make([]int, n)
	if k == 0 {
		return y, 0
	}
	total := pvqV(n, k)
	if total <= 1 {
		return y, 0
	}
	idx := uint64(rc.Uint(uint32(total)))
	ryy := cwrsi(n, k, idx, y)
	return y, ryy
}

// normalizeResidual converts integer pulse vector to unit-norm float vector.
func normalizeResidual(iy []int, x []float32, n int, ryy int, gain float32) {
	if ryy == 0 {
		for i := 0; i < n; i++ {
			x[i] = 0
		}
		return
	}
	g := gain / float32(math.Sqrt(float64(ryy)))
	for i := 0; i < n; i++ {
		x[i] = float32(iy[i]) * g
	}
}

// expRotation applies the spreading rotation to a PVQ vector.
// RFC 6716 Section 4.3.7.3.
func expRotation(x []float32, n, spread, k int) {
	if spread == spreadNone || 2*k >= n {
		return
	}

	factor := spreadFactor[spread-1]
	gain := float64(n) / float64(n+factor*k)
	theta := 0.5 * gain * gain
	c := float32(math.Cos(theta * math.Pi / 2))
	s := float32(math.Sin(theta * math.Pi / 2))

	// Apply Givens rotations forward then backward.
	// Forward pass.
	for i := n - 2; i >= 0; i-- {
		x0 := x[i]
		x1 := x[i+1]
		x[i] = c*x0 - s*x1
		x[i+1] = s*x0 + c*x1
	}
	// Backward pass.
	for i := 0; i < n-1; i++ {
		x0 := x[i]
		x1 := x[i+1]
		x[i] = c*x0 + s*x1
		x[i+1] = -s*x0 + c*x1
	}
}

// algUnquant performs full PVQ decode for a band:
// decode pulses → normalize → rotate.
func algUnquant(rc *RangeDecoder, n, k, spread int, gain float32) []float32 {
	x := make([]float32, n)
	if k == 0 {
		return x
	}
	iy, ryy := decodePulses(rc, n, k)
	normalizeResidual(iy, x, n, ryy, gain)
	expRotation(x, n, spread, k)
	return x
}
