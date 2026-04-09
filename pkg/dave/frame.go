package dave

import (
	"crypto/cipher"
	"errors"
	"fmt"
)

// Protocol constants.
const (
	MagicMarker       = 0xFAFA
	TruncatedTagSize  = 8         // AES-GCM tag truncated to 64 bits
	FullGCMTagSize    = 16
	NonceSize         = 12        // AES-GCM nonce (96 bits)
	MinSupplementSize = TruncatedTagSize + 1 + 1 + 2 // tag + nonce(min 1) + suppl_size + magic
)

var (
	ErrNotProtocolFrame = errors.New("dave: not a protocol frame")
	ErrBadMagic         = errors.New("dave: invalid magic marker")
	ErrBadSupplSize     = errors.New("dave: invalid supplemental data size")
	ErrDecryptFailed    = errors.New("dave: decryption failed")
	ErrBadRanges        = errors.New("dave: invalid unencrypted ranges")
)

// UnencryptedRange represents a byte range within the media frame that is
// left unencrypted (for codec packetizer/depacketizer compatibility).
type UnencryptedRange struct {
	Offset uint32
	Size   uint32
}

// ProtocolFrame is a parsed DAVE protocol frame.
type ProtocolFrame struct {
	InterleavedFrame  []byte             // combined encrypted + unencrypted media data
	Tag               []byte             // 8-byte truncated AES-GCM auth tag
	Nonce             uint32             // truncated 32-bit synchronization nonce
	UnencryptedRanges []UnencryptedRange // ranges of plaintext within the interleaved frame
	SupplementalSize  uint8              // size of supplemental data section
}

// IsProtocolFrame performs the protocol frame check per the DAVE spec:
// checks magic marker, supplemental size, nonce, and range validity.
func IsProtocolFrame(data []byte) bool {
	if len(data) < MinSupplementSize {
		return false
	}
	// Check magic marker (last 2 bytes).
	if data[len(data)-2] != 0xFA || data[len(data)-1] != 0xFA {
		return false
	}
	// Check supplemental size byte.
	supplSize := int(data[len(data)-3])
	if supplSize < MinSupplementSize-1 || supplSize >= len(data) {
		return false
	}
	return true
}

// ParseProtocolFrame parses a DAVE protocol frame from a reconstructed
// media frame (after depacketization).
func ParseProtocolFrame(data []byte) (*ProtocolFrame, error) {
	if !IsProtocolFrame(data) {
		return nil, ErrNotProtocolFrame
	}

	// Magic is last 2 bytes.
	// Supplemental size is 1 byte before magic.
	supplSize := int(data[len(data)-3])

	// Supplemental data starts at: len(data) - 2 (magic) - 1 (suppl size) - (supplSize - 3)
	// Actually: supplemental data = tag + nonce + ranges + suppl_size + magic
	// suppl_size counts everything but the interleaved frame.
	// Position of tag: end of interleaved frame.
	supplStart := len(data) - 2 - 1 // before magic and suppl_size byte
	// supplSize includes: tag(8) + nonce(var) + ranges(var) + suppl_size(1) + magic(2)
	// So interleaved frame ends at: len(data) - supplSize - 2
	// Wait, re-reading the spec:
	// suppl_size "includes everything but the interleaved frame"
	// i.e. tag + nonce + ranges + this_byte + magic
	interleavedEnd := len(data) - int(supplSize) - 2
	// The -2 is because magic is after suppl_size in the layout, and suppl_size
	// field itself says it includes the magic marker.

	// Let me re-derive from the spec layout:
	// [interleaved_frame | tag(8) | nonce(var) | ranges(var) | suppl_size(1) | magic(2)]
	// suppl_size = 8 + len(nonce) + len(ranges) + 1 + 2
	// total = len(interleaved_frame) + suppl_size
	// so interleaved_frame ends at: len(data) - suppl_size

	_ = supplStart
	interleavedEnd = len(data) - supplSize

	if interleavedEnd < 0 {
		return nil, ErrBadSupplSize
	}

	pf := &ProtocolFrame{
		SupplementalSize: uint8(supplSize),
	}
	pf.InterleavedFrame = data[:interleavedEnd]

	// Parse supplemental data after the interleaved frame.
	suppl := data[interleavedEnd:]
	pos := 0

	// Tag: 8 bytes.
	if pos+TruncatedTagSize > len(suppl) {
		return nil, ErrBadSupplSize
	}
	pf.Tag = suppl[pos : pos+TruncatedTagSize]
	pos += TruncatedTagSize

	// Nonce: ULEB128.
	nonce, n, err := DecodeULEB128(suppl[pos:])
	if err != nil {
		return nil, fmt.Errorf("dave: parse nonce: %w", err)
	}
	pf.Nonce = nonce
	pos += n

	// Unencrypted ranges: pairs of ULEB128 (offset, size) until we hit suppl_size byte.
	// Remaining bytes before suppl_size(1) + magic(2) are range pairs.
	rangeEnd := len(suppl) - 3 // before suppl_size + magic
	for pos < rangeEnd {
		offset, n1, err := DecodeULEB128(suppl[pos:])
		if err != nil {
			return nil, ErrBadRanges
		}
		pos += n1
		if pos >= rangeEnd {
			return nil, ErrBadRanges
		}
		size, n2, err := DecodeULEB128(suppl[pos:])
		if err != nil {
			return nil, ErrBadRanges
		}
		pos += n2
		pf.UnencryptedRanges = append(pf.UnencryptedRanges, UnencryptedRange{Offset: offset, Size: size})
	}

	return pf, nil
}

// BuildProtocolFrame constructs a DAVE protocol frame from components.
func BuildProtocolFrame(interleavedFrame, tag []byte, nonce uint32, ranges []UnencryptedRange) []byte {
	var suppl []byte

	// Tag.
	suppl = append(suppl, tag[:TruncatedTagSize]...)

	// Nonce (ULEB128).
	suppl = append(suppl, EncodeULEB128(nonce)...)

	// Unencrypted ranges (ULEB128 pairs).
	for _, r := range ranges {
		suppl = append(suppl, EncodeULEB128(r.Offset)...)
		suppl = append(suppl, EncodeULEB128(r.Size)...)
	}

	// Supplemental size: suppl so far + 1 (this byte) + 2 (magic).
	supplSize := len(suppl) + 1 + 2
	suppl = append(suppl, byte(supplSize))

	// Magic marker.
	suppl = append(suppl, 0xFA, 0xFA)

	// Combine.
	out := make([]byte, len(interleavedFrame)+len(suppl))
	copy(out, interleavedFrame)
	copy(out[len(interleavedFrame):], suppl)
	return out
}

// ---------------------------------------------------------------------------
// Frame Encryptor / Decryptor
// ---------------------------------------------------------------------------

// Codec identifies the media codec for encryption handling.
type Codec int

const (
	CodecOpus Codec = iota
	CodecVP8
	CodecVP9
	CodecH264
	CodecAV1
)

// UnencryptedRangesForCodec returns the ranges that must stay unencrypted
// for the given codec and frame data. For Opus, all bytes are encrypted.
func UnencryptedRangesForCodec(codec Codec, frame []byte) []UnencryptedRange {
	switch codec {
	case CodecOpus:
		// All Opus frames are fully encrypted.
		return nil
	case CodecVP8:
		if len(frame) == 0 {
			return nil
		}
		// P flag is bit 0 of first byte; P=0 means keyframe → 10 bytes unencrypted.
		if frame[0]&0x01 == 0 {
			size := uint32(10)
			if uint32(len(frame)) < size {
				size = uint32(len(frame))
			}
			return []UnencryptedRange{{Offset: 0, Size: size}}
		}
		return []UnencryptedRange{{Offset: 0, Size: 1}}
	case CodecVP9:
		// All VP9 frames fully encrypted.
		return nil
	default:
		// Unknown codec: encrypt everything.
		return nil
	}
}

// FrameCryptor handles DAVE frame encryption and decryption.
// It holds the AES-GCM cipher derived from the sender's media key.
type FrameCryptor struct {
	gcm   cipher.AEAD
	nonce uint32
	codec Codec
}

// NewFrameCryptor creates a frame cryptor from a 16-byte AES-128 key.
// Uses AES-GCM with 8-byte truncated tags per the DAVE protocol spec.
func NewFrameCryptor(key []byte, codec Codec) (*FrameCryptor, error) {
	gcm, err := newTruncatedGCM(key, TruncatedTagSize)
	if err != nil {
		return nil, err
	}
	return &FrameCryptor{gcm: gcm, codec: codec}, nil
}

// Encrypt encrypts a media frame per the DAVE protocol.
func (fc *FrameCryptor) Encrypt(frame []byte) ([]byte, error) {
	ranges := UnencryptedRangesForCodec(fc.codec, frame)
	plaintext, aad := splitFrame(frame, ranges)

	nonce := fc.nonce
	fc.nonce++

	fullNonce := expandNonce(nonce)
	// Seal appends the 8-byte tag (since we used NewGCMWithTagSize).
	sealed := fc.gcm.Seal(nil, fullNonce, plaintext, aad)

	// sealed = ciphertext(len(plaintext)) + tag(8)
	encrypted := sealed[:len(sealed)-TruncatedTagSize]
	tag := sealed[len(sealed)-TruncatedTagSize:]

	// Rebuild interleaved frame: unencrypted ranges stay, encrypted ranges replaced.
	interleaved := interleaveFrame(encrypted, frame, ranges)

	return BuildProtocolFrame(interleaved, tag, nonce, ranges), nil
}

// Decrypt decrypts a DAVE protocol frame.
func (fc *FrameCryptor) Decrypt(senderID uint64, data []byte) ([]byte, error) {
	pf, err := ParseProtocolFrame(data)
	if err != nil {
		return nil, err
	}

	_, aad := splitFrame(pf.InterleavedFrame, pf.UnencryptedRanges)
	ciphertext := extractCiphertext(pf.InterleavedFrame, pf.UnencryptedRanges)

	// Combine ciphertext + 8-byte tag for GCM Open (matches our tag size).
	input := append(ciphertext, pf.Tag...)

	fullNonce := expandNonce(pf.Nonce)
	plaintext, err := fc.gcm.Open(nil, fullNonce, input, aad)
	if err != nil {
		return nil, ErrDecryptFailed
	}

	// Reconstruct original frame: unencrypted ranges from interleaved + decrypted ranges.
	return reconstructFrame(plaintext, pf.InterleavedFrame, pf.UnencryptedRanges), nil
}

// ---------------------------------------------------------------------------
// Frame manipulation helpers
// ---------------------------------------------------------------------------

// splitFrame separates a frame into (encrypted_data, additional_data) based
// on the unencrypted ranges. encrypted_data is all bytes NOT in unencrypted
// ranges; additional_data is all bytes IN unencrypted ranges.
func splitFrame(frame []byte, ranges []UnencryptedRange) (encrypted, aad []byte) {
	if len(ranges) == 0 {
		return append([]byte{}, frame...), nil
	}

	// Build a set of encrypted byte positions.
	isUnencrypted := make([]bool, len(frame))
	for _, r := range ranges {
		end := int(r.Offset + r.Size)
		if end > len(frame) {
			end = len(frame)
		}
		for i := int(r.Offset); i < end; i++ {
			isUnencrypted[i] = true
		}
	}

	for i, b := range frame {
		if isUnencrypted[i] {
			aad = append(aad, b)
		} else {
			encrypted = append(encrypted, b)
		}
	}
	return
}

// extractCiphertext pulls the ciphertext bytes from an interleaved frame
// (bytes that are NOT in unencrypted ranges).
func extractCiphertext(interleaved []byte, ranges []UnencryptedRange) []byte {
	ct, _ := splitFrame(interleaved, ranges)
	return ct
}

// interleaveFrame rebuilds the interleaved frame: encrypted bytes replace
// the original encrypted ranges, unencrypted ranges stay from the original.
func interleaveFrame(encrypted, original []byte, ranges []UnencryptedRange) []byte {
	out := make([]byte, len(original))

	isUnencrypted := make([]bool, len(original))
	for _, r := range ranges {
		end := int(r.Offset + r.Size)
		if end > len(original) {
			end = len(original)
		}
		for i := int(r.Offset); i < end; i++ {
			isUnencrypted[i] = true
		}
	}

	encIdx := 0
	for i := range original {
		if isUnencrypted[i] {
			out[i] = original[i]
		} else {
			if encIdx < len(encrypted) {
				out[i] = encrypted[encIdx]
				encIdx++
			}
		}
	}
	return out
}

// reconstructFrame rebuilds the original frame from decrypted plaintext
// and the unencrypted ranges from the interleaved frame.
func reconstructFrame(plaintext, interleaved []byte, ranges []UnencryptedRange) []byte {
	out := make([]byte, len(interleaved))

	isUnencrypted := make([]bool, len(interleaved))
	for _, r := range ranges {
		end := int(r.Offset + r.Size)
		if end > len(interleaved) {
			end = len(interleaved)
		}
		for i := int(r.Offset); i < end; i++ {
			isUnencrypted[i] = true
		}
	}

	ptIdx := 0
	for i := range interleaved {
		if isUnencrypted[i] {
			out[i] = interleaved[i]
		} else {
			if ptIdx < len(plaintext) {
				out[i] = plaintext[ptIdx]
				ptIdx++
			}
		}
	}
	return out
}

// expandNonce takes a truncated 32-bit nonce and produces the full 96-bit
// AES-GCM nonce: 8 zero bytes + 4 bytes of the truncated nonce (big-endian
// in the least significant position).
func expandNonce(truncated uint32) []byte {
	nonce := make([]byte, NonceSize)
	nonce[8] = byte(truncated >> 24)
	nonce[9] = byte(truncated >> 16)
	nonce[10] = byte(truncated >> 8)
	nonce[11] = byte(truncated)
	return nonce
}
