package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock WebSocket
// ---------------------------------------------------------------------------

type mockWS struct {
	mu       sync.Mutex
	incoming [][]byte
	readIdx  int
	outgoing [][]byte
	closed   bool
}

func (m *mockWS) ReadMessage() (int, []byte, error) {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return 0, nil, fmt.Errorf("closed")
		}
		if m.readIdx < len(m.incoming) {
			data := m.incoming[m.readIdx]
			m.readIdx++
			m.mu.Unlock()
			return 1, data, nil
		}
		m.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
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

func (m *mockWS) queue(op int, d any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, _ := json.Marshal(d)
	p := Payload{Op: op, D: raw}
	data, _ := json.Marshal(p)
	m.incoming = append(m.incoming, data)
}

func (m *mockWS) queueDispatch(event string, d any, seq int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, _ := json.Marshal(d)
	p := Payload{Op: OpDispatch, D: raw, T: event, S: &seq}
	data, _ := json.Marshal(p)
	m.incoming = append(m.incoming, data)
}

func (m *mockWS) getOutgoing() []Payload {
	m.mu.Lock()
	defer m.mu.Unlock()
	var payloads []Payload
	for _, raw := range m.outgoing {
		var p Payload
		json.Unmarshal(raw, &p)
		payloads = append(payloads, p)
	}
	return payloads
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestConnect_Handshake(t *testing.T) {
	ws := &mockWS{}

	// Server sends Hello, then READY.
	ws.queue(OpHello, HelloData{HeartbeatInterval: 45000})
	seq := 1
	ws.queueDispatch("READY", ReadyData{
		V:         10,
		User:      ReadyUser{ID: "123456", Username: "testbot", Bot: true},
		SessionID: "sess-abc",
	}, seq)

	c := New(Config{Token: "my-bot-token", Intents: IntentGuilds | IntentGuildVoiceStates})
	c.SetWSConn(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Verify state.
	if c.UserID() != "123456" {
		t.Errorf("UserID = %q", c.UserID())
	}
	if c.SessionID() != "sess-abc" {
		t.Errorf("SessionID = %q", c.SessionID())
	}

	// Verify Identify was sent.
	msgs := ws.getOutgoing()
	foundIdentify := false
	for _, m := range msgs {
		if m.Op == OpIdentify {
			foundIdentify = true
			var id IdentifyData
			json.Unmarshal(m.D, &id)
			if id.Token != "Bot my-bot-token" {
				t.Errorf("Token = %q", id.Token)
			}
			if id.Intents != 129 {
				t.Errorf("Intents = %d", id.Intents)
			}
		}
	}
	if !foundIdentify {
		t.Error("Identify not sent")
	}
}

func TestConnect_DispatchHandlers(t *testing.T) {
	ws := &mockWS{}
	ws.queue(OpHello, HelloData{HeartbeatInterval: 45000})
	ws.queueDispatch("READY", ReadyData{
		User: ReadyUser{ID: "1"}, SessionID: "s",
	}, 1)

	c := New(Config{Token: "tok"})
	c.SetWSConn(ws)

	var received string
	var mu sync.Mutex
	c.On("GUILD_CREATE", func(event string, data json.RawMessage) {
		mu.Lock()
		received = event
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Connect(ctx)
	defer c.Close()

	// Queue a GUILD_CREATE after connection is established.
	ws.queueDispatch("GUILD_CREATE", map[string]any{"id": "999"}, 2)

	// Wait a bit for the read loop to process it.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if received != "GUILD_CREATE" {
		t.Errorf("handler not called, got %q", received)
	}
	mu.Unlock()
}

func TestConnect_Heartbeat(t *testing.T) {
	ws := &mockWS{}
	ws.queue(OpHello, HelloData{HeartbeatInterval: 50}) // 50ms for fast test
	ws.queueDispatch("READY", ReadyData{User: ReadyUser{ID: "1"}, SessionID: "s"}, 1)

	c := New(Config{Token: "tok"})
	c.SetWSConn(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	c.Connect(ctx)
	defer c.Close()

	// Wait for heartbeats.
	time.Sleep(200 * time.Millisecond)

	msgs := ws.getOutgoing()
	heartbeats := 0
	for _, m := range msgs {
		if m.Op == OpHeartbeat {
			heartbeats++
		}
	}
	if heartbeats == 0 {
		t.Error("no heartbeats sent")
	}
}

func TestConnect_NoDialerOrWS(t *testing.T) {
	c := New(Config{Token: "tok"})
	err := c.Connect(context.Background())
	if err == nil {
		t.Error("expected error with no dialer or WS")
	}
}

func TestJoinVoice(t *testing.T) {
	ws := &mockWS{}
	ws.queue(OpHello, HelloData{HeartbeatInterval: 45000})
	ws.queueDispatch("READY", ReadyData{
		User: ReadyUser{ID: "botuser123"}, SessionID: "main-sess",
	}, 1)

	c := New(Config{Token: "tok"})
	c.SetWSConn(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c.Connect(ctx)
	defer c.Close()

	// Simulate the server responses that come after opcode 4.
	go func() {
		time.Sleep(50 * time.Millisecond)
		ws.queueDispatch("VOICE_STATE_UPDATE", VoiceState{
			GuildID:   "guild1",
			UserID:    "botuser123",
			SessionID: "voice-sess-xyz",
		}, 2)
		ws.queueDispatch("VOICE_SERVER_UPDATE", VoiceServerUpdate{
			Token:    "voice-tok-abc",
			GuildID:  "guild1",
			Endpoint: "us-west-42.discord.media:443",
		}, 3)
	}()

	session, err := c.JoinVoice(ctx, "guild1", "channel1")
	if err != nil {
		t.Fatal(err)
	}

	if session.GuildID != "guild1" {
		t.Errorf("GuildID = %q", session.GuildID)
	}
	if session.ChannelID != "channel1" {
		t.Errorf("ChannelID = %q", session.ChannelID)
	}
	if session.UserID != "botuser123" {
		t.Errorf("UserID = %q", session.UserID)
	}
	if session.SessionID != "voice-sess-xyz" {
		t.Errorf("SessionID = %q", session.SessionID)
	}
	if session.Token != "voice-tok-abc" {
		t.Errorf("Token = %q", session.Token)
	}
	if session.Endpoint != "us-west-42.discord.media:443" {
		t.Errorf("Endpoint = %q", session.Endpoint)
	}

	// Verify opcode 4 was sent.
	msgs := ws.getOutgoing()
	foundVSU := false
	for _, m := range msgs {
		if m.Op == OpVoiceStateUpdate {
			foundVSU = true
			var vsu VoiceStateUpdateSend
			json.Unmarshal(m.D, &vsu)
			if vsu.GuildID != "guild1" {
				t.Errorf("guild_id = %q", vsu.GuildID)
			}
			if vsu.ChannelID == nil || *vsu.ChannelID != "channel1" {
				t.Errorf("channel_id = %v", vsu.ChannelID)
			}
		}
	}
	if !foundVSU {
		t.Error("opcode 4 not sent")
	}
}

func TestJoinVoice_Timeout(t *testing.T) {
	ws := &mockWS{}
	ws.queue(OpHello, HelloData{HeartbeatInterval: 45000})
	ws.queueDispatch("READY", ReadyData{User: ReadyUser{ID: "1"}, SessionID: "s"}, 1)

	c := New(Config{Token: "tok"})
	c.SetWSConn(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	c.Connect(ctx)
	defer c.Close()

	// Don't send voice events → should timeout.
	_, err := c.JoinVoice(ctx, "g", "c")
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestLeaveVoice(t *testing.T) {
	ws := &mockWS{}
	ws.queue(OpHello, HelloData{HeartbeatInterval: 45000})
	ws.queueDispatch("READY", ReadyData{User: ReadyUser{ID: "1"}, SessionID: "s"}, 1)

	c := New(Config{Token: "tok"})
	c.SetWSConn(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Connect(ctx)
	defer c.Close()

	if err := c.LeaveVoice("guild1"); err != nil {
		t.Fatal(err)
	}

	msgs := ws.getOutgoing()
	found := false
	for _, m := range msgs {
		if m.Op == OpVoiceStateUpdate {
			var vsu VoiceStateUpdateSend
			json.Unmarshal(m.D, &vsu)
			if vsu.ChannelID == nil {
				found = true
			}
		}
	}
	if !found {
		t.Error("leave voice (channel_id=null) not sent")
	}
}

func TestVoiceSession_ToVoiceConfig(t *testing.T) {
	vs := &VoiceSession{
		GuildID:   "g",
		ChannelID: "c",
		UserID:    "u",
		SessionID: "s",
		Token:     "t",
		Endpoint:  "ep",
	}
	cfg := vs.ToVoiceConfig()
	if cfg.GuildID != "g" || cfg.Token != "t" || cfg.Endpoint != "ep" {
		t.Errorf("config = %+v", cfg)
	}
}
