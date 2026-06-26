package decode

import (
	"testing"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/obu"
	"github.com/mgvs/go-av1/tile"
)

// solidYTU is a real aomenc 3.13 temporal unit: a 64x64 keyframe with Y=100,
// U=V=128. Unlike the flat frame, this carries a coded residual — one DC
// coefficient in luma (DC prediction is 128, so the residual is a constant -28).
// dav1d reconstructs Y=100, U=V=128. This exercises the full M5 entry: coefficient
// tokens (eob, coeff_base_eob, coeff_br, dc_sign, Exp-Golomb), dequantization and
// the inverse 64x64 DCT.
var solidYTU = []byte{
	0x12, 0x00,
	0x0a, 0x06, 0x18, 0x15, 0x7f, 0xfd, 0xb0, 0x08,
	0x32, 0x0c, 0x12, 0x10, 0x00, 0x00, 0x40, 0x00, 0x00, 0x00, 0x00, 0xeb, 0xcc, 0xd7,
}

// TestDecodeTexturedFrame decodes the solid-Y keyframe and checks the reconstructed
// planes are bit-exact with dav1d: luma uniformly 100 (DC prediction + coded DC
// residual), chroma uniformly 128 (all-zero residual).
func TestDecodeTexturedFrame(t *testing.T) {
	obus, err := obu.Split(solidYTU)
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
	want := []uint16{100, 128, 128} // Y, U, V
	for p, pl := range frame.Planes {
		for i, v := range pl.Data {
			if v != want[p] {
				t.Fatalf("plane %d sample %d = %d want %d", p, i, v, want[p])
			}
		}
	}
}
