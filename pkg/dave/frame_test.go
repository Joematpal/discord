package dave

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestIsProtocolFrame(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		key := make([]byte, 16)
		rand.Read(key)
		fc, _ := NewFrameCryptor(key, CodecOpus)
		frame := []byte{0xF8, 0xFF, 0xFE}
		encrypted, _ := fc.Encrypt(frame)
		if !IsProtocolFrame(encrypted) {
			t.Error("should be detected as protocol frame")
		}
	})

	t.Run("too short", func(t *testing.T) {
		if IsProtocolFrame([]byte{0xFA, 0xFA}) {
			t.Error("too short should fail")
		}
	})

	t.Run("wrong magic", func(t *testing.T) {
		data := make([]byte, 20)
		data[18] = 0x00
		data[19] = 0x00
		if IsProtocolFrame(data) {
			t.Error("wrong magic should fail")
		}
	})
}

func TestFrameCryptor_OpusRoundtrip(t *testing.T) {
	key := make([]byte, 16)
	rand.Read(key)

	encryptor, err := NewFrameCryptor(key, CodecOpus)
	if err != nil {
		t.Fatal(err)
	}
	decryptor, err := NewFrameCryptor(key, CodecOpus)
	if err != nil {
		t.Fatal(err)
	}

	original := []byte{0xF8, 0xFF, 0xFE}
	encrypted, err := encryptor.Encrypt(original)
	if err != nil {
		t.Fatal(err)
	}

	// Encrypted should be different from original.
	if bytes.Equal(encrypted, original) {
		t.Error("encrypted should differ from original")
	}

	// Should have magic marker.
	if encrypted[len(encrypted)-2] != 0xFA || encrypted[len(encrypted)-1] != 0xFA {
		t.Error("missing magic marker")
	}

	decrypted, err := decryptor.Decrypt(0, encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, original) {
		t.Errorf("decrypted = %v, want %v", decrypted, original)
	}
}

func TestFrameCryptor_LargeFrame(t *testing.T) {
	key := make([]byte, 16)
	rand.Read(key)

	enc, _ := NewFrameCryptor(key, CodecOpus)
	dec, _ := NewFrameCryptor(key, CodecOpus)

	original := make([]byte, 500)
	for i := range original {
		original[i] = byte(i)
	}

	encrypted, err := enc.Encrypt(original)
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := dec.Decrypt(0, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, original) {
		t.Error("large frame roundtrip failed")
	}
}

func TestFrameCryptor_MultipleFrames(t *testing.T) {
	key := make([]byte, 16)
	rand.Read(key)

	enc, _ := NewFrameCryptor(key, CodecOpus)
	dec, _ := NewFrameCryptor(key, CodecOpus)

	for i := 0; i < 10; i++ {
		frame := []byte{byte(i), byte(i + 1), byte(i + 2)}
		encrypted, err := enc.Encrypt(frame)
		if err != nil {
			t.Fatalf("frame %d: encrypt: %v", i, err)
		}
		decrypted, err := dec.Decrypt(0, encrypted)
		if err != nil {
			t.Fatalf("frame %d: decrypt: %v", i, err)
		}
		if !bytes.Equal(decrypted, frame) {
			t.Fatalf("frame %d: roundtrip failed", i)
		}
	}
}

func TestFrameCryptor_WrongKey(t *testing.T) {
	key1 := make([]byte, 16)
	key2 := make([]byte, 16)
	rand.Read(key1)
	rand.Read(key2)

	enc, _ := NewFrameCryptor(key1, CodecOpus)
	dec, _ := NewFrameCryptor(key2, CodecOpus)

	encrypted, _ := enc.Encrypt([]byte{0x01, 0x02, 0x03})
	_, err := dec.Decrypt(0, encrypted)
	if err != ErrDecryptFailed {
		t.Errorf("expected ErrDecryptFailed, got %v", err)
	}
}

func TestUnencryptedRangesForCodec(t *testing.T) {
	t.Run("opus", func(t *testing.T) {
		ranges := UnencryptedRangesForCodec(CodecOpus, []byte{0x01, 0x02})
		if ranges != nil {
			t.Errorf("Opus should have no unencrypted ranges, got %v", ranges)
		}
	})
	t.Run("vp8 keyframe", func(t *testing.T) {
		frame := make([]byte, 20)
		frame[0] = 0x00 // P=0 → keyframe
		ranges := UnencryptedRangesForCodec(CodecVP8, frame)
		if len(ranges) != 1 || ranges[0].Offset != 0 || ranges[0].Size != 10 {
			t.Errorf("VP8 keyframe: %v", ranges)
		}
	})
	t.Run("vp8 interframe", func(t *testing.T) {
		frame := []byte{0x01} // P=1 → interframe
		ranges := UnencryptedRangesForCodec(CodecVP8, frame)
		if len(ranges) != 1 || ranges[0].Size != 1 {
			t.Errorf("VP8 interframe: %v", ranges)
		}
	})
	t.Run("vp9", func(t *testing.T) {
		ranges := UnencryptedRangesForCodec(CodecVP9, []byte{0x01})
		if ranges != nil {
			t.Error("VP9 should have no unencrypted ranges")
		}
	})
}

func TestBuildParseProtocolFrame(t *testing.T) {
	tag := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	interleaved := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	nonce := uint32(42)
	ranges := []UnencryptedRange{{Offset: 0, Size: 1}}

	built := BuildProtocolFrame(interleaved, tag, nonce, ranges)

	if !IsProtocolFrame(built) {
		t.Fatal("built frame should pass protocol check")
	}

	pf, err := ParseProtocolFrame(built)
	if err != nil {
		t.Fatal(err)
	}
	if pf.Nonce != 42 {
		t.Errorf("Nonce = %d", pf.Nonce)
	}
	if !bytes.Equal(pf.Tag, tag) {
		t.Errorf("Tag = %v", pf.Tag)
	}
	if !bytes.Equal(pf.InterleavedFrame, interleaved) {
		t.Errorf("InterleavedFrame = %v", pf.InterleavedFrame)
	}
	if len(pf.UnencryptedRanges) != 1 {
		t.Fatalf("ranges = %d", len(pf.UnencryptedRanges))
	}
	if pf.UnencryptedRanges[0].Offset != 0 || pf.UnencryptedRanges[0].Size != 1 {
		t.Errorf("range = %v", pf.UnencryptedRanges[0])
	}
}

func TestParseProtocolFrame_NotProtocol(t *testing.T) {
	_, err := ParseProtocolFrame([]byte{0x01, 0x02, 0x03})
	if err != ErrNotProtocolFrame {
		t.Errorf("got %v", err)
	}
}

func TestExpandNonce(t *testing.T) {
	n := expandNonce(0x12345678)
	if len(n) != 12 {
		t.Fatalf("len = %d", len(n))
	}
	// First 8 bytes should be zero.
	for i := 0; i < 8; i++ {
		if n[i] != 0 {
			t.Errorf("byte %d = 0x%02X, want 0", i, n[i])
		}
	}
	if n[8] != 0x12 || n[9] != 0x34 || n[10] != 0x56 || n[11] != 0x78 {
		t.Errorf("nonce = %v", n[8:])
	}
}
