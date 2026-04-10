// disrecord — a Discord bot that joins voice channels and records audio.
//
// Commands:
//
//	/join   — bot joins your current voice channel
//	/leave  — bot leaves the voice channel
//	/ping   — responds with pong + latency info
//
// Usage:
//
//	export DISCORD_TOKEN=your_bot_token
//	export DISCORD_APP_ID=your_application_id
//	go run ./cmd/disrecord/
//
// Recorded audio is saved as WAV files in the current directory.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joematpal/discord/pkg/discord"
	"github.com/joematpal/discord/pkg/gateway"
	"github.com/joematpal/discord/pkg/opus"
	"github.com/joematpal/discord/pkg/voice"

	"github.com/gorilla/websocket"
)

func main() {
	loadEnv(".env")

	token := os.Getenv("DISCORD_TOKEN")
	appID := os.Getenv("DISCORD_APP_ID")

	if token == "" || appID == "" {
		fmt.Fprintln(os.Stderr, "set DISCORD_TOKEN and DISCORD_APP_ID (in .env or environment)")
		os.Exit(1)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bot := newBot(token, appID, log)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := bot.run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Bot
// ---------------------------------------------------------------------------

type bot struct {
	token string
	appID string
	log   gateway.Logger

	gw *gateway.Client

	mu       sync.Mutex
	sessions map[string]*voiceSession // guild_id → active session
}

type voiceSession struct {
	vc       *voice.VoiceConnection
	cancel   context.CancelFunc
	ogg      *voice.OggOpusWriter
	recorder *recorder
	filename string
}

func newBot(token, appID string, log gateway.Logger) *bot {
	return &bot{
		token:    token,
		appID:    appID,
		log:      log,
		sessions: make(map[string]*voiceSession),
	}
}

func (b *bot) run(ctx context.Context) error {
	// 1. Register slash commands.
	b.log.Info("registering commands...")
	if err := b.registerCommands(); err != nil {
		return fmt.Errorf("register commands: %w", err)
	}

	// 2. Connect to gateway.
	b.log.Info("connecting to gateway...")
	b.gw = gateway.New(gateway.Config{
		Token:              b.token,
		Intents:            gateway.IntentGuilds | gateway.IntentGuildVoiceStates,
		Dialer:             &wsDialer{},
		Logger:             b.log,
		InteractionHandler: b.handleInteraction,
	})

	if err := b.gw.Connect(ctx); err != nil {
		return fmt.Errorf("gateway connect: %w", err)
	}
	defer b.gw.Close()

	b.log.Info("bot online", "user_id", b.gw.UserID())

	// 3. Wait for shutdown.
	<-ctx.Done()

	// 4. Cleanup: leave all voice sessions.
	b.mu.Lock()
	for guildID, sess := range b.sessions {
		sess.cancel()
		sess.vc.Close()
		if sess.ogg != nil {
			sess.ogg.Close()
		}
		if sess.recorder != nil {
			sess.recorder.close()
		}
		b.log.Info("shutdown: left guild", "guild_id", guildID, "file", sess.filename)
	}
	b.mu.Unlock()

	return nil
}

// ---------------------------------------------------------------------------
// Command registration
// ---------------------------------------------------------------------------

func (b *bot) registerCommands() error {
	appIDSnowflake, _ := discord.ParseSnowflake(b.appID)
	client := discord.NewClient(discord.ClientConfig{
		ApplicationID: appIDSnowflake,
		BotToken:      b.token,
	})

	cmds := []discord.Command{
		*discord.NewSlashCommand("join", "Join your voice channel and start recording"),
		*discord.NewSlashCommand("leave", "Leave the voice channel and save recording"),
		*discord.NewSlashCommand("record", "Join your voice channel and start recording"),
		*discord.NewSlashCommand("ping", "Check if the bot is alive"),
	}

	guildID := os.Getenv("DISCORD_GUILD_ID")

	if guildID != "" {
		// Guild commands appear instantly.
		guildSnowflake, _ := discord.ParseSnowflake(guildID)
		registered, err := client.BulkOverwriteGuildCommands(context.Background(), guildSnowflake, cmds)
		if err != nil {
			b.log.Error("guild command registration failed", "guild_id", guildID, "error", err)
			return err
		}
		for _, cmd := range registered {
			b.log.Info("guild command registered", "guild_id", guildID, "name", cmd.Name, "id", cmd.ID)
		}

		// Clear stale global commands so old ones don't linger.
		old, _ := client.ListGlobalCommands(context.Background())
		if len(old) > 0 {
			b.log.Info("clearing stale global commands", "count", len(old))
			for _, cmd := range old {
				b.log.Info("removing global command", "name", cmd.Name, "id", cmd.ID)
			}
			client.BulkOverwriteGlobalCommands(context.Background(), []discord.Command{})
		}
	} else {
		// Global commands take up to an hour to propagate.
		registered, err := client.BulkOverwriteGlobalCommands(context.Background(), cmds)
		if err != nil {
			b.log.Error("global command registration failed", "error", err)
			return err
		}
		for _, cmd := range registered {
			b.log.Info("global command registered", "name", cmd.Name, "id", cmd.ID)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Interaction handler (gateway-based)
// ---------------------------------------------------------------------------

func (b *bot) handleInteraction(raw json.RawMessage) (json.RawMessage, error) {
	var interaction discord.Interaction
	if err := json.Unmarshal(raw, &interaction); err != nil {
		b.log.Error("interaction unmarshal failed", "error", err)
		return nil, err
	}

	b.log.Info("handling interaction",
		"id", interaction.ID,
		"type", interaction.Type,
		"guild_id", interaction.GuildID,
		"channel_id", interaction.ChannelID,
	)

	// PING → PONG (type 1).
	if interaction.Type == discord.InteractionTypePing {
		b.log.Debug("responding to PING with PONG")
		return json.Marshal(discord.Pong())
	}

	if interaction.Type != discord.InteractionTypeCommand || interaction.Data == nil {
		b.log.Warn("ignoring non-command interaction", "type", interaction.Type)
		return nil, nil
	}

	b.log.Info("command invoked", "command", interaction.Data.Name, "user", interaction.Member)

	var resp *discord.InteractionResponse
	switch interaction.Data.Name {
	case "ping":
		resp = discord.MessageResponse("Pong!")
	case "join", "record":
		resp = b.handleJoin(&interaction)
	case "leave":
		resp = b.handleLeave(&interaction)
	default:
		b.log.Warn("unknown command", "name", interaction.Data.Name)
		resp = discord.EphemeralResponse(fmt.Sprintf("Unknown command: %s", interaction.Data.Name))
	}

	return json.Marshal(resp)
}

func (b *bot) handleJoin(i *discord.Interaction) *discord.InteractionResponse {
	guildID := i.GuildID.String()
	if guildID == "0" {
		return discord.EphemeralResponse("This command only works in a server.")
	}

	// Look up the invoking user's current voice channel.
	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID.String()
	} else if i.User != nil {
		userID = i.User.ID.String()
	}
	channelID := b.gw.UserVoiceChannel(guildID, userID)
	if channelID == "" {
		return discord.EphemeralResponse("You need to be in a voice channel first.")
	}

	// Check if already recording in this guild.
	b.mu.Lock()
	if _, ok := b.sessions[guildID]; ok {
		b.mu.Unlock()
		return discord.EphemeralResponse("Already recording. Use /leave first.")
	}
	b.mu.Unlock()

	b.log.Info("join requested", "guild_id", guildID, "channel_id", channelID, "user_id", userID)
	go b.joinVoice(guildID, channelID)

	return discord.MessageResponse(fmt.Sprintf("Joining <#%s> and starting recording...", channelID))
}

func (b *bot) joinVoice(guildID, channelID string) {
	ctx, cancel := context.WithCancel(context.Background())

	session, err := b.gw.JoinVoice(ctx, guildID, channelID)
	if err != nil {
		cancel()
		b.log.Error("join voice failed", "guild_id", guildID, "error", err)
		return
	}
	b.log.Info("voice session acquired", "guild_id", guildID, "endpoint", session.Endpoint)

	outDir := os.Getenv("OUTPUT_DIR")
	if outDir == "" {
		outDir = "./recordings"
	}
	os.MkdirAll(outDir, 0o755)

	outputFormat := os.Getenv("OUTPUT_FORMAT")
	if outputFormat == "" {
		outputFormat = "ogg"
	}

	ts := time.Now().Format("20060102_150405")
	var sess *voiceSession

	switch outputFormat {
	case "wav":
		// WAV path: Opus decode → PCM → WAV file.
		filename := fmt.Sprintf("%s/recording_%s_%s.wav", outDir, guildID, ts)
		rec, err := newRecorder(filename, 48000, 1)
		if err != nil {
			cancel()
			b.log.Error("create recorder failed", "error", err)
			return
		}
		dec, err := opus.NewDecoder(48000, 1)
		if err != nil {
			cancel()
			rec.close()
			b.log.Error("create decoder failed", "error", err)
			return
		}
		vcfg := session.ToVoiceConfig(func(cfg *voice.VoiceConfig) {
			cfg.Logger = b.log
			cfg.Decoder = dec
			cfg.Handler = voice.AudioHandlerFunc(func(ssrc uint32, pcm []int16, seq uint16, ts uint32) {
				rec.write(pcm)
			})
		})
		vc := voice.NewVoiceConnection(vcfg)
		if err := vc.Connect(ctx, &voiceDialer{}); err != nil {
			cancel()
			rec.close()
			b.log.Error("voice connect failed", "guild_id", guildID, "error", err)
			return
		}
		sess = &voiceSession{vc: vc, cancel: cancel, recorder: rec, filename: filename}

	default: // "ogg"
		// OGG path: raw Opus packets → OGG/Opus file.
		filename := fmt.Sprintf("%s/recording_%s_%s.ogg", outDir, guildID, ts)
		oggWriter, err := voice.NewOggOpusFileWriter(filename, 48000, 1)
		if err != nil {
			cancel()
			b.log.Error("create ogg writer failed", "error", err)
			return
		}
		vcfg := session.ToVoiceConfig(func(cfg *voice.VoiceConfig) {
			cfg.Logger = b.log
			cfg.OpusHandler = voice.OpusHandlerFunc(func(ssrc uint32, opusData []byte, seq uint16, ts uint32) {
				oggWriter.WritePacket(opusData, 960)
			})
		})
		vc := voice.NewVoiceConnection(vcfg)
		if err := vc.Connect(ctx, &voiceDialer{}); err != nil {
			cancel()
			oggWriter.Close()
			b.log.Error("voice connect failed", "guild_id", guildID, "error", err)
			return
		}
		sess = &voiceSession{vc: vc, cancel: cancel, ogg: oggWriter, filename: filename}
	}

	b.mu.Lock()
	b.sessions[guildID] = sess
	b.mu.Unlock()

	b.log.Info("recording started", "guild_id", guildID, "format", outputFormat, "file", sess.filename)

	// Listen in background.
	go func() {
		if err := sess.vc.ListenAudio(ctx); err != nil {
			b.log.Error("listen audio ended", "guild_id", guildID, "error", err)
		}
	}()
}

func (b *bot) handleLeave(i *discord.Interaction) *discord.InteractionResponse {
	guildID := i.GuildID.String()

	b.mu.Lock()
	sess, ok := b.sessions[guildID]
	if !ok {
		b.mu.Unlock()
		return discord.EphemeralResponse("Not in a voice channel.")
	}
	delete(b.sessions, guildID)
	b.mu.Unlock()

	sess.cancel()
	sess.vc.Close()
	if sess.ogg != nil {
		sess.ogg.Close()
	}
	if sess.recorder != nil {
		sess.recorder.close()
	}

	b.gw.LeaveVoice(guildID)

	return discord.MessageResponse(fmt.Sprintf("Left voice channel. Recording saved to `%s`.", sess.filename))
}

// ---------------------------------------------------------------------------
// WAV recorder — writes PCM samples to a WAV file
// ---------------------------------------------------------------------------

type recorder struct {
	mu           sync.Mutex
	f            *os.File
	filename     string
	totalSamples int64
	sampleRate   uint32
	channels     uint16
}

func newRecorder(filename string, sampleRate uint32, channels uint16) (*recorder, error) {
	f, err := os.Create(filename)
	if err != nil {
		return nil, err
	}
	r := &recorder{
		f:          f,
		filename:   filename,
		sampleRate: sampleRate,
		channels:   channels,
	}
	// Write placeholder WAV header.
	if err := r.writeHeader(); err != nil {
		f.Close()
		return nil, err
	}
	return r, nil
}

func (r *recorder) writeHeader() error {
	var buf [44]byte
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], 0) // placeholder
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], r.channels)
	binary.LittleEndian.PutUint32(buf[24:28], r.sampleRate)
	blockAlign := r.channels * 2
	binary.LittleEndian.PutUint32(buf[28:32], r.sampleRate*uint32(blockAlign))
	binary.LittleEndian.PutUint16(buf[32:34], blockAlign)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], 0) // placeholder
	_, err := r.f.Write(buf[:])
	return err
}

func (r *recorder) write(pcm []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()
	binary.Write(r.f, binary.LittleEndian, pcm)
	r.totalSamples += int64(len(pcm))
}

func (r *recorder) close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	dataSize := uint32(r.totalSamples * 2)
	// Fix WAV header sizes.
	r.f.Seek(4, io.SeekStart)
	binary.Write(r.f, binary.LittleEndian, uint32(36+dataSize))
	r.f.Seek(40, io.SeekStart)
	binary.Write(r.f, binary.LittleEndian, dataSize)
	r.f.Close()
}

// ---------------------------------------------------------------------------
// Dialers (gorilla/websocket + net)
// ---------------------------------------------------------------------------

type wsDialer struct{}

func (d *wsDialer) Dial(ctx context.Context, url string) (gateway.WSConn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	return conn, err
}

type voiceDialer struct{}

func (d *voiceDialer) DialWS(ctx context.Context, url string) (voice.WSConn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	return conn, err
}

func (d *voiceDialer) DialUDP(ctx context.Context, addr string) (voice.UDPConn, error) {
	return (&voice.NetDialer{}).DialUDP(ctx, addr)
}

// ---------------------------------------------------------------------------
// REST helper — for editing interaction responses after deferred reply
// ---------------------------------------------------------------------------

func editInteractionResponse(appID, token, content string) {
	body, _ := json.Marshal(map[string]string{"content": content})
	url := fmt.Sprintf("https://discord.com/api/v10/webhooks/%s/%s/messages/@original", appID, token)
	req, _ := http.NewRequest(http.MethodPatch, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// .env loader — simple key=value parser, does not override existing env vars
// ---------------------------------------------------------------------------

func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // missing .env is fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// Strip inline comments.
		if idx := strings.Index(v, " #"); idx >= 0 {
			v = strings.TrimSpace(v[:idx])
		}
		// Don't override existing env vars.
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}
