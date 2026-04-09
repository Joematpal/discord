package opus

import (
	"bytes"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TOC parsing
// ---------------------------------------------------------------------------

func TestParseTOC(t *testing.T) {
	tests := []struct {
		name   string
		b      byte
		config uint8
		stereo bool
		code   uint8
	}{
		{"zero byte", 0x00, 0, false, 0},
		{"SILK NB mono code0", 0x08, 1, false, 0},                // config 1
		{"SILK WB stereo code1", 0x55, 10, true, 1},              // 01010 1 01
		{"Hybrid SWB mono code2", 0x62, 12, false, 2},            // 01100 0 10
		{"CELT FB stereo code3", 0xFF, 31, true, 3},              // 11111 1 11
		{"silence TOC", 0xF8, 31, false, 0},                      // 11111 0 00
		{"CELT NB mono code0", 0x80, 16, false, 0},               // 10000 0 00
		{"config 20 stereo code2", 0xA6, 20, true, 2},            // 10100 1 10
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toc := ParseTOC(tt.b)
			if toc.Config != tt.config {
				t.Errorf("Config = %d, want %d", toc.Config, tt.config)
			}
			if toc.Stereo != tt.stereo {
				t.Errorf("Stereo = %v, want %v", toc.Stereo, tt.stereo)
			}
			if toc.Code != tt.code {
				t.Errorf("Code = %d, want %d", toc.Code, tt.code)
			}
		})
	}
}

func TestTOCByte_Roundtrip(t *testing.T) {
	for b := 0; b < 256; b++ {
		toc := ParseTOC(byte(b))
		got := toc.Byte()
		if got != byte(b) {
			t.Fatalf("roundtrip failed for 0x%02X: got 0x%02X", b, got)
		}
	}
}

func TestTOCMode(t *testing.T) {
	// Expected modes for all 32 configs.
	expected := []Mode{
		// 0-11: SILK
		ModeSILK, ModeSILK, ModeSILK, ModeSILK,
		ModeSILK, ModeSILK, ModeSILK, ModeSILK,
		ModeSILK, ModeSILK, ModeSILK, ModeSILK,
		// 12-15: Hybrid
		ModeHybrid, ModeHybrid, ModeHybrid, ModeHybrid,
		// 16-31: CELT
		ModeCELT, ModeCELT, ModeCELT, ModeCELT,
		ModeCELT, ModeCELT, ModeCELT, ModeCELT,
		ModeCELT, ModeCELT, ModeCELT, ModeCELT,
		ModeCELT, ModeCELT, ModeCELT, ModeCELT,
	}
	for cfg := uint8(0); cfg < 32; cfg++ {
		toc := TOC{Config: cfg}
		if got := toc.Mode(); got != expected[cfg] {
			t.Errorf("config %d: Mode() = %v, want %v", cfg, got, expected[cfg])
		}
	}
}

func TestTOCBandwidth(t *testing.T) {
	expected := []Bandwidth{
		// 0-3: SILK NB
		BandwidthNarrowband, BandwidthNarrowband, BandwidthNarrowband, BandwidthNarrowband,
		// 4-7: SILK MB
		BandwidthMediumband, BandwidthMediumband, BandwidthMediumband, BandwidthMediumband,
		// 8-11: SILK WB
		BandwidthWideband, BandwidthWideband, BandwidthWideband, BandwidthWideband,
		// 12-13: Hybrid SWB
		BandwidthSuperwideband, BandwidthSuperwideband,
		// 14-15: Hybrid FB
		BandwidthFullband, BandwidthFullband,
		// 16-19: CELT NB
		BandwidthNarrowband, BandwidthNarrowband, BandwidthNarrowband, BandwidthNarrowband,
		// 20-23: CELT WB
		BandwidthWideband, BandwidthWideband, BandwidthWideband, BandwidthWideband,
		// 24-27: CELT SWB
		BandwidthSuperwideband, BandwidthSuperwideband, BandwidthSuperwideband, BandwidthSuperwideband,
		// 28-31: CELT FB
		BandwidthFullband, BandwidthFullband, BandwidthFullband, BandwidthFullband,
	}
	for cfg := uint8(0); cfg < 32; cfg++ {
		toc := TOC{Config: cfg}
		if got := toc.Bandwidth(); got != expected[cfg] {
			t.Errorf("config %d: Bandwidth() = %v, want %v", cfg, got, expected[cfg])
		}
	}
}

func TestTOCFrameDuration(t *testing.T) {
	ms := time.Millisecond
	us := time.Microsecond

	expected := []time.Duration{
		// SILK (0-11): 10, 20, 40, 60 repeated 3×
		10 * ms, 20 * ms, 40 * ms, 60 * ms,
		10 * ms, 20 * ms, 40 * ms, 60 * ms,
		10 * ms, 20 * ms, 40 * ms, 60 * ms,
		// Hybrid (12-15): 10, 20 repeated 2×
		10 * ms, 20 * ms, 10 * ms, 20 * ms,
		// CELT (16-31): 2.5, 5, 10, 20 repeated 4×
		2500 * us, 5 * ms, 10 * ms, 20 * ms,
		2500 * us, 5 * ms, 10 * ms, 20 * ms,
		2500 * us, 5 * ms, 10 * ms, 20 * ms,
		2500 * us, 5 * ms, 10 * ms, 20 * ms,
	}
	for cfg := uint8(0); cfg < 32; cfg++ {
		toc := TOC{Config: cfg}
		if got := toc.FrameDuration(); got != expected[cfg] {
			t.Errorf("config %d: FrameDuration() = %v, want %v", cfg, got, expected[cfg])
		}
	}
}

func TestTOCChannels(t *testing.T) {
	mono := TOC{Stereo: false}
	if mono.Channels() != 1 {
		t.Error("mono should be 1 channel")
	}
	stereo := TOC{Stereo: true}
	if stereo.Channels() != 2 {
		t.Error("stereo should be 2 channels")
	}
}

// ---------------------------------------------------------------------------
// Bandwidth / Mode helpers
// ---------------------------------------------------------------------------

func TestBandwidthSampleRate(t *testing.T) {
	tests := []struct {
		bw   Bandwidth
		rate int
	}{
		{BandwidthNarrowband, 8000},
		{BandwidthMediumband, 12000},
		{BandwidthWideband, 16000},
		{BandwidthSuperwideband, 24000},
		{BandwidthFullband, 48000},
	}
	for _, tt := range tests {
		if got := tt.bw.SampleRate(); got != tt.rate {
			t.Errorf("%v.SampleRate() = %d, want %d", tt.bw, got, tt.rate)
		}
	}
}

func TestBandwidthString(t *testing.T) {
	if s := BandwidthFullband.String(); s != "fullband" {
		t.Errorf("got %q", s)
	}
	if s := Bandwidth(99).String(); s != "Bandwidth(99)" {
		t.Errorf("got %q", s)
	}
}

func TestModeString(t *testing.T) {
	if s := ModeSILK.String(); s != "SILK" {
		t.Errorf("got %q", s)
	}
	if s := Mode(99).String(); s != "Mode(99)" {
		t.Errorf("got %q", s)
	}
}

// ---------------------------------------------------------------------------
// Frame size encoding
// ---------------------------------------------------------------------------

func TestReadFrameSize(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		size  int
		bytes int
	}{
		{"zero", []byte{0}, 0, 1},
		{"small", []byte{100}, 100, 1},
		{"max-one-byte", []byte{251}, 251, 1},
		{"two-byte-min", []byte{252, 0}, 252, 2},
		{"two-byte-256", []byte{252, 1}, 256, 2},
		{"two-byte-max", []byte{255, 255}, 1275, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size, n, err := ReadFrameSize(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			if size != tt.size {
				t.Errorf("size = %d, want %d", size, tt.size)
			}
			if n != tt.bytes {
				t.Errorf("bytesRead = %d, want %d", n, tt.bytes)
			}
		})
	}
}

func TestReadFrameSize_Errors(t *testing.T) {
	if _, _, err := ReadFrameSize(nil); err != ErrTruncatedPacket {
		t.Errorf("empty: got %v", err)
	}
	if _, _, err := ReadFrameSize([]byte{252}); err != ErrTruncatedPacket {
		t.Errorf("short two-byte: got %v", err)
	}
}

func TestEncodeFrameSize(t *testing.T) {
	tests := []struct {
		size     int
		expected []byte
	}{
		{0, []byte{0}},
		{100, []byte{100}},
		{251, []byte{251}},
		{252, []byte{252, 0}},
		{256, []byte{252, 1}},
		{1275, []byte{255, 255}},
	}
	for _, tt := range tests {
		got := EncodeFrameSize(tt.size)
		if !bytes.Equal(got, tt.expected) {
			t.Errorf("EncodeFrameSize(%d) = %v, want %v", tt.size, got, tt.expected)
		}
	}
}

func TestFrameSizeRoundtrip(t *testing.T) {
	for size := 0; size <= MaxFrameSize; size++ {
		encoded := EncodeFrameSize(size)
		decoded, n, err := ReadFrameSize(encoded)
		if err != nil {
			t.Fatalf("size %d: encode=%v, ReadFrameSize error: %v", size, encoded, err)
		}
		if n != len(encoded) {
			t.Fatalf("size %d: consumed %d bytes, encoded %d", size, n, len(encoded))
		}
		if decoded != size {
			t.Fatalf("size %d: roundtrip got %d", size, decoded)
		}
	}
}

// ---------------------------------------------------------------------------
// Packet parsing — Code 0
// ---------------------------------------------------------------------------

func TestParsePacket_Code0(t *testing.T) {
	// TOC = config 1, mono, code 0 → SILK NB 20ms, 1 frame.
	frame := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	pkt := append([]byte{0x08}, frame...) // 0x08 = config 1, mono, code 0

	p, err := ParsePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(p.Frames))
	}
	if !bytes.Equal(p.Frames[0], frame) {
		t.Error("frame data mismatch")
	}
	if p.Duration() != 20*time.Millisecond {
		t.Errorf("Duration = %v, want 20ms", p.Duration())
	}
}

func TestParsePacket_Code0_Empty(t *testing.T) {
	// A code-0 packet with no payload is valid (DTX / comfort noise).
	pkt := []byte{0x08}
	p, err := ParsePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 1 || len(p.Frames[0]) != 0 {
		t.Error("expected 1 empty frame")
	}
}

// ---------------------------------------------------------------------------
// Packet parsing — Code 1
// ---------------------------------------------------------------------------

func TestParsePacket_Code1(t *testing.T) {
	// TOC = config 10, stereo, code 1 → SILK WB 40ms, 2 equal frames.
	// 0x55 = 01010 1 01
	frame1 := []byte{0x01, 0x02, 0x03}
	frame2 := []byte{0x04, 0x05, 0x06}
	pkt := append([]byte{0x55}, frame1...)
	pkt = append(pkt, frame2...)

	p, err := ParsePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(p.Frames))
	}
	if !bytes.Equal(p.Frames[0], frame1) {
		t.Error("frame 0 mismatch")
	}
	if !bytes.Equal(p.Frames[1], frame2) {
		t.Error("frame 1 mismatch")
	}
	if p.TOC.Channels() != 2 {
		t.Error("expected stereo")
	}
	if p.Duration() != 80*time.Millisecond {
		t.Errorf("Duration = %v, want 80ms", p.Duration())
	}
}

func TestParsePacket_Code1_OddPayload(t *testing.T) {
	// Odd-length payload is invalid for code 1.
	pkt := []byte{0x01, 0xAA, 0xBB, 0xCC} // 3 bytes payload, not even
	_, err := ParsePacket(pkt)
	if err != ErrInvalidPacket {
		t.Errorf("expected ErrInvalidPacket, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Packet parsing — Code 2
// ---------------------------------------------------------------------------

func TestParsePacket_Code2(t *testing.T) {
	// TOC: config 0, mono, code 2 → SILK NB 10ms, 2 frames.
	// 0x02 = 00000 0 10
	frame1 := []byte{0x11, 0x22}
	frame2 := []byte{0x33, 0x44, 0x55}

	var pkt []byte
	pkt = append(pkt, 0x02)                      // TOC
	pkt = append(pkt, EncodeFrameSize(2)...)      // frame1 size
	pkt = append(pkt, frame1...)
	pkt = append(pkt, frame2...)

	p, err := ParsePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(p.Frames))
	}
	if !bytes.Equal(p.Frames[0], frame1) {
		t.Errorf("frame 0 = %v, want %v", p.Frames[0], frame1)
	}
	if !bytes.Equal(p.Frames[1], frame2) {
		t.Errorf("frame 1 = %v, want %v", p.Frames[1], frame2)
	}
}

func TestParsePacket_Code2_TwoByteSize(t *testing.T) {
	// Frame1 is 300 bytes → needs 2-byte size encoding.
	frame1 := make([]byte, 300)
	for i := range frame1 {
		frame1[i] = byte(i)
	}
	frame2 := []byte{0xAA, 0xBB}

	var pkt []byte
	pkt = append(pkt, 0x02)
	pkt = append(pkt, EncodeFrameSize(300)...)
	pkt = append(pkt, frame1...)
	pkt = append(pkt, frame2...)

	p, err := ParsePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Frames[0], frame1) {
		t.Error("frame 0 mismatch")
	}
	if !bytes.Equal(p.Frames[1], frame2) {
		t.Error("frame 1 mismatch")
	}
}

// ---------------------------------------------------------------------------
// Packet parsing — Code 3 CBR
// ---------------------------------------------------------------------------

func TestParsePacket_Code3_CBR(t *testing.T) {
	// TOC: config 19, mono, code 3 → CELT NB 20ms.
	// 0x9B = 10011 0 11
	frameSize := 10
	frameCount := 3
	// Frame count byte: V=0, P=0, M=3 → 0x03
	var pkt []byte
	pkt = append(pkt, 0x9B) // TOC
	pkt = append(pkt, 0x03) // CBR, no padding, 3 frames

	for i := 0; i < frameCount; i++ {
		frame := make([]byte, frameSize)
		for j := range frame {
			frame[j] = byte(i)
		}
		pkt = append(pkt, frame...)
	}

	p, err := ParsePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(p.Frames))
	}
	for i, f := range p.Frames {
		if len(f) != frameSize {
			t.Errorf("frame %d: len = %d, want %d", i, len(f), frameSize)
		}
		if f[0] != byte(i) {
			t.Errorf("frame %d: first byte = %d, want %d", i, f[0], i)
		}
	}
	if p.Duration() != 60*time.Millisecond {
		t.Errorf("Duration = %v, want 60ms", p.Duration())
	}
}

// ---------------------------------------------------------------------------
// Packet parsing — Code 3 VBR
// ---------------------------------------------------------------------------

func TestParsePacket_Code3_VBR(t *testing.T) {
	// TOC: config 23, mono, code 3 → CELT WB 20ms.
	// 0xBB = 10111 0 11
	frame1 := []byte{0x01, 0x02, 0x03}       // 3 bytes
	frame2 := []byte{0x04, 0x05}              // 2 bytes
	frame3 := []byte{0x06, 0x07, 0x08, 0x09} // 4 bytes (last, implicit size)

	// Frame count byte: V=1, P=0, M=3 → 0x83
	var pkt []byte
	pkt = append(pkt, 0xBB)                       // TOC
	pkt = append(pkt, 0x83)                        // VBR, no padding, 3 frames
	pkt = append(pkt, EncodeFrameSize(3)...)       // frame1 size
	pkt = append(pkt, EncodeFrameSize(2)...)       // frame2 size
	pkt = append(pkt, frame1...)
	pkt = append(pkt, frame2...)
	pkt = append(pkt, frame3...)

	p, err := ParsePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(p.Frames))
	}
	if !bytes.Equal(p.Frames[0], frame1) {
		t.Errorf("frame 0 = %v", p.Frames[0])
	}
	if !bytes.Equal(p.Frames[1], frame2) {
		t.Errorf("frame 1 = %v", p.Frames[1])
	}
	if !bytes.Equal(p.Frames[2], frame3) {
		t.Errorf("frame 2 = %v", p.Frames[2])
	}
}

// ---------------------------------------------------------------------------
// Packet parsing — Code 3 with padding
// ---------------------------------------------------------------------------

func TestParsePacket_Code3_Padding(t *testing.T) {
	// CBR, 2 frames, with 10 bytes of padding.
	// TOC: config 0, mono, code 3 → SILK NB 10ms.
	// 0x03 = 00000 0 11
	frameSize := 5
	// Frame count byte: V=0, P=1, M=2 → 0x42
	var pkt []byte
	pkt = append(pkt, 0x03) // TOC
	pkt = append(pkt, 0x42) // CBR, padding, 2 frames
	pkt = append(pkt, 10)   // padding length = 10

	for i := 0; i < 2; i++ {
		f := make([]byte, frameSize)
		for j := range f {
			f[j] = byte(i + 1)
		}
		pkt = append(pkt, f...)
	}
	// Append 10 bytes of padding.
	pkt = append(pkt, make([]byte, 10)...)

	p, err := ParsePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(p.Frames))
	}
	if p.Padding != 10 {
		t.Errorf("Padding = %d, want 10", p.Padding)
	}
	for i, f := range p.Frames {
		if len(f) != frameSize {
			t.Errorf("frame %d: len = %d, want %d", i, len(f), frameSize)
		}
	}
}

func TestParsePacket_Code3_LargePadding(t *testing.T) {
	// Padding encoded with continuation bytes (255, 255, 10) → 254+254+10 = 518.
	frameSize := 4
	var pkt []byte
	pkt = append(pkt, 0x03) // TOC
	pkt = append(pkt, 0x41) // CBR, padding, 1 frame
	pkt = append(pkt, 255)  // continuation
	pkt = append(pkt, 255)  // continuation
	pkt = append(pkt, 10)   // final = 10; total = 254+254+10 = 518

	pkt = append(pkt, make([]byte, frameSize)...)
	pkt = append(pkt, make([]byte, 518)...) // padding data

	p, err := ParsePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if p.Padding != 518 {
		t.Errorf("Padding = %d, want 518", p.Padding)
	}
	if len(p.Frames) != 1 || len(p.Frames[0]) != frameSize {
		t.Errorf("frame len = %d, want %d", len(p.Frames[0]), frameSize)
	}
}

// ---------------------------------------------------------------------------
// Packet parsing — error cases
// ---------------------------------------------------------------------------

func TestParsePacket_Errors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		err  error
	}{
		{"empty", nil, ErrEmptyPacket},
		{"empty slice", []byte{}, ErrEmptyPacket},
		{"code3 no frame count", []byte{0x03}, ErrTruncatedPacket},
		{"code3 zero frames", []byte{0x03, 0x00}, ErrTooManyFrames},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParsePacket(tt.data)
			if err != tt.err {
				t.Errorf("got %v, want %v", err, tt.err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsSilence
// ---------------------------------------------------------------------------

func TestIsSilence(t *testing.T) {
	if !IsSilence([]byte{0xF8, 0xFF, 0xFE}) {
		t.Error("expected true for silence frame")
	}
	if IsSilence([]byte{0xF8, 0xFF}) {
		t.Error("expected false for 2-byte packet")
	}
	if IsSilence([]byte{0xF8, 0xFF, 0xFE, 0x00}) {
		t.Error("expected false for 4-byte packet")
	}
	if IsSilence([]byte{0x00, 0xFF, 0xFE}) {
		t.Error("expected false for wrong TOC")
	}
}

// ---------------------------------------------------------------------------
// FrameCount
// ---------------------------------------------------------------------------

func TestFrameCount(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		count int
	}{
		{"code0", []byte{0x00, 0xAA}, 1},
		{"code1", []byte{0x01, 0xAA, 0xBB}, 2},
		{"code2", []byte{0x02, 0x01, 0xAA, 0xBB}, 2},
		{"code3 m=5", []byte{0x03, 0x05}, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := FrameCount(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			if n != tt.count {
				t.Errorf("FrameCount = %d, want %d", n, tt.count)
			}
		})
	}
}

func TestFrameCount_Errors(t *testing.T) {
	if _, err := FrameCount(nil); err != ErrEmptyPacket {
		t.Errorf("nil: got %v", err)
	}
	if _, err := FrameCount([]byte{0x03}); err != ErrTruncatedPacket {
		t.Errorf("truncated code3: got %v", err)
	}
	if _, err := FrameCount([]byte{0x03, 0x00}); err != ErrTooManyFrames {
		t.Errorf("zero frames: got %v", err)
	}
}

// ---------------------------------------------------------------------------
// PacketDuration
// ---------------------------------------------------------------------------

func TestPacketDuration(t *testing.T) {
	// Silence frame: config 31 = CELT FB 20ms, code 0 = 1 frame → 20ms.
	d, err := PacketDuration(SilenceFrame)
	if err != nil {
		t.Fatal(err)
	}
	if d != 20*time.Millisecond {
		t.Errorf("silence duration = %v, want 20ms", d)
	}
}

func TestPacketDuration_Code3(t *testing.T) {
	// config 1 = SILK NB 20ms, code 3, 3 frames → 60ms.
	data := []byte{0x0B, 0x03} // 0x0B = 00001 0 11 → config 1, mono, code 3
	d, err := PacketDuration(data)
	if err != nil {
		t.Fatal(err)
	}
	if d != 60*time.Millisecond {
		t.Errorf("duration = %v, want 60ms", d)
	}
}

// ---------------------------------------------------------------------------
// Silence frame analysis
// ---------------------------------------------------------------------------

func TestSilenceFrame_Properties(t *testing.T) {
	p, err := ParsePacket(SilenceFrame)
	if err != nil {
		t.Fatal(err)
	}
	if p.TOC.Mode() != ModeCELT {
		t.Errorf("mode = %v, want CELT", p.TOC.Mode())
	}
	if p.TOC.Bandwidth() != BandwidthFullband {
		t.Errorf("bandwidth = %v, want fullband", p.TOC.Bandwidth())
	}
	if p.TOC.FrameDuration() != 20*time.Millisecond {
		t.Errorf("frame duration = %v, want 20ms", p.TOC.FrameDuration())
	}
	if p.TOC.Channels() != 1 {
		t.Error("expected mono")
	}
	if len(p.Frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(p.Frames))
	}
	if !bytes.Equal(p.Frames[0], []byte{0xFF, 0xFE}) {
		t.Errorf("frame data = %v", p.Frames[0])
	}
}
