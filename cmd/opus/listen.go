package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/joematpal/discord/pkg/gateway"
	"github.com/joematpal/discord/pkg/opus"
	"github.com/joematpal/discord/pkg/voice"

	"github.com/gorilla/websocket"
	"github.com/urfave/cli/v3"
)

func listenCmd() *cli.Command {
	return &cli.Command{
		Name:  "listen",
		Usage: "Join a voice channel and write received audio as WAV to stdout",
		Description: `Connects to the Discord gateway, joins the specified voice channel,
decodes incoming Opus audio to PCM, and writes a WAV stream to stdout.

Pipe to ffplay for live playback:
  opus listen --token BOT_TOKEN --guild 123 --channel 456 | ffplay -f wav -i pipe:0

Or save to file:
  opus listen --token BOT_TOKEN --guild 123 --channel 456 > recording.wav

Press Ctrl-C to stop recording.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "token",
				Aliases:  []string{"t"},
				Usage:    "bot token",
				Required: true,
				Sources:  cli.EnvVars("DISCORD_TOKEN"),
			},
			&cli.StringFlag{
				Name:     "guild",
				Aliases:  []string{"g"},
				Usage:    "guild (server) ID",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "channel",
				Aliases:  []string{"c"},
				Usage:    "voice channel ID",
				Required: true,
			},
			&cli.IntFlag{
				Name:  "rate",
				Value: 48000,
				Usage: "sample rate",
			},
		},
		Action: listenAction,
	}
}

func listenAction(ctx context.Context, cmd *cli.Command) error {
	token := cmd.String("token")
	guildID := cmd.String("guild")
	channelID := cmd.String("channel")
	sampleRate := int(cmd.Int("rate"))

	fmt.Fprintf(os.Stderr, "connecting to gateway...\n")

	// 1. Connect to the main gateway.
	gw := gateway.New(gateway.Config{
		Token:   token,
		Intents: gateway.IntentGuilds | gateway.IntentGuildVoiceStates,
		Dialer:  &wsDialerImpl{},
	})

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := gw.Connect(ctx); err != nil {
		return fmt.Errorf("gateway connect: %w", err)
	}
	defer gw.Close()

	fmt.Fprintf(os.Stderr, "logged in as %s, joining voice...\n", gw.UserID())

	// 2. Join voice channel.
	session, err := gw.JoinVoice(ctx, guildID, channelID)
	if err != nil {
		return fmt.Errorf("join voice: %w", err)
	}
	defer gw.LeaveVoice(guildID)

	fmt.Fprintf(os.Stderr, "voice session: endpoint=%s ssrc pending...\n", session.Endpoint)

	// 3. Create opus decoder.
	dec, err := opus.NewDecoder(sampleRate, 1)
	if err != nil {
		return fmt.Errorf("create decoder: %w", err)
	}

	// 4. Set up WAV writer — write header with placeholder size, fix on exit.
	wavOut := &wavStreamWriter{
		w:          os.Stdout,
		sampleRate: uint32(sampleRate),
		channels:   1,
	}
	if err := wavOut.writeHeader(); err != nil {
		return fmt.Errorf("write WAV header: %w", err)
	}

	// 5. Connect to voice gateway.
	vcfg := session.ToVoiceConfig(func(cfg *voice.VoiceConfig) {
		cfg.Decoder = dec
		cfg.Handler = voice.AudioHandlerFunc(func(ssrc uint32, pcm []int16, seq uint16, ts uint32) {
			wavOut.writeSamples(pcm)
		})
	})

	vc := voice.NewVoiceConnection(vcfg)
	if err := vc.Connect(ctx, &voiceDialerImpl{}); err != nil {
		return fmt.Errorf("voice connect: %w", err)
	}
	defer vc.Close()

	fmt.Fprintf(os.Stderr, "listening (Ctrl-C to stop)...\n")

	// 6. Listen until cancelled.
	err = vc.ListenAudio(ctx)

	// 7. Finalize WAV (update sizes if stdout is seekable).
	wavOut.finalize()

	fmt.Fprintf(os.Stderr, "done: %d samples written\n", wavOut.totalSamples)
	return err
}

// ---------------------------------------------------------------------------
// WAV stream writer — writes a WAV header up front, streams samples
// ---------------------------------------------------------------------------

type wavStreamWriter struct {
	w            *os.File
	sampleRate   uint32
	channels     uint16
	mu           sync.Mutex
	totalSamples int64
}

func (ws *wavStreamWriter) writeHeader() error {
	// Write a WAV header with max-size placeholders.
	// If stdout is a file, we'll seek back and fix the sizes.
	var buf [44]byte
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], 0xFFFFFFFF) // placeholder
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], ws.channels)
	binary.LittleEndian.PutUint32(buf[24:28], ws.sampleRate)
	blockAlign := ws.channels * 2
	binary.LittleEndian.PutUint32(buf[28:32], ws.sampleRate*uint32(blockAlign))
	binary.LittleEndian.PutUint16(buf[32:34], blockAlign)
	binary.LittleEndian.PutUint16(buf[34:36], 16) // bits per sample
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], 0xFFFFFFFF) // placeholder
	_, err := ws.w.Write(buf[:])
	return err
}

func (ws *wavStreamWriter) writeSamples(pcm []int16) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	binary.Write(ws.w, binary.LittleEndian, pcm)
	ws.totalSamples += int64(len(pcm))
}

func (ws *wavStreamWriter) finalize() {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	dataSize := uint32(ws.totalSamples * 2) // 16-bit samples
	// Try to seek back and fix the header sizes.
	if _, err := ws.w.Seek(4, 0); err != nil {
		return // pipe, can't seek — that's fine
	}
	binary.Write(ws.w, binary.LittleEndian, uint32(36+dataSize))
	ws.w.Seek(40, 0)
	binary.Write(ws.w, binary.LittleEndian, dataSize)
}

// ---------------------------------------------------------------------------
// Production dialers using gorilla/websocket
// ---------------------------------------------------------------------------

// wsDialerImpl implements gateway.WSDialer using gorilla/websocket.
type wsDialerImpl struct{}

func (d *wsDialerImpl) Dial(ctx context.Context, url string) (gateway.WSConn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// voiceDialerImpl implements voice.Dialer using gorilla/websocket + net.
type voiceDialerImpl struct{}

func (d *voiceDialerImpl) DialWS(ctx context.Context, url string) (voice.WSConn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (d *voiceDialerImpl) DialUDP(ctx context.Context, addr string) (voice.UDPConn, error) {
	nd := &voice.NetDialer{}
	return nd.DialUDP(ctx, addr)
}
