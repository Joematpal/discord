package voice

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/joematpal/discord/pkg/dave"

	"golang.org/x/crypto/chacha20poly1305"
)

// Connect performs the voice gateway handshake and UDP setup:
//  1. Receive Hello → start heartbeat
//  2. Send Identify
//  3. Receive Ready → get SSRC, UDP endpoint
//  4. UDP IP Discovery
//  5. Send Select Protocol
//  6. Receive Session Description → get secret key
//  7. Start receive loop
//
// The WSConn and UDPConn can be pre-injected (for testing) or will be
// created via the provided Dialer.
func (vc *VoiceConnection) Connect(ctx context.Context, dialer Dialer) error {
	vc.state = StateConnecting

	// 1. Dial voice gateway WebSocket (if not already injected).
	if vc.ws == nil {
		wsURL := "wss://" + vc.config.Endpoint + "/?v=8"
		vc.log.Info("dialing voice gateway", "url", wsURL)
		ws, err := dialer.DialWS(ctx, wsURL)
		if err != nil {
			return fmt.Errorf("voice: dial WS: %w", err)
		}
		vc.ws = ws
		vc.log.Info("voice gateway connected")
	}

	// 2. Read Hello (opcode 8).
	hello, err := vc.readOp(OpcodeHello)
	if err != nil {
		return fmt.Errorf("voice: read hello: %w", err)
	}
	var helloPayload HelloPayload
	if err := json.Unmarshal(hello.Data, &helloPayload); err != nil {
		return fmt.Errorf("voice: decode hello: %w", err)
	}
	vc.log.Info("voice hello received", "heartbeat_interval_ms", helloPayload.HeartbeatInterval)

	// Start heartbeat loop.
	ctx, vc.cancel = context.WithCancel(ctx)
	go vc.heartbeatLoop(ctx, time.Duration(helloPayload.HeartbeatInterval)*time.Millisecond)

	// 3. Send Identify (opcode 0).
	daveVersion := 1
	if vc.config.MaxDAVEVersion != nil {
		daveVersion = *vc.config.MaxDAVEVersion
	}
	vc.log.Info("sending voice identify", "dave_version", daveVersion)
	identifyData, _ := EncodeGatewayMsg(OpcodeIdentify, IdentifyPayload{
		ServerID:               vc.config.GuildID,
		UserID:                 vc.config.UserID,
		SessionID:              vc.config.SessionID,
		Token:                  vc.config.Token,
		MaxDaveProtocolVersion: daveVersion,
	})
	if err := vc.wsWrite(1, identifyData); err != nil {
		return fmt.Errorf("voice: send identify: %w", err)
	}

	// 4. Receive Ready (opcode 2).
	readyMsg, err := vc.readOp(OpcodeReady)
	if err != nil {
		return fmt.Errorf("voice: read ready: %w", err)
	}
	var ready ReadyPayload
	if err := json.Unmarshal(readyMsg.Data, &ready); err != nil {
		return fmt.Errorf("voice: decode ready: %w", err)
	}
	vc.ssrc = ready.SSRC
	vc.log.Info("voice ready", "ssrc", ready.SSRC, "ip", ready.IP, "port", ready.Port, "modes", ready.Modes)

	// 5. UDP IP Discovery.
	if vc.udp == nil {
		addr := fmt.Sprintf("%s:%d", ready.IP, ready.Port)
		udp, err := dialer.DialUDP(ctx, addr)
		if err != nil {
			return fmt.Errorf("voice: dial UDP %s: %w", addr, err)
		}
		vc.udp = udp
	}

	localIP, localPort, err := vc.ipDiscovery()
	if err != nil {
		return fmt.Errorf("voice: IP discovery: %w", err)
	}
	vc.log.Info("ip discovery complete", "local_ip", localIP, "local_port", localPort)

	// 6. Send Select Protocol (opcode 1).
	// Prefer aead_aes256_gcm_rtpsize (matches discordgo), fall back to xchacha.
	mode := "xsalsa20_poly1305"
	for _, m := range ready.Modes {
		if m == "aead_aes256_gcm_rtpsize" {
			mode = m
			break
		}
		if m == "aead_xchacha20_poly1305_rtpsize" {
			mode = m
		}
	}

	selectData, _ := EncodeGatewayMsg(OpcodeSelectProtocol, SelectProtocolPayload{
		Protocol: "udp",
		Data: SelectProtocolData{
			Address: localIP,
			Port:    localPort,
			Mode:    mode,
		},
	})
	vc.log.Info("selecting protocol", "mode", mode)
	if err := vc.wsWrite(1, selectData); err != nil {
		return fmt.Errorf("voice: send select protocol: %w", err)
	}

	// 7. Read messages until we're fully connected.
	// After Select Protocol, the server sends Session Description (opcode 4).
	// If DAVE is active, it also sends DAVE opcodes (binary) that we must handle.
	if daveVersion > 0 {
		vc.daveSession = dave.NewSession(vc.config.UserID)
	}

	if err := vc.completeHandshake(); err != nil {
		return fmt.Errorf("voice: handshake: %w", err)
	}

	// Start background reader for Speaking events and ongoing DAVE messages.
	go vc.readVoiceWS(ctx)

	vc.log.Info("voice connection ready", "ssrc", vc.ssrc, "dave", vc.daveSession != nil)
	vc.state = StateReady
	return nil
}

// completeHandshake reads voice gateway messages until we get Session Description
// and complete any DAVE key exchange.
func (vc *VoiceConnection) completeHandshake() error {
	gotSessionDesc := false
	if vc.daveSession == nil {
		vc.daveReady.Store(true)
	}

	for !gotSessionDesc || !vc.daveReady.Load() {
		msgType, data, err := vc.ws.ReadMessage()
		if err != nil {
			vc.log.Error("voice ws read error during handshake", "error", err, "got_session_desc", gotSessionDesc, "dave_ready", vc.daveReady.Load())
			return err
		}
		vc.log.Debug("handshake message", "type", msgType, "len", len(data))

		// Binary messages are DAVE opcodes.
		if msgType == 2 || (len(data) > 0 && data[0] != '{') {
			if err := vc.handleDAVEBinary(data); err != nil {
				vc.log.Error("dave binary error", "error", err)
			}
			continue
		}

		// JSON messages.
		msg, err := DecodeGatewayMsg(data)
		if err != nil {
			vc.log.Debug("voice gateway parse error", "error", err)
			continue
		}
		if msg.Seq != nil {
			vc.wsSeqAck.Store(int64(*msg.Seq))
		}

		switch msg.Op {
		case OpcodeSessionDescription:
			var sd SessionDescPayload
			json.Unmarshal(msg.Data, &sd)
			vc.secretKey = sd.SecretKey
			vc.encryptMode = sd.Mode
			vc.log.Info("session description received", "mode", sd.Mode, "key_len", len(sd.SecretKey), "dave_version", sd.DaveProtocolVersion)

			// Initialize transport AEAD cipher.
			switch sd.Mode {
			case "aead_aes256_gcm_rtpsize":
				block, err := aes.NewCipher(sd.SecretKey)
				if err != nil {
					return fmt.Errorf("voice: aes cipher: %w", err)
				}
				vc.aead, err = cipher.NewGCM(block)
				if err != nil {
					return fmt.Errorf("voice: gcm: %w", err)
				}
			case "aead_xchacha20_poly1305_rtpsize":
				var err error
				vc.aead, err = chacha20poly1305.NewX(sd.SecretKey)
				if err != nil {
					return fmt.Errorf("voice: xchacha: %w", err)
				}
			default:
				// Legacy modes (xsalsa20_poly1305, etc.) — no AEAD init,
				// ListenAudio will pass payload through without transport decrypt.
				vc.log.Warn("legacy encryption mode, no transport AEAD", "mode", sd.Mode)
			}
			if vc.aead != nil {
				vc.log.Info("transport cipher initialized", "mode", sd.Mode, "nonce_size", vc.aead.NonceSize())
			}
			gotSessionDesc = true

		case OpcodeDavePrepareEpoch:
			var payload struct {
				Epoch           uint64 `json:"epoch"`
				ProtocolVersion int    `json:"protocol_version"`
			}
			json.Unmarshal(msg.Data, &payload)
			vc.log.Info("dave prepare epoch", "epoch", payload.Epoch, "version", payload.ProtocolVersion)
			if vc.daveSession != nil {
				kp, err := vc.daveSession.HandlePrepareEpoch(payload.Epoch)
				if err != nil {
					return fmt.Errorf("dave prepare epoch: %w", err)
				}
				// Send key package (opcode 26, binary).
				vc.sendDAVEKeyPackage(kp)
			}

		case OpcodeDaveTransition:
			var payload struct {
				TransitionID    uint16 `json:"transition_id"`
				ProtocolVersion int    `json:"protocol_version"`
			}
			json.Unmarshal(msg.Data, &payload)
			vc.log.Info("dave prepare transition", "transition_id", payload.TransitionID, "version", payload.ProtocolVersion)
			if vc.daveSession != nil {
				vc.daveSession.HandlePrepareTransition(uint16(payload.TransitionID), payload.ProtocolVersion)
			}

		case OpcodeDaveExecute:
			var payload struct {
				TransitionID uint16 `json:"transition_id"`
			}
			json.Unmarshal(msg.Data, &payload)
			vc.log.Info("dave execute transition", "transition_id", payload.TransitionID)
			if vc.daveSession != nil {
				if err := vc.daveSession.HandleExecuteTransition(payload.TransitionID); err != nil {
					vc.log.Error("dave execute transition error", "error", err)
				}
				vc.daveReady.Store(true)
			}

		case OpcodeSpeaking:
			var sp SpeakingPayload
			json.Unmarshal(msg.Data, &sp)
			vc.log.Info("speaking event during handshake", "ssrc", sp.SSRC, "user_id", sp.UserID, "speaking", sp.Speaking, "raw", string(msg.Data))
			if sp.UserID != "" && sp.SSRC != 0 {
				vc.ssrcUsers.Store(sp.SSRC, sp.UserID)
				vc.log.Info("ssrc mapped (speaking)", "ssrc", sp.SSRC, "user_id", sp.UserID)
			}

		case 12: // CLIENT_CONNECT — single user join, has audio_ssrc
			var data struct {
				UserID    string `json:"user_id"`
				AudioSSRC uint32 `json:"audio_ssrc"`
			}
			json.Unmarshal(msg.Data, &data)
			vc.log.Info("client connect during handshake", "ssrc", data.AudioSSRC, "user_id", data.UserID, "raw", string(msg.Data))
			if data.UserID != "" && data.AudioSSRC != 0 {
				vc.ssrcUsers.Store(data.AudioSSRC, data.UserID)
				vc.log.Info("ssrc mapped (client_connect)", "ssrc", data.AudioSSRC, "user_id", data.UserID)
			}

		case OpcodeHeartbeatACK:
			// ignore

		default:
			vc.log.Debug("voice gateway op during handshake", "op", msg.Op, "raw", string(msg.Data))
		}
	}
	return nil
}

// handleDAVEBinary processes a binary DAVE message from the voice gateway.
// Binary format: [sequence_number(2)][opcode(1)][payload...]
func (vc *VoiceConnection) handleDAVEBinary(data []byte) error {
	if len(data) < 3 {
		return fmt.Errorf("dave binary too short: %d bytes", len(data))
	}
	if vc.daveSession == nil {
		return nil
	}

	// seq := binary.BigEndian.Uint16(data[0:2])
	opcode := data[2]
	payload := data[3:]

	switch opcode {
	case 25: // dave_mls_external_sender_package
		vc.log.Info("dave external sender package", "len", len(payload))
		// Generate and send our key package immediately.
		if vc.daveSession != nil {
			kp, err := vc.daveSession.GenerateKeyPackage()
			if err != nil {
				return fmt.Errorf("dave generate key package: %w", err)
			}
			vc.sendDAVEKeyPackage(kp)
		}

	case 27: // dave_mls_proposals
		vc.log.Debug("dave mls proposals", "len", len(payload))

	case 28: // dave_mls_commit
		vc.log.Debug("dave mls commit", "len", len(payload))

	case 30: // dave_mls_welcome
		// Format: [transition_id(2)][Welcome message...]
		if len(payload) < 2 {
			return fmt.Errorf("dave welcome too short")
		}
		transitionID := uint16(payload[0])<<8 | uint16(payload[1])
		welcomeData := payload[2:]
		vc.log.Info("dave mls welcome", "transition_id", transitionID, "welcome_len", len(welcomeData))

		// Welcome is wrapped in MLSMessage: [version(2)][WireFormat(2)][body...]
		// WireFormat 4 = mls_welcome. Skip the 4-byte MLSMessage header.
		if len(welcomeData) >= 4 {
			mlsVersion := uint16(welcomeData[0])<<8 | uint16(welcomeData[1])
			wireFormat := uint16(welcomeData[2])<<8 | uint16(welcomeData[3])
			vc.log.Debug("mls message header", "version", mlsVersion, "wire_format", wireFormat)
			if wireFormat == 4 { // mls_welcome
				welcomeData = welcomeData[4:]
			}
		}

		if err := vc.daveSession.HandleWelcome(welcomeData); err != nil {
			vc.log.Error("dave welcome processing failed", "error", err)
			return fmt.Errorf("dave welcome: %w", err)
		}
		vc.log.Info("dave welcome processed successfully")

		// Send ready_for_transition.
		readyData, _ := EncodeGatewayMsg(OpcodeDaveReady, map[string]int{"transition_id": int(transitionID)})
		vc.wsWrite(1, readyData)
		vc.log.Info("dave ready_for_transition sent", "transition_id", transitionID)

		// Activate DAVE immediately for transition_id 0 (initialization).
		vc.daveSession.HandlePrepareTransition(transitionID, 1)
		if err := vc.daveSession.HandleExecuteTransition(transitionID); err != nil {
			vc.log.Error("dave activate failed", "error", err)
		} else {
			vc.log.Info("dave session activated", "active", vc.daveSession.IsActive())
		}
		vc.daveReady.Store(true)

	default:
		vc.log.Debug("dave unknown binary opcode", "opcode", opcode, "len", len(payload))
	}
	return nil
}

// sendDAVEKeyPackage sends an MLS key package (opcode 26) as a binary message.
func (vc *VoiceConnection) sendDAVEKeyPackage(kp []byte) {
	// Binary format: [opcode(1)][key_package_data...]
	msg := make([]byte, 1+len(kp))
	msg[0] = 26
	copy(msg[1:], kp)
	vc.wsWrite(2, msg)
	vc.log.Info("dave key package sent", "len", len(kp))
}

// ipDiscovery sends a 74-byte IP discovery packet and reads the response
// to determine our external IP and port as seen by the voice server.
func (vc *VoiceConnection) ipDiscovery() (ip string, port int, err error) {
	// Send: 74-byte packet with type=0x1, length=70, SSRC.
	buf := make([]byte, 74)
	binary.BigEndian.PutUint16(buf[0:2], 0x1)     // type: request
	binary.BigEndian.PutUint16(buf[2:4], 70)      // length
	binary.BigEndian.PutUint32(buf[4:8], vc.ssrc) // SSRC

	if _, err := vc.udp.Write(buf); err != nil {
		return "", 0, err
	}

	// Read response.
	vc.udp.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp := make([]byte, 74)
	n, err := vc.udp.Read(resp)
	if err != nil {
		return "", 0, err
	}
	if n < 74 {
		return "", 0, fmt.Errorf("voice: IP discovery response too short (%d bytes)", n)
	}

	// IP starts at byte 8, null-terminated.
	ipEnd := 8
	for ipEnd < 72 && resp[ipEnd] != 0 {
		ipEnd++
	}
	ip = string(resp[8:ipEnd])
	port = int(binary.BigEndian.Uint16(resp[72:74]))
	return ip, port, nil
}

// readOp reads gateway messages until one with the expected opcode arrives.
func (vc *VoiceConnection) readOp(expected VoiceOpcode) (*GatewayMessage, error) {
	for {
		_, data, err := vc.ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		msg, err := DecodeGatewayMsg(data)
		if err != nil {
			continue
		}
		if msg.Op == expected {
			return msg, nil
		}
	}
}

// heartbeatLoop sends v8 voice heartbeats at the given interval.
func (vc *VoiceConnection) heartbeatLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seqAck := int(vc.wsSeqAck.Load())
			hb := struct {
				T      int64 `json:"t"`
				SeqAck int   `json:"seq_ack"`
			}{
				T:      time.Now().Unix(),
				SeqAck: seqAck,
			}
			data, _ := EncodeGatewayMsg(OpcodeHeartbeat, hb)
			if err := vc.wsWrite(1, data); err != nil {
				vc.log.Error("heartbeat write failed", "error", err)
				return
			}
			vc.log.Debug("heartbeat sent", "seq_ack", seqAck)
		}
	}
}

// SetSpeaking sends the Speaking opcode (5) to indicate we're transmitting.
func (vc *VoiceConnection) SetSpeaking(speaking SpeakingFlag) error {
	data, _ := EncodeGatewayMsg(OpcodeSpeaking, SpeakingPayload{
		Speaking: speaking,
		Delay:    0,
		SSRC:     vc.ssrc,
	})
	return vc.wsWrite(1, data)
}

// ---------------------------------------------------------------------------
// Dialer — abstracts real network connections for testability
// ---------------------------------------------------------------------------

// Dialer creates WebSocket and UDP connections. Implement this for
// production (using gorilla/websocket + net.DialUDP) or inject a mock.
type Dialer interface {
	DialWS(ctx context.Context, url string) (WSConn, error)
	DialUDP(ctx context.Context, addr string) (UDPConn, error)
}

// NetDialer is a production Dialer using real network connections.
// Requires a WSDialer to be injected since we don't depend on any
// specific WebSocket library.
type NetDialer struct {
	// WSDialFunc dials a WebSocket. Users should provide this using their
	// preferred WS library (e.g. gorilla/websocket, nhooyr/websocket).
	WSDialFunc func(ctx context.Context, url string) (WSConn, error)
}

func (d *NetDialer) DialWS(ctx context.Context, url string) (WSConn, error) {
	if d.WSDialFunc == nil {
		return nil, fmt.Errorf("voice: WSDialFunc not set")
	}
	return d.WSDialFunc(ctx, url)
}

func (d *NetDialer) DialUDP(ctx context.Context, addr string) (UDPConn, error) {
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	return net.DialUDP("udp", nil, raddr)
}
