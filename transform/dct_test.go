package transform

import "testing"

// TestInverseDCT4DC checks the hand-derived result that a 4-point inverse DCT of a
// DC-only input [x,0,0,0] yields four equal samples Round2(2896*x, 12).
func TestInverseDCT4DC(t *testing.T) {
	for _, x := range []int64{4096, 1000, -2048, 12345} {
		tArr := []int64{x, 0, 0, 0}
		InverseDCT(tArr, 2, 16)
		want := round2(2896*x, 12)
		for i, v := range tArr {
			if v != want {
				t.Fatalf("x=%d: T[%d]=%d want %d", x, i, v, want)
			}
		}
	}
}

// TestInverseDCTDCConstant checks that a DC-only input produces a constant output
// for every supported size n (a defining property of the DCT).
func TestInverseDCTDCConstant(t *testing.T) {
	for n := 2; n <= 6; n++ {
		size := 1 << uint(n)
		tArr := make([]int64, size)
		tArr[0] = 4096
		InverseDCT(tArr, n, 18)
		for i := 1; i < size; i++ {
			if tArr[i] != tArr[0] {
				t.Fatalf("n=%d: T[%d]=%d != T[0]=%d (not constant)", n, i, tArr[i], tArr[0])
			}
		}
	}
}

// TestInverse2DDCConstant checks the full 2D transform of a DC-only block is a flat
// residual.
func TestInverse2DDCConstant(t *testing.T) {
	// TX_64X64 with a single DC coefficient.
	dq := [][]int64{{200}}
	res, w, h, err := Inverse2D(4 /*TX_64X64*/, DCTDCT, dq, 8)
	if err != nil {
		t.Fatal(err)
	}
	if w != 64 || h != 64 {
		t.Fatalf("dims %dx%d", w, h)
	}
	for i := 1; i < len(res); i++ {
		if res[i] != res[0] {
			t.Fatalf("residual not flat: res[%d]=%d res[0]=%d", i, res[i], res[0])
		}
	}
}
