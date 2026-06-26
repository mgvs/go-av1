package decode

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/obu"
	"github.com/mgvs/go-av1/tile"
)

// dirTU is a real aomenc 3.13 temporal unit: a 32x32 grayscale keyframe (mandelbrot
// luma, flat chroma), in-loop filters disabled. aomenc codes it with a rich mix of
// intra predictors — DC, directional (D45/D135/D157/D203), SMOOTH, SMOOTH_H and
// Paeth — plus tx_mode_select with split transform sizes. It exercises the full
// directional intra predictor (angles + intra edge filter), angle_delta signaling
// and the smaller transform coefficient paths. Output is byte-exact with dav1d.
var dirTU = []byte{
	0x12, 0x00,
	0x0a, 0x06, 0x18, 0x11, 0x3f, 0xf6, 0x00, 0x20,
	0x32, 0x6e, 0x1a, 0x00, 0x00, 0x00, 0x50, 0xcf, 0x0a, 0x46, 0xc3, 0xe8, 0xab, 0x22,
	0x4c, 0xcf, 0x95, 0xc6, 0x54, 0x4b, 0xe8, 0x21, 0xe9, 0xa8, 0xa5, 0x5e, 0xb3, 0x6a,
	0xde, 0xc6, 0xfd, 0xea, 0xe8, 0xdc, 0x2d, 0xdd, 0x9c, 0xb8, 0xfc, 0x32, 0xd7, 0xce,
	0xbc, 0x5c, 0xe7, 0xc4, 0x53, 0x62, 0x31, 0xe3, 0x3d, 0xf5, 0xb2, 0xfa, 0x37, 0x59,
	0x3a, 0x34, 0xae, 0x78, 0xb4, 0x7b, 0xb3, 0xa9, 0x5e, 0xaa, 0x77, 0xf8, 0x12, 0x5f,
	0x08, 0x16, 0x86, 0x61, 0x88, 0xaf, 0xb0, 0x4a, 0xf4, 0x2e, 0xb4, 0x6b, 0xc6, 0x9f,
	0xb2, 0xf4, 0x49, 0xe7, 0x22, 0x25, 0x3a, 0x85, 0x5e, 0xd1, 0x8b, 0xd6, 0x82, 0xbb,
	0xd0, 0x72, 0x38, 0xa2, 0x6c, 0x66, 0xc9, 0x2b, 0xfe, 0x65, 0xa9, 0x7a, 0x9f, 0x80,
}

const dirSHA256 = "dbe010afb40ee5967a72dac7758eb937fa619c120d414cb34a82fad14de0ac71"

func TestDecodeDirectionalFrame(t *testing.T) {
	obus, err := obu.Split(dirTU)
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
	if got := hex.EncodeToString(hsh.Sum(nil)); got != dirSHA256 {
		t.Fatalf("reconstruction sha256 = %s want %s", got, dirSHA256)
	}
}
