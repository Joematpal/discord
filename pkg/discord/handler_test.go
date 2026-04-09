package discord

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// alwaysVerifier is a test verifier that always passes.
type alwaysVerifier struct{}

func (alwaysVerifier) Verify(_, _ []byte) bool { return true }

// neverVerifier is a test verifier that always fails.
type neverVerifier struct{}

func (neverVerifier) Verify(_, _ []byte) bool { return false }

func TestMux_Ping(t *testing.T) {
	m := NewMux()
	resp, err := m.HandleInteraction(&Interaction{Type: InteractionTypePing})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Type != CallbackPong {
		t.Errorf("Type = %d", resp.Type)
	}
}

func TestMux_Command(t *testing.T) {
	m := NewMux()
	m.CommandFunc("ping", func(i *Interaction) (*InteractionResponse, error) {
		return MessageResponse("pong!"), nil
	})

	resp, err := m.HandleInteraction(&Interaction{
		Type: InteractionTypeCommand,
		Data: &InteractionData{Name: "ping"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Data.Content != "pong!" {
		t.Errorf("Content = %q", resp.Data.Content)
	}
}

func TestMux_Component(t *testing.T) {
	m := NewMux()
	m.ComponentFunc("btn_ok", func(i *Interaction) (*InteractionResponse, error) {
		return MessageResponse("clicked!"), nil
	})

	resp, err := m.HandleInteraction(&Interaction{
		Type: InteractionTypeComponent,
		Data: &InteractionData{CustomID: "btn_ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Data.Content != "clicked!" {
		t.Errorf("Content = %q", resp.Data.Content)
	}
}

func TestMux_Autocomplete(t *testing.T) {
	m := NewMux()
	m.AutocompleteFunc("search", func(i *Interaction) (*InteractionResponse, error) {
		return AutocompleteResponse(Choice("Option A", "a")), nil
	})

	resp, err := m.HandleInteraction(&Interaction{
		Type: InteractionTypeAutocomplete,
		Data: &InteractionData{Name: "search"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Choices) != 1 {
		t.Fatalf("choices = %d", len(resp.Data.Choices))
	}
}

func TestMux_Modal(t *testing.T) {
	m := NewMux()
	m.ModalFunc("feedback_form", func(i *Interaction) (*InteractionResponse, error) {
		return MessageResponse("received!"), nil
	})

	resp, err := m.HandleInteraction(&Interaction{
		Type: InteractionTypeModalSubmit,
		Data: &InteractionData{CustomID: "feedback_form"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Data.Content != "received!" {
		t.Errorf("Content = %q", resp.Data.Content)
	}
}

func TestMux_Default(t *testing.T) {
	m := NewMux()
	m.Default(InteractionHandlerFunc(func(i *Interaction) (*InteractionResponse, error) {
		return MessageResponse("fallback"), nil
	}))

	resp, err := m.HandleInteraction(&Interaction{
		Type: InteractionTypeCommand,
		Data: &InteractionData{Name: "unknown"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Data.Content != "fallback" {
		t.Errorf("Content = %q", resp.Data.Content)
	}
}

func TestMux_NoHandler(t *testing.T) {
	m := NewMux()
	_, err := m.HandleInteraction(&Interaction{
		Type: InteractionTypeCommand,
		Data: &InteractionData{Name: "nope"},
	})
	if err == nil {
		t.Error("expected error for unregistered command")
	}
}

// ---------------------------------------------------------------------------
// WebhookHandler tests
// ---------------------------------------------------------------------------

func TestWebhookHandler_Ping(t *testing.T) {
	m := NewMux()
	wh := NewWebhookHandler(alwaysVerifier{}, m)

	body, _ := json.Marshal(Interaction{Type: InteractionTypePing})
	req := httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader(body))
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(make([]byte, 64)))
	req.Header.Set("X-Signature-Timestamp", "12345")

	w := httptest.NewRecorder()
	wh.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp InteractionResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Type != CallbackPong {
		t.Errorf("Type = %d", resp.Type)
	}
}

func TestWebhookHandler_BadSignature(t *testing.T) {
	m := NewMux()
	wh := NewWebhookHandler(neverVerifier{}, m)

	body, _ := json.Marshal(Interaction{Type: InteractionTypePing})
	req := httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader(body))
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(make([]byte, 64)))
	req.Header.Set("X-Signature-Timestamp", "12345")

	w := httptest.NewRecorder()
	wh.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestWebhookHandler_InvalidSigHex(t *testing.T) {
	m := NewMux()
	wh := NewWebhookHandler(alwaysVerifier{}, m)

	req := httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Signature-Ed25519", "not-hex!!")
	req.Header.Set("X-Signature-Timestamp", "12345")

	w := httptest.NewRecorder()
	wh.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestWebhookHandler_MethodNotAllowed(t *testing.T) {
	wh := NewWebhookHandler(alwaysVerifier{}, NewMux())
	req := httptest.NewRequest(http.MethodGet, "/interactions", nil)
	w := httptest.NewRecorder()
	wh.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d", w.Code)
	}
}

func TestWebhookHandler_BadJSON(t *testing.T) {
	wh := NewWebhookHandler(alwaysVerifier{}, NewMux())
	req := httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader([]byte("not json")))
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(make([]byte, 64)))
	req.Header.Set("X-Signature-Timestamp", "12345")

	w := httptest.NewRecorder()
	wh.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestWebhookHandler_RealEd25519(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	verifier := &Ed25519Verifier{key: pub}

	m := NewMux()
	m.CommandFunc("test", func(i *Interaction) (*InteractionResponse, error) {
		return MessageResponse("ok"), nil
	})
	wh := NewWebhookHandler(verifier, m)

	interaction := Interaction{
		Type:    InteractionTypeCommand,
		Data:    &InteractionData{Name: "test"},
		Token:   "tok",
		Version: 1,
	}
	body, _ := json.Marshal(interaction)
	timestamp := "1234567890"
	msg := append([]byte(timestamp), body...)
	sig := ed25519.Sign(priv, msg)

	req := httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader(body))
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", timestamp)

	w := httptest.NewRecorder()
	wh.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp InteractionResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.Content != "ok" {
		t.Errorf("Content = %q", resp.Data.Content)
	}
}

func TestNewEd25519Verifier_BadHex(t *testing.T) {
	_, err := NewEd25519Verifier("not-hex")
	if err == nil {
		t.Error("expected error")
	}
}

func TestNewEd25519Verifier_BadLength(t *testing.T) {
	_, err := NewEd25519Verifier(hex.EncodeToString([]byte("short")))
	if err == nil {
		t.Error("expected error for wrong key length")
	}
}

// ---------------------------------------------------------------------------
// WebhookHandler with nil verifier (no verification)
// ---------------------------------------------------------------------------

func TestWebhookHandler_NilVerifier(t *testing.T) {
	m := NewMux()
	wh := NewWebhookHandler(nil, m)

	body, _ := json.Marshal(Interaction{Type: InteractionTypePing})
	req := httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader(body))
	// No signature headers at all.

	w := httptest.NewRecorder()
	wh.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// WebhookHandler command routing end-to-end
// ---------------------------------------------------------------------------

func TestWebhookHandler_CommandRouting(t *testing.T) {
	m := NewMux()
	m.CommandFunc("echo", func(i *Interaction) (*InteractionResponse, error) {
		text := ""
		if i.Data != nil && len(i.Data.Options) > 0 {
			text = i.Data.Options[0].StringValue()
		}
		return MessageResponse(text), nil
	})
	wh := NewWebhookHandler(nil, m)

	interaction := Interaction{
		Type: InteractionTypeCommand,
		Data: &InteractionData{
			Name: "echo",
			Options: []OptionData{
				{Name: "text", Type: OptionTypeString, Value: "hello world"},
			},
		},
	}
	body, _ := json.Marshal(interaction)
	req := httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	wh.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	respBody, _ := io.ReadAll(w.Body)
	var resp InteractionResponse
	json.Unmarshal(respBody, &resp)
	if resp.Data.Content != "hello world" {
		t.Errorf("Content = %q", resp.Data.Content)
	}
}
