package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock WebSocket — simulates the voice gateway handshake
// ---------------------------------------------------------------------------

type mockWS struct {
	mu       sync.Mutex
	incoming [][]byte // messages to be read by the client
	readIdx  int
	outgoing [][]byte // messages written by the client
	closed   bool
}

func (m *mockWS) ReadMessage() (int, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.readIdx >= len(m.incoming) {
		// Simulate blocking; in real tests we'd use channels.
		m.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		m.mu.Lock()
		if m.closed {
			return 0, nil, fmt.Errorf("closed")
		}
	}
	data := m.incoming[m.readIdx]
	m.readIdx++
	return 1, data, nil
}

func (m *mockWS) WriteMessage(messageType int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.outgoing = append(m.outgoing, cp)
	return nil
}

func (m *mockWS) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockWS) queueMessage(op VoiceOpcode, payload any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, _ := EncodeGatewayMsg(op, payload)
	m.incoming = append(m.incoming, data)
}

// mockDiscoveryUDP simulates IP discovery then serves audio packets.
type mockDiscoveryUDP struct {
	mockUDP
	ssrc         uint32
	discoveryDone bool
}

func (m *mockDiscoveryUDP) Read(b []byte) (int, error) {
	m.mu.Lock()
	if !m.discoveryDone {
		m.discoveryDone = true
		m.mu.Unlock()
		resp := make([]byte, 74)
		binary.BigEndian.PutUint16(resp[0:2], 0x2)
		binary.BigEndian.PutUint16(resp[2:4], 70)
		binary.BigEndian.PutUint32(resp[4:8], m.ssrc)
		copy(resp[8:], "1.2.3.4")
		binary.BigEndian.PutUint16(resp[72:74], 12345)
		return copy(b, resp), nil
	}
	// After discovery, serve from readBuf.
	if m.readIdx < len(m.readBuf) {
		n := copy(b, m.readBuf[m.readIdx])
		m.readIdx++
		m.mu.Unlock()
		return n, nil
	}
	m.mu.Unlock()
	return 0, &timeoutError{}
}

// mockDialer returns pre-built mocks.
type mockDialer struct {
	ws  *mockWS
	udp *mockDiscoveryUDP
}

func (d *mockDialer) DialWS(ctx context.Context, url string) (WSConn, error) {
	return d.ws, nil
}
func (d *mockDialer) DialUDP(ctx context.Context, addr string) (UDPConn, error) {
	return d.udp, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestConnect_FullHandshake(t *testing.T) {
	ws := &mockWS{}
	udp := &mockDiscoveryUDP{ssrc: 42}

	// Queue the gateway messages the server would send.
	ws.queueMessage(OpcodeHello, HelloPayload{HeartbeatInterval: 41250})
	ws.queueMessage(OpcodeReady, ReadyPayload{
		SSRC:  42,
		IP:    "10.0.0.1",
		Port:  50000,
		Modes: []string{"xsalsa20_poly1305", "aead_xchacha20_poly1305_rtpsize"},
	})
	ws.queueMessage(OpcodeSessionDescription, SessionDescPayload{
		Mode:      "aead_xchacha20_poly1305_rtpsize",
		SecretKey: make([]byte, 32),
	})

	noDave := 0
	vc := NewVoiceConnection(VoiceConfig{
		GuildID:        "111",
		UserID:         "222",
		SessionID:      "sess-abc",
		Token:          "tok-xyz",
		Endpoint:       "voice.discord.gg",
		MaxDAVEVersion: &noDave,
	})

	dialer := &mockDialer{ws: ws, udp: udp}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := vc.Connect(ctx, dialer)
	if err != nil {
		t.Fatal(err)
	}
	defer vc.Close()

	// Verify state.
	if vc.State() != StateReady {
		t.Errorf("state = %d, want Ready", vc.State())
	}
	if vc.SSRC() != 42 {
		t.Errorf("SSRC = %d", vc.SSRC())
	}

	// Verify the client sent Identify and SelectProtocol.
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if len(ws.outgoing) < 2 {
		t.Fatalf("sent %d messages, want >= 2", len(ws.outgoing))
	}

	// Check Identify.
	var identMsg GatewayMessage
	json.Unmarshal(ws.outgoing[0], &identMsg)
	if identMsg.Op != OpcodeIdentify {
		t.Errorf("msg 0 op = %d, want Identify", identMsg.Op)
	}
	var ident IdentifyPayload
	json.Unmarshal(identMsg.Data, &ident)
	if ident.ServerID != "111" || ident.UserID != "222" {
		t.Errorf("Identify = %+v", ident)
	}

	// Check SelectProtocol.
	var selectMsg GatewayMessage
	json.Unmarshal(ws.outgoing[1], &selectMsg)
	if selectMsg.Op != OpcodeSelectProtocol {
		t.Errorf("msg 1 op = %d, want SelectProtocol", selectMsg.Op)
	}
	var sp SelectProtocolPayload
	json.Unmarshal(selectMsg.Data, &sp)
	if sp.Data.Address != "1.2.3.4" {
		t.Errorf("Address = %q", sp.Data.Address)
	}
	if sp.Data.Port != 12345 {
		t.Errorf("Port = %d", sp.Data.Port)
	}
	if sp.Data.Mode != "aead_xchacha20_poly1305_rtpsize" {
		t.Errorf("Mode = %q", sp.Data.Mode)
	}
}

func TestConnect_ListenAfterHandshake(t *testing.T) {
	ws := &mockWS{}

	// Build an RTP audio packet from SSRC=100.
	audioPkt := &RTPPacket{
		Header: RTPHeader{Version: 2, PayloadType: 120, Sequence: 1, Timestamp: 960, SSRC: 100},
		Payload: []byte{0xF8, 0xFF, 0xFE},
	}

	udp := &mockDiscoveryUDP{ssrc: 42}
	// After IP discovery, add an audio packet.
	udp.readBuf = [][]byte{audioPkt.Marshal()}

	ws.queueMessage(OpcodeHello, HelloPayload{HeartbeatInterval: 41250})
	ws.queueMessage(OpcodeReady, ReadyPayload{SSRC: 42, IP: "10.0.0.1", Port: 50000, Modes: []string{"xsalsa20_poly1305"}})
	ws.queueMessage(OpcodeSessionDescription, SessionDescPayload{Mode: "xsalsa20_poly1305"})

	capture := &audioCapture{}
	noDave := 0
	vc := NewVoiceConnection(VoiceConfig{
		GuildID:        "111",
		UserID:         "222",
		SessionID:      "sess",
		Token:          "tok",
		Handler:        capture,
		Decoder:        &mockDecoder{},
		MaxDAVEVersion: &noDave,
	})

	dialer := &mockDialer{ws: ws, udp: udp}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := vc.Connect(ctx, dialer); err != nil {
		t.Fatal(err)
	}
	defer vc.Close()

	// Override the UDP to serve audio after handshake.
	// The mockDiscoveryUDP consumed the IP discovery read; now set up audio reads.
	udp.readIdx = 0
	udp.readBuf = [][]byte{audioPkt.Marshal()}

	listenCtx, listenCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer listenCancel()
	vc.ListenAudio(listenCtx)

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.packets) == 0 {
		t.Error("expected to receive audio after handshake")
	}
}

func TestConnect_SetSpeaking(t *testing.T) {
	ws := &mockWS{}
	ws.queueMessage(OpcodeHello, HelloPayload{HeartbeatInterval: 41250})
	ws.queueMessage(OpcodeReady, ReadyPayload{SSRC: 42, IP: "10.0.0.1", Port: 50000, Modes: []string{"xsalsa20_poly1305"}})
	ws.queueMessage(OpcodeSessionDescription, SessionDescPayload{Mode: "xsalsa20_poly1305"})

	noDave := 0
	vc := NewVoiceConnection(VoiceConfig{GuildID: "1", UserID: "2", SessionID: "s", Token: "t", MaxDAVEVersion: &noDave})
	dialer := &mockDialer{ws: ws, udp: &mockDiscoveryUDP{ssrc: 42}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	vc.Connect(ctx, dialer)
	defer vc.Close()

	if err := vc.SetSpeaking(SpeakingMicrophone); err != nil {
		t.Fatal(err)
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()
	// Find the speaking message.
	found := false
	for _, raw := range ws.outgoing {
		var msg GatewayMessage
		json.Unmarshal(raw, &msg)
		if msg.Op == OpcodeSpeaking {
			found = true
			var sp SpeakingPayload
			json.Unmarshal(msg.Data, &sp)
			if sp.Speaking != SpeakingMicrophone {
				t.Errorf("Speaking = %d", sp.Speaking)
			}
			if sp.SSRC != 42 {
				t.Errorf("SSRC = %d", sp.SSRC)
			}
		}
	}
	if !found {
		t.Error("Speaking message not sent")
	}
}
