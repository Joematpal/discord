// Package gateway implements the Discord main Gateway WebSocket client.
// It handles authentication, heartbeating, dispatch event routing, and
// voice channel join/leave.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	dlog "discord/pkg/log"
)

// Gateway opcodes.
const (
	OpDispatch        = 0
	OpHeartbeat       = 1
	OpIdentify        = 2
	OpVoiceStateUpdate = 4
	OpResume          = 6
	OpReconnect       = 7
	OpInvalidSession  = 9
	OpHello           = 10
	OpHeartbeatACK    = 11
)

// Intents required for voice.
const (
	IntentGuilds          = 1 << 0
	IntentGuildVoiceStates = 1 << 7
)

// WSConn abstracts a WebSocket connection for testability.
type WSConn interface {
	ReadMessage() (messageType int, data []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
}

// WSDialer creates WebSocket connections for testability.
type WSDialer interface {
	Dial(ctx context.Context, url string) (WSConn, error)
}

// ---------------------------------------------------------------------------
// Payloads
// ---------------------------------------------------------------------------

// Payload is the gateway message envelope.
type Payload struct {
	Op int              `json:"op"`
	D  json.RawMessage  `json:"d"`
	S  *int             `json:"s,omitempty"`
	T  string           `json:"t,omitempty"`
}

// HelloData from opcode 10.
type HelloData struct {
	HeartbeatInterval int `json:"heartbeat_interval"` // ms
}

// IdentifyData for opcode 2.
type IdentifyData struct {
	Token      string             `json:"token"`
	Intents    int                `json:"intents"`
	Properties IdentifyProperties `json:"properties"`
}

// IdentifyProperties describes the connecting client.
type IdentifyProperties struct {
	OS      string `json:"os"`
	Browser string `json:"browser"`
	Device  string `json:"device"`
}

// ReadyData from t=READY.
type ReadyData struct {
	V                int             `json:"v"`
	User             ReadyUser       `json:"user"`
	SessionID        string          `json:"session_id"`
	ResumeGatewayURL string          `json:"resume_gateway_url"`
}

// ReadyUser is the bot user from READY.
type ReadyUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

// VoiceStateUpdateSend is opcode 4 (sent by client to join/leave voice).
type VoiceStateUpdateSend struct {
	GuildID   string  `json:"guild_id"`
	ChannelID *string `json:"channel_id"` // nil to disconnect
	SelfMute  bool    `json:"self_mute"`
	SelfDeaf  bool    `json:"self_deaf"`
}

// VoiceState from t=VOICE_STATE_UPDATE dispatch.
type VoiceState struct {
	GuildID   string  `json:"guild_id,omitempty"`
	ChannelID *string `json:"channel_id"`
	UserID    string  `json:"user_id"`
	SessionID string  `json:"session_id"`
	Deaf      bool    `json:"deaf"`
	Mute      bool    `json:"mute"`
	SelfDeaf  bool    `json:"self_deaf"`
	SelfMute  bool    `json:"self_mute"`
}

// VoiceServerUpdate from t=VOICE_SERVER_UPDATE dispatch.
type VoiceServerUpdate struct {
	Token    string `json:"token"`
	GuildID  string `json:"guild_id"`
	Endpoint string `json:"endpoint"`
}

// DispatchHandler is called for each dispatch event.
type DispatchHandler func(eventName string, data json.RawMessage)

// InteractionHandler processes an INTERACTION_CREATE and returns a response.
// If nil, interactions are ignored. The response is sent via the REST callback.
type InteractionHandler func(interaction json.RawMessage) (response json.RawMessage, err error)

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Config configures the gateway client.
type Config struct {
	Token     string    // Bot token (no "Bot " prefix needed, added automatically)
	Intents   int       // Gateway intents bitfield
	Dialer    WSDialer  // WebSocket dialer
	Logger    Logger // structured logger (defaults to slog.Default())
	OS        string    // Reported OS (default "linux")
	Browser   string    // Reported browser (default "discord")
	Device    string    // Reported device (default "discord")

	// InteractionHandler handles INTERACTION_CREATE events from the gateway.
	// The response is POSTed to the interaction callback URL via REST.
	InteractionHandler InteractionHandler
}

// Client is a Discord main gateway client.
type Client struct {
	config Config
	log    Logger
	ws     WSConn

	sessionID        string
	resumeGatewayURL string
	userID           string

	seq     atomic.Int64
	cancel  context.CancelFunc

	mu       sync.RWMutex
	handlers map[string][]DispatchHandler

	// Voice state cache: maps "guild_id:user_id" → VoiceState.
	voiceStates sync.Map

	// Voice join coordination.
	voiceMu        sync.Mutex
	voiceStateCh   chan *VoiceState
	voiceServerCh  chan *VoiceServerUpdate
}

// New creates a gateway client.
func New(cfg Config) *Client {
	if cfg.Intents == 0 {
		cfg.Intents = IntentGuilds | IntentGuildVoiceStates
	}
	if cfg.OS == "" {
		cfg.OS = "linux"
	}
	if cfg.Browser == "" {
		cfg.Browser = "discord"
	}
	if cfg.Device == "" {
		cfg.Device = "discord"
	}
	log := cfg.Logger
	if log == nil {
		log = dlog.Nop()
	}
	return &Client{
		config:   cfg,
		log:      log,
		handlers: make(map[string][]DispatchHandler),
	}
}

// On registers a handler for a dispatch event (e.g. "READY", "MESSAGE_CREATE").
func (c *Client) On(event string, h DispatchHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[event] = append(c.handlers[event], h)
}

// UserID returns the bot's user ID (available after READY).
func (c *Client) UserID() string { return c.userID }

// SessionID returns the session ID (available after READY).
func (c *Client) SessionID() string { return c.sessionID }

// SetWSConn injects a WebSocket (for testing).
func (c *Client) SetWSConn(ws WSConn) { c.ws = ws }

// ---------------------------------------------------------------------------
// Connect
// ---------------------------------------------------------------------------

// Connect performs the full gateway handshake:
//  1. Dial WebSocket (or use injected one)
//  2. Receive Hello → start heartbeat
//  3. Send Identify
//  4. Receive READY → store session info
//  5. Start read loop (dispatches events to handlers)
func (c *Client) Connect(ctx context.Context) error {
	if c.ws == nil {
		if c.config.Dialer == nil {
			return fmt.Errorf("gateway: no dialer or WebSocket connection provided")
		}
		ws, err := c.config.Dialer.Dial(ctx, "wss://gateway.discord.gg/?v=10&encoding=json")
		if err != nil {
			return fmt.Errorf("gateway: dial: %w", err)
		}
		c.ws = ws
	}

	// 1. Receive Hello.
	msg, err := c.readPayload()
	if err != nil {
		return fmt.Errorf("gateway: read hello: %w", err)
	}
	if msg.Op != OpHello {
		return fmt.Errorf("gateway: expected Hello (10), got op %d", msg.Op)
	}
	var hello HelloData
	json.Unmarshal(msg.D, &hello)
	c.log.Info("gateway hello received", "heartbeat_interval_ms", hello.HeartbeatInterval)

	// Start heartbeat.
	ctx, c.cancel = context.WithCancel(ctx)
	go c.heartbeatLoop(ctx, time.Duration(hello.HeartbeatInterval)*time.Millisecond)

	// 2. Send Identify.
	token := c.config.Token
	if len(token) < 4 || token[:4] != "Bot " {
		token = "Bot " + token
	}
	if err := c.sendOp(OpIdentify, IdentifyData{
		Token:   token,
		Intents: c.config.Intents,
		Properties: IdentifyProperties{
			OS:      c.config.OS,
			Browser: c.config.Browser,
			Device:  c.config.Device,
		},
	}); err != nil {
		return fmt.Errorf("gateway: send identify: %w", err)
	}

	// 3. Wait for READY.
	for {
		msg, err := c.readPayload()
		if err != nil {
			return fmt.Errorf("gateway: read ready: %w", err)
		}
		if msg.S != nil {
			c.seq.Store(int64(*msg.S))
		}
		if msg.Op == OpDispatch && msg.T == "READY" {
			var ready ReadyData
			json.Unmarshal(msg.D, &ready)
			c.sessionID = ready.SessionID
			c.resumeGatewayURL = ready.ResumeGatewayURL
			c.userID = ready.User.ID
			c.log.Info("gateway ready", "user_id", c.userID, "session_id", c.sessionID)
			break
		}
	}

	// 4. Start read loop in background.
	go c.readLoop(ctx)

	return nil
}

// Close shuts down the gateway connection.
func (c *Client) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.ws != nil {
		return c.ws.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Read loop — dispatches events to registered handlers
// ---------------------------------------------------------------------------

func (c *Client) readLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := c.readPayload()
		if err != nil {
			return
		}

		if msg.S != nil {
			c.seq.Store(int64(*msg.S))
		}

		if msg.Op == OpDispatch {
			c.dispatch(msg.T, msg.D)
		}
	}
}

func (c *Client) dispatch(event string, data json.RawMessage) {
	c.log.Debug("dispatch", "event", event, "data_len", len(data))

	// Internal voice coordination.
	switch event {
	case "GUILD_CREATE":
		// Cache initial voice states from guild payload.
		var guild struct {
			ID          string       `json:"id"`
			VoiceStates []VoiceState `json:"voice_states"`
		}
		json.Unmarshal(data, &guild)
		for i := range guild.VoiceStates {
			vs := &guild.VoiceStates[i]
			if vs.GuildID == "" {
				vs.GuildID = guild.ID
			}
			key := vs.GuildID + ":" + vs.UserID
			if vs.ChannelID != nil {
				c.voiceStates.Store(key, vs)
			} else {
				c.voiceStates.Delete(key)
			}
		}
		c.log.Debug("cached guild voice states", "guild_id", guild.ID, "count", len(guild.VoiceStates))

	case "VOICE_STATE_UPDATE":
		var vs VoiceState
		json.Unmarshal(data, &vs)

		// Update cache.
		key := vs.GuildID + ":" + vs.UserID
		if vs.ChannelID != nil {
			c.voiceStates.Store(key, &vs)
		} else {
			c.voiceStates.Delete(key)
		}

		// Notify JoinVoice if this is our own state.
		if vs.UserID == c.userID {
			c.voiceMu.Lock()
			ch := c.voiceStateCh
			c.voiceMu.Unlock()
			if ch != nil {
				select {
				case ch <- &vs:
				default:
				}
			}
		}
	case "VOICE_SERVER_UPDATE":
		var vsu VoiceServerUpdate
		json.Unmarshal(data, &vsu)
		c.voiceMu.Lock()
		ch := c.voiceServerCh
		c.voiceMu.Unlock()
		if ch != nil {
			select {
			case ch <- &vsu:
			default:
			}
		}
	}

	// Interaction handling via REST callback.
	if event == "INTERACTION_CREATE" && c.config.InteractionHandler != nil {
		go c.handleInteraction(data)
	}

	// User-registered handlers.
	c.mu.RLock()
	handlers := c.handlers[event]
	c.mu.RUnlock()
	for _, h := range handlers {
		h(event, data)
	}
}

// ---------------------------------------------------------------------------
// Heartbeat
// ---------------------------------------------------------------------------

func (c *Client) heartbeatLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seq := c.seq.Load()
			c.sendOp(OpHeartbeat, seq)
		}
	}
}

// ---------------------------------------------------------------------------
// Interaction REST callback
// ---------------------------------------------------------------------------

// handleInteraction calls the InteractionHandler and POSTs the response
// to Discord's interaction callback endpoint.
func (c *Client) handleInteraction(data json.RawMessage) {
	// Log the incoming interaction.
	var preview struct {
		ID   string `json:"id"`
		Type int    `json:"type"`
		Data *struct {
			Name string `json:"name"`
		} `json:"data,omitempty"`
	}
	json.Unmarshal(data, &preview)
	cmdName := ""
	if preview.Data != nil {
		cmdName = preview.Data.Name
	}
	c.log.Info("interaction received", "id", preview.ID, "type", preview.Type, "command", cmdName)

	resp, err := c.config.InteractionHandler(data)
	if err != nil {
		c.log.Error("interaction handler error", "id", preview.ID, "error", err)
		return
	}
	if resp == nil {
		c.log.Warn("interaction handler returned nil response", "id", preview.ID)
		return
	}

	// Extract interaction id and token from the raw data.
	var partial struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &partial); err != nil {
		c.log.Error("interaction unmarshal error", "error", err)
		return
	}

	url := fmt.Sprintf("https://discord.com/api/v10/interactions/%s/%s/callback", partial.ID, partial.Token)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(resp))
	if err != nil {
		c.log.Error("interaction callback request error", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		c.log.Error("interaction callback failed", "id", partial.ID, "error", err)
		return
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		body, _ := io.ReadAll(r.Body)
		c.log.Error("interaction callback rejected", "id", partial.ID, "status", r.StatusCode, "body", string(body))
	} else {
		c.log.Info("interaction response sent", "id", partial.ID, "status", r.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (c *Client) readPayload() (*Payload, error) {
	_, data, err := c.ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	var p Payload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) sendOp(op int, d any) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	p := Payload{Op: op, D: raw}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return c.ws.WriteMessage(1, data)
}
