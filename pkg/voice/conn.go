package voice

import (
	"context"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"discord/pkg/dave"
	dlog "discord/pkg/log"

)

// ---------------------------------------------------------------------------
// Interfaces — all external dependencies are injected for testability
// ---------------------------------------------------------------------------

// WSConn abstracts a WebSocket connection. In production this wraps a real
// WebSocket; in tests it can be a channel-based mock.
type WSConn interface {
	ReadMessage() (messageType int, data []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
}

// UDPConn abstracts the UDP socket used for RTP audio.
type UDPConn interface {
	Read(b []byte) (int, error)
	Write(b []byte) (int, error)
	Close() error
	SetReadDeadline(t time.Time) error
}

// AudioHandler is called for each decoded audio packet received.
// ssrc identifies the sender; pcm contains interleaved int16 samples.
type AudioHandler interface {
	HandleAudio(ssrc uint32, pcm []int16, seq uint16, timestamp uint32)
}

// OpusHandler is called for each raw Opus packet received (after transport +
// DAVE decryption, before Opus decode). If set, this is called INSTEAD of
// decoding to PCM and calling AudioHandler.
type OpusHandler interface {
	HandleOpus(ssrc uint32, opus []byte, seq uint16, timestamp uint32)
}

// OpusHandlerFunc adapts a function to OpusHandler.
type OpusHandlerFunc func(ssrc uint32, opus []byte, seq uint16, timestamp uint32)

func (f OpusHandlerFunc) HandleOpus(ssrc uint32, opus []byte, seq uint16, timestamp uint32) {
	f(ssrc, opus, seq, timestamp)
}

// AudioHandlerFunc adapts a function to AudioHandler.
type AudioHandlerFunc func(ssrc uint32, pcm []int16, seq uint16, timestamp uint32)

func (f AudioHandlerFunc) HandleAudio(ssrc uint32, pcm []int16, seq uint16, timestamp uint32) {
	f(ssrc, pcm, seq, timestamp)
}

// OpusDecoder decodes Opus packets to PCM. Matches pkg/opus.Decoder's API.
type OpusDecoder interface {
	Decode(data []byte, frameSize int, fec bool) ([]int16, error)
}

// OpusEncoder encodes PCM to Opus packets. Matches pkg/opus.Encoder's API.
type OpusEncoder interface {
	Encode(pcm []int16, frameSize int) ([]byte, error)
}

// FrameDecryptor decrypts a DAVE-encrypted frame. Nil means no E2EE.
type FrameDecryptor interface {
	Decrypt(senderID uint64, frame []byte) ([]byte, error)
}

// FrameEncryptor encrypts a frame for DAVE E2EE. Nil means no E2EE.
type FrameEncryptor interface {
	Encrypt(frame []byte) ([]byte, error)
}

// ---------------------------------------------------------------------------
// VoiceConfig — everything needed to connect to a voice session
// ---------------------------------------------------------------------------

// VoiceConfig contains the parameters for establishing a voice connection.
type VoiceConfig struct {
	GuildID   string
	ChannelID string
	UserID    string
	SessionID string
	Token     string
	Endpoint  string // voice server WebSocket endpoint (from Voice Server Update)

	// Audio pipeline.
	Decoder   OpusDecoder
	Encoder   OpusEncoder
	Handler   AudioHandler

	// OpusHandler receives raw Opus packets. If set, Decoder and Handler are not used.
	OpusHandler OpusHandler

	// E2EE (optional).
	Decryptor FrameDecryptor
	Encryptor FrameEncryptor

	// MaxDAVEVersion advertises DAVE protocol support in voice Identify.
	// 0 = no DAVE, 1 = DAVE v1. Default 1.
	MaxDAVEVersion *int

	// Logger (optional, defaults to silent).
	Logger dlog.Logger
}

// ---------------------------------------------------------------------------
// VoiceConnection — ties the voice gateway + UDP together
// ---------------------------------------------------------------------------

// State represents the connection lifecycle.
type State int

const (
	StateDisconnected State = iota
	StateConnecting
	StateReady
	StateSpeaking
	StateDisconnecting
)

// VoiceConnection manages a single voice session: gateway signaling + UDP audio.
type VoiceConnection struct {
	config VoiceConfig
	log    dlog.Logger

	state         State
	ssrc          uint32
	daveSession   *dave.Session
	daveReady     atomic.Bool
	secretKey     []byte       // transport encryption key from Session Description
	encryptMode   string      // encryption mode
	aead          cipher.AEAD // transport AEAD cipher
	wsSeqAck      atomic.Int64 // last voice WS sequence number (for heartbeat)

	ws    WSConn
	wsMu  sync.Mutex // gorilla/websocket doesn't support concurrent writes
	udp   UDPConn

	cancel context.CancelFunc

	// Receiving: maps sender SSRC → their Opus decoder.
	receivers map[uint32]OpusDecoder

	// SSRC → user ID mapping (populated from Speaking opcode).
	ssrcUsers sync.Map // uint32 → string

	// Sending state.
	sequence  uint16
	timestamp uint32
}

// NewVoiceConnection creates a connection (but does not connect yet).
func NewVoiceConnection(cfg VoiceConfig) *VoiceConnection {
	log := cfg.Logger
	if log == nil {
		log = dlog.Nop()
	}
	return &VoiceConnection{
		config:    cfg,
		log:       log,
		state:     StateDisconnected,
		receivers: make(map[uint32]OpusDecoder),
	}
}

// State returns the current connection state.
func (vc *VoiceConnection) State() State { return vc.state }

// SSRC returns the local SSRC assigned by the voice gateway.
func (vc *VoiceConnection) SSRC() uint32 { return vc.ssrc }

// SetWSConn injects a WebSocket connection (for testing or custom transports).
func (vc *VoiceConnection) SetWSConn(ws WSConn) { vc.ws = ws }

// SetUDPConn injects a UDP connection (for testing or custom transports).
func (vc *VoiceConnection) SetUDPConn(udp UDPConn) { vc.udp = udp }

// ---------------------------------------------------------------------------
// Receive loop
// ---------------------------------------------------------------------------

// ListenAudio reads RTP packets from UDP, decrypts transport + DAVE, decodes
// Opus, and delivers PCM to the AudioHandler.
// This matches discordgo's opusReceiver exactly.
func (vc *VoiceConnection) ListenAudio(ctx context.Context) error {
	if vc.udp == nil {
		return ErrNoUDP
	}
	if vc.config.Handler == nil && vc.config.OpusHandler == nil {
		return ErrNoHandler
	}

	recvbuf := make([]byte, 2048)
	nonceSize := 12 // default GCM nonce size
	if vc.aead != nil {
		nonceSize = vc.aead.NonceSize()
	}
	nonce := make([]byte, nonceSize)
	pktCount := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		vc.udp.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		rlen, err := vc.udp.Read(recvbuf)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			return err
		}

		// RTP v2 check.
		if rlen < 12 || (recvbuf[0]&0xC0) != 0x80 {
			continue
		}

		// Parse RTP header fields directly from the buffer.
		seq := binary.BigEndian.Uint16(recvbuf[2:4])
		timestamp := binary.BigEndian.Uint32(recvbuf[4:8])
		ssrc := binary.BigEndian.Uint32(recvbuf[8:12])

		// Skip our own SSRC.
		if ssrc == vc.ssrc {
			continue
		}

		var plain []byte

		if vc.aead != nil {
			// Transport decrypt (production path).
			cc := int(recvbuf[0] & 0x0F)
			hasExt := (recvbuf[0] & 0x10) != 0
			baseHeaderLen := 12 + (4 * cc)
			if rlen < baseHeaderLen {
				continue
			}

			aadLen := baseHeaderLen
			extPayloadBytes := 0
			if hasExt {
				if rlen < baseHeaderLen+4 {
					continue
				}
				extLenWords := int(binary.BigEndian.Uint16(recvbuf[baseHeaderLen+2 : baseHeaderLen+4]))
				extPayloadBytes = extLenWords * 4
				aadLen = baseHeaderLen + 4
			}

			if rlen < aadLen+4 {
				continue
			}

			payload := recvbuf[aadLen:rlen]
			if len(payload) < 4 {
				continue
			}
			nonceCounter := payload[len(payload)-4:]
			ciphertext := payload[:len(payload)-4]

			for i := range nonce {
				nonce[i] = 0
			}
			binary.LittleEndian.PutUint32(nonce[:4], binary.LittleEndian.Uint32(nonceCounter))

			var err error
			plain, err = vc.aead.Open(nil, nonce, ciphertext, recvbuf[:aadLen])
			if err != nil {
				continue
			}

			if extPayloadBytes > 0 {
				if len(plain) < extPayloadBytes {
					continue
				}
				plain = plain[extPayloadBytes:]
			}
		} else {
			// No transport encryption (test path): extract payload after RTP header.
			pkt, err := ParseRTP(recvbuf[:rlen])
			if err != nil {
				continue
			}
			plain = pkt.Payload
		}

		pktCount++
		if pktCount <= 3 {
			hasMagic := len(plain) > 2 && plain[len(plain)-2] == 0xFA && plain[len(plain)-1] == 0xFA
			vc.log.Info("transport decrypted",
				"ssrc", ssrc, "seq", seq, "len", len(plain),
				"dave_magic", hasMagic,
				"dave_active", vc.daveSession != nil && vc.daveSession.IsActive(),
				"user_id", vc.ssrcToUserID(ssrc),
				"first4", fmt.Sprintf("%x", plain[:min(4, len(plain))]),
			)
		}

		// DAVE E2E decrypt.
		if vc.daveSession != nil && vc.daveSession.IsActive() {
			hasMagic := len(plain) > 2 && plain[len(plain)-2] == 0xFA && plain[len(plain)-1] == 0xFA
			if hasMagic {
				userID := vc.ssrcToUserID(ssrc)
				if userID == "" {
					vc.log.Warn("dave frame but no user mapping", "ssrc", ssrc)
					continue
				}
				decrypted, err := vc.daveSession.DecryptFrame(userID, plain)
				if err != nil {
					vc.log.Warn("dave decrypt failed", "ssrc", ssrc, "user_id", userID, "len", len(plain), "error", err)
					continue
				}
				if pktCount <= 10 {
					vc.log.Info("dave decrypted", "ssrc", ssrc, "in_len", len(plain), "out_len", len(decrypted), "first4", fmt.Sprintf("%x", decrypted[:min(4, len(decrypted))]))
				}
				plain = decrypted
			}
			// No magic = silence/passthrough, decode as-is.
		}

		// Deliver raw Opus or decode to PCM.
		if vc.config.OpusHandler != nil {
			vc.config.OpusHandler.HandleOpus(ssrc, plain, seq, timestamp)
		} else {
			decoder := vc.getReceiver(ssrc)
			if decoder == nil {
				continue
			}
			pcm, err := decoder.Decode(plain, 960, false)
			if err != nil {
				continue
			}
			vc.config.Handler.HandleAudio(ssrc, pcm, seq, timestamp)
		}
	}
}

func (vc *VoiceConnection) getReceiver(ssrc uint32) OpusDecoder {
	dec, ok := vc.receivers[ssrc]
	if ok {
		return dec
	}
	// For new SSRCs, the caller should register decoders.
	// If a default decoder factory is available we'd use it here.
	// For now, use the connection's default decoder.
	if vc.config.Decoder != nil {
		vc.receivers[ssrc] = vc.config.Decoder
		return vc.config.Decoder
	}
	return nil
}

// RegisterReceiver maps an SSRC to a specific Opus decoder.
func (vc *VoiceConnection) RegisterReceiver(ssrc uint32, dec OpusDecoder) {
	vc.receivers[ssrc] = dec
}

// ---------------------------------------------------------------------------
// Send
// ---------------------------------------------------------------------------

// SendAudio sends a single Opus frame over UDP as an RTP packet.
func (vc *VoiceConnection) SendAudio(opusData []byte) error {
	if vc.udp == nil {
		return ErrNoUDP
	}

	payload := opusData

	// DAVE encryption.
	if vc.config.Encryptor != nil {
		encrypted, err := vc.config.Encryptor.Encrypt(payload)
		if err != nil {
			return err
		}
		payload = encrypted
	}

	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:     rtpVersion,
			PayloadType: 0x78, // Discord uses PT 120 for Opus
			Sequence:    vc.sequence,
			Timestamp:   vc.timestamp,
			SSRC:        vc.ssrc,
		},
		Payload: payload,
	}

	_, err := vc.udp.Write(pkt.Marshal())
	vc.sequence++
	vc.timestamp += 960 // 20ms at 48kHz
	return err
}

// SendPCM encodes PCM samples to Opus and sends the result.
func (vc *VoiceConnection) SendPCM(pcm []int16) error {
	if vc.config.Encoder == nil {
		return ErrNoEncoder
	}
	opusData, err := vc.config.Encoder.Encode(pcm, 960)
	if err != nil {
		return err
	}
	return vc.SendAudio(opusData)
}

// Close shuts down the voice connection.
func (vc *VoiceConnection) Close() error {
	vc.state = StateDisconnecting
	var firstErr error
	if vc.cancel != nil {
		vc.cancel()
	}
	if vc.udp != nil {
		if err := vc.udp.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if vc.ws != nil {
		if err := vc.ws.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	vc.state = StateDisconnected
	return firstErr
}

// wsWrite safely writes to the voice WebSocket with mutex protection.
func (vc *VoiceConnection) wsWrite(messageType int, data []byte) error {
	vc.wsMu.Lock()
	defer vc.wsMu.Unlock()
	return vc.ws.WriteMessage(messageType, data)
}

// ssrcToUserID looks up the user ID for a given SSRC (from Speaking events).
func (vc *VoiceConnection) ssrcToUserID(ssrc uint32) string {
	if v, ok := vc.ssrcUsers.Load(ssrc); ok {
		return v.(string)
	}
	return ""
}

// readVoiceWS reads voice gateway messages in the background, handling
// Speaking events (SSRC→userID mapping) and DAVE opcodes.
func (vc *VoiceConnection) readVoiceWS(ctx context.Context) {
	vc.log.Info("readVoiceWS started")
	defer vc.log.Info("readVoiceWS exited")
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgType, data, err := vc.ws.ReadMessage()
		if err != nil {
			vc.log.Error("voice WS read error", "error", err)
			return
		}

		// Binary = DAVE opcode.
		if msgType == 2 {
			vc.handleDAVEBinary(data)
			continue
		}

		msg, err := DecodeGatewayMsg(data)
		if err != nil {
			continue
		}
		if msg.Seq != nil {
			vc.wsSeqAck.Store(int64(*msg.Seq))
		}

		switch msg.Op {
		case OpcodeSpeaking:
			var sp SpeakingPayload
			json.Unmarshal(msg.Data, &sp)
			if sp.UserID != "" && sp.SSRC != 0 {
				vc.ssrcUsers.Store(sp.SSRC, sp.UserID)
				vc.log.Debug("ssrc mapped", "ssrc", sp.SSRC, "user_id", sp.UserID)
			}
		case 12: // CLIENT_CONNECT
			var data struct {
				UserID    string `json:"user_id"`
				AudioSSRC uint32 `json:"audio_ssrc"`
			}
			json.Unmarshal(msg.Data, &data)
			if data.UserID != "" && data.AudioSSRC != 0 {
				vc.ssrcUsers.Store(data.AudioSSRC, data.UserID)
				vc.log.Debug("ssrc mapped (client_connect)", "ssrc", data.AudioSSRC, "user_id", data.UserID)
			}
		case OpcodeDaveTransition:
			var payload struct {
				TransitionID    uint16 `json:"transition_id"`
				ProtocolVersion int    `json:"protocol_version"`
			}
			json.Unmarshal(msg.Data, &payload)
			if vc.daveSession != nil {
				vc.daveSession.HandlePrepareTransition(payload.TransitionID, payload.ProtocolVersion)
			}
		case OpcodeDaveExecute:
			var payload struct {
				TransitionID uint16 `json:"transition_id"`
			}
			json.Unmarshal(msg.Data, &payload)
			if vc.daveSession != nil {
				vc.daveSession.HandleExecuteTransition(payload.TransitionID)
			}
		case OpcodeDavePrepareEpoch:
			var payload struct {
				Epoch           uint64 `json:"epoch"`
				ProtocolVersion int    `json:"protocol_version"`
			}
			json.Unmarshal(msg.Data, &payload)
			if vc.daveSession != nil {
				kp, err := vc.daveSession.HandlePrepareEpoch(payload.Epoch)
				if err == nil {
					vc.sendDAVEKeyPackage(kp)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	ErrNoUDP     = errors.New("voice: UDP connection not set")
	ErrNoHandler = errors.New("voice: no audio handler configured")
	ErrNoEncoder = errors.New("voice: no encoder configured")
)

// isTimeout checks if an error is a timeout (net.Error).
func isTimeout(err error) bool {
	type timeoutErr interface {
		Timeout() bool
	}
	if te, ok := err.(timeoutErr); ok {
		return te.Timeout()
	}
	return false
}
