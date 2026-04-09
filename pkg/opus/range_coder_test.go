package opus

import "testing"

func TestRangeCoderSymbolRoundtrip(t *testing.T) {
	// Encode several symbols with different CDF distributions, then decode.
	type sym struct {
		fl, fh, ft uint32
	}
	symbols := []sym{
		{0, 3, 10},
		{3, 7, 10},
		{7, 10, 10},
		{0, 1, 2},
		{1, 2, 2},
		{0, 1, 256},
		{128, 129, 256},
		{255, 256, 256},
		{0, 100, 1000},
		{500, 600, 1000},
		{999, 1000, 1000},
	}

	enc := NewRangeEncoder(256)
	for _, s := range symbols {
		enc.Encode(s.fl, s.fh, s.ft)
	}
	data := enc.Done()
	if enc.Error {
		t.Fatal("encoder error")
	}

	dec := NewRangeDecoder(data)
	for i, s := range symbols {
		fs := dec.Decode(s.ft)
		if fs < s.fl || fs >= s.fh {
			t.Fatalf("symbol %d: Decode(%d) = %d, want in [%d, %d)", i, s.ft, fs, s.fl, s.fh)
		}
		dec.Update(s.fl, s.fh, s.ft)
	}
	if dec.Error {
		t.Fatal("decoder error")
	}
}

func TestRangeCoderBitLogPRoundtrip(t *testing.T) {
	bitValues := []int{0, 1, 1, 0, 0, 0, 1, 0, 1, 1}
	logps := []uint{1, 1, 2, 3, 4, 8, 1, 2, 3, 4}

	enc := NewRangeEncoder(256)
	for i, v := range bitValues {
		enc.BitLogP(v, logps[i])
	}
	data := enc.Done()
	if enc.Error {
		t.Fatal("encoder error")
	}

	dec := NewRangeDecoder(data)
	for i, want := range bitValues {
		got := dec.BitLogP(logps[i])
		if got != want {
			t.Fatalf("bit %d: got %d, want %d (logp=%d)", i, got, want, logps[i])
		}
	}
}

func TestRangeCoderUintRoundtrip(t *testing.T) {
	type tc struct {
		val, ft uint32
	}
	cases := []tc{
		{0, 2},
		{1, 2},
		{0, 100},
		{50, 100},
		{99, 100},
		{0, 1000},
		{500, 1000},
		{999, 1000},
		{0, 65536},
		{32768, 65536},
		{65535, 65536},
	}

	enc := NewRangeEncoder(256)
	for _, c := range cases {
		enc.Uint(c.val, c.ft)
	}
	data := enc.Done()
	if enc.Error {
		t.Fatal("encoder error")
	}

	dec := NewRangeDecoder(data)
	for i, c := range cases {
		got := dec.Uint(c.ft)
		if got != c.val {
			t.Fatalf("uint %d: got %d, want %d (ft=%d)", i, got, c.val, c.ft)
		}
	}
}

func TestRangeCoderBitsRoundtrip(t *testing.T) {
	type tc struct {
		val uint32
		n   uint
	}
	cases := []tc{
		{0, 1},
		{1, 1},
		{0x0F, 4},
		{0xFF, 8},
		{0xABCD, 16},
	}

	enc := NewRangeEncoder(256)
	for _, c := range cases {
		enc.Bits(c.val, c.n)
	}
	data := enc.Done()
	if enc.Error {
		t.Fatal("encoder error")
	}

	dec := NewRangeDecoder(data)
	for i, c := range cases {
		got := dec.Bits(c.n)
		if got != c.val {
			t.Fatalf("bits %d: got 0x%X, want 0x%X (n=%d)", i, got, c.val, c.n)
		}
	}
}

func TestRangeCoderMixedRoundtrip(t *testing.T) {
	// Mix range-coded symbols, logp bits, uints, and raw bits.
	enc := NewRangeEncoder(256)
	enc.Encode(2, 5, 10)
	enc.BitLogP(1, 3)
	enc.Uint(42, 100)
	enc.Bits(0xBE, 8)
	enc.Encode(0, 1, 4)
	enc.BitLogP(0, 1)
	enc.Uint(7, 8)
	enc.Bits(0x5, 3)
	data := enc.Done()
	if enc.Error {
		t.Fatal("encoder error")
	}

	dec := NewRangeDecoder(data)

	fs := dec.Decode(10)
	if fs < 2 || fs >= 5 {
		t.Fatalf("sym0: got %d, want in [2,5)", fs)
	}
	dec.Update(2, 5, 10)

	if got := dec.BitLogP(3); got != 1 {
		t.Fatalf("bit0: got %d, want 1", got)
	}

	if got := dec.Uint(100); got != 42 {
		t.Fatalf("uint0: got %d, want 42", got)
	}

	if got := dec.Bits(8); got != 0xBE {
		t.Fatalf("bits0: got 0x%X, want 0xBE", got)
	}

	fs = dec.Decode(4)
	if fs != 0 {
		t.Fatalf("sym1: got %d, want 0", fs)
	}
	dec.Update(0, 1, 4)

	if got := dec.BitLogP(1); got != 0 {
		t.Fatalf("bit1: got %d, want 0", got)
	}

	if got := dec.Uint(8); got != 7 {
		t.Fatalf("uint1: got %d, want 7", got)
	}

	if got := dec.Bits(3); got != 0x5 {
		t.Fatalf("bits1: got 0x%X, want 0x5", got)
	}

	if dec.Error {
		t.Fatal("decoder error")
	}
}

func TestRangeCoderDecodeBinRoundtrip(t *testing.T) {
	type sym struct {
		fl, fh uint32
		bits   uint
	}
	symbols := []sym{
		{0, 64, 8},   // [0,64) out of 256
		{64, 128, 8}, // [64,128) out of 256
		{0, 1, 1},    // [0,1) out of 2
		{1, 2, 1},    // [1,2) out of 2
		{0, 8, 4},    // [0,8) out of 16
	}

	enc := NewRangeEncoder(256)
	for _, s := range symbols {
		enc.EncodeBin(s.fl, s.fh, s.bits)
	}
	data := enc.Done()
	if enc.Error {
		t.Fatal("encoder error")
	}

	dec := NewRangeDecoder(data)
	for i, s := range symbols {
		fs := dec.DecodeBin(s.bits)
		if fs < s.fl || fs >= s.fh {
			t.Fatalf("sym %d: DecodeBin(%d) = %d, want in [%d, %d)", i, s.bits, fs, s.fl, s.fh)
		}
		dec.Update(s.fl, s.fh, 1<<s.bits)
	}
}

func TestRangeCoderTell(t *testing.T) {
	enc := NewRangeEncoder(256)
	startBits := enc.Tell()
	enc.Encode(0, 1, 2) // ~1 bit
	afterOne := enc.Tell()
	if afterOne <= startBits {
		t.Errorf("Tell didn't advance: before=%d, after=%d", startBits, afterOne)
	}
}
