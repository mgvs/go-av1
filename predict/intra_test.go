package predict

import "testing"

func TestDCValueNoNeighbors(t *testing.T) {
	// No neighbors: DC = 1 << (BitDepth-1). This is exactly the case for the first
	// block of a flat keyframe — predicts 128 (8-bit), matching dav1d's output.
	for _, bd := range []int{8, 10, 12} {
		want := 1 << uint(bd-1)
		if got := DCValue(nil, nil, false, false, 6, 6, bd); got != want {
			t.Errorf("bitDepth %d: DC = %d want %d", bd, got, want)
		}
	}
}

func TestDCValueLeftOnly(t *testing.T) {
	left := []int{100, 100, 100, 100}
	if got := DCValue(nil, left, false, true, 2, 2, 8); got != 100 {
		t.Errorf("left-only DC = %d want 100", got)
	}
	// (10+20+30+40 + 2) >> 2 = 102 >> 2 = 25
	left = []int{10, 20, 30, 40}
	if got := DCValue(nil, left, false, true, 2, 2, 8); got != 25 {
		t.Errorf("left-only DC = %d want 25", got)
	}
}

func TestDCValueAboveOnly(t *testing.T) {
	above := []int{200, 200, 200, 200}
	if got := DCValue(above, nil, true, false, 2, 2, 8); got != 200 {
		t.Errorf("above-only DC = %d want 200", got)
	}
}

func TestDCValueBoth(t *testing.T) {
	// 4x4: left and above all 128 -> avg 128.
	a := []int{128, 128, 128, 128}
	l := []int{128, 128, 128, 128}
	if got := DCValue(a, l, true, true, 2, 2, 8); got != 128 {
		t.Errorf("both DC = %d want 128", got)
	}
	// sum = (1+2+3+4)+(5+6+7+8)=36; +((4+4)>>1)=4 -> 40; /8 = 5
	a = []int{1, 2, 3, 4}
	l = []int{5, 6, 7, 8}
	if got := DCValue(a, l, true, true, 2, 2, 8); got != 5 {
		t.Errorf("both DC = %d want 5", got)
	}
}
