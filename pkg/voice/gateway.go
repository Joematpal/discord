package voice

import "encoding/json"

// Voice Gateway opcodes (Discord Voice Connection docs).
type VoiceOpcode int

const (
	OpcodeIdentify           VoiceOpcode = 0
	OpcodeSelectProtocol     VoiceOpcode = 1
	OpcodeReady              VoiceOpcode = 2
	OpcodeHeartbeat          VoiceOpcode = 3
	OpcodeSessionDescription VoiceOpcode = 4
	OpcodeSpeaking           VoiceOpcode = 5
	OpcodeHeartbeatACK       VoiceOpcode = 6
	OpcodeResume             VoiceOpcode = 7
	OpcodeHello              VoiceOpcode = 8
	OpcodeResumed            VoiceOpcode = 9
	OpcodeClientDisconnect   VoiceOpcode = 13
	OpcodeDaveTransition     VoiceOpcode = 21
	OpcodeDaveExecute        VoiceOpcode = 22
	OpcodeDaveReady          VoiceOpcode = 23
	OpcodeDavePrepareEpoch   VoiceOpcode = 24
	OpcodeDaveMLS            VoiceOpcode = 25
)

// SpeakingFlag is a bitfield for the Speaking opcode.
type SpeakingFlag int

const (
	SpeakingMicrophone SpeakingFlag = 1 << 0
	SpeakingSoundshare SpeakingFlag = 1 << 1
	SpeakingPriority   SpeakingFlag = 1 << 2
)

// GatewayMessage is the generic envelope for voice gateway messages.
type GatewayMessage struct {
	Op   VoiceOpcode     `json:"op"`
	Data json.RawMessage `json:"d"`
	Seq  *int            `json:"s,omitempty"`
}

// ---------------------------------------------------------------------------
// Payloads for each opcode
// ---------------------------------------------------------------------------

// IdentifyPayload is sent by the client (opcode 0).
type IdentifyPayload struct {
	ServerID  string `json:"server_id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
	MaxDaveProtocolVersion int `json:"max_dave_protocol_version,omitempty"`
}

// ReadyPayload is received from the server (opcode 2).
type ReadyPayload struct {
	SSRC  uint32   `json:"ssrc"`
	IP    string   `json:"ip"`
	Port  int      `json:"port"`
	Modes []string `json:"modes"`
	DaveProtocolVersion int `json:"dave_protocol_version,omitempty"`
}

// SelectProtocolPayload is sent by the client (opcode 1).
type SelectProtocolPayload struct {
	Protocol string                 `json:"protocol"`
	Data     SelectProtocolData     `json:"data"`
}

// SelectProtocolData is the data field within SelectProtocol.
type SelectProtocolData struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Mode    string `json:"mode"`
}

// SessionDescPayload is received from the server (opcode 4).
type SessionDescPayload struct {
	Mode      string `json:"mode"`
	SecretKey []byte `json:"secret_key"`
	DaveProtocolVersion int `json:"dave_protocol_version,omitempty"`
}

// HelloPayload is received from the server (opcode 8).
type HelloPayload struct {
	HeartbeatInterval float64 `json:"heartbeat_interval"`
}

// SpeakingPayload is sent/received (opcode 5).
type SpeakingPayload struct {
	Speaking SpeakingFlag `json:"speaking"`
	Delay    int          `json:"delay"`
	SSRC     uint32       `json:"ssrc"`
	UserID   string       `json:"user_id,omitempty"` // only on receive
}

// ResumePayload is sent by the client (opcode 7).
type ResumePayload struct {
	ServerID  string `json:"server_id"`
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
	Seq       int    `json:"seq,omitempty"`
}

// ---------------------------------------------------------------------------
// Gateway message helpers
// ---------------------------------------------------------------------------

// EncodeGatewayMsg creates a serialized gateway message.
func EncodeGatewayMsg(op VoiceOpcode, data any) ([]byte, error) {
	d, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(GatewayMessage{Op: op, Data: d})
}

// DecodeGatewayMsg parses a raw gateway message.
func DecodeGatewayMsg(raw []byte) (*GatewayMessage, error) {
	var msg GatewayMessage
	err := json.Unmarshal(raw, &msg)
	return &msg, err
}

// DecodePayload unmarshals the Data field of a GatewayMessage into the target.
func DecodePayload(msg *GatewayMessage, target any) error {
	return json.Unmarshal(msg.Data, target)
}
