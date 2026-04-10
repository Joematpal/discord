// +build ignore

package main

import (
	"encoding/binary"
	"fmt"
	"os"

	pionopus "github.com/pion/opus"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/test_pion_decode.go input.ogg output.raw")
		os.Exit(1)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	dec, _ := pionopus.NewDecoderWithOutput(48000, 1)
	out := make([]float32, 960)

	// Try decoding the first audio packet (skip OGG container).
	// Find first non-header Opus packet by looking for TOC bytes.
	// For a quick test, just try raw decode of the first 50 bytes as if it were a packet.

	// Actually, let's try with a known Hybrid packet: TOC 0x78
	testPacket := []byte{0x78} // just TOC, will fail but shows the error
	_, err = dec.DecodeToFloat32(testPacket, out)
	fmt.Printf("Hybrid TOC only: %v\n", err)

	// Try SILK WB 20ms TOC
	testPacket2 := make([]byte, len(data))
	copy(testPacket2, data)
	if len(testPacket2) > 0 {
		testPacket2[0] = 0x48 // SILK WB 20ms
	}
	_, err = dec.DecodeToFloat32(testPacket2[:50], out)
	fmt.Printf("Rewritten to SILK WB: %v\n", err)

	_ = binary.LittleEndian
}
