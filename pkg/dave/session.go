package dave

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"strconv"
	"sync"
)

// Session manages DAVE E2EE state for a voice connection.
type Session struct {
	mu                  sync.Mutex
	userID              string
	active              bool
	epoch               uint64
	pendingTransitionID uint16
	pendingVersion      int

	exporterSecret    []byte
	senderKey         []byte
	senderNonce       uint32
	frameCipher       cipher.AEAD
	ratchetBaseSecret []byte
	currentGeneration uint32
	hasPendingKey     bool

	kpBundle *KeyPackageBundle
}

// NewSession creates a new DAVE session for the given user.
func NewSession(userID string) *Session {
	return &Session{userID: userID}
}

// GenerateKeyPackage generates a new MLS key package.
func (s *Session) GenerateKeyPackage() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generateKeyPackageLocked()
}

func (s *Session) generateKeyPackageLocked() ([]byte, error) {
	bundle, err := GenerateKeyPackage(s.userID)
	if err != nil {
		return nil, err
	}
	s.kpBundle = bundle
	return bundle.Serialized, nil
}

// HandleWelcome processes an MLS Welcome message.
func (s *Session) HandleWelcome(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kpBundle == nil {
		return fmt.Errorf("dave: no key package generated")
	}
	result, err := ProcessWelcome(data, s.kpBundle)
	if err != nil {
		return fmt.Errorf("dave: process welcome: %w", err)
	}
	s.exporterSecret = result.ExporterSecret
	s.epoch = result.Epoch
	s.hasPendingKey = true
	return nil
}

// HandlePrepareTransition stores a pending transition.
func (s *Session) HandlePrepareTransition(transitionID uint16, protocolVersion int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingTransitionID = transitionID
	s.pendingVersion = protocolVersion
}

// HandleExecuteTransition executes a previously announced transition.
func (s *Session) HandleExecuteTransition(transitionID uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if transitionID != s.pendingTransitionID {
		if s.senderKey != nil {
			s.active = true
		}
		return nil
	}

	if s.pendingVersion > 0 {
		if s.hasPendingKey && s.exporterSecret != nil {
			if err := s.deriveSenderKeyLocked(); err != nil {
				return err
			}
			s.hasPendingKey = false
		}
		if s.senderKey == nil {
			return nil
		}
		s.active = true
	} else {
		s.active = false
		s.senderKey = nil
		s.frameCipher = nil
		s.hasPendingKey = false
	}
	return nil
}

// HandlePrepareEpoch resets for a new epoch and returns a fresh key package.
func (s *Session) HandlePrepareEpoch(epoch uint64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch = epoch
	s.active = false
	s.senderKey = nil
	s.frameCipher = nil
	s.exporterSecret = nil
	return s.generateKeyPackageLocked()
}

// IsActive returns whether DAVE encryption is active.
func (s *Session) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *Session) deriveSenderKeyLocked() error {
	if s.exporterSecret == nil {
		return fmt.Errorf("dave: no exporter secret")
	}
	userIDNum, err := strconv.ParseUint(s.userID, 10, 64)
	if err != nil {
		return err
	}
	ctx := make([]byte, 8)
	binary.LittleEndian.PutUint64(ctx, userIDNum)

	baseSecret, err := MLSExport(s.exporterSecret, ExportLabel, ctx, KeySize)
	if err != nil {
		return err
	}
	s.ratchetBaseSecret = baseSecret
	s.currentGeneration = 0
	s.senderNonce = 0

	key, err := HashRatchetGetKey(baseSecret, 0)
	if err != nil {
		return err
	}
	s.senderKey = key

	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	s.frameCipher, err = cipher.NewGCM(block)
	return err
}

// DecryptFrame decrypts a DAVE frame from a sender.
// Returns data unchanged when DAVE is not active.
func (s *Session) DecryptFrame(senderUserID string, data []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active || s.exporterSecret == nil {
		return data, nil
	}

	userIDNum, err := strconv.ParseUint(senderUserID, 10, 64)
	if err != nil {
		return nil, err
	}
	ctx := make([]byte, 8)
	binary.LittleEndian.PutUint64(ctx, userIDNum)

	baseSecret, err := MLSExport(s.exporterSecret, ExportLabel, ctx, KeySize)
	if err != nil {
		return nil, err
	}

	return DecryptSecureFrame(baseSecret, data)
}

// DecryptSecureFrame decrypts a DAVE-encrypted frame using AES-CTR
// (skipping truncated tag verification, matching discordgo's approach).
func DecryptSecureFrame(baseSecret, data []byte) ([]byte, error) {
	if len(data) < TruncatedTagSize+4 {
		return data, nil
	}
	if data[len(data)-2] != 0xFA || data[len(data)-1] != 0xFA {
		return data, nil
	}
	supplementalSize := int(data[len(data)-3])
	if len(data) < supplementalSize || supplementalSize < TruncatedTagSize+3 {
		return nil, fmt.Errorf("dave: invalid supplemental size: %d", supplementalSize)
	}

	opusDataLen := len(data) - supplementalSize
	nonceBytes := data[opusDataLen+TruncatedTagSize : len(data)-3]
	nonceCounter, _, _ := DecodeULEB128(nonceBytes)

	key, err := HashRatchetGetKey(baseSecret, nonceCounter>>24)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// AES-GCM uses AES-CTR starting at J0+1. J0 = nonce || 0x00000001.
	nonce12 := make([]byte, 12)
	binary.LittleEndian.PutUint32(nonce12[8:], nonceCounter)
	ctr := make([]byte, 16)
	copy(ctr[:12], nonce12)
	binary.BigEndian.PutUint32(ctr[12:], 2)

	stream := cipher.NewCTR(block, ctr)
	plain := make([]byte, opusDataLen)
	stream.XORKeyStream(plain, data[:opusDataLen])
	return plain, nil
}

// Reset clears all session state.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exporterSecret = nil
	s.senderKey = nil
	s.senderNonce = 0
	s.frameCipher = nil
	s.active = false
	s.kpBundle = nil
	s.pendingTransitionID = 0
	s.pendingVersion = 0
	s.ratchetBaseSecret = nil
	s.currentGeneration = 0
	s.hasPendingKey = false
}
