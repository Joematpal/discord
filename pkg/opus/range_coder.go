package opus

// Range coder implementation per RFC 6716 Section 4.1.
// This is the entropy coding layer used by both SILK and CELT codecs.
// The encoder writes range-coded data from the front and raw bits from the
// end of the buffer; the decoder reads both directions from the same buffer.

import "math/bits"

// Range coding constants (RFC 6716 Section 4.1).
const (
	ecSymBits    = 8
	ecCodeBits   = 32
	ecSymMax     = (1 << ecSymBits) - 1                     // 255
	ecCodeShift  = ecCodeBits - ecSymBits - 1               // 23
	ecCodeTop    = uint32(1) << (ecCodeBits - 1)            // 2^31
	ecCodeBot    = ecCodeTop >> ecSymBits                    // 2^23
	ecCodeExtra  = (ecCodeBits-2)%ecSymBits + 1             // 7
	ecUintBits   = 8
	ecWindowSize = 32
)

func ilog(v uint32) int { return bits.Len32(v) }

func min32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Range Decoder
// ---------------------------------------------------------------------------

// RangeDecoder reads range-coded and raw-bit symbols from a byte buffer.
type RangeDecoder struct {
	buf     []byte
	storage uint32
	offs    uint32

	val uint32
	rng uint32
	ext uint32 // cached rng/ft for Decode→Update pair
	rem int    // last byte read

	endOffs   uint32
	endWindow uint32
	nEndBits  int

	nbitsTotal int
	Error      bool
}

// NewRangeDecoder initializes a decoder over data.
func NewRangeDecoder(data []byte) *RangeDecoder {
	d := &RangeDecoder{
		buf:     data,
		storage: uint32(len(data)),
	}
	d.nbitsTotal = ecCodeBits + 1 - ((ecCodeBits-ecCodeExtra)/ecSymBits)*ecSymBits
	d.rng = 1 << ecCodeExtra // 128
	d.rem = d.readByte()
	d.val = d.rng - 1 - uint32(d.rem>>(ecSymBits-ecCodeExtra))
	d.normalize()
	return d
}

func (d *RangeDecoder) readByte() int {
	if d.offs < d.storage {
		b := int(d.buf[d.offs])
		d.offs++
		return b
	}
	return 0
}

func (d *RangeDecoder) readByteFromEnd() int {
	if d.endOffs < d.storage {
		d.endOffs++
		return int(d.buf[d.storage-d.endOffs])
	}
	return 0
}

func (d *RangeDecoder) normalize() {
	for d.rng <= ecCodeBot {
		d.nbitsTotal += ecSymBits
		d.rng <<= ecSymBits
		sym := d.rem
		d.rem = d.readByte()
		sym = (sym<<ecSymBits | d.rem) >> (ecSymBits - ecCodeExtra)
		d.val = ((d.val << ecSymBits) + uint32(255-byte(sym))) & (ecCodeTop - 1)
	}
}

// Decode returns the cumulative frequency for a symbol with total ft.
// Call Update with the symbol's fl, fh, ft after looking up the symbol.
func (d *RangeDecoder) Decode(ft uint32) uint32 {
	d.ext = d.rng / ft
	s := d.val / d.ext
	return ft - min32(s+1, ft)
}

// DecodeBin is like Decode but for a power-of-two total (1 << bits).
func (d *RangeDecoder) DecodeBin(logBits uint) uint32 {
	ft := uint32(1) << logBits
	d.ext = d.rng >> logBits
	s := d.val / d.ext
	return ft - min32(s+1, ft)
}

// Update advances the decoder state after a symbol with cumulative
// frequency [fl, fh) out of total ft has been identified.
func (d *RangeDecoder) Update(fl, fh, ft uint32) {
	s := d.ext * (ft - fh)
	d.val -= s
	if fl > 0 {
		d.rng = d.ext * (fh - fl)
	} else {
		d.rng -= s
	}
	d.normalize()
}

// BitLogP decodes a single bit where P(1) = 1/(2^logp).
func (d *RangeDecoder) BitLogP(logp uint) int {
	r := d.rng
	v := d.val
	s := r >> logp
	ret := 0
	if v < s {
		ret = 1
	}
	if ret == 0 {
		d.val = v - s
	}
	if ret != 0 {
		d.rng = s
	} else {
		d.rng = r - s
	}
	d.normalize()
	return ret
}

// Uint decodes a uniformly-distributed unsigned integer in [0, ft).
func (d *RangeDecoder) Uint(ft uint32) uint32 {
	if ft <= 1 {
		return 0
	}
	ft--
	ftb := ilog(ft)
	if ftb > ecUintBits {
		ftb -= ecUintBits
		top := (ft >> ftb) + 1
		s := d.Decode(top)
		d.Update(s, s+1, top)
		t := s<<ftb | d.Bits(uint(ftb))
		if t <= ft {
			return t
		}
		d.Error = true
		return ft
	}
	ft++
	s := d.Decode(ft)
	d.Update(s, s+1, ft)
	return s
}

// Bits reads n raw bits from the end of the buffer.
func (d *RangeDecoder) Bits(n uint) uint32 {
	window := d.endWindow
	available := d.nEndBits
	if uint(available) < n {
		for available <= ecWindowSize-ecSymBits {
			window |= uint32(d.readByteFromEnd()) << uint(available)
			available += ecSymBits
		}
	}
	ret := window & ((uint32(1) << n) - 1)
	window >>= n
	available -= int(n)
	d.endWindow = window
	d.nEndBits = available
	d.nbitsTotal += int(n)
	return ret
}

// Tell returns the number of bits decoded so far.
func (d *RangeDecoder) Tell() int {
	return d.nbitsTotal - ilog(d.rng)
}

// ---------------------------------------------------------------------------
// Range Encoder
// ---------------------------------------------------------------------------

// RangeEncoder writes range-coded and raw-bit symbols into a byte buffer.
type RangeEncoder struct {
	buf     []byte
	storage uint32
	offs    uint32

	val uint32
	rng uint32
	ext uint32 // carry propagation extension count
	rem int    // buffered byte, -1 = none

	endOffs   uint32
	endWindow uint32
	nEndBits  int

	nbitsTotal int
	Error      bool
}

// NewRangeEncoder creates an encoder with the given buffer capacity in bytes.
func NewRangeEncoder(capacity int) *RangeEncoder {
	return &RangeEncoder{
		buf:        make([]byte, capacity),
		storage:    uint32(capacity),
		rng:        ecCodeTop,
		rem:        -1,
		nbitsTotal: ecCodeBits + 1,
	}
}

func (e *RangeEncoder) writeByte(v byte) {
	if e.offs+e.endOffs < e.storage {
		e.buf[e.offs] = v
		e.offs++
	} else {
		e.Error = true
	}
}

func (e *RangeEncoder) writeByteAtEnd(v byte) {
	if e.offs+e.endOffs < e.storage {
		e.endOffs++
		e.buf[e.storage-e.endOffs] = v
	} else {
		e.Error = true
	}
}

func (e *RangeEncoder) carryOut(c int) {
	if c != ecSymMax {
		carry := c >> ecSymBits
		if e.rem >= 0 {
			e.writeByte(byte(e.rem + carry))
		}
		if e.ext > 0 {
			sym := byte((ecSymMax + carry) & ecSymMax)
			for e.ext > 0 {
				e.writeByte(sym)
				e.ext--
			}
		}
		e.rem = c & ecSymMax
	} else {
		e.ext++
	}
}

func (e *RangeEncoder) normalize() {
	for e.rng <= ecCodeBot {
		e.carryOut(int(e.val >> ecCodeShift))
		e.val = (e.val << ecSymBits) & (ecCodeTop - 1)
		e.rng <<= ecSymBits
		e.nbitsTotal += ecSymBits
	}
}

// Encode encodes a symbol with cumulative frequency [fl, fh) out of total ft.
func (e *RangeEncoder) Encode(fl, fh, ft uint32) {
	r := e.rng / ft
	if fl > 0 {
		e.val += e.rng - r*(ft-fl)
		e.rng = r * (fh - fl)
	} else {
		e.rng -= r * (ft - fh)
	}
	e.normalize()
}

// EncodeBin is like Encode but for a power-of-two total (1 << bits).
func (e *RangeEncoder) EncodeBin(fl, fh uint32, logBits uint) {
	r := e.rng >> logBits
	if fl > 0 {
		e.val += e.rng - r*((1<<logBits)-fl)
		e.rng = r * (fh - fl)
	} else {
		e.rng -= r * ((1 << logBits) - fh)
	}
	e.normalize()
}

// BitLogP encodes a single bit where P(1) = 1/(2^logp).
func (e *RangeEncoder) BitLogP(val int, logp uint) {
	r := e.rng
	s := r >> logp
	r -= s
	if val != 0 {
		e.val += r
	}
	if val != 0 {
		e.rng = s
	} else {
		e.rng = r
	}
	e.normalize()
}

// Uint encodes a uniformly-distributed unsigned integer val in [0, ft).
func (e *RangeEncoder) Uint(val, ft uint32) {
	if ft <= 1 {
		return
	}
	ft--
	ftb := ilog(ft)
	if ftb > ecUintBits {
		ftb -= ecUintBits
		top := (ft >> ftb) + 1
		s := val >> ftb
		e.Encode(s, s+1, top)
		e.Bits(val&((1<<ftb)-1), uint(ftb))
	} else {
		ft++
		e.Encode(val, val+1, ft)
	}
}

// Bits writes n raw bits to the end of the buffer.
func (e *RangeEncoder) Bits(val uint32, n uint) {
	window := e.endWindow
	used := e.nEndBits
	if used+int(n) > ecWindowSize {
		for used >= ecSymBits {
			e.writeByteAtEnd(byte(window & ecSymMax))
			window >>= ecSymBits
			used -= ecSymBits
		}
	}
	window |= val << uint(used)
	used += int(n)
	e.endWindow = window
	e.nEndBits = used
	e.nbitsTotal += int(n)
}

// Tell returns the number of bits used so far.
func (e *RangeEncoder) Tell() int {
	return e.nbitsTotal - ilog(e.rng)
}

// Done finalizes encoding and returns the buffer.
// The returned slice has the same length as the capacity passed to NewRangeEncoder.
func (e *RangeEncoder) Done() []byte {
	l := ecCodeBits - ilog(e.rng)
	msk := (ecCodeTop - 1) >> uint(l)
	end := (e.val + msk) &^ msk
	if (end | msk) >= e.val+e.rng {
		l++
		msk >>= 1
		end = (e.val + msk) &^ msk
	}
	for l > 0 {
		e.carryOut(int(end >> ecCodeShift))
		end = (end << ecSymBits) & (ecCodeTop - 1)
		l -= ecSymBits
	}
	if e.rem >= 0 || e.ext > 0 {
		e.carryOut(0)
	}

	// Flush end window.
	window := e.endWindow
	used := e.nEndBits
	for used >= ecSymBits {
		e.writeByteAtEnd(byte(window & ecSymMax))
		window >>= ecSymBits
		used -= ecSymBits
	}
	if used > 0 {
		// Remaining partial bits go into the gap between front and end.
		pos := e.storage - e.endOffs - 1
		if pos >= e.offs && pos < e.storage {
			e.buf[pos] |= byte(window)
		}
	}

	return e.buf[:e.storage]
}
