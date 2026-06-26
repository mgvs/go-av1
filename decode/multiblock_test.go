package decode

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/obu"
	"github.com/mgvs/go-av1/tile"
)

// wideTU is a real aomenc 3.13 temporal unit: a 128x64 keyframe (a horizontal
// gradient) with all in-loop filters disabled. It is two horizontally-adjacent
// BLOCK_64X64 blocks. This exercises multi-block decode end to end: the partition
// at a superblock boundary, DC intra prediction of the second block from the first
// block's reconstructed right edge (BlockDecoded availability), the cross-block
// coefficient level/DC contexts, and a coded chroma TX_32X32 residual (tx_type
// derived from the chroma mode). Output is byte-exact with dav1d.
var wideTU = []byte{
	0x12, 0x00,
	0x0a, 0x06, 0x18, 0x19, 0x7f, 0xfe, 0xc0, 0x04,
	0x32, 0x16, 0x15, 0x00, 0x00, 0x00, 0x20, 0x22, 0x64, 0x79, 0xa7, 0xcd, 0x2d, 0xd6,
	0x5c, 0xa1, 0x5b, 0xbd, 0x65, 0x2a, 0xca, 0x97, 0xf8, 0xc0,
}

const wideSHA256 = "1e5460af7f6b99b9cb3530aa139b0905bbc5f8c24e46d38e446655d761c983b4"

func TestDecodeMultiBlockFrame(t *testing.T) {
	obus, err := obu.Split(wideTU)
	if err != nil {
		t.Fatal(err)
	}
	var seq *header.SequenceHeader
	st := &header.State{}
	var frame *Frame
	for _, o := range obus {
		switch o.Type {
		case obu.TypeSequenceHeader:
			if seq, err = header.ParseSequenceHeader(o.Payload); err != nil {
				t.Fatalf("seq: %v", err)
			}
		case obu.TypeFrame:
			fh, off, err := header.ParseFrameHeader(seq, st, o.Payload, o.TemporalID, o.SpatialID)
			if err != nil {
				t.Fatalf("frame header: %v", err)
			}
			tiles, err := tile.SplitTileGroup(fh, o.Payload[off:])
			if err != nil {
				t.Fatalf("tile split: %v", err)
			}
			if frame, err = DecodeFrame(seq, fh, tiles); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
	}
	hsh := sha256.New()
	for _, pl := range frame.Planes {
		buf := make([]byte, len(pl.Data))
		for i, v := range pl.Data {
			buf[i] = byte(v)
		}
		hsh.Write(buf)
	}
	if got := hex.EncodeToString(hsh.Sum(nil)); got != wideSHA256 {
		t.Fatalf("reconstruction sha256 = %s want %s", got, wideSHA256)
	}
}
