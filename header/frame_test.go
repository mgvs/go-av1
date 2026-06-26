package header

import "testing"

// Real aomenc 3.13 keyframe headers (sequence header OBU payload + the frame OBU
// header prefix, i.e. the bytes the uncompressed_header parser consumes). These are
// regression fixtures: a 1-bit parsing error shifts every later field, so matching
// the full field set pins the bit layout. The streams all decode in dav1d.

func parse(t *testing.T, seqBytes, frameBytes []byte) (*FrameHeader, int) {
	t.Helper()
	seq, err := ParseSequenceHeader(seqBytes)
	if err != nil {
		t.Fatalf("seq: %v", err)
	}
	fh, off, err := ParseFrameHeader(seq, &State{}, frameBytes, 0, 0)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	return fh, off
}

// TestFrameHeaderLossless: 64x64 still keyframe, lossless (base_q_idx 0).
func TestFrameHeaderLossless(t *testing.T) {
	seq := []byte{0x18, 0x15, 0x7f, 0xfd, 0xb0, 0x08}
	frame := []byte{0x44, 0x00, 0x00}
	fh, off := parse(t, seq, frame)
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"width", fh.FrameWidth, 64},
		{"height", fh.FrameHeight, 64},
		{"intra", fh.FrameIsIntra, true},
		{"baseQ", fh.BaseQIdx, 0},
		{"lossless", fh.CodedLossless, true},
		{"txmode", fh.TxMode, Only4x4},
		{"tileCols", fh.TileCols, 1},
		{"tileRows", fh.TileRows, 1},
		{"usesLR", fh.UsesLr, false},
		{"hdrBytes", off, 3},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v want %v", c.name, c.got, c.want)
		}
	}
}

// TestFrameHeaderLossy: 640x480 keyframe, lossy — exercises the full non-lossless
// path: nonzero loop-filter levels, CDEF and loop restoration.
func TestFrameHeaderLossy(t *testing.T) {
	seq := []byte{0x19, 0x26, 0x27, 0xfe, 0xfb, 0x60, 0x10}
	frame := []byte{0x44, 0x3c, 0x00, 0x10, 0x44, 0x0c, 0x43, 0x70, 0x57, 0x58, 0x14, 0x80}
	fh, off := parse(t, seq, frame)
	if fh.FrameWidth != 640 || fh.FrameHeight != 480 {
		t.Errorf("dims = %dx%d want 640x480", fh.FrameWidth, fh.FrameHeight)
	}
	if fh.BaseQIdx != 60 {
		t.Errorf("baseQ = %d want 60", fh.BaseQIdx)
	}
	if fh.CodedLossless {
		t.Errorf("CodedLossless = true want false")
	}
	if fh.TxMode != TxModeSelect {
		t.Errorf("TxMode = %d want %d", fh.TxMode, TxModeSelect)
	}
	if fh.LoopFilterLevel != [4]int{1, 1, 4, 3} {
		t.Errorf("LoopFilterLevel = %v want [1 1 4 3]", fh.LoopFilterLevel)
	}
	if fh.CdefBits != 1 {
		t.Errorf("CdefBits = %d want 1", fh.CdefBits)
	}
	if !fh.UsesLr {
		t.Errorf("UsesLr = false want true")
	}
	if off != 12 {
		t.Errorf("hdrBytes = %d want 12", off)
	}
}
