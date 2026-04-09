package voice

import (
	"encoding/binary"
	"io"
	"os"
	"sync"
)

// OggOpusWriter writes Opus packets to an OGG/Opus file (RFC 7845).
type OggOpusWriter struct {
	mu        sync.Mutex
	w         io.Writer
	f         *os.File // non-nil if we opened the file
	serial    uint32
	pageSeqNo uint32
	granule   uint64
	preSkip   uint16
	closed    bool
}

// NewOggOpusFileWriter creates a writer that writes to a file.
func NewOggOpusFileWriter(filename string, sampleRate uint32, channels uint8) (*OggOpusWriter, error) {
	f, err := os.Create(filename)
	if err != nil {
		return nil, err
	}
	w := &OggOpusWriter{
		w:       f,
		f:       f,
		serial:  0x4F707573,
		preSkip: 312,
	}
	if err := w.writeHeaders(channels, sampleRate); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// WritePacket writes a single Opus packet to the OGG stream.
// frameSamples is typically 960 for 20ms at 48kHz.
func (w *OggOpusWriter) WritePacket(opusData []byte, frameSamples uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.granule += frameSamples
	return w.writePage(0x00, w.granule, [][]byte{opusData})
}

// Close finalizes the OGG stream.
func (w *OggOpusWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	// Write EOS page.
	w.writePage(0x04, w.granule, [][]byte{})
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}

func (w *OggOpusWriter) writeHeaders(channels uint8, inputRate uint32) error {
	head := makeOpusHead(channels, inputRate, w.preSkip)
	if err := w.writePage(0x02, 0, [][]byte{head}); err != nil {
		return err
	}
	tags := makeOpusTags()
	return w.writePage(0x00, 0, [][]byte{tags})
}

func (w *OggOpusWriter) writePage(headerType byte, granule uint64, packets [][]byte) error {
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
	binary.LittleEndian.PutUint32(hdr[14:18], w.serial)
	binary.LittleEndian.PutUint32(hdr[18:22], w.pageSeqNo)
	hdr[26] = byte(len(segments))
	copy(hdr[27:], segments)

	crc := oggCRC(hdr)
	crc = oggCRCUpdate(crc, data)
	binary.LittleEndian.PutUint32(hdr[22:26], crc)

	w.pageSeqNo++

	if _, err := w.w.Write(hdr); err != nil {
		return err
	}
	_, err := w.w.Write(data)
	return err
}

func makeOpusHead(channels uint8, inputRate uint32, preSkip uint16) []byte {
	h := make([]byte, 19)
	copy(h[0:8], "OpusHead")
	h[8] = 1
	h[9] = channels
	binary.LittleEndian.PutUint16(h[10:12], preSkip)
	binary.LittleEndian.PutUint32(h[12:16], inputRate)
	binary.LittleEndian.PutUint16(h[16:18], 0)
	h[18] = 0
	return h
}

func makeOpusTags() []byte {
	vendor := "discord/pkg/voice"
	t := make([]byte, 8+4+len(vendor)+4)
	copy(t[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(t[8:12], uint32(len(vendor)))
	copy(t[12:12+len(vendor)], vendor)
	binary.LittleEndian.PutUint32(t[12+len(vendor):], 0)
	return t
}

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

func oggCRC(data []byte) uint32  { return oggCRCUpdate(0, data) }
func oggCRCUpdate(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}
