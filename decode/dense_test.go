package decode

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/obu"
	"github.com/mgvs/go-av1/tile"
)

// denseTU is a real aomenc 3.13 temporal unit: a 64x64 keyframe of a radial cosine
// pattern, encoded with --enable-cdef=0 --enable-restoration=0 so no in-loop filter
// runs. It is one BLOCK_64X64, DC-predicted, with a *dense* residual — 231 AC
// coefficients (eob=231) spread across the 2D frequency plane. This stresses the
// coeff_base scan loop, the 2D neighbor-magnitude contexts and the full 64-point
// inverse DCT. Output is byte-exact with dav1d.
var denseTU = []byte{
	0x12, 0x00,
	0x0a, 0x06, 0x18, 0x15, 0x7f, 0xfd, 0x80, 0x08,
	0x32, 0x2f, 0x1a, 0x00, 0x00, 0x00, 0x40, 0x2d, 0x98, 0x75, 0x14, 0x37, 0x97, 0xdc,
	0x11, 0x77, 0xb5, 0xe8, 0x5c, 0x31, 0x0f, 0xa2, 0xa9, 0xf1, 0xbe, 0x63, 0x9f, 0x9c,
	0xd5, 0x49, 0xbc, 0x68, 0x57, 0xec, 0x48, 0x9f, 0x87, 0xae, 0xe8, 0x14, 0xcf, 0xc0,
	0x96, 0xdd, 0x1f, 0xbe, 0xfa, 0x54, 0x80,
}

const denseSHA256 = "7df377bcfce298bc9535c28b81970a43876962e92dd6281041093431925131ab"

func TestDecodeDenseFrame(t *testing.T) {
	obus, err := obu.Split(denseTU)
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
	if got := hex.EncodeToString(hsh.Sum(nil)); got != denseSHA256 {
		t.Fatalf("reconstruction sha256 = %s want %s", got, denseSHA256)
	}
}
