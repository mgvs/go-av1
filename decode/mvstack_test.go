package decode

import (
	"testing"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
)

// newMvTestDecoder builds a frame decoder with empty inter neighbor grids of the
// given mi dimensions, ready for find_mv_stack unit tests.
func newMvTestDecoder(miRows, miCols int) *frameDecoder {
	fd := &frameDecoder{
		fh:            &header.FrameHeader{},
		miRows:        miRows,
		miCols:        miCols,
		miRowEnd:      miRows,
		miColEnd:      miCols,
		miSizes:       makeGrid(miRows, miCols),
		yModes:        makeGrid(miRows, miCols),
		miWrittenGrid: make([][]bool, miRows),
	}
	fd.gridMvs = make([][][2]MV, miRows)
	fd.gridRefFrames = make([][][2]int, miRows)
	for r := 0; r < miRows; r++ {
		fd.gridMvs[r] = make([][2]MV, miCols)
		fd.gridRefFrames[r] = make([][2]int, miCols)
		fd.miWrittenGrid[r] = make([]bool, miCols)
		for c := 0; c < miCols; c++ {
			fd.gridRefFrames[r][c] = [2]int{IntraFrame, header.NoneFrame}
		}
	}
	return fd
}

// TestFindMvStackAboveNeighbor: a single inter neighbor above, sharing the current
// reference frame and coded as NEWMV, should land its (precision-lowered) MV at the
// top of the stack and drive the close-match contexts.
func TestFindMvStackAboveNeighbor(t *testing.T) {
	fd := newMvTestDecoder(16, 16)
	fd.miRow, fd.miCol, fd.miSize = 4, 4, predict.Block16x16
	fd.refFrame = [2]int{header.LastFrame, header.NoneFrame}

	// Above neighbor at (3,4): inter, ref = LAST_FRAME, NEWMV, MV (8,16).
	fd.gridRefFrames[3][4] = [2]int{header.LastFrame, header.NoneFrame}
	fd.miSizes[3][4] = predict.Block16x16
	fd.yModes[3][4] = newMv
	fd.gridMvs[3][4][0] = MV{Row: 8, Col: 16}
	fd.miWrittenGrid[3][4] = true

	fd.findMvStack(false)

	if fd.numMvFound != 1 {
		t.Fatalf("NumMvFound = %d want 1", fd.numMvFound)
	}
	if fd.refStackMv[0][0] != (MV{Row: 8, Col: 16}) {
		t.Fatalf("RefStackMv[0] = %+v want {8 16}", fd.refStackMv[0][0])
	}
	// Global-motion fill (identity) provides the second candidate slot.
	if fd.refStackMv[1][0] != (MV{}) {
		t.Fatalf("RefStackMv[1] = %+v want {0 0}", fd.refStackMv[1][0])
	}
	// CloseMatches==1, numNew==1 -> NewMvContext = 3 - 1 = 2; RefMvContext = 2 + 1 = 3.
	if fd.newMvContext != 2 {
		t.Fatalf("NewMvContext = %d want 2", fd.newMvContext)
	}
	if fd.refMvContext != 3 {
		t.Fatalf("RefMvContext = %d want 3", fd.refMvContext)
	}
}

// TestFindMvStackEmpty: no neighbors -> empty stack, global fill, zero contexts.
func TestFindMvStackEmpty(t *testing.T) {
	fd := newMvTestDecoder(16, 16)
	fd.miRow, fd.miCol, fd.miSize = 0, 0, predict.Block16x16
	fd.refFrame = [2]int{header.LastFrame, header.NoneFrame}

	fd.findMvStack(false)

	if fd.numMvFound != 0 {
		t.Fatalf("NumMvFound = %d want 0", fd.numMvFound)
	}
	if fd.refStackMv[0][0] != (MV{}) || fd.refStackMv[1][0] != (MV{}) {
		t.Fatalf("global fill not applied: %+v %+v", fd.refStackMv[0][0], fd.refStackMv[1][0])
	}
	if fd.newMvContext != 0 || fd.refMvContext != 0 {
		t.Fatalf("contexts = (%d,%d) want (0,0)", fd.newMvContext, fd.refMvContext)
	}
}

// TestLowerMvPrecision: with high precision disabled, odd components round toward 0.
func TestLowerMvPrecision(t *testing.T) {
	fd := &frameDecoder{fh: &header.FrameHeader{}}
	mv := MV{Row: 7, Col: -5}
	fd.lowerMvPrecision(&mv)
	if mv != (MV{Row: 6, Col: -4}) {
		t.Fatalf("lowerMvPrecision = %+v want {6 -4}", mv)
	}
	fd.fh.AllowHighPrecisionMV = true
	mv = MV{Row: 7, Col: -5}
	fd.lowerMvPrecision(&mv)
	if mv != (MV{Row: 7, Col: -5}) {
		t.Fatalf("high precision should be unchanged, got %+v", mv)
	}
}
