package discord

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// InteractionHandler processes a single interaction and returns a response.
// This is the core interface consumers implement.
type InteractionHandler interface {
	HandleInteraction(i *Interaction) (*InteractionResponse, error)
}

// InteractionHandlerFunc adapts a function to the InteractionHandler interface.
type InteractionHandlerFunc func(*Interaction) (*InteractionResponse, error)

func (f InteractionHandlerFunc) HandleInteraction(i *Interaction) (*InteractionResponse, error) {
	return f(i)
}

// Verifier validates the Ed25519 signature on incoming webhook interactions.
// Extracted as an interface so tests can swap in a no-op verifier.
type Verifier interface {
	Verify(message, sig []byte) bool
}

// Ed25519Verifier verifies using a real Ed25519 public key.
type Ed25519Verifier struct {
	key ed25519.PublicKey
}

// NewEd25519Verifier creates a verifier from a hex-encoded public key
// (from the Discord Developer Portal).
func NewEd25519Verifier(hexKey string) (*Ed25519Verifier, error) {
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("discord: invalid public key hex: %w", err)
	}
	if len(keyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("discord: public key must be %d bytes, got %d", ed25519.PublicKeySize, len(keyBytes))
	}
	return &Ed25519Verifier{key: ed25519.PublicKey(keyBytes)}, nil
}

func (v *Ed25519Verifier) Verify(message, sig []byte) bool {
	return ed25519.Verify(v.key, message, sig)
}

// ---------------------------------------------------------------------------
// Mux — routes interactions to handlers
// ---------------------------------------------------------------------------

// Mux routes interactions to registered handlers by command name,
// component custom_id, or interaction type.
type Mux struct {
	mu              sync.RWMutex
	commands        map[string]InteractionHandler // keyed by command name
	components      map[string]InteractionHandler // keyed by custom_id
	autocomplete    map[string]InteractionHandler // keyed by command name
	modals          map[string]InteractionHandler // keyed by custom_id
	defaultHandler  InteractionHandler
}

// NewMux creates an empty interaction router.
func NewMux() *Mux {
	return &Mux{
		commands:     make(map[string]InteractionHandler),
		components:   make(map[string]InteractionHandler),
		autocomplete: make(map[string]InteractionHandler),
		modals:       make(map[string]InteractionHandler),
	}
}

// Command registers a handler for a slash/user/message command by name.
func (m *Mux) Command(name string, h InteractionHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commands[name] = h
}

// CommandFunc is a convenience for registering a function as a command handler.
func (m *Mux) CommandFunc(name string, f func(*Interaction) (*InteractionResponse, error)) {
	m.Command(name, InteractionHandlerFunc(f))
}

// Component registers a handler for a message component by custom_id.
func (m *Mux) Component(customID string, h InteractionHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.components[customID] = h
}

// ComponentFunc is a convenience for registering a function as a component handler.
func (m *Mux) ComponentFunc(customID string, f func(*Interaction) (*InteractionResponse, error)) {
	m.Component(customID, InteractionHandlerFunc(f))
}

// Autocomplete registers an autocomplete handler for a command name.
func (m *Mux) Autocomplete(name string, h InteractionHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autocomplete[name] = h
}

// AutocompleteFunc is a convenience for registering a function as an autocomplete handler.
func (m *Mux) AutocompleteFunc(name string, f func(*Interaction) (*InteractionResponse, error)) {
	m.Autocomplete(name, InteractionHandlerFunc(f))
}

// Modal registers a handler for a modal submit by custom_id.
func (m *Mux) Modal(customID string, h InteractionHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modals[customID] = h
}

// ModalFunc is a convenience for registering a function as a modal handler.
func (m *Mux) ModalFunc(customID string, f func(*Interaction) (*InteractionResponse, error)) {
	m.Modal(customID, InteractionHandlerFunc(f))
}

// Default sets a fallback handler for unmatched interactions.
func (m *Mux) Default(h InteractionHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultHandler = h
}

// HandleInteraction routes an interaction to the appropriate handler.
func (m *Mux) HandleInteraction(i *Interaction) (*InteractionResponse, error) {
	if i.Type == InteractionTypePing {
		return Pong(), nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var h InteractionHandler
	switch i.Type {
	case InteractionTypeCommand:
		if i.Data != nil {
			h = m.commands[i.Data.Name]
		}
	case InteractionTypeComponent:
		if i.Data != nil {
			h = m.components[i.Data.CustomID]
		}
	case InteractionTypeAutocomplete:
		if i.Data != nil {
			h = m.autocomplete[i.Data.Name]
		}
	case InteractionTypeModalSubmit:
		if i.Data != nil {
			h = m.modals[i.Data.CustomID]
		}
	}

	if h == nil {
		h = m.defaultHandler
	}
	if h == nil {
		return nil, fmt.Errorf("discord: no handler for interaction type %d", i.Type)
	}
	return h.HandleInteraction(i)
}

// ---------------------------------------------------------------------------
// WebhookHandler — net/http.Handler for Discord webhook interactions
// ---------------------------------------------------------------------------

// WebhookHandler is an http.Handler that verifies Ed25519 signatures,
// deserializes interactions, routes them through a Mux, and writes responses.
type WebhookHandler struct {
	verifier Verifier
	handler  InteractionHandler
}

// NewWebhookHandler creates an HTTP handler for webhook-based interactions.
func NewWebhookHandler(verifier Verifier, handler InteractionHandler) *WebhookHandler {
	return &WebhookHandler{verifier: verifier, handler: handler}
}

// ServeHTTP implements http.Handler.
func (wh *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Verify Ed25519 signature.
	if wh.verifier != nil {
		sig, err := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		timestamp := r.Header.Get("X-Signature-Timestamp")
		msg := append([]byte(timestamp), body...)
		if !wh.verifier.Verify(msg, sig) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var interaction Interaction
	if err := json.Unmarshal(body, &interaction); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	resp, err := wh.handler.HandleInteraction(&interaction)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
