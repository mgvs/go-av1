package predict

import "testing"

// dirPlane builds an 8x8 plane seeding row y=3 (x=4..11 clamped) and column x=3
// so a block at (4,4) sees AboveRow / LeftCol from them.
func dirPlane(above, left []uint16) *Plane {
	p := NewPlane(12, 12)
	p.Set(3, 3, 99)
	for j := 0; j < 8; j++ {
		p.Set(4+j, 3, above[j])
	}
	for i := 0; i < 4; i++ {
		p.Set(3, 4+i, left[i])
	}
	return p
}

// TestDirectionalV: V_PRED (pAngle 90) copies AboveRow down every row.
func TestDirectionalV(t *testing.T) {
	above := []uint16{10, 20, 30, 40, 50, 60, 70, 80}
	left := []uint16{1, 2, 3, 4}
	p := dirPlane(above, left)
	// angleDelta 0, edge filter disabled to keep AboveRow unmodified.
	if err := p.PredictIntra(4, 4, 2, 2, true, true, false, false, ModeV, 8, 11, 11, 0, 0, false, false, 0); err != nil {
		t.Fatal(err)
	}
	for i := 4; i < 8; i++ {
		for j := 0; j < 4; j++ {
			if got := p.At(4+j, i); got != above[j] {
				t.Fatalf("V_PRED (%d,%d) = %d want %d", 4+j, i, got, above[j])
			}
		}
	}
}

// TestDirectionalH: H_PRED (pAngle 180) copies LeftCol across every column.
func TestDirectionalH(t *testing.T) {
	above := []uint16{10, 20, 30, 40, 50, 60, 70, 80}
	left := []uint16{11, 22, 33, 44}
	p := dirPlane(above, left)
	if err := p.PredictIntra(4, 4, 2, 2, true, true, false, false, ModeH, 8, 11, 11, 0, 0, false, false, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		for j := 4; j < 8; j++ {
			if got := p.At(j, 4+i); got != left[i] {
				t.Fatalf("H_PRED (%d,%d) = %d want %d", j, 4+i, got, left[i])
			}
		}
	}
}
