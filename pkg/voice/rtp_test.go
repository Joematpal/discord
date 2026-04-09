package voice

import (
	"bytes"
	"testing"
)

func TestParseRTP_Basic(t *testing.T) {
	// Build a minimal RTP packet: V=2, no padding, no extension, CC=0,
	// M=0, PT=120, Seq=42, TS=1000, SSRC=12345, payload "hello"
	raw := make([]byte, 12+5)
	raw[0] = 0x80 // V=2
	raw[1] = 0x78 // PT=120
	raw[2] = 0x00
	raw[3] = 42 // seq=42
	raw[4] = 0x00
	raw[5] = 0x00
	raw[6] = 0x03
	raw[7] = 0xE8 // ts=1000
	raw[8] = 0x00
	raw[9] = 0x00
	raw[10] = 0x30
	raw[11] = 0x39 // ssrc=12345
	copy(raw[12:], "hello")

	pkt, err := ParseRTP(raw)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Header.Version != 2 {
		t.Errorf("Version = %d", pkt.Header.Version)
	}
	if pkt.Header.PayloadType != 120 {
		t.Errorf("PT = %d", pkt.Header.PayloadType)
	}
	if pkt.Header.Sequence != 42 {
		t.Errorf("Seq = %d", pkt.Header.Sequence)
	}
	if pkt.Header.Timestamp != 1000 {
		t.Errorf("TS = %d", pkt.Header.Timestamp)
	}
	if pkt.Header.SSRC != 12345 {
		t.Errorf("SSRC = %d", pkt.Header.SSRC)
	}
	if string(pkt.Payload) != "hello" {
		t.Errorf("Payload = %q", pkt.Payload)
	}
}

func TestRTPPacket_MarshalRoundtrip(t *testing.T) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:     2,
			PayloadType: 120,
			Marker:      true,
			Sequence:    1000,
			Timestamp:   48000,
			SSRC:        99999,
		},
		Payload: []byte{0xDE, 0xAD, 0xBE, 0xEF},
	}
	data := orig.Marshal()
	parsed, err := ParseRTP(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Version != 2 {
		t.Errorf("Version = %d", parsed.Header.Version)
	}
	if parsed.Header.PayloadType != 120 {
		t.Errorf("PT = %d", parsed.Header.PayloadType)
	}
	if !parsed.Header.Marker {
		t.Error("Marker should be set")
	}
	if parsed.Header.Sequence != 1000 {
		t.Errorf("Seq = %d", parsed.Header.Sequence)
	}
	if parsed.Header.Timestamp != 48000 {
		t.Errorf("TS = %d", parsed.Header.Timestamp)
	}
	if parsed.Header.SSRC != 99999 {
		t.Errorf("SSRC = %d", parsed.Header.SSRC)
	}
	if !bytes.Equal(parsed.Payload, orig.Payload) {
		t.Errorf("Payload mismatch")
	}
}

func TestRTP_WithExtension(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:     2,
			Extension:   true,
			PayloadType: 111,
			Sequence:    5,
			Timestamp:   960,
			SSRC:        42,
		},
		ExtProfile: 0xBEDE,
		ExtData:    []byte{0x01, 0x02, 0x03, 0x04}, // 4 bytes = 1 word
		Payload:    []byte{0xAA, 0xBB},
	}
	data := pkt.Marshal()
	parsed, err := ParseRTP(data)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Header.Extension {
		t.Error("Extension should be set")
	}
	if parsed.ExtProfile != 0xBEDE {
		t.Errorf("ExtProfile = 0x%X", parsed.ExtProfile)
	}
	if !bytes.Equal(parsed.ExtData, pkt.ExtData) {
		t.Errorf("ExtData mismatch")
	}
	if !bytes.Equal(parsed.Payload, pkt.Payload) {
		t.Errorf("Payload mismatch")
	}
}

func TestRTP_HeaderSize(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{Version: 2, Extension: true},
		ExtData: make([]byte, 8), // 2 words
	}
	if pkt.HeaderSize() != 12+4+8 {
		t.Errorf("HeaderSize = %d, want 24", pkt.HeaderSize())
	}
}

func TestParseRTP_TooShort(t *testing.T) {
	_, err := ParseRTP([]byte{0x80})
	if err != ErrRTPTooShort {
		t.Errorf("got %v", err)
	}
}

func TestParseRTP_BadVersion(t *testing.T) {
	raw := make([]byte, 12)
	raw[0] = 0x00 // version 0
	_, err := ParseRTP(raw)
	if err != ErrRTPVersion {
		t.Errorf("got %v", err)
	}
}

func TestRTP_Padding(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version: 2,
			Padding: true,
			Sequence: 1,
			SSRC: 1,
		},
		Payload: []byte{0x01, 0x02},
	}
	data := pkt.Marshal()
	parsed, _ := ParseRTP(data)
	if !parsed.Header.Padding {
		t.Error("Padding should be set")
	}
}
