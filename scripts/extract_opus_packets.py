#!/usr/bin/env python3
"""Extract raw Opus packets from an OGG/Opus file and print stats."""
import struct, sys

def read_ogg_pages(f):
    pages = []
    while True:
        sync = f.read(4)
        if len(sync) < 4:
            break
        if sync != b'OggS':
            print(f"Bad sync at offset {f.tell()-4}", file=sys.stderr)
            break
        hdr = f.read(23)
        version, htype, granule, serial, seqno, crc, nseg = struct.unpack('<BBQIIIB', hdr)
        segtable = f.read(nseg)
        total = sum(segtable)
        data = f.read(total)
        pages.append((htype, granule, seqno, segtable, data))
    return pages

if len(sys.argv) < 2:
    print("Usage: extract_opus_packets.py <file.ogg>")
    sys.exit(1)

with open(sys.argv[1], 'rb') as f:
    pages = read_ogg_pages(f)

print(f"Total OGG pages: {len(pages)}")

packets = []
page_idx = 0
for htype, granule, seqno, segtable, data in pages:
    page_idx += 1
    # Reassemble packets from segments.
    offset = 0
    partial = b''
    for seg_size in segtable:
        seg = data[offset:offset+seg_size]
        offset += seg_size
        partial += seg
        if seg_size < 255:
            packets.append(partial)
            partial = b''

# First 2 packets are OpusHead and OpusTags.
print(f"Total packets: {len(packets)}")
if len(packets) >= 2:
    print(f"  OpusHead: {packets[0][:8]}")
    print(f"  OpusTags: {packets[1][:8]}")

audio_packets = packets[2:]
print(f"Audio packets: {len(audio_packets)}")

if audio_packets:
    sizes = [len(p) for p in audio_packets]
    silence = sum(1 for p in audio_packets if p == b'\xf8\xff\xfe')
    speech = len(audio_packets) - silence
    print(f"  Silence frames: {silence}")
    print(f"  Speech frames:  {speech}")
    print(f"  Size range: {min(sizes)}-{max(sizes)} bytes")
    print(f"  First 5 speech packet sizes:", [len(p) for p in audio_packets if p != b'\xf8\xff\xfe'][:5])

    # Print first speech packet as hex for test vector.
    for p in audio_packets:
        if p != b'\xf8\xff\xfe':
            print(f"\nFirst speech packet ({len(p)} bytes):")
            print(f"  TOC: 0x{p[0]:02x} (config={p[0]>>3}, stereo={bool((p[0]>>2)&1)}, code={p[0]&3})")
            print(f"  Hex: {p[:32].hex()}")
            if len(p) > 32:
                print(f"       {p[32:64].hex()}")
            break
