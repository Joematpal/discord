package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

func main() {
	key := make([]byte, 16)
	rand.Read(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		fmt.Println("aes error:", err)
		return
	}
	gcm, err := cipher.NewGCMWithTagSize(block, 8)
	fmt.Println("gcm:", gcm != nil, "err:", err)

	if gcm != nil {
		nonce := make([]byte, gcm.NonceSize())
		plaintext := []byte("hello world")
		sealed := gcm.Seal(nil, nonce, plaintext, nil)
		fmt.Printf("plaintext: %d bytes, sealed: %d bytes (overhead: %d)\n",
			len(plaintext), len(sealed), len(sealed)-len(plaintext))

		opened, err := gcm.Open(nil, nonce, sealed, nil)
		fmt.Printf("opened: %q, err: %v\n", opened, err)
	}
}
