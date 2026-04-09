#!/bin/bash

# Check if a filename was provided
if [ -z "$1" ]; then
    echo "Usage: $0 input_file.wav"
    exit 1
fi

INPUT="$1"
OPUS_TEMP="temp_converted.opus"
FINAL_WAV="result_back_to.wav"

echo "--- Converting WAV to Opus (128k) ---"
# ffmpeg -i "$INPUT" -c:a libopus -b:a 128k "$OPUS_TEMP" -y
go run ./cmd/opus -b 48000 $INPUT > $OPUS_TEMP

echo "--- Converting Opus back to WAV ---"
ffmpeg -i "$OPUS_TEMP" "$FINAL_WAV" -y

echo "--- Playing the final WAV file ---"
ffplay -autoexit "$FINAL_WAV"

# Optional: Clean up temporary files
# rm "$OPUS_TEMP" "$FINAL_WAV"
