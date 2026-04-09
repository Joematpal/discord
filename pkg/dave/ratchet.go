package dave

import (
	"crypto/sha256"
	"sync"
	"time"
)

const (
	// ExporterLabel is the MLS exporter label for DAVE sender keys.
	ExporterLabel = "Discord Secure Frames v0"

	// KeySize is the AES-128 key size in bytes.
	KeySize = 16
)

// KeyRatchet implements the per-sender key ratchet described in the DAVE spec.
// The base secret is derived from MLS-Exporter; generations are ratcheted
// forward using HKDF-like derivation.
type KeyRatchet struct {
	mu          sync.Mutex
	baseSecret  []byte
	cache       map[uint32]*cachedKey
	generation  uint32
	currentKey  []byte
}

type cachedKey struct {
	key       []byte
	expiresAt time.Time
}

// NewKeyRatchet creates a ratchet from the base secret exported from MLS.
func NewKeyRatchet(baseSecret []byte) *KeyRatchet {
	initial := deriveKey(baseSecret, 0)
	kr := &KeyRatchet{
		baseSecret: baseSecret,
		cache:      make(map[uint32]*cachedKey),
		currentKey: initial,
	}
	kr.cache[0] = &cachedKey{key: initial, expiresAt: time.Now().Add(10 * time.Second)}
	return kr
}

// Get returns the key for the given generation, ratcheting forward if needed.
// Returns an error if the generation has been erased.
func (kr *KeyRatchet) Get(generation uint32) ([]byte, error) {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	// Evict expired entries.
	kr.evictExpired()

	// Check cache.
	if ck, ok := kr.cache[generation]; ok {
		return ck.key, nil
	}

	// If requested generation is behind our current, it's been erased.
	if generation < kr.generation {
		return nil, ErrGenerationErased
	}

	// Ratchet forward.
	for kr.generation < generation {
		kr.generation++
		kr.currentKey = deriveKey(kr.baseSecret, kr.generation)
		kr.cache[kr.generation] = &cachedKey{
			key:       kr.currentKey,
			expiresAt: time.Now().Add(10 * time.Second),
		}
	}

	return kr.currentKey, nil
}

// Generation returns the current generation.
func (kr *KeyRatchet) Generation() uint32 {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	return kr.generation
}

func (kr *KeyRatchet) evictExpired() {
	now := time.Now()
	for gen, ck := range kr.cache {
		if gen < kr.generation && now.After(ck.expiresAt) {
			delete(kr.cache, gen)
		}
	}
}

// deriveKey derives the key for a specific generation from the base secret.
// Uses SHA-256 truncated to KeySize bytes, keyed by generation.
func deriveKey(baseSecret []byte, generation uint32) []byte {
	h := sha256.New()
	h.Write(baseSecret)
	h.Write([]byte{byte(generation >> 24), byte(generation >> 16), byte(generation >> 8), byte(generation)})
	return h.Sum(nil)[:KeySize]
}

// ---------------------------------------------------------------------------
// SenderKeyStore manages key ratchets for all senders in a DAVE session.
// ---------------------------------------------------------------------------

// SenderKeyStore maps sender IDs to their key ratchets.
type SenderKeyStore struct {
	mu       sync.RWMutex
	ratchets map[uint64]*KeyRatchet
}

// NewSenderKeyStore creates an empty sender key store.
func NewSenderKeyStore() *SenderKeyStore {
	return &SenderKeyStore{ratchets: make(map[uint64]*KeyRatchet)}
}

// SetSenderKey sets the base secret for a sender, creating a new ratchet.
func (s *SenderKeyStore) SetSenderKey(senderID uint64, baseSecret []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ratchets[senderID] = NewKeyRatchet(baseSecret)
}

// GetKey retrieves the key for a sender at a given generation.
func (s *SenderKeyStore) GetKey(senderID uint64, generation uint32) ([]byte, error) {
	s.mu.RLock()
	kr, ok := s.ratchets[senderID]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownSender
	}
	return kr.Get(generation)
}

// RemoveSender removes a sender's ratchet.
func (s *SenderKeyStore) RemoveSender(senderID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ratchets, senderID)
}

// Clear removes all sender ratchets (e.g., on epoch change).
func (s *SenderKeyStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ratchets = make(map[uint64]*KeyRatchet)
}

// Errors.
var (
	ErrGenerationErased = errNew("dave: generation has been erased")
	ErrUnknownSender    = errNew("dave: unknown sender")
)

func errNew(s string) error { return &daveError{s} }

type daveError struct{ msg string }

func (e *daveError) Error() string { return e.msg }
