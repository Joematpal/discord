#!/usr/bin/env python3
"""Analyze a WAV file for audio content."""
import wave, struct, sys

if len(sys.argv) < 2:
    print("Usage: analyze_wav.py <file.wav>")
    sys.exit(1)

w = wave.open(sys.argv[1], 'rb')
frames = w.readframes(w.getnframes())
samples = struct.unpack('<%dh' % (len(frames)//2), frames)
total = len(samples)
nonzero = sum(1 for s in samples if abs(s) > 100)
peak = max(abs(s) for s in samples) if samples else 0
print(f'total samples: {total}')
print(f'non-zero (>100): {nonzero} ({100*nonzero/total:.1f}%)')
print(f'peak amplitude: {peak}')
print(f'duration: {total/48000:.1f}s')
print(f'channels: {w.getnchannels()}')
print(f'sample rate: {w.getframerate()}')
w.close()
