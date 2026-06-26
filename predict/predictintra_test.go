package predict

import "testing"

// setupPlane returns an 8x8 plane whose row y=3 and column x=3 are seeded, so a 4x4
// block predicted at (4,4) sees AboveRow = plane[3][4..7] and LeftCol = plane[4..7][3]
// and AboveRow[-1] = plane[3][3].
func setupPlane(above, left []uint16, topLeft uint16) *Plane {
	p := NewPlane(8, 8)
	p.Set(3, 3, topLeft)
	for j := 0; j < 4; j++ {
		p.Set(4+j, 3, above[j]) // AboveRow[j]
	}
	for i := 0; i < 4; i++ {
		p.Set(3, 4+i, left[i]) // LeftCol[i]
	}
	return p
}

func TestPredictIntraDCNeighbors(t *testing.T) {
	above := []uint16{10, 20, 30, 40}
	left := []uint16{50, 60, 70, 80}
	p := setupPlane(above, left, 5)
	// haveAboveRight/haveBelowLeft false so the edges extend (rightmost above /
	// bottom left are repeated) — only the first 4 of each are used by DC anyway.
	if err := p.PredictIntra(4, 4, 2, 2, true, true, false, false, ModeDC, 8, 7, 7, 0, 0, false, false, 0); err != nil {
		t.Fatal(err)
	}
	// DC = (sum(left)+sum(above) + 4) / 8 = (260+100+4)/8 = 45.
	for y := 4; y < 8; y++ {
		for x := 4; x < 8; x++ {
			if p.At(x, y) != 45 {
				t.Fatalf("DC (%d,%d) = %d want 45", x, y, p.At(x, y))
			}
		}
	}
}

func TestPredictIntraPaethNeighbors(t *testing.T) {
	above := []uint16{100, 100, 100, 100}
	left := []uint16{10, 10, 10, 10}
	p := setupPlane(above, left, 20)
	if err := p.PredictIntra(4, 4, 2, 2, true, true, false, false, ModePaeth, 8, 7, 7, 0, 0, false, false, 0); err != nil {
		t.Fatal(err)
	}
	// base = above + left - topLeft = 100+10-20 = 90; pTop=10 smallest -> above=100.
	if p.At(4, 4) != 100 {
		t.Fatalf("Paeth (4,4) = %d want 100", p.At(4, 4))
	}
}

func TestPredictIntraSmoothNeighbors(t *testing.T) {
	// All edges 60 -> SMOOTH predicts 60 everywhere.
	above := []uint16{60, 60, 60, 60}
	left := []uint16{60, 60, 60, 60}
	p := setupPlane(above, left, 60)
	if err := p.PredictIntra(4, 4, 2, 2, true, true, false, false, ModeSmooth, 8, 7, 7, 0, 0, false, false, 0); err != nil {
		t.Fatal(err)
	}
	for y := 4; y < 8; y++ {
		for x := 4; x < 8; x++ {
			if p.At(x, y) != 60 {
				t.Fatalf("Smooth (%d,%d) = %d want 60", x, y, p.At(x, y))
			}
		}
	}
}

// TestPredictIntraNoNeighbors checks the base-value fallback (first block): DC with
// no neighbors predicts 1<<(bitDepth-1).
func TestPredictIntraNoNeighbors(t *testing.T) {
	p := NewPlane(8, 8)
	if err := p.PredictIntra(0, 0, 2, 2, false, false, false, false, ModeDC, 8, 7, 7, 0, 0, false, false, 0); err != nil {
		t.Fatal(err)
	}
	if p.At(0, 0) != 128 {
		t.Fatalf("DC no-neighbors = %d want 128", p.At(0, 0))
	}
}
