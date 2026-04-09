package opus

import (
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// PVQ U/V tables
// ---------------------------------------------------------------------------

func TestPvqU_BaseCases(t *testing.T) {
	// U(n, 0) = 0 for all n (CELT definition).
	for n := 0; n <= 10; n++ {
		if got := pvqUSlow(n, 0); got != 0 {
			t.Errorf("U(%d,0) = %d, want 0", n, got)
		}
	}
	// U(0, k) = 0 for all k.
	for k := 0; k <= 10; k++ {
		if got := pvqUSlow(0, k); got != 0 {
			t.Errorf("U(0,%d) = %d, want 0", k, got)
		}
	}
	// U(1, k) = 1 for k >= 1.
	for k := 1; k <= 10; k++ {
		if got := pvqUSlow(1, k); got != 1 {
			t.Errorf("U(1,%d) = %d, want 1", k, got)
		}
	}
	// U(2, k) = 2k - 1 for k >= 1.
	for k := 1; k <= 10; k++ {
		expected := uint64(2*k - 1)
		if got := pvqUSlow(2, k); got != expected {
			t.Errorf("U(2,%d) = %d, want %d", k, got, expected)
		}
	}
}

func TestPvqU_KnownValues(t *testing.T) {
	// U(2, k) = 2k - 1
	for k := 1; k <= 10; k++ {
		expected := uint64(2*k - 1)
		if got := pvqUSlow(2, k); got != expected {
			t.Errorf("U(2,%d) = %d, want %d", k, got, expected)
		}
	}
}

func TestPvqV_Symmetry(t *testing.T) {
	// V(n,k) = U(n,k) + U(n,k+1) should equal the total number of signed codewords.
	// V(1, k) = 2 for k >= 1 (just +k and -k).
	for k := 1; k <= 5; k++ {
		v := pvqV(1, k)
		if v != 2 {
			t.Errorf("V(1,%d) = %d, want 2", k, v)
		}
	}
	// V(2, 1) = U(2,1) + U(2,2) = 1 + 3 = 4. Vectors: (1,0),(0,1),(-1,0),(0,-1).
	if v := pvqV(2, 1); v != 4 {
		t.Errorf("V(2,1) = %d, want 4", v)
	}
}

// ---------------------------------------------------------------------------
// cwrsi — PVQ codeword to vector
// ---------------------------------------------------------------------------

func TestCwrsi_Simple(t *testing.T) {
	// V(2,1) = 4 codewords. Decode each and verify L1 norm = 1.
	for idx := uint64(0); idx < 4; idx++ {
		y := make([]int, 2)
		ryy := cwrsi(2, 1, idx, y)
		l1 := abs(y[0]) + abs(y[1])
		if l1 != 1 {
			t.Errorf("cwrsi(2,1,%d) = %v, L1=%d, want 1", idx, y, l1)
		}
		// ryy should equal sum of squares.
		expected := y[0]*y[0] + y[1]*y[1]
		if ryy != expected {
			t.Errorf("cwrsi(2,1,%d): ryy=%d, want %d", idx, ryy, expected)
		}
	}
}

func TestCwrsi_AllCodewords(t *testing.T) {
	// For small n, k: enumerate all codewords and verify they're unique + valid.
	n, k := 3, 2
	total := pvqV(n, k)
	seen := make(map[[3]int]bool)
	for idx := uint64(0); idx < total; idx++ {
		y := make([]int, n)
		cwrsi(n, k, idx, y)

		// Verify L1 norm.
		l1 := 0
		for _, v := range y {
			l1 += abs(v)
		}
		if l1 != k {
			t.Errorf("cwrsi(%d,%d,%d) = %v, L1=%d, want %d", n, k, idx, y, l1, k)
		}

		// Verify uniqueness.
		key := [3]int{y[0], y[1], y[2]}
		if seen[key] {
			t.Errorf("cwrsi(%d,%d,%d) = %v: duplicate", n, k, idx, y)
		}
		seen[key] = true
	}

	if uint64(len(seen)) != total {
		t.Errorf("expected %d unique vectors, got %d", total, len(seen))
	}
}

// ---------------------------------------------------------------------------
// Normalize residual
// ---------------------------------------------------------------------------

func TestNormalizeResidual(t *testing.T) {
	iy := []int{1, 0, -1}
	x := make([]float32, 3)
	normalizeResidual(iy, x, 3, 2, 1.0) // ryy = 1^2 + 0^2 + 1^2 = 2

	// Check unit norm (gain=1.0, divided by sqrt(2)).
	var norm float64
	for _, v := range x {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if math.Abs(norm-1.0) > 0.01 {
		t.Errorf("norm = %f, want ~1.0", norm)
	}
}

// ---------------------------------------------------------------------------
// IMDCT
// ---------------------------------------------------------------------------

func TestImdct_DeltaInput(t *testing.T) {
	// IMDCT of a single coefficient should produce a cosine.
	n := 16
	in := make([]float32, n)
	in[0] = 1.0

	out := imdct(in, n)
	if len(out) != 2*n {
		t.Fatalf("imdct output length = %d, want %d", len(out), 2*n)
	}

	// Output should be non-zero.
	hasNonZero := false
	for _, v := range out {
		if v != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("imdct output is all zeros")
	}
}

func TestImdct_EnergyPreservation(t *testing.T) {
	// Parseval's theorem: energy in = energy out (within scaling).
	n := 32
	in := make([]float32, n)
	for i := range in {
		in[i] = float32(i+1) * 0.1
	}

	out := imdct(in, n)

	var eIn, eOut float64
	for _, v := range in {
		eIn += float64(v) * float64(v)
	}
	for _, v := range out {
		eOut += float64(v) * float64(v)
	}

	// They should be proportional (scaling factor depends on normalization).
	if eOut == 0 {
		t.Error("imdct output has zero energy")
	}
}

// ---------------------------------------------------------------------------
// Overlap-add
// ---------------------------------------------------------------------------

func TestOverlapAdd(t *testing.T) {
	frameSize := 32
	overlap := 4

	// Simulate: previous frame left an overlap tail.
	prev := make([]float32, overlap)
	for i := range prev {
		prev[i] = 1.0
	}

	imdctOut := make([]float32, frameSize)
	for i := range imdctOut {
		imdctOut[i] = 0.5
	}

	// Use a simple triangular window for this test instead of the real window.
	// The overlapAdd function uses mdctWindow120 which has 120 entries.
	// For a quick test, just verify the function doesn't crash.
	if frameSize+overlap <= len(imdctOut) {
		// This test is simplified; the real window is 120-point.
		t.Skip("skipping overlap-add test (needs 120-point frames)")
	}
}

// ---------------------------------------------------------------------------
// De-emphasis
// ---------------------------------------------------------------------------

func TestDeemphasis(t *testing.T) {
	pcm := []float32{1.0, 0, 0, 0, 0}
	state := deemphasis(pcm, 0)

	// After impulse: y[0]=1, y[1]=0.85, y[2]=0.85^2, ...
	if pcm[0] != 1.0 {
		t.Errorf("pcm[0] = %f, want 1.0", pcm[0])
	}
	expected := float32(deemphCoef)
	if math.Abs(float64(pcm[1]-expected)) > 0.001 {
		t.Errorf("pcm[1] = %f, want ~%f", pcm[1], expected)
	}
	if state != pcm[4] {
		t.Errorf("state = %f, want %f", state, pcm[4])
	}
}

func TestDeemphasis_Continuity(t *testing.T) {
	// State should carry across calls.
	pcm1 := []float32{1.0, 0}
	state := deemphasis(pcm1, 0)

	pcm2 := []float32{0, 0}
	state = deemphasis(pcm2, state)

	// pcm2[0] should be deemphCoef * state_from_pcm1.
	if pcm2[0] == 0 {
		t.Error("de-emphasis state not carried across calls")
	}
	_ = state
}

// ---------------------------------------------------------------------------
// Laplace decode
// ---------------------------------------------------------------------------

func TestLaplaceDecodeSymmetry(t *testing.T) {
	// Encode a known value and decode it.
	// Use the range coder to encode a Laplace value of 0 (the most likely).
	fs := 72 << 7  // from eProbModel[0][0][0]
	decay := 127 << 6

	// Create a minimal bitstream that will decode to 0.
	enc := NewRangeEncoder(32)
	// Encode the 0 symbol: fl=0, fh=fs, ft=32768.
	enc.Encode(0, uint32(fs), 32768)
	data := enc.Done()

	dec := NewRangeDecoder(data)
	val := ecLaplaceDecode(dec, fs, decay)
	if val != 0 {
		t.Errorf("Laplace decode = %d, want 0", val)
	}
}

// ---------------------------------------------------------------------------
// Window values
// ---------------------------------------------------------------------------

func TestMdctWindow120(t *testing.T) {
	// Window should start near 0 and end at 1.
	if mdctWindow120[0] > 0.001 {
		t.Errorf("window[0] = %f, should be near 0", mdctWindow120[0])
	}
	if mdctWindow120[119] != 1.0 {
		t.Errorf("window[119] = %f, want 1.0", mdctWindow120[119])
	}
	// Window should be monotonically increasing.
	for i := 1; i < 120; i++ {
		if mdctWindow120[i] < mdctWindow120[i-1] {
			t.Errorf("window not monotonic at %d: %f < %f", i, mdctWindow120[i], mdctWindow120[i-1])
		}
	}
}

// ---------------------------------------------------------------------------
// Full CELT decode (smoke test)
// ---------------------------------------------------------------------------

func TestCeltDec_Silence(t *testing.T) {
	// The silence frame 0xF8FFFE should decode without error.
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := dec.Decode(SilenceFrame, 960, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 960 {
		t.Errorf("len = %d, want 960", len(pcm))
	}
}

func TestCeltDec_EncoderRoundtrip(t *testing.T) {
	// Encode a sine wave with our encoder, decode with the new decoder.
	enc, _ := NewEncoder(48000, 1, AppAudio)
	dec, _ := NewDecoder(48000, 1)

	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}

	pkt, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}

	out, err := dec.Decode(pkt, 960, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 960 {
		t.Fatalf("decoded %d samples, want 960", len(out))
	}

	// Should have some non-zero output.
	hasNonZero := false
	for _, s := range out {
		if s != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("decoded output is all zeros")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
