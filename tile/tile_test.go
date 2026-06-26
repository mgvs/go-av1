package tile

import (
	"bytes"
	"testing"

	"github.com/mgvs/go-av1/header"
)

// TestSplitTileGroup builds a synthetic 2x2 tile group with known tile sizes
// (3,5,7 explicit via le(1), last = remainder) and checks the split returns the
// right data slices and mode-info bounds.
func TestSplitTileGroup(t *testing.T) {
	fh := &header.FrameHeader{
		TileCols:      2,
		TileRows:      2,
		TileColsLog2:  1,
		TileRowsLog2:  1,
		TileSizeBytes: 1,
		MiColStarts:   []int{0, 16, 32},
		MiRowStarts:   []int{0, 16, 32},
	}
	t0 := bytes.Repeat([]byte{0xA0}, 3)
	t1 := bytes.Repeat([]byte{0xA1}, 5)
	t2 := bytes.Repeat([]byte{0xA2}, 7)
	t3 := bytes.Repeat([]byte{0xA3}, 4)

	var data []byte
	data = append(data, 0x00) // tile_start_and_end_present_flag=0 (+ alignment)
	data = append(data, byte(len(t0)-1))
	data = append(data, t0...)
	data = append(data, byte(len(t1)-1))
	data = append(data, t1...)
	data = append(data, byte(len(t2)-1))
	data = append(data, t2...)
	data = append(data, t3...) // last tile: remainder, no size field

	tiles, err := SplitTileGroup(fh, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(tiles) != 4 {
		t.Fatalf("got %d tiles want 4", len(tiles))
	}
	want := [][]byte{t0, t1, t2, t3}
	for i, w := range want {
		if !bytes.Equal(tiles[i].Data, w) {
			t.Errorf("tile %d data = %x want %x", i, tiles[i].Data, w)
		}
	}
	// Tile numbering: 0->(r0,c0) 1->(r0,c1) 2->(r1,c0) 3->(r1,c1).
	bounds := []struct{ row, col, mrs, mre, mcs, mce int }{
		{0, 0, 0, 16, 0, 16},
		{0, 1, 0, 16, 16, 32},
		{1, 0, 16, 32, 0, 16},
		{1, 1, 16, 32, 16, 32},
	}
	for i, b := range bounds {
		tl := tiles[i]
		if tl.Row != b.row || tl.Col != b.col ||
			tl.MiRowStart != b.mrs || tl.MiRowEnd != b.mre ||
			tl.MiColStart != b.mcs || tl.MiColEnd != b.mce {
			t.Errorf("tile %d bounds = %+v want %v", i, tl, b)
		}
	}
}

// TestSplitSingleTile checks the common single-tile case (no header/size fields).
func TestSplitSingleTile(t *testing.T) {
	fh := &header.FrameHeader{
		TileCols: 1, TileRows: 1,
		MiColStarts: []int{0, 16}, MiRowStarts: []int{0, 16},
	}
	data := bytes.Repeat([]byte{0x5A}, 100)
	tiles, err := SplitTileGroup(fh, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(tiles) != 1 || !bytes.Equal(tiles[0].Data, data) {
		t.Fatalf("single tile split wrong: %d tiles", len(tiles))
	}
	if d := tiles[0].Decoder(false); d == nil {
		t.Fatal("nil decoder")
	}
}
