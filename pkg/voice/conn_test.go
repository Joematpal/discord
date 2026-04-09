package voice

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockUDP struct {
	mu       sync.Mutex
	readBuf  [][]byte
	readIdx  int
	written  [][]byte
	closed   bool
}

func (m *mockUDP) Read(b []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readIdx >= len(m.readBuf) {
		return 0, &timeoutError{}
	}
	n := copy(b, m.readBuf[m.readIdx])
	m.readIdx++
	return n, nil
}

func (m *mockUDP) Write(b []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(b))
	copy(cp, b)
	m.written = append(m.written, cp)
	return len(b), nil
}

func (m *mockUDP) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockUDP) SetReadDeadline(t time.Time) error { return nil }

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

type mockDecoder struct {
	lastData []byte
}

func (d *mockDecoder) Decode(data []byte, frameSize int, fec bool) ([]int16, error) {
	d.lastData = data
	pcm := make([]int16, frameSize)
	for i := range pcm {
		pcm[i] = int16(i)
	}
	return pcm, nil
}

type mockEncoder struct{}

func (e *mockEncoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	return []byte{0xF8, 0xFF, 0xFE}, nil // silence frame
}

type audioCapture struct {
	mu      sync.Mutex
	packets []capturedAudio
}

type capturedAudio struct {
	SSRC      uint32
	PCM       []int16
	Seq       uint16
	Timestamp uint32
}

func (c *audioCapture) HandleAudio(ssrc uint32, pcm []int16, seq uint16, ts uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]int16, len(pcm))
	copy(cp, pcm)
	c.packets = append(c.packets, capturedAudio{ssrc, cp, seq, ts})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSendAudio(t *testing.T) {
	udp := &mockUDP{}
	vc := NewVoiceConnection(VoiceConfig{})
	vc.SetUDPConn(udp)
	vc.ssrc = 42

	err := vc.SendAudio([]byte{0xF8, 0xFF, 0xFE})
	if err != nil {
		t.Fatal(err)
	}

	if len(udp.written) != 1 {
		t.Fatalf("written %d packets", len(udp.written))
	}

	// Parse the written RTP packet.
	pkt, err := ParseRTP(udp.written[0])
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Header.SSRC != 42 {
		t.Errorf("SSRC = %d", pkt.Header.SSRC)
	}
	if pkt.Header.Sequence != 0 {
		t.Errorf("Seq = %d", pkt.Header.Sequence)
	}
	if pkt.Header.PayloadType != 0x78 {
		t.Errorf("PT = %d", pkt.Header.PayloadType)
	}
	if !bytes.Equal(pkt.Payload, []byte{0xF8, 0xFF, 0xFE}) {
		t.Errorf("Payload = %v", pkt.Payload)
	}
}

func TestSendAudio_SequenceIncrements(t *testing.T) {
	udp := &mockUDP{}
	vc := NewVoiceConnection(VoiceConfig{})
	vc.SetUDPConn(udp)
	vc.ssrc = 1

	for i := 0; i < 3; i++ {
		vc.SendAudio([]byte{0x00})
	}

	if len(udp.written) != 3 {
		t.Fatalf("written %d", len(udp.written))
	}
	for i, raw := range udp.written {
		pkt, _ := ParseRTP(raw)
		if pkt.Header.Sequence != uint16(i) {
			t.Errorf("packet %d: seq = %d", i, pkt.Header.Sequence)
		}
		expectedTS := uint32(i) * 960
		if pkt.Header.Timestamp != expectedTS {
			t.Errorf("packet %d: ts = %d, want %d", i, pkt.Header.Timestamp, expectedTS)
		}
	}
}

func TestSendPCM(t *testing.T) {
	udp := &mockUDP{}
	enc := &mockEncoder{}
	vc := NewVoiceConnection(VoiceConfig{Encoder: enc})
	vc.SetUDPConn(udp)
	vc.ssrc = 1

	err := vc.SendPCM(make([]int16, 960))
	if err != nil {
		t.Fatal(err)
	}
	if len(udp.written) != 1 {
		t.Fatal("expected 1 packet")
	}
	pkt, _ := ParseRTP(udp.written[0])
	if !bytes.Equal(pkt.Payload, []byte{0xF8, 0xFF, 0xFE}) {
		t.Errorf("Payload = %v", pkt.Payload)
	}
}

func TestSendAudio_NoUDP(t *testing.T) {
	vc := NewVoiceConnection(VoiceConfig{})
	err := vc.SendAudio([]byte{0x00})
	if !errors.Is(err, ErrNoUDP) {
		t.Errorf("got %v", err)
	}
}

func TestSendPCM_NoEncoder(t *testing.T) {
	vc := NewVoiceConnection(VoiceConfig{})
	vc.SetUDPConn(&mockUDP{})
	err := vc.SendPCM(make([]int16, 960))
	if !errors.Is(err, ErrNoEncoder) {
		t.Errorf("got %v", err)
	}
}

func TestListenAudio(t *testing.T) {
	// Build an RTP packet from SSRC=100.
	sendPkt := &RTPPacket{
		Header: RTPHeader{
			Version:     2,
			PayloadType: 120,
			Sequence:    7,
			Timestamp:   4800,
			SSRC:        100,
		},
		Payload: []byte{0xF8, 0xFF, 0xFE},
	}

	udp := &mockUDP{readBuf: [][]byte{sendPkt.Marshal()}}
	capture := &audioCapture{}
	dec := &mockDecoder{}

	vc := NewVoiceConnection(VoiceConfig{
		Decoder: dec,
		Handler: capture,
	})
	vc.SetUDPConn(udp)
	vc.ssrc = 42 // our SSRC, different from sender

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := vc.ListenAudio(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()

	if len(capture.packets) != 1 {
		t.Fatalf("captured %d packets", len(capture.packets))
	}
	if capture.packets[0].SSRC != 100 {
		t.Errorf("SSRC = %d", capture.packets[0].SSRC)
	}
	if capture.packets[0].Seq != 7 {
		t.Errorf("Seq = %d", capture.packets[0].Seq)
	}
	if len(capture.packets[0].PCM) != 960 {
		t.Errorf("PCM len = %d", len(capture.packets[0].PCM))
	}
}

func TestListenAudio_SkipOwnSSRC(t *testing.T) {
	// Packet from our own SSRC should be skipped.
	sendPkt := &RTPPacket{
		Header: RTPHeader{Version: 2, PayloadType: 120, SSRC: 42},
		Payload: []byte{0x00},
	}
	udp := &mockUDP{readBuf: [][]byte{sendPkt.Marshal()}}
	capture := &audioCapture{}

	vc := NewVoiceConnection(VoiceConfig{
		Decoder: &mockDecoder{},
		Handler: capture,
	})
	vc.SetUDPConn(udp)
	vc.ssrc = 42

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	vc.ListenAudio(ctx)

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.packets) != 0 {
		t.Errorf("should skip own SSRC, got %d packets", len(capture.packets))
	}
}

func TestListenAudio_NoUDP(t *testing.T) {
	vc := NewVoiceConnection(VoiceConfig{Handler: &audioCapture{}})
	err := vc.ListenAudio(context.Background())
	if !errors.Is(err, ErrNoUDP) {
		t.Errorf("got %v", err)
	}
}

func TestListenAudio_NoHandler(t *testing.T) {
	vc := NewVoiceConnection(VoiceConfig{})
	vc.SetUDPConn(&mockUDP{})
	err := vc.ListenAudio(context.Background())
	if !errors.Is(err, ErrNoHandler) {
		t.Errorf("got %v", err)
	}
}

func TestVoiceConnection_Close(t *testing.T) {
	udp := &mockUDP{}
	vc := NewVoiceConnection(VoiceConfig{})
	vc.SetUDPConn(udp)

	if err := vc.Close(); err != nil {
		t.Fatal(err)
	}
	if vc.State() != StateDisconnected {
		t.Errorf("state = %d", vc.State())
	}
	if !udp.closed {
		t.Error("UDP should be closed")
	}
}

func TestRegisterReceiver(t *testing.T) {
	vc := NewVoiceConnection(VoiceConfig{})
	dec := &mockDecoder{}
	vc.RegisterReceiver(100, dec)

	got := vc.getReceiver(100)
	if got != dec {
		t.Error("registered decoder not returned")
	}
}

func TestAudioHandlerFunc(t *testing.T) {
	called := false
	f := AudioHandlerFunc(func(ssrc uint32, pcm []int16, seq uint16, ts uint32) {
		called = true
	})
	f.HandleAudio(1, nil, 0, 0)
	if !called {
		t.Error("not called")
	}
}

// ---------------------------------------------------------------------------
// DAVE integration: encrypt → send → receive → decrypt
// ---------------------------------------------------------------------------

type mockEncryptor struct{}

func (m *mockEncryptor) Encrypt(frame []byte) ([]byte, error) {
	// Simple XOR "encryption" for testing.
	out := make([]byte, len(frame))
	for i, b := range frame {
		out[i] = b ^ 0xFF
	}
	return out, nil
}

type mockDecryptor struct{}

func (m *mockDecryptor) Decrypt(senderID uint64, frame []byte) ([]byte, error) {
	out := make([]byte, len(frame))
	for i, b := range frame {
		out[i] = b ^ 0xFF
	}
	return out, nil
}

func TestSendReceive_WithDAVE(t *testing.T) {
	// Sender side: encrypt + send.
	senderUDP := &mockUDP{}
	sender := NewVoiceConnection(VoiceConfig{Encryptor: &mockEncryptor{}})
	sender.SetUDPConn(senderUDP)
	sender.ssrc = 1

	original := []byte{0xF8, 0xFF, 0xFE} // silence frame
	if err := sender.SendAudio(original); err != nil {
		t.Fatal(err)
	}

	// Receiver side: feed the sent packet, decrypt.
	capture := &audioCapture{}
	receiver := NewVoiceConnection(VoiceConfig{
		Decoder:   &mockDecoder{},
		Handler:   capture,
		Decryptor: &mockDecryptor{},
	})
	receiver.SetUDPConn(&mockUDP{readBuf: senderUDP.written})
	receiver.ssrc = 2

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	receiver.ListenAudio(ctx)

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.packets) != 1 {
		t.Fatalf("captured %d", len(capture.packets))
	}
	// The mock decoder was called, so we got audio through the pipeline.
	if capture.packets[0].SSRC != 1 {
		t.Errorf("SSRC = %d", capture.packets[0].SSRC)
	}
}
