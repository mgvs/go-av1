package decode

import (
	"testing"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/obu"
	"github.com/mgvs/go-av1/tile"
)

// flatTU is a real aomenc 3.13 temporal unit: a 64x64 keyframe whose pixels are all
// 128 (Y=U=V). dav1d reconstructs it to a uniform 128 frame. It is the simplest
// end-to-end matching frame: one BLOCK_64X64, DC prediction, skip-coded with an
// all-zero residual. Bytes: temporal delimiter + sequence header + frame OBU.
var flatTU = []byte{
	0x12, 0x00, // temporal delimiter
	0x0a, 0x06, 0x18, 0x15, 0x7f, 0xfd, 0xb0, 0x08, // sequence header OBU
	0x32, 0x09, 0x12, 0xb0, 0x00, 0x00, 0x40, 0x00, 0x00, 0x00, 0x2e, // frame OBU
}

// TestDecodeFlatFrame decodes the flat keyframe end-to-end (OBU split → headers →
// tile split → reconstruction) and checks every reconstructed sample equals 128,
// matching dav1d. It also pins the decoded entropy symbols against the libaom
// inspect oracle (partition NONE, skip 0, DC luma/chroma, all-zero residual).
func TestDecodeFlatFrame(t *testing.T) {
	obus, err := obu.Split(flatTU)
	if err != nil {
		t.Fatal(err)
	}
	var seq *header.SequenceHeader
	st := &header.State{}
	var frame *Frame
	for _, o := range obus {
		switch o.Type {
		case obu.TypeSequenceHeader:
			seq, err = header.ParseSequenceHeader(o.Payload)
			if err != nil {
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
			frame, err = DecodeFrame(seq, fh, tiles)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
	}
	if frame == nil {
		t.Fatal("no frame decoded")
	}

	// Every sample of every plane must be 128 (dav1d reference).
	if got := len(frame.Planes); got != 3 {
		t.Fatalf("planes = %d want 3", got)
	}
	for p, pl := range frame.Planes {
		for i, v := range pl.Data {
			if v != 128 {
				t.Fatalf("plane %d sample %d = %d want 128", p, i, v)
			}
		}
	}

	// Symbol-level check against the libaom inspect oracle.
	want := []string{
		"partition(r0,c0,bs12,ctx0)=0", // BLOCK_64X64 -> PARTITION_NONE
		"skip(ctx0)=0",
		"y_mode(a0,l0)=0", // DC_PRED
		"uv_mode(y0)=0",   // DC_PRED
		"all_zero(p0,txSzCtx4,ctx0)=1",
		"all_zero(p1,txSzCtx3,ctx7)=1",
		"all_zero(p2,txSzCtx3,ctx7)=1",
	}
	if len(frame.Trace) != len(want) {
		t.Fatalf("trace = %v\nwant %v", frame.Trace, want)
	}
	for i := range want {
		if frame.Trace[i] != want[i] {
			t.Errorf("trace[%d] = %q want %q", i, frame.Trace[i], want[i])
		}
	}
}
