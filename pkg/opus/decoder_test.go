package opus

import (
	"testing"
)

func TestNewDecoder(t *testing.T) {
	d, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if d.SampleRate() != 48000 {
		t.Errorf("SampleRate = %d", d.SampleRate())
	}
	if d.Channels() != 2 {
		t.Errorf("Channels = %d", d.Channels())
	}
}

func TestNewDecoder_InvalidRate(t *testing.T) {
	_, err := NewDecoder(44100, 1)
	if err != ErrBadSampleRate {
		t.Errorf("got %v, want ErrBadSampleRate", err)
	}
}

func TestNewDecoder_InvalidChannels(t *testing.T) {
	_, err := NewDecoder(48000, 0)
	if err != ErrBadChannels {
		t.Errorf("got %v, want ErrBadChannels", err)
	}
	_, err = NewDecoder(48000, 3)
	if err != ErrBadChannels {
		t.Errorf("got %v, want ErrBadChannels", err)
	}
}

func TestDecoder_SilenceFrame(t *testing.T) {
	d, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := d.Decode(SilenceFrame, 960, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 960 {
		t.Fatalf("len = %d, want 960", len(pcm))
	}
}

func TestDecoder_PLC(t *testing.T) {
	d, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	// First decode a real frame to populate state.
	_, _ = d.Decode(SilenceFrame, 960, false)
	// Then do PLC (nil data).
	pcm, err := d.DecodeFloat(nil, 960, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 960 {
		t.Fatalf("PLC len = %d, want 960", len(pcm))
	}
}

func TestDecoder_PLCFade(t *testing.T) {
	d, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Successive PLC calls should produce fading output.
	_, _ = d.Decode(SilenceFrame, 960, false)
	d.lastGoodSamples = make([]float32, 960)
	for i := range d.lastGoodSamples {
		d.lastGoodSamples[i] = 0.5
	}
	d.plcCount = 0

	pcm1, _ := d.DecodeFloat(nil, 960, false)
	pcm2, _ := d.DecodeFloat(nil, 960, false)

	// Later PLC should have lower amplitude.
	var sum1, sum2 float32
	for i := 0; i < 960; i++ {
		sum1 += abs32(pcm1[i])
		sum2 += abs32(pcm2[i])
	}
	if sum2 >= sum1 && sum1 > 0 {
		t.Errorf("PLC should fade: sum1=%f, sum2=%f", sum1, sum2)
	}
}

func TestDecoder_Reset(t *testing.T) {
	d, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	d.plcCount = 10
	d.Reset()
	if d.plcCount != 0 {
		t.Error("Reset didn't clear PLC count")
	}
}

func TestDecoder_AllSampleRates(t *testing.T) {
	for _, sr := range []int{8000, 12000, 16000, 24000, 48000} {
		d, err := NewDecoder(sr, 1)
		if err != nil {
			t.Fatalf("rate %d: %v", sr, err)
		}
		if d.SampleRate() != sr {
			t.Errorf("rate %d: got %d", sr, d.SampleRate())
		}
	}
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
