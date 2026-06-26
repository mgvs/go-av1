package decode

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/obu"
	"github.com/mgvs/go-av1/tile"
)

// TestDecoderRefStore drives the keyframe through the sequence Decoder (rather than
// the standalone DecodeFrame) and confirms: the output is byte-identical to the
// direct path, and the decoded frame populates the reference store with the right
// order hint for every slot selected by refresh_frame_flags.
func TestDecoderRefStore(t *testing.T) {
	obus, err := obu.Split(grayTU)
	if err != nil {
		t.Fatal(err)
	}
	var seq *header.SequenceHeader
	var dec *Decoder
	var frame *Frame
	var fh *header.FrameHeader
	for _, o := range obus {
		switch o.Type {
		case obu.TypeSequenceHeader:
			if seq, err = header.ParseSequenceHeader(o.Payload); err != nil {
				t.Fatalf("seq: %v", err)
			}
			dec = NewDecoder(seq)
		case obu.TypeFrame:
			var off int
			fh, off, err = header.ParseFrameHeader(seq, dec.State(), o.Payload, o.TemporalID, o.SpatialID)
			if err != nil {
				t.Fatalf("frame header: %v", err)
			}
			tiles, err := tile.SplitTileGroup(fh, o.Payload[off:])
			if err != nil {
				t.Fatalf("tile split: %v", err)
			}
			if frame, err = dec.DecodeFrame(fh, tiles); err != nil {
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
	if got := hex.EncodeToString(hsh.Sum(nil)); got != graySHA256 {
		t.Fatalf("driver reconstruction sha256 = %s want %s", got, graySHA256)
	}

	// A keyframe with show_frame refreshes all slots; each must hold this frame.
	for i := 0; i < header.NumRefFrames; i++ {
		if fh.RefreshFrameFlags&(1<<uint(i)) == 0 {
			continue
		}
		if dec.refs[i] == nil {
			t.Fatalf("ref slot %d not populated", i)
		}
		if dec.refs[i].OrderHint != fh.OrderHint {
			t.Fatalf("ref slot %d order hint = %d want %d", i, dec.refs[i].OrderHint, fh.OrderHint)
		}
		if dec.refs[i].Planes[0] != frame.Planes[0] {
			t.Fatalf("ref slot %d does not reference the decoded frame", i)
		}
	}
}
