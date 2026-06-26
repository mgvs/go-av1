package decode

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/obu"
	"github.com/mgvs/go-av1/tile"
)

// smoothTU is a real aomenc 3.13 temporal unit: a 16x16 keyframe of a vertical ramp,
// in-loop filters disabled. aomenc encodes it as one BLOCK_16X16 with SMOOTH_V_PRED
// (the smooth vertical predictor) and a TX_16X16 transform. It exercises the
// non-DC intra predictor in the full pipeline, the signaled luma intra_tx_type, and
// the TX_16X16 coefficient path (eob_pt_256 + the 16x16 scan). Output is byte-exact
// with dav1d.
var smoothTU = []byte{
	0x12, 0x00,
	0x0a, 0x06, 0x18, 0x0c, 0xff, 0xd8, 0x00, 0x80,
	0x32, 0x0b, 0x18, 0x00, 0x00, 0x00, 0x40, 0x65, 0xea, 0x44, 0xa2, 0x7a, 0x9a,
}

const smoothSHA256 = "ed8b07d6436ceb91504ec00de04094197bbc13fb1a16c6064aecfc96585eca7b"

func TestDecodeSmoothPredFrame(t *testing.T) {
	obus, err := obu.Split(smoothTU)
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
	if got := hex.EncodeToString(hsh.Sum(nil)); got != smoothSHA256 {
		t.Fatalf("reconstruction sha256 = %s want %s", got, smoothSHA256)
	}
}
