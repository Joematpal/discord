// Package dave implements Discord's Audio & Video E2EE (DAVE) protocol.
package dave

// ULEB128 encoding/decoding used for nonces and unencrypted ranges
// in the DAVE protocol supplemental data.

import "errors"

var ErrULEB128Overflow = errors.New("dave: ULEB128 value overflow")

// EncodeULEB128 encodes a uint32 as ULEB128 bytes.
func EncodeULEB128(value uint32) []byte {
	if value == 0 {
		return []byte{0}
	}
	var buf []byte
	for value >= 0x80 {
		buf = append(buf, byte(0x80|(value&0x7F)))
		value >>= 7
	}
	buf = append(buf, byte(value))
	return buf
}

// DecodeULEB128 decodes a ULEB128 value from data, returning the value
// and the number of bytes consumed.
func DecodeULEB128(data []byte) (value uint32, n int, err error) {
	var shift uint
	for i, b := range data {
		if shift >= 35 {
			return 0, 0, ErrULEB128Overflow
		}
		value |= uint32(b&0x7F) << shift
		shift += 7
		if b&0x80 == 0 {
			return value, i + 1, nil
		}
	}
	return 0, 0, ErrULEB128Overflow
}
