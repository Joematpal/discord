package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/joematpal/discord/pkg/voice"
)

// VoiceSession holds the info needed to connect to a voice gateway,
// returned by JoinVoice.
type VoiceSession struct {
	GuildID   string
	ChannelID string
	UserID    string
	SessionID string
	Token     string // voice server token (NOT bot token)
	Endpoint  string // voice server endpoint (no wss:// prefix)
}

// ToVoiceConfig converts a VoiceSession into a voice.VoiceConfig,
// applying the provided options.
func (vs *VoiceSession) ToVoiceConfig(opts ...func(*voice.VoiceConfig)) voice.VoiceConfig {
	cfg := voice.VoiceConfig{
		GuildID:   vs.GuildID,
		ChannelID: vs.ChannelID,
		UserID:    vs.UserID,
		SessionID: vs.SessionID,
		Token:     vs.Token,
		Endpoint:  vs.Endpoint,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// JoinVoice sends opcode 4 (Voice State Update) to join a voice channel,
// then waits for both VOICE_STATE_UPDATE and VOICE_SERVER_UPDATE events.
// Returns a VoiceSession with everything needed to call voice.Connect().
func (c *Client) JoinVoice(ctx context.Context, guildID, channelID string) (*VoiceSession, error) {
	// Set up channels to receive the two events.
	c.voiceMu.Lock()
	c.voiceStateCh = make(chan *VoiceState, 1)
	c.voiceServerCh = make(chan *VoiceServerUpdate, 1)
	c.voiceMu.Unlock()

	defer func() {
		c.voiceMu.Lock()
		c.voiceStateCh = nil
		c.voiceServerCh = nil
		c.voiceMu.Unlock()
	}()

	// Send Voice State Update (opcode 4).
	chID := channelID
	if err := c.sendOp(OpVoiceStateUpdate, VoiceStateUpdateSend{
		GuildID:   guildID,
		ChannelID: &chID,
		SelfMute:  false,
		SelfDeaf:  false,
	}); err != nil {
		return nil, fmt.Errorf("gateway: send voice state update: %w", err)
	}

	// Wait for both events with timeout.
	timeout := 10 * time.Second
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var vs *VoiceState
	var vsu *VoiceServerUpdate

	for vs == nil || vsu == nil {
		select {
		case <-tctx.Done():
			return nil, fmt.Errorf("gateway: voice join timed out waiting for events")
		case v := <-c.voiceStateCh:
			vs = v
		case v := <-c.voiceServerCh:
			vsu = v
		}
	}

	return &VoiceSession{
		GuildID:   guildID,
		ChannelID: channelID,
		UserID:    c.userID,
		SessionID: vs.SessionID,
		Token:     vsu.Token,
		Endpoint:  vsu.Endpoint,
	}, nil
}

// LeaveVoice sends opcode 4 with null channel_id to disconnect.
func (c *Client) LeaveVoice(guildID string) error {
	return c.sendOp(OpVoiceStateUpdate, VoiceStateUpdateSend{
		GuildID:   guildID,
		ChannelID: nil,
	})
}

// UserVoiceChannel returns the voice channel ID a user is currently in
// for the given guild, or "" if they're not in one.
func (c *Client) UserVoiceChannel(guildID, userID string) string {
	key := guildID + ":" + userID
	val, ok := c.voiceStates.Load(key)
	if !ok {
		return ""
	}
	vs := val.(*VoiceState)
	if vs.ChannelID == nil {
		return ""
	}
	return *vs.ChannelID
}
