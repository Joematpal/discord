package opus

import (
	"math"
	"testing"
)

func TestNewEncoder(t *testing.T) {
	e, err := NewEncoder(48000, 2, AppAudio)
	if err != nil {
		t.Fatal(err)
	}
	if e.SampleRate() != 48000 {
		t.Errorf("SampleRate = %d", e.SampleRate())
	}
	if e.Channels() != 2 {
		t.Errorf("Channels = %d", e.Channels())
	}
	if e.Application() != AppAudio {
		t.Errorf("Application = %v", e.Application())
	}
}

func TestNewEncoder_InvalidRate(t *testing.T) {
	_, err := NewEncoder(44100, 1, AppVoIP)
	if err != ErrBadSampleRate {
		t.Errorf("got %v", err)
	}
}

func TestNewEncoder_InvalidChannels(t *testing.T) {
	_, err := NewEncoder(48000, 0, AppVoIP)
	if err != ErrBadChannels {
		t.Errorf("got %v", err)
	}
}

func TestNewEncoder_InvalidApp(t *testing.T) {
	_, err := NewEncoder(48000, 1, Application(9999))
	if err != ErrBadApplication {
		t.Errorf("got %v", err)
	}
}

func TestEncoder_SetBitrate(t *testing.T) {
	e, _ := NewEncoder(48000, 1, AppVoIP)
	if err := e.SetBitrate(64000); err != nil {
		t.Fatal(err)
	}
	if e.Bitrate() != 64000 {
		t.Errorf("Bitrate = %d", e.Bitrate())
	}
	if err := e.SetBitrate(100); err == nil {
		t.Error("expected error for bitrate 100")
	}
}

func TestEncoder_SetComplexity(t *testing.T) {
	e, _ := NewEncoder(48000, 1, AppVoIP)
	if err := e.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	if e.Complexity() != 10 {
		t.Errorf("Complexity = %d", e.Complexity())
	}
	if err := e.SetComplexity(11); err == nil {
		t.Error("expected error for complexity 11")
	}
}

func TestEncoder_Silence(t *testing.T) {
	e, _ := NewEncoder(48000, 1, AppVoIP)
	pcm := make([]int16, 960)
	pkt, err := e.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	if !IsSilence(pkt) {
		t.Errorf("silent input should produce silence frame, got %d bytes: %v", len(pkt), pkt)
	}
}

func TestEncoder_NonSilence(t *testing.T) {
	e, _ := NewEncoder(48000, 1, AppAudio)
	pcm := make([]int16, 960)
	// Generate a 440 Hz sine wave.
	for i := range pcm {
		pcm[i] = int16(16000 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	pkt, err := e.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) < 2 {
		t.Fatalf("packet too small: %d bytes", len(pkt))
	}
	// Verify TOC byte.
	toc := ParseTOC(pkt[0])
	if toc.Mode() != ModeCELT {
		t.Errorf("mode = %v, want CELT", toc.Mode())
	}
	if toc.Bandwidth() != BandwidthFullband {
		t.Errorf("bandwidth = %v, want fullband", toc.Bandwidth())
	}
}

func TestEncoder_EncodeFloat(t *testing.T) {
	e, _ := NewEncoder(48000, 1, AppAudio)
	pcm := make([]float32, 960)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/48000))
	}
	pkt, err := e.EncodeFloat(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) < 2 {
		t.Fatal("packet too small")
	}
}

func TestEncoder_SmallBuffer(t *testing.T) {
	e, _ := NewEncoder(48000, 1, AppVoIP)
	pcm := make([]int16, 10)
	_, err := e.Encode(pcm, 960)
	if err == nil {
		t.Error("expected error for small PCM buffer")
	}
}

func TestEncoder_Reset(t *testing.T) {
	e, _ := NewEncoder(48000, 1, AppVoIP)
	e.Reset()
	// After reset, encoding should still work.
	pcm := make([]int16, 960)
	_, err := e.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEncoderDecoder_Roundtrip(t *testing.T) {
	enc, _ := NewEncoder(48000, 1, AppAudio)
	dec, _ := NewDecoder(48000, 1)

	// Encode a sine wave.
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	pkt, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}

	// Decode it back.
	out, err := dec.Decode(pkt, 960, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 960 {
		t.Fatalf("decoded %d samples, want 960", len(out))
	}

	// The output should have some non-zero samples (not silent).
	hasNonZero := false
	for _, s := range out {
		if s != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("decoded output is all zeros for non-silent input")
	}
}

func TestApplicationString(t *testing.T) {
	if s := AppVoIP.String(); s != "VoIP" {
		t.Errorf("got %q", s)
	}
	if s := AppAudio.String(); s != "Audio" {
		t.Errorf("got %q", s)
	}
	if s := AppLowDelay.String(); s != "LowDelay" {
		t.Errorf("got %q", s)
	}
	if s := Application(9999).String(); s != "Unknown" {
		t.Errorf("got %q", s)
	}
}
