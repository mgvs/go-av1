package predict

import "testing"

// TestPaethConstant: with all neighbors equal to v, Paeth predicts v everywhere
// (base = v+v-v = v, all distances 0, picks left = v).
func TestPaethConstant(t *testing.T) {
	above := []int{50, 50, 50, 50}
	left := []int{50, 50, 50, 50}
	p := Paeth(above, left, 50, 4, 4)
	for i := range p {
		for _, v := range p[i] {
			if v != 50 {
				t.Fatalf("Paeth = %d want 50", v)
			}
		}
	}
}

// TestPaethPicks checks the three-way selection against hand computation.
func TestPaethPicks(t *testing.T) {
	// above[j]=100, left[i]=10, topLeft=20. base = 100+10-20 = 90.
	// pLeft=|90-10|=80, pTop=|90-100|=10, pTopLeft=|90-20|=70.
	// pTop smallest -> predict above = 100.
	p := Paeth([]int{100}, []int{10}, 20, 1, 1)
	if p[0][0] != 100 {
		t.Fatalf("Paeth = %d want 100", p[0][0])
	}
}

// TestSmoothConstant: with all edge samples equal to v, SMOOTH predicts v
// (the four weighted terms sum to 512*v, Round2(512*v,9)=v).
func TestSmoothConstant(t *testing.T) {
	for _, mode := range []int{ModeSmooth, ModeSmoothV, ModeSmoothH} {
		above := make([]int, 8)
		left := make([]int, 8)
		for i := range above {
			above[i] = 77
			left[i] = 77
		}
		p := Smooth(mode, 3, 3, above, left)
		for i := range p {
			for _, v := range p[i] {
				if v != 77 {
					t.Fatalf("mode %d Smooth = %d want 77", mode, v)
				}
			}
		}
	}
}

// TestSmoothVTopRow: SMOOTH_V at row 0 uses weight smWeights[0]=255, so the value
// is Round2(255*above + 1*bottomLeft, 8), close to above.
func TestSmoothVTopRow(t *testing.T) {
	above := []int{200, 200, 200, 200}
	left := []int{0, 0, 0, 100} // bottomLeft = 100
	p := Smooth(ModeSmoothV, 2, 2, above, left)
	// row 0: Round2(255*200 + 1*100, 8) = Round2(51100,8) = (51100+128)>>8 = 200
	if p[0][0] != 200 {
		t.Fatalf("SmoothV row0 = %d want 200", p[0][0])
	}
}
