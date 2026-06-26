package decode

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/obu"
	"github.com/mgvs/go-av1/tile"
)

// gradTU is a real aomenc 3.13 temporal unit: a 64x64 keyframe of a horizontal
// gradient. It is one BLOCK_64X64, DC-predicted, but carries a non-trivial residual
// — 46 AC coefficients (eob=46) coded into the 64-point DCT. This exercises the full
// M5 coefficient path: eob_pt + eob_extra, the coeff_base scan loop with neighbor
// contexts, coeff_br, signs and Exp-Golomb, dequantization and the multi-coefficient
// inverse 64x64 DCT. Decoded output is byte-exact with dav1d.
var gradTU = []byte{
	0x12, 0x00,
	0x0a, 0x06, 0x18, 0x15, 0x7f, 0xfd, 0xb0, 0x08,
	0x32, 0x11, 0x1a, 0x00, 0x00, 0x00, 0x50, 0x00, 0x00, 0x00, 0x25, 0xac, 0x9c, 0xb8, 0x44, 0x29, 0xd1, 0x81, 0x9c,
}

// gradSHA256 is the SHA-256 of the dav1d reference reconstruction (Y then U then V,
// 6144 bytes total) for gradTU.
const gradSHA256 = "f7dc5730522d6cd91042bac1585eef75c04a7d39699d8ce5623193d95a37b309"

func TestDecodeGradientFrame(t *testing.T) {
	obus, err := obu.Split(gradTU)
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
	if frame == nil {
		t.Fatal("no frame")
	}
	// Hash the planes (Y, U, V) and compare to the dav1d reference.
	hsh := sha256.New()
	buf := make([]byte, 0, 4096)
	for _, pl := range frame.Planes {
		buf = buf[:0]
		for _, v := range pl.Data {
			buf = append(buf, byte(v))
		}
		hsh.Write(buf)
	}
	if got := hex.EncodeToString(hsh.Sum(nil)); got != gradSHA256 {
		t.Fatalf("reconstruction sha256 = %s want %s", got, gradSHA256)
	}
}
