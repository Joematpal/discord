package dave

import (
	"bytes"
	"testing"
)

func TestKeyRatchet_InitialKey(t *testing.T) {
	secret := []byte("base-secret-1234")
	kr := NewKeyRatchet(secret)

	key, err := kr.Get(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != KeySize {
		t.Errorf("key len = %d", len(key))
	}
}

func TestKeyRatchet_DeterministicKeys(t *testing.T) {
	secret := []byte("deterministic-test")

	kr1 := NewKeyRatchet(secret)
	kr2 := NewKeyRatchet(secret)

	for gen := uint32(0); gen < 5; gen++ {
		k1, _ := kr1.Get(gen)
		k2, _ := kr2.Get(gen)
		if !bytes.Equal(k1, k2) {
			t.Fatalf("gen %d: keys differ", gen)
		}
	}
}

func TestKeyRatchet_DifferentGenerations(t *testing.T) {
	kr := NewKeyRatchet([]byte("test-secret"))
	key0, _ := kr.Get(0)
	key1, _ := kr.Get(1)
	key5, _ := kr.Get(5)

	if bytes.Equal(key0, key1) {
		t.Error("gen 0 and 1 should differ")
	}
	if bytes.Equal(key1, key5) {
		t.Error("gen 1 and 5 should differ")
	}
}

func TestKeyRatchet_DifferentSecrets(t *testing.T) {
	kr1 := NewKeyRatchet([]byte("secret-A"))
	kr2 := NewKeyRatchet([]byte("secret-B"))

	k1, _ := kr1.Get(0)
	k2, _ := kr2.Get(0)
	if bytes.Equal(k1, k2) {
		t.Error("different secrets should produce different keys")
	}
}

func TestKeyRatchet_SkipAhead(t *testing.T) {
	kr := NewKeyRatchet([]byte("skip-ahead"))
	// Skip to generation 100.
	key, err := kr.Get(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != KeySize {
		t.Errorf("key len = %d", len(key))
	}
	if kr.Generation() != 100 {
		t.Errorf("generation = %d", kr.Generation())
	}
}

func TestKeyRatchet_CachedPrevious(t *testing.T) {
	kr := NewKeyRatchet([]byte("cache-test"))
	key0, _ := kr.Get(0)
	kr.Get(5) // advance to gen 5

	// Gen 0 should still be cached (within 10s window).
	cached, err := kr.Get(0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cached, key0) {
		t.Error("cached key should match original")
	}
}

// ---------------------------------------------------------------------------
// SenderKeyStore
// ---------------------------------------------------------------------------

func TestSenderKeyStore(t *testing.T) {
	store := NewSenderKeyStore()
	store.SetSenderKey(42, []byte("sender-42-secret"))
	store.SetSenderKey(99, []byte("sender-99-secret"))

	k42, err := store.GetKey(42, 0)
	if err != nil {
		t.Fatal(err)
	}
	k99, err := store.GetKey(99, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(k42, k99) {
		t.Error("different senders should have different keys")
	}
}

func TestSenderKeyStore_UnknownSender(t *testing.T) {
	store := NewSenderKeyStore()
	_, err := store.GetKey(999, 0)
	if err != ErrUnknownSender {
		t.Errorf("got %v", err)
	}
}

func TestSenderKeyStore_RemoveSender(t *testing.T) {
	store := NewSenderKeyStore()
	store.SetSenderKey(1, []byte("secret"))
	store.RemoveSender(1)
	_, err := store.GetKey(1, 0)
	if err != ErrUnknownSender {
		t.Errorf("got %v", err)
	}
}

func TestSenderKeyStore_Clear(t *testing.T) {
	store := NewSenderKeyStore()
	store.SetSenderKey(1, []byte("a"))
	store.SetSenderKey(2, []byte("b"))
	store.Clear()

	_, err := store.GetKey(1, 0)
	if err != ErrUnknownSender {
		t.Error("expected unknown after clear")
	}
}

func TestSenderKeyStore_EpochTransition(t *testing.T) {
	store := NewSenderKeyStore()
	store.SetSenderKey(1, []byte("epoch-1-secret"))
	key1, _ := store.GetKey(1, 0)

	// Simulate epoch change: set new secret.
	store.SetSenderKey(1, []byte("epoch-2-secret"))
	key2, _ := store.GetKey(1, 0)

	if bytes.Equal(key1, key2) {
		t.Error("keys should differ across epochs")
	}
}
