package voice

import (
	"encoding/json"
	"testing"
)

func TestEncodeGatewayMsg_Identify(t *testing.T) {
	data, err := EncodeGatewayMsg(OpcodeIdentify, IdentifyPayload{
		ServerID:  "111",
		UserID:    "222",
		SessionID: "sess-abc",
		Token:     "tok-xyz",
	})
	if err != nil {
		t.Fatal(err)
	}

	var msg GatewayMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Op != OpcodeIdentify {
		t.Errorf("Op = %d", msg.Op)
	}

	var payload IdentifyPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ServerID != "111" {
		t.Errorf("ServerID = %q", payload.ServerID)
	}
	if payload.Token != "tok-xyz" {
		t.Errorf("Token = %q", payload.Token)
	}
}

func TestDecodeGatewayMsg_Ready(t *testing.T) {
	raw := `{"op":2,"d":{"ssrc":12345,"ip":"1.2.3.4","port":50000,"modes":["xsalsa20_poly1305"]}}`
	msg, err := DecodeGatewayMsg([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Op != OpcodeReady {
		t.Errorf("Op = %d", msg.Op)
	}

	var ready ReadyPayload
	if err := DecodePayload(msg, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.SSRC != 12345 {
		t.Errorf("SSRC = %d", ready.SSRC)
	}
	if ready.IP != "1.2.3.4" {
		t.Errorf("IP = %q", ready.IP)
	}
	if ready.Port != 50000 {
		t.Errorf("Port = %d", ready.Port)
	}
	if len(ready.Modes) != 1 || ready.Modes[0] != "xsalsa20_poly1305" {
		t.Errorf("Modes = %v", ready.Modes)
	}
}

func TestDecodeGatewayMsg_Hello(t *testing.T) {
	raw := `{"op":8,"d":{"heartbeat_interval":41250}}`
	msg, _ := DecodeGatewayMsg([]byte(raw))
	var hello HelloPayload
	DecodePayload(msg, &hello)
	if hello.HeartbeatInterval != 41250 {
		t.Errorf("Interval = %f", hello.HeartbeatInterval)
	}
}

func TestDecodeGatewayMsg_SessionDesc(t *testing.T) {
	raw := `{"op":4,"d":{"mode":"xsalsa20_poly1305","secret_key":"AQIDBA=="}}`
	msg, _ := DecodeGatewayMsg([]byte(raw))
	var sd SessionDescPayload
	DecodePayload(msg, &sd)
	if sd.Mode != "xsalsa20_poly1305" {
		t.Errorf("Mode = %q", sd.Mode)
	}
}

func TestEncodeGatewayMsg_Speaking(t *testing.T) {
	data, _ := EncodeGatewayMsg(OpcodeSpeaking, SpeakingPayload{
		Speaking: SpeakingMicrophone,
		SSRC:     42,
	})
	msg, _ := DecodeGatewayMsg(data)
	if msg.Op != OpcodeSpeaking {
		t.Errorf("Op = %d", msg.Op)
	}
	var sp SpeakingPayload
	DecodePayload(msg, &sp)
	if sp.Speaking != SpeakingMicrophone {
		t.Errorf("Speaking = %d", sp.Speaking)
	}
	if sp.SSRC != 42 {
		t.Errorf("SSRC = %d", sp.SSRC)
	}
}

func TestEncodeGatewayMsg_SelectProtocol(t *testing.T) {
	data, _ := EncodeGatewayMsg(OpcodeSelectProtocol, SelectProtocolPayload{
		Protocol: "udp",
		Data: SelectProtocolData{
			Address: "1.2.3.4",
			Port:    12345,
			Mode:    "xsalsa20_poly1305",
		},
	})
	msg, _ := DecodeGatewayMsg(data)
	var sp SelectProtocolPayload
	DecodePayload(msg, &sp)
	if sp.Protocol != "udp" {
		t.Errorf("Protocol = %q", sp.Protocol)
	}
	if sp.Data.Address != "1.2.3.4" {
		t.Errorf("Address = %q", sp.Data.Address)
	}
}

func TestDAVEOpcodes(t *testing.T) {
	// Verify DAVE opcodes are defined correctly.
	if OpcodeDaveTransition != 21 {
		t.Errorf("DaveTransition = %d", OpcodeDaveTransition)
	}
	if OpcodeDaveExecute != 22 {
		t.Errorf("DaveExecute = %d", OpcodeDaveExecute)
	}
	if OpcodeDaveReady != 23 {
		t.Errorf("DaveReady = %d", OpcodeDaveReady)
	}
	if OpcodeDavePrepareEpoch != 24 {
		t.Errorf("DavePrepareEpoch = %d", OpcodeDavePrepareEpoch)
	}
	if OpcodeDaveMLS != 25 {
		t.Errorf("DaveMLS = %d", OpcodeDaveMLS)
	}
}
