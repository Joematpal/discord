// Package voice implements Discord voice connections — the Voice Gateway
// WebSocket protocol, UDP RTP audio send/receive, and Opus decode/encode.
package voice

import (
	"encoding/binary"
	"errors"
)

// RTP header constants.
const (
	rtpVersion     = 2
	rtpHeaderSize  = 12
	rtpMaxSize     = 1500
)

var (
	ErrRTPTooShort = errors.New("voice: RTP packet too short")
	ErrRTPVersion  = errors.New("voice: unsupported RTP version")
)

// RTPHeader is a parsed RTP fixed header (RFC 3550 Section 5.1).
type RTPHeader struct {
	Version    uint8
	Padding    bool
	Extension  bool
	CSRCCount  uint8
	Marker     bool
	PayloadType uint8
	Sequence   uint16
	Timestamp  uint32
	SSRC       uint32
}

// RTPPacket is a complete RTP packet with header, optional extension, and payload.
type RTPPacket struct {
	Header    RTPHeader
	ExtProfile uint16   // extension profile, if Header.Extension
	ExtData   []byte    // extension data bytes, if Header.Extension
	Payload   []byte
}

// ParseRTP parses a raw RTP packet from the wire.
func ParseRTP(data []byte) (*RTPPacket, error) {
	if len(data) < rtpHeaderSize {
		return nil, ErrRTPTooShort
	}

	pkt := &RTPPacket{}
	pkt.Header.Version = (data[0] >> 6) & 0x03
	if pkt.Header.Version != rtpVersion {
		return nil, ErrRTPVersion
	}
	pkt.Header.Padding = (data[0]>>5)&1 == 1
	pkt.Header.Extension = (data[0]>>4)&1 == 1
	pkt.Header.CSRCCount = data[0] & 0x0F
	pkt.Header.Marker = (data[1]>>7)&1 == 1
	pkt.Header.PayloadType = data[1] & 0x7F
	pkt.Header.Sequence = binary.BigEndian.Uint16(data[2:4])
	pkt.Header.Timestamp = binary.BigEndian.Uint32(data[4:8])
	pkt.Header.SSRC = binary.BigEndian.Uint32(data[8:12])

	offset := rtpHeaderSize + int(pkt.Header.CSRCCount)*4
	if offset > len(data) {
		return nil, ErrRTPTooShort
	}

	// RTP header extension (RFC 3550 Section 5.3.1).
	if pkt.Header.Extension {
		if offset+4 > len(data) {
			return nil, ErrRTPTooShort
		}
		pkt.ExtProfile = binary.BigEndian.Uint16(data[offset : offset+2])
		extLen := int(binary.BigEndian.Uint16(data[offset+2:offset+4])) * 4
		offset += 4
		if offset+extLen > len(data) {
			return nil, ErrRTPTooShort
		}
		pkt.ExtData = data[offset : offset+extLen]
		offset += extLen
	}

	pkt.Payload = data[offset:]
	return pkt, nil
}

// Marshal serializes an RTP packet to wire format.
func (pkt *RTPPacket) Marshal() []byte {
	size := rtpHeaderSize + int(pkt.Header.CSRCCount)*4
	if pkt.Header.Extension {
		size += 4 + len(pkt.ExtData)
	}
	size += len(pkt.Payload)

	buf := make([]byte, size)
	buf[0] = rtpVersion<<6 | pkt.Header.CSRCCount
	if pkt.Header.Padding {
		buf[0] |= 1 << 5
	}
	if pkt.Header.Extension {
		buf[0] |= 1 << 4
	}
	buf[1] = pkt.Header.PayloadType
	if pkt.Header.Marker {
		buf[1] |= 1 << 7
	}
	binary.BigEndian.PutUint16(buf[2:4], pkt.Header.Sequence)
	binary.BigEndian.PutUint32(buf[4:8], pkt.Header.Timestamp)
	binary.BigEndian.PutUint32(buf[8:12], pkt.Header.SSRC)

	off := rtpHeaderSize
	if pkt.Header.Extension {
		binary.BigEndian.PutUint16(buf[off:off+2], pkt.ExtProfile)
		binary.BigEndian.PutUint16(buf[off+2:off+4], uint16(len(pkt.ExtData)/4))
		off += 4
		copy(buf[off:], pkt.ExtData)
		off += len(pkt.ExtData)
	}

	copy(buf[off:], pkt.Payload)
	return buf
}

// HeaderSize returns the total RTP header size including CSRC and extensions.
func (pkt *RTPPacket) HeaderSize() int {
	size := rtpHeaderSize + int(pkt.Header.CSRCCount)*4
	if pkt.Header.Extension {
		size += 4 + len(pkt.ExtData)
	}
	return size
}
