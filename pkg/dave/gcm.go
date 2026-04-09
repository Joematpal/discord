package dave

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
)

// truncatedGCM wraps a standard AES block cipher and implements AES-GCM
// with a truncated authentication tag. Go's standard library requires tags
// of at least 12 bytes, but the DAVE protocol uses 8-byte tags.
//
// This implements GCM (Galois/Counter Mode) from scratch using the AES block
// cipher, supporting arbitrary tag sizes down to 1 byte.
type truncatedGCM struct {
	block   cipher.Block
	tagSize int
	h       [2]uint64 // GHASH key H = AES_K(0^128)
}

// newTruncatedGCM creates an AES-GCM cipher with the given tag size.
func newTruncatedGCM(key []byte, tagSize int) (*truncatedGCM, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g := &truncatedGCM{block: block, tagSize: tagSize}

	// Derive H = AES_K(0^128).
	var hBlock [16]byte
	block.Encrypt(hBlock[:], hBlock[:])
	g.h[0] = binary.BigEndian.Uint64(hBlock[:8])
	g.h[1] = binary.BigEndian.Uint64(hBlock[8:])
	return g, nil
}

func (g *truncatedGCM) NonceSize() int { return NonceSize }
func (g *truncatedGCM) Overhead() int  { return g.tagSize }

// Seal encrypts plaintext with additional data, appending a truncated tag.
func (g *truncatedGCM) Seal(dst, nonce, plaintext, aad []byte) []byte {
	if len(nonce) != NonceSize {
		panic("dave: invalid nonce size")
	}

	// Generate counter block: nonce || 0x00000001.
	var j0 [16]byte
	copy(j0[:], nonce)
	j0[12] = 0
	j0[13] = 0
	j0[14] = 0
	j0[15] = 1

	// Encrypt plaintext with CTR starting at J0+1.
	ctr := incr(j0)
	ciphertext := make([]byte, len(plaintext))
	g.ctr(ciphertext, plaintext, ctr)

	// Compute GHASH over AAD and ciphertext.
	tag := g.computeTag(aad, ciphertext, j0)

	// Append truncated tag.
	ret, out := sliceForAppend(dst, len(ciphertext)+g.tagSize)
	copy(out, ciphertext)
	copy(out[len(ciphertext):], tag[:g.tagSize])
	return ret
}

// Open decrypts ciphertext with additional data, verifying the truncated tag.
func (g *truncatedGCM) Open(dst, nonce, ciphertextAndTag, aad []byte) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, errors.New("dave: invalid nonce size")
	}
	if len(ciphertextAndTag) < g.tagSize {
		return nil, errors.New("dave: ciphertext too short")
	}

	ctLen := len(ciphertextAndTag) - g.tagSize
	ciphertext := ciphertextAndTag[:ctLen]
	providedTag := ciphertextAndTag[ctLen:]

	var j0 [16]byte
	copy(j0[:], nonce)
	j0[12] = 0
	j0[13] = 0
	j0[14] = 0
	j0[15] = 1

	// Compute expected tag.
	expectedTag := g.computeTag(aad, ciphertext, j0)

	// Constant-time comparison of truncated tags.
	ok := true
	for i := 0; i < g.tagSize; i++ {
		if expectedTag[i] != providedTag[i] {
			ok = false
		}
	}
	if !ok {
		return nil, errors.New("dave: authentication failed")
	}

	// Decrypt.
	ctr := incr(j0)
	plaintext := make([]byte, ctLen)
	g.ctr(plaintext, ciphertext, ctr)

	ret, out := sliceForAppend(dst, len(plaintext))
	copy(out, plaintext)
	return ret, nil
}

// computeTag computes the full 16-byte GCM authentication tag.
func (g *truncatedGCM) computeTag(aad, ciphertext []byte, j0 [16]byte) [16]byte {
	// GHASH(H, AAD, C)
	var s [2]uint64 // 128-bit state

	// Process AAD.
	g.ghashBlocks(&s, aad)

	// Process ciphertext.
	g.ghashBlocks(&s, ciphertext)

	// Process length block: len(AAD)*8 || len(C)*8.
	var lenBlock [16]byte
	binary.BigEndian.PutUint64(lenBlock[:8], uint64(len(aad))*8)
	binary.BigEndian.PutUint64(lenBlock[8:], uint64(len(ciphertext))*8)
	g.ghashBlock(&s, lenBlock[:])

	// T = GHASH XOR E(K, J0).
	var encJ0 [16]byte
	g.block.Encrypt(encJ0[:], j0[:])

	var tag [16]byte
	binary.BigEndian.PutUint64(tag[:8], s[0]^binary.BigEndian.Uint64(encJ0[:8]))
	binary.BigEndian.PutUint64(tag[8:], s[1]^binary.BigEndian.Uint64(encJ0[8:]))
	return tag
}

// ghashBlocks processes data through GHASH, padding the last block.
func (g *truncatedGCM) ghashBlocks(s *[2]uint64, data []byte) {
	for len(data) >= 16 {
		g.ghashBlock(s, data[:16])
		data = data[16:]
	}
	if len(data) > 0 {
		var block [16]byte
		copy(block[:], data)
		g.ghashBlock(s, block[:])
	}
}

// ghashBlock processes one 128-bit block: S = (S XOR X) * H.
func (g *truncatedGCM) ghashBlock(s *[2]uint64, block []byte) {
	s[0] ^= binary.BigEndian.Uint64(block[:8])
	s[1] ^= binary.BigEndian.Uint64(block[8:])
	gfMul(s, &g.h)
}

// ctr encrypts/decrypts using AES-CTR starting from the given counter block.
func (g *truncatedGCM) ctr(dst, src []byte, counter [16]byte) {
	var keystream [16]byte
	for i := 0; i < len(src); i += 16 {
		g.block.Encrypt(keystream[:], counter[:])
		end := i + 16
		if end > len(src) {
			end = len(src)
		}
		for j := i; j < end; j++ {
			dst[j] = src[j] ^ keystream[j-i]
		}
		counter = incr(counter)
	}
}

// incr increments the rightmost 32 bits of a 128-bit counter block.
func incr(counter [16]byte) [16]byte {
	v := binary.BigEndian.Uint32(counter[12:16])
	v++
	binary.BigEndian.PutUint32(counter[12:16], v)
	return counter
}

// gfMul multiplies two 128-bit elements in GF(2^128) with the GCM polynomial.
// This is the schoolbook method; not constant-time but correct.
func gfMul(x, y *[2]uint64) {
	var z [2]uint64
	v := *y

	for i := 0; i < 128; i++ {
		// Check bit i of x (MSB first).
		word := 0
		bit := uint(i)
		if bit >= 64 {
			word = 1
			bit -= 64
		}
		if x[word]&(1<<(63-bit)) != 0 {
			z[0] ^= v[0]
			z[1] ^= v[1]
		}

		// Shift v right by 1 in GF(2^128).
		carry := v[0] & 1
		v[0] >>= 1
		v[1] = (v[1] >> 1) | (carry << 63)
		if carry != 0 {
			// XOR with the GCM reduction polynomial: x^128 + x^7 + x^2 + x + 1.
			// The feedback is applied to the MSB of the high word.
			v[0] ^= 0xE100000000000000
		}
	}
	*x = z
}

func sliceForAppend(in []byte, n int) (head, tail []byte) {
	if total := len(in) + n; cap(in) >= total {
		head = in[:total]
	} else {
		head = make([]byte, total)
		copy(head, in)
	}
	tail = head[len(in):]
	return
}
