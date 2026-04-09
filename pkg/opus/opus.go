// Package opus implements Opus audio packet parsing and construction
// per RFC 6716 in pure Go.
package opus

import (
	"errors"
	"fmt"
	"time"
)

// Errors returned by the parser.
var (
	ErrEmptyPacket     = errors.New("opus: empty packet")
	ErrInvalidPacket   = errors.New("opus: invalid packet")
	ErrTruncatedPacket = errors.New("opus: truncated packet")
	ErrTooManyFrames   = errors.New("opus: frame count exceeds limit")
	ErrFrameTooLarge   = errors.New("opus: frame size exceeds 1275 bytes")
)

// Bandwidth represents an Opus audio bandwidth.
type Bandwidth uint8

const (
	BandwidthNarrowband    Bandwidth = iota // 4 kHz passband, 8 kHz sample rate
	BandwidthMediumband                     // 6 kHz passband, 12 kHz sample rate
	BandwidthWideband                       // 8 kHz passband, 16 kHz sample rate
	BandwidthSuperwideband                  // 12 kHz passband, 24 kHz sample rate
	BandwidthFullband                       // 20 kHz passband, 48 kHz sample rate
)

func (b Bandwidth) String() string {
	switch b {
	case BandwidthNarrowband:
		return "narrowband"
	case BandwidthMediumband:
		return "mediumband"
	case BandwidthWideband:
		return "wideband"
	case BandwidthSuperwideband:
		return "superwideband"
	case BandwidthFullband:
		return "fullband"
	default:
		return fmt.Sprintf("Bandwidth(%d)", b)
	}
}

// SampleRate returns the sample rate in Hz for this bandwidth.
func (b Bandwidth) SampleRate() int {
	switch b {
	case BandwidthNarrowband:
		return 8000
	case BandwidthMediumband:
		return 12000
	case BandwidthWideband:
		return 16000
	case BandwidthSuperwideband:
		return 24000
	case BandwidthFullband:
		return 48000
	default:
		return 0
	}
}

// Mode represents the Opus coding mode.
type Mode uint8

const (
	ModeSILK   Mode = iota // SILK-only (speech optimized)
	ModeHybrid             // Hybrid SILK+CELT
	ModeCELT               // CELT-only (music / general audio)
)

func (m Mode) String() string {
	switch m {
	case ModeSILK:
		return "SILK"
	case ModeHybrid:
		return "Hybrid"
	case ModeCELT:
		return "CELT"
	default:
		return fmt.Sprintf("Mode(%d)", m)
	}
}

const (
	// MaxFrameSize is the maximum size of a single Opus frame in bytes (RFC 6716 Section 3.4).
	MaxFrameSize = 1275

	// MaxPacketDuration is the maximum duration of an Opus packet.
	MaxPacketDuration = 120 * time.Millisecond

	// MaxFramesPerPacket is the maximum number of frames in a code-3 packet.
	MaxFramesPerPacket = 48
)

// SilenceFrame is the 3-byte Opus silence packet used by Discord's SFU.
// TOC 0xF8 = config 31 (CELT fullband 20 ms), mono, code 0.
var SilenceFrame = []byte{0xF8, 0xFF, 0xFE}

// ---------------------------------------------------------------------------
// TOC (Table of Contents byte) — RFC 6716 Section 3.1
// ---------------------------------------------------------------------------

// TOC represents a parsed Opus TOC byte.
//
//	 0 1 2 3 4 5 6 7
//	+-+-+-+-+-+-+-+-+
//	| config  |s| c |
//	+-+-+-+-+-+-+-+-+
type TOC struct {
	Config uint8 // configuration number 0–31
	Stereo bool  // true = stereo, false = mono
	Code   uint8 // packet code 0–3
}

// ParseTOC parses a single Opus TOC byte.
func ParseTOC(b byte) TOC {
	return TOC{
		Config: b >> 3,
		Stereo: (b>>2)&1 == 1,
		Code:   b & 0x03,
	}
}

// Byte encodes the TOC back to a single byte.
func (t TOC) Byte() byte {
	b := t.Config << 3
	if t.Stereo {
		b |= 1 << 2
	}
	b |= t.Code & 0x03
	return b
}

// Mode returns the coding mode for this configuration.
//
//	Configs  0–11: SILK
//	Configs 12–15: Hybrid
//	Configs 16–31: CELT
func (t TOC) Mode() Mode {
	switch {
	case t.Config <= 11:
		return ModeSILK
	case t.Config <= 15:
		return ModeHybrid
	default:
		return ModeCELT
	}
}

// Bandwidth returns the audio bandwidth for this configuration.
//
//	RFC 6716 Table 2:
//	  0– 3  SILK   NB     12–13 Hybrid SWB    20–23 CELT NB
//	  4– 7  SILK   MB     14–15 Hybrid FB     24–27 CELT SWB
//	  8–11  SILK   WB     16–19 CELT   NB     28–31 CELT FB
func (t TOC) Bandwidth() Bandwidth {
	switch {
	case t.Config <= 3:
		return BandwidthNarrowband
	case t.Config <= 7:
		return BandwidthMediumband
	case t.Config <= 11:
		return BandwidthWideband
	case t.Config <= 13:
		return BandwidthSuperwideband
	case t.Config <= 15:
		return BandwidthFullband
	case t.Config <= 19:
		return BandwidthNarrowband
	case t.Config <= 23:
		return BandwidthWideband
	case t.Config <= 27:
		return BandwidthSuperwideband
	default:
		return BandwidthFullband
	}
}

// FrameDuration returns the duration of one frame for this configuration.
//
//	SILK   frame sizes: 10, 20, 40, 60 ms  (index = config % 4)
//	Hybrid frame sizes: 10, 20 ms           (index = config % 2)
//	CELT   frame sizes: 2.5, 5, 10, 20 ms  (index = config % 4)
func (t TOC) FrameDuration() time.Duration {
	switch {
	case t.Config <= 11: // SILK
		return [4]time.Duration{
			10 * time.Millisecond,
			20 * time.Millisecond,
			40 * time.Millisecond,
			60 * time.Millisecond,
		}[t.Config%4]
	case t.Config <= 15: // Hybrid
		return [2]time.Duration{
			10 * time.Millisecond,
			20 * time.Millisecond,
		}[t.Config%2]
	default: // CELT
		return [4]time.Duration{
			2500 * time.Microsecond,
			5 * time.Millisecond,
			10 * time.Millisecond,
			20 * time.Millisecond,
		}[t.Config%4]
	}
}

// Channels returns 1 for mono, 2 for stereo.
func (t TOC) Channels() int {
	if t.Stereo {
		return 2
	}
	return 1
}

// ---------------------------------------------------------------------------
// Packet — RFC 6716 Section 3.2
// ---------------------------------------------------------------------------

// Packet is a parsed Opus packet.
type Packet struct {
	TOC     TOC
	Frames  [][]byte
	Padding int // number of padding bytes (code 3 only)
}

// Duration returns the total duration of all frames in the packet.
func (p *Packet) Duration() time.Duration {
	return time.Duration(len(p.Frames)) * p.TOC.FrameDuration()
}

// ParsePacket parses a complete Opus packet per RFC 6716 Section 3.
func ParsePacket(data []byte) (*Packet, error) {
	if len(data) == 0 {
		return nil, ErrEmptyPacket
	}

	toc := ParseTOC(data[0])
	payload := data[1:]

	switch toc.Code {
	case 0:
		return parseCode0(toc, payload)
	case 1:
		return parseCode1(toc, payload)
	case 2:
		return parseCode2(toc, payload)
	case 3:
		return parseCode3(toc, payload)
	default:
		return nil, ErrInvalidPacket
	}
}

// Code 0: one frame in the packet (RFC 6716 Section 3.2.2).
func parseCode0(toc TOC, payload []byte) (*Packet, error) {
	if len(payload) > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	return &Packet{
		TOC:    toc,
		Frames: [][]byte{payload},
	}, nil
}

// Code 1: two equal-size frames (RFC 6716 Section 3.2.3).
func parseCode1(toc TOC, payload []byte) (*Packet, error) {
	if len(payload)%2 != 0 {
		return nil, ErrInvalidPacket
	}
	half := len(payload) / 2
	if half > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	return &Packet{
		TOC: toc,
		Frames: [][]byte{
			payload[:half],
			payload[half:],
		},
	}, nil
}

// Code 2: two frames, different sizes (RFC 6716 Section 3.2.4).
func parseCode2(toc TOC, payload []byte) (*Packet, error) {
	size1, n, err := ReadFrameSize(payload)
	if err != nil {
		return nil, err
	}
	if size1 > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	rest := payload[n:]
	if size1 > len(rest) {
		return nil, ErrTruncatedPacket
	}
	frame1 := rest[:size1]
	frame2 := rest[size1:]
	if len(frame2) > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	return &Packet{
		TOC:    toc,
		Frames: [][]byte{frame1, frame2},
	}, nil
}

// Code 3: arbitrary number of frames (RFC 6716 Section 3.2.5).
func parseCode3(toc TOC, payload []byte) (*Packet, error) {
	if len(payload) < 1 {
		return nil, ErrTruncatedPacket
	}

	fcb := payload[0]
	m := int(fcb & 0x3F)        // frame count
	hasPadding := fcb&0x40 != 0 // bit 6
	isVBR := fcb&0x80 != 0      // bit 7

	if m == 0 || m > MaxFramesPerPacket {
		return nil, ErrTooManyFrames
	}
	if time.Duration(m)*toc.FrameDuration() > MaxPacketDuration {
		return nil, ErrTooManyFrames
	}

	pos := 1 // past frame-count byte

	// Decode padding length (RFC 6716 Section 3.2.5).
	paddingLen := 0
	if hasPadding {
		for pos < len(payload) {
			b := payload[pos]
			pos++
			if b == 255 {
				paddingLen += 254
			} else {
				paddingLen += int(b)
				break
			}
		}
	}

	dataLen := len(payload) - pos - paddingLen
	if dataLen < 0 {
		return nil, ErrTruncatedPacket
	}

	frames := make([][]byte, m)

	if isVBR {
		// VBR: read M-1 explicit frame sizes; last frame fills remainder.
		sizes := make([]int, m)
		total := 0
		for i := 0; i < m-1; i++ {
			s, n, err := ReadFrameSize(payload[pos:])
			if err != nil {
				return nil, err
			}
			if s > MaxFrameSize {
				return nil, ErrFrameTooLarge
			}
			sizes[i] = s
			total += s
			pos += n
		}

		lastSize := len(payload) - pos - paddingLen - total
		if lastSize < 0 || lastSize > MaxFrameSize {
			return nil, ErrFrameTooLarge
		}
		sizes[m-1] = lastSize

		for i := 0; i < m; i++ {
			end := pos + sizes[i]
			if end > len(payload)-paddingLen {
				return nil, ErrTruncatedPacket
			}
			frames[i] = payload[pos:end]
			pos += sizes[i]
		}
	} else {
		// CBR: all frames identical size.
		remaining := len(payload) - pos - paddingLen
		if remaining < 0 || remaining%m != 0 {
			return nil, ErrInvalidPacket
		}
		frameSize := remaining / m
		if frameSize > MaxFrameSize {
			return nil, ErrFrameTooLarge
		}
		for i := 0; i < m; i++ {
			end := pos + frameSize
			if end > len(payload) {
				return nil, ErrTruncatedPacket
			}
			frames[i] = payload[pos:end]
			pos += frameSize
		}
	}

	return &Packet{
		TOC:     toc,
		Frames:  frames,
		Padding: paddingLen,
	}, nil
}

// ---------------------------------------------------------------------------
// Frame size encoding — RFC 6716 Section 3.2.1
// ---------------------------------------------------------------------------

// ReadFrameSize reads a 1- or 2-byte frame size.
//
//	0–251   → size is the byte value (1 byte consumed)
//	252–255 → size = first_byte + second_byte*4 (2 bytes consumed)
func ReadFrameSize(data []byte) (size, bytesRead int, err error) {
	if len(data) < 1 {
		return 0, 0, ErrTruncatedPacket
	}
	b := data[0]
	if b < 252 {
		return int(b), 1, nil
	}
	if len(data) < 2 {
		return 0, 0, ErrTruncatedPacket
	}
	return int(b) + int(data[1])*4, 2, nil
}

// EncodeFrameSize encodes a frame size (0–1275) into 1 or 2 bytes.
func EncodeFrameSize(size int) []byte {
	if size < 252 {
		return []byte{byte(size)}
	}
	first := byte(252 + size%4)
	second := byte((size - int(first)) / 4)
	return []byte{first, second}
}

// ---------------------------------------------------------------------------
// Convenience helpers
// ---------------------------------------------------------------------------

// IsSilence reports whether data is a Discord silence frame (0xF8 0xFF 0xFE).
func IsSilence(data []byte) bool {
	return len(data) == 3 &&
		data[0] == 0xF8 && data[1] == 0xFF && data[2] == 0xFE
}

// FrameCount returns the number of Opus frames without fully parsing the packet.
func FrameCount(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, ErrEmptyPacket
	}
	switch data[0] & 0x03 {
	case 0:
		return 1, nil
	case 1, 2:
		return 2, nil
	case 3:
		if len(data) < 2 {
			return 0, ErrTruncatedPacket
		}
		m := int(data[1] & 0x3F)
		if m == 0 || m > MaxFramesPerPacket {
			return 0, ErrTooManyFrames
		}
		return m, nil
	}
	return 0, ErrInvalidPacket
}

// PacketDuration returns the total duration of an Opus packet without fully parsing it.
func PacketDuration(data []byte) (time.Duration, error) {
	if len(data) == 0 {
		return 0, ErrEmptyPacket
	}
	toc := ParseTOC(data[0])
	n, err := FrameCount(data)
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * toc.FrameDuration(), nil
}
