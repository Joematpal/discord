package dave

import "testing"

func TestULEB128_Roundtrip(t *testing.T) {
	values := []uint32{0, 1, 127, 128, 255, 256, 16383, 16384, 1<<24 - 1, 1 << 24, 0xFFFFFFFF}
	for _, v := range values {
		encoded := EncodeULEB128(v)
		decoded, n, err := DecodeULEB128(encoded)
		if err != nil {
			t.Fatalf("value %d: encode=%v, decode error: %v", v, encoded, err)
		}
		if n != len(encoded) {
			t.Fatalf("value %d: consumed %d, encoded %d bytes", v, n, len(encoded))
		}
		if decoded != v {
			t.Fatalf("value %d: roundtrip got %d", v, decoded)
		}
	}
}

func TestULEB128_KnownEncodings(t *testing.T) {
	tests := []struct {
		value uint32
		bytes []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7F}},
		{128, []byte{0x80, 0x01}},
		{0x3FFF, []byte{0xFF, 0x7F}},
		{0x4000, []byte{0x80, 0x80, 0x01}},
	}
	for _, tt := range tests {
		got := EncodeULEB128(tt.value)
		if len(got) != len(tt.bytes) {
			t.Errorf("value %d: got %v, want %v", tt.value, got, tt.bytes)
			continue
		}
		for i := range got {
			if got[i] != tt.bytes[i] {
				t.Errorf("value %d: byte %d = 0x%02X, want 0x%02X", tt.value, i, got[i], tt.bytes[i])
			}
		}
	}
}

func TestDecodeULEB128_Overflow(t *testing.T) {
	// 6 continuation bytes would overflow uint32.
	data := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
	_, _, err := DecodeULEB128(data)
	if err != ErrULEB128Overflow {
		t.Errorf("got %v", err)
	}
}

func TestDecodeULEB128_Empty(t *testing.T) {
	_, _, err := DecodeULEB128(nil)
	if err != ErrULEB128Overflow {
		t.Errorf("got %v", err)
	}
}

func TestDecodeULEB128_Unterminated(t *testing.T) {
	// Only continuation bytes, no terminator.
	_, _, err := DecodeULEB128([]byte{0x80, 0x80})
	if err != ErrULEB128Overflow {
		t.Errorf("got %v", err)
	}
}
