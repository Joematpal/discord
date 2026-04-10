package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/joematpal/discord/pkg/opus"

	"github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		Name:  "opus",
		Usage: "Opus audio tools",
		Commands: []*cli.Command{
			encodeCmd(),
			decodeCmd(),
			listenCmd(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func encodeCmd() *cli.Command {
	return &cli.Command{
		Name:      "encode",
		Usage:     "Encode a WAV file to OGG/Opus on stdout",
		ArgsUsage: "<input.wav>",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:    "bitrate",
				Aliases: []string{"b"},
				Value:   96000,
				Usage:   "target bitrate in bits/s",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 1 {
				return fmt.Errorf("missing input WAV file\n\nUsage:\n  opus encode input.wav | ffplay -i pipe:0")
			}

			f, err := os.Open(args.First())
			if err != nil {
				return err
			}
			defer f.Close()

			wav, err := readWAV(f)
			if err != nil {
				return fmt.Errorf("read WAV: %w", err)
			}

			sampleRate := int(wav.sampleRate)
			channels := int(wav.channels)

			enc, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
			if err != nil {
				return fmt.Errorf("create encoder: %w", err)
			}
			enc.SetBitrate(int(cmd.Int("bitrate")))

			frameSize := sampleRate / 50 // 20ms
			frameSamples := frameSize * channels

			ogg := &oggWriter{w: os.Stdout, serial: 0x4F707573}
			if err := ogg.writeHeaders(uint8(channels), uint32(sampleRate)); err != nil {
				return fmt.Errorf("write OGG headers: %w", err)
			}

			preSkip := uint64(312)
			granule := preSkip

			for off := 0; off < len(wav.samples); off += frameSamples {
				frame := make([]int16, frameSamples)
				copy(frame, wav.samples[off:])

				pkt, err := enc.Encode(frame, frameSize)
				if err != nil {
					return fmt.Errorf("encode frame: %w", err)
				}

				granule48k := uint64(frameSize) * 48000 / uint64(sampleRate)
				granule += granule48k
				last := off+frameSamples >= len(wav.samples)

				if err := ogg.writeAudioPacket(pkt, granule, last); err != nil {
					return fmt.Errorf("write OGG page: %w", err)
				}
			}

			dur := float64(len(wav.samples)/channels) / float64(sampleRate)
			fmt.Fprintf(os.Stderr, "encoded %.1fs (%d ch, %d Hz) to OGG/Opus\n", dur, channels, sampleRate)
			return nil
		},
	}
}

func decodeCmd() *cli.Command {
	return &cli.Command{
		Name:      "decode",
		Usage:     "Decode an OGG/Opus file to WAV on stdout",
		ArgsUsage: "<input.ogg>",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:    "rate",
				Aliases: []string{"r"},
				Value:   48000,
				Usage:   "output sample rate",
			},
			&cli.IntFlag{
				Name:    "channels",
				Aliases: []string{"c"},
				Value:   1,
				Usage:   "output channel count (1 or 2)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 1 {
				return fmt.Errorf("missing input OGG/Opus file\n\nUsage:\n  opus decode input.ogg | ffplay -f s16le -ar 48000 -ac 1 -i pipe:0\n  opus decode input.ogg > output.wav")
			}

			f, err := os.Open(args.First())
			if err != nil {
				return err
			}
			defer f.Close()

			sampleRate := int(cmd.Int("rate"))
			channels := int(cmd.Int("channels"))

			dec, err := opus.NewDecoder(sampleRate, channels)
			if err != nil {
				return fmt.Errorf("create decoder: %w", err)
			}

			frameSize := sampleRate / 50 // 20ms

			// Read OGG pages and extract Opus packets.
			packets, err := readOGGOpusPackets(f)
			if err != nil {
				return fmt.Errorf("read OGG: %w", err)
			}

			// Decode all packets to PCM.
			var allPCM []int16
			for _, pkt := range packets {
				pcm, err := dec.Decode(pkt, frameSize, false)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: decode error: %v (skipping frame)\n", err)
					continue
				}
				allPCM = append(allPCM, pcm...)
			}

			// Write WAV to stdout.
			if err := writeWAV(os.Stdout, allPCM, uint32(sampleRate), uint16(channels)); err != nil {
				return fmt.Errorf("write WAV: %w", err)
			}

			dur := float64(len(allPCM)/channels) / float64(sampleRate)
			fmt.Fprintf(os.Stderr, "decoded %d packets → %.1fs (%d ch, %d Hz)\n",
				len(packets), dur, channels, sampleRate)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// OGG/Opus reader — extracts Opus packets from OGG container
// ---------------------------------------------------------------------------

func readOGGOpusPackets(r io.Reader) ([][]byte, error) {
	var packets [][]byte
	pageIdx := 0

	for {
		// Read OGG page header.
		var hdr [27]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}
		if string(hdr[:4]) != "OggS" {
			return nil, fmt.Errorf("invalid OGG sync pattern")
		}

		numSegments := int(hdr[26])
		segTable := make([]byte, numSegments)
		if _, err := io.ReadFull(r, segTable); err != nil {
			return nil, err
		}

		// Calculate total page data size.
		totalSize := 0
		for _, s := range segTable {
			totalSize += int(s)
		}

		pageData := make([]byte, totalSize)
		if _, err := io.ReadFull(r, pageData); err != nil {
			return nil, err
		}

		// Reassemble packets from segments.
		// Segments of 255 bytes are continuations; a segment < 255 terminates a packet.
		offset := 0
		var partial []byte
		for _, segSize := range segTable {
			seg := pageData[offset : offset+int(segSize)]
			offset += int(segSize)
			partial = append(partial, seg...)
			if segSize < 255 {
				// Packet complete.
				if pageIdx >= 2 && len(partial) > 0 {
					// Skip first 2 pages (OpusHead + OpusTags).
					pkt := make([]byte, len(partial))
					copy(pkt, partial)
					packets = append(packets, pkt)
				}
				partial = partial[:0]
			}
		}

		pageIdx++
	}

	return packets, nil
}

// ---------------------------------------------------------------------------
// WAV writer
// ---------------------------------------------------------------------------

func writeWAV(w io.Writer, samples []int16, sampleRate uint32, channels uint16) error {
	bitsPerSample := uint16(16)
	blockAlign := channels * bitsPerSample / 8
	byteRate := sampleRate * uint32(blockAlign)
	dataSize := uint32(len(samples)) * uint32(bitsPerSample/8)

	// RIFF header.
	var buf [44]byte
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], 36+dataSize)
	copy(buf[8:12], "WAVE")

	// fmt chunk.
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16) // chunk size
	binary.LittleEndian.PutUint16(buf[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(buf[22:24], channels)
	binary.LittleEndian.PutUint32(buf[24:28], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:32], byteRate)
	binary.LittleEndian.PutUint16(buf[32:34], blockAlign)
	binary.LittleEndian.PutUint16(buf[34:36], bitsPerSample)

	// data chunk.
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], dataSize)

	if _, err := w.Write(buf[:]); err != nil {
		return err
	}

	return binary.Write(w, binary.LittleEndian, samples)
}

// ---------------------------------------------------------------------------
// WAV reader
// ---------------------------------------------------------------------------

type wavFile struct {
	sampleRate    uint32
	channels      uint16
	bitsPerSample uint16
	samples       []int16
}

func readWAV(r io.ReadSeeker) (*wavFile, error) {
	var id [4]byte
	if _, err := io.ReadFull(r, id[:]); err != nil {
		return nil, err
	}
	if string(id[:]) != "RIFF" {
		return nil, fmt.Errorf("not a RIFF file")
	}

	var fileSize uint32
	binary.Read(r, binary.LittleEndian, &fileSize)

	if _, err := io.ReadFull(r, id[:]); err != nil {
		return nil, err
	}
	if string(id[:]) != "WAVE" {
		return nil, fmt.Errorf("not a WAVE file")
	}

	wav := &wavFile{}
	dataFound := false

	for !dataFound {
		if _, err := io.ReadFull(r, id[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}
		var chunkSize uint32
		if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
			return nil, err
		}

		switch string(id[:]) {
		case "fmt ":
			var audioFormat uint16
			binary.Read(r, binary.LittleEndian, &audioFormat)
			if audioFormat != 1 {
				return nil, fmt.Errorf("unsupported format %d (only PCM/1)", audioFormat)
			}
			binary.Read(r, binary.LittleEndian, &wav.channels)
			binary.Read(r, binary.LittleEndian, &wav.sampleRate)
			r.Seek(4, io.SeekCurrent) // byte rate
			r.Seek(2, io.SeekCurrent) // block align
			binary.Read(r, binary.LittleEndian, &wav.bitsPerSample)
			if chunkSize > 16 {
				r.Seek(int64(chunkSize-16), io.SeekCurrent)
			}

		case "data":
			dataFound = true
			n := int(chunkSize) / int(wav.bitsPerSample/8)
			wav.samples = make([]int16, n)
			switch wav.bitsPerSample {
			case 16:
				if err := binary.Read(r, binary.LittleEndian, wav.samples); err != nil {
					return nil, fmt.Errorf("read PCM16: %w", err)
				}
			case 8:
				buf := make([]byte, n)
				if _, err := io.ReadFull(r, buf); err != nil {
					return nil, fmt.Errorf("read PCM8: %w", err)
				}
				for i, b := range buf {
					wav.samples[i] = (int16(b) - 128) << 8
				}
			default:
				return nil, fmt.Errorf("unsupported bit depth %d", wav.bitsPerSample)
			}

		default:
			r.Seek(int64(chunkSize), io.SeekCurrent)
		}
	}

	if !dataFound {
		return nil, fmt.Errorf("no data chunk found")
	}
	return wav, nil
}

// ---------------------------------------------------------------------------
// OGG/Opus writer (RFC 3533 + RFC 7845)
// ---------------------------------------------------------------------------

type oggWriter struct {
	w         io.Writer
	serial    uint32
	pageSeqNo uint32
}

func (o *oggWriter) writeHeaders(channels uint8, inputRate uint32) error {
	head := makeOpusHead(channels, inputRate)
	if err := o.writePage(0x02, 0, [][]byte{head}); err != nil { // BOS
		return err
	}
	tags := makeOpusTags()
	return o.writePage(0x00, 0, [][]byte{tags})
}

func (o *oggWriter) writeAudioPacket(pkt []byte, granule uint64, eos bool) error {
	flags := byte(0x00)
	if eos {
		flags = 0x04
	}
	return o.writePage(flags, granule, [][]byte{pkt})
}

func (o *oggWriter) writePage(headerType byte, granule uint64, packets [][]byte) error {
	var segments []byte
	var data []byte
	for _, pkt := range packets {
		for len(pkt) >= 255 {
			segments = append(segments, 255)
			data = append(data, pkt[:255]...)
			pkt = pkt[255:]
		}
		segments = append(segments, byte(len(pkt)))
		data = append(data, pkt...)
	}

	hdr := make([]byte, 27+len(segments))
	copy(hdr[0:4], "OggS")
	hdr[4] = 0 // version
	hdr[5] = headerType
	binary.LittleEndian.PutUint64(hdr[6:14], granule)
	binary.LittleEndian.PutUint32(hdr[14:18], o.serial)
	binary.LittleEndian.PutUint32(hdr[18:22], o.pageSeqNo)
	hdr[26] = byte(len(segments))
	copy(hdr[27:], segments)

	crc := oggCRC(hdr)
	crc = oggCRCUpdate(crc, data)
	binary.LittleEndian.PutUint32(hdr[22:26], crc)

	o.pageSeqNo++

	if _, err := o.w.Write(hdr); err != nil {
		return err
	}
	_, err := o.w.Write(data)
	return err
}

func makeOpusHead(channels uint8, inputRate uint32) []byte {
	h := make([]byte, 19)
	copy(h[0:8], "OpusHead")
	h[8] = 1 // version
	h[9] = channels
	binary.LittleEndian.PutUint16(h[10:12], 312) // pre-skip
	binary.LittleEndian.PutUint32(h[12:16], inputRate)
	binary.LittleEndian.PutUint16(h[16:18], 0) // output gain
	h[18] = 0                                  // channel mapping family
	return h
}

func makeOpusTags() []byte {
	vendor := "github.com/joematpal/discord/pkg/opus"
	t := make([]byte, 8+4+len(vendor)+4)
	copy(t[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(t[8:12], uint32(len(vendor)))
	copy(t[12:12+len(vendor)], vendor)
	binary.LittleEndian.PutUint32(t[12+len(vendor):], 0)
	return t
}

// OGG CRC-32 (polynomial 0x04C11DB7, direct / non-reflected).
var oggCRCTable [256]uint32

func init() {
	for i := 0; i < 256; i++ {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		oggCRCTable[i] = r
	}
}

func oggCRC(data []byte) uint32 {
	return oggCRCUpdate(0, data)
}

func oggCRCUpdate(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}
