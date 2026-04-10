#!/usr/bin/env python3
"""Analyze TOC bytes from an OGG/Opus file to see which modes are used."""
import struct, sys
from collections import Counter

def read_ogg_pages(f):
    pages = []
    while True:
        sync = f.read(4)
        if len(sync) < 4: break
        if sync != b'OggS': break
        hdr = f.read(23)
        _, _, granule, serial, seqno, crc, nseg = struct.unpack('<BBQIIIB', hdr)
        segtable = f.read(nseg)
        total = sum(segtable)
        data = f.read(total)
        pages.append((segtable, data))
    return pages

MODES = {0: 'SILK', 1: 'SILK', 2: 'SILK', 3: 'SILK',
         4: 'SILK', 5: 'SILK', 6: 'SILK', 7: 'SILK',
         8: 'SILK', 9: 'SILK', 10: 'SILK', 11: 'SILK',
         12: 'Hybrid', 13: 'Hybrid', 14: 'Hybrid', 15: 'Hybrid',
         16: 'CELT', 17: 'CELT', 18: 'CELT', 19: 'CELT',
         20: 'CELT', 21: 'CELT', 22: 'CELT', 23: 'CELT',
         24: 'CELT', 25: 'CELT', 26: 'CELT', 27: 'CELT',
         28: 'CELT', 29: 'CELT', 30: 'CELT', 31: 'CELT'}

if len(sys.argv) < 2:
    print("Usage: analyze_opus_tocs.py <file.ogg>")
    sys.exit(1)

with open(sys.argv[1], 'rb') as f:
    pages = read_ogg_pages(f)

packets = []
for segtable, data in pages:
    offset = 0
    partial = b''
    for seg_size in segtable:
        seg = data[offset:offset+seg_size]
        offset += seg_size
        partial += seg
        if seg_size < 255:
            packets.append(partial)
            partial = b''

audio = packets[2:]  # skip OpusHead + OpusTags
toc_counter = Counter()
mode_counter = Counter()
config_counter = Counter()

for p in audio:
    if not p: continue
    toc = p[0]
    config = toc >> 3
    stereo = bool((toc >> 2) & 1)
    code = toc & 3
    mode = MODES.get(config, '?')
    toc_counter[f'0x{toc:02x}'] += 1
    mode_counter[mode] += 1
    config_counter[config] += 1

print(f"Total audio packets: {len(audio)}")
print(f"\nModes used:")
for mode, count in mode_counter.most_common():
    print(f"  {mode}: {count} ({100*count/len(audio):.1f}%)")
print(f"\nConfigs used:")
for cfg, count in config_counter.most_common():
    print(f"  config {cfg} ({MODES[cfg]}): {count}")
print(f"\nTOC bytes:")
for toc, count in toc_counter.most_common():
    print(f"  {toc}: {count}")
