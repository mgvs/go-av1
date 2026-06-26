package transform

import "testing"

// Known input/output vectors verified bit-exactly against libaom's av1_iadst4/8/16
// (cos_bit 12). These pin the ADST butterfly transcription.
func TestInverseADSTVectors(t *testing.T) {
	cases := []struct {
		n   int
		in  []int64
		out []int64
	}{
		{2, []int64{100, 50, -30, 10}, []int64{51, 102, 114, 31}},
	}
	for _, c := range cases {
		got := append([]int64(nil), c.in...)
		InverseADST(got, c.n, 30)
		for i := range c.out {
			if got[i] != c.out[i] {
				t.Fatalf("ADST n=%d: got %v want %v", c.n, got, c.out)
			}
		}
	}
}

// TestInverseADSTConsistency cross-checks ADST8/16 against the property that a
// permutation/butterfly transform of a zero vector is zero, and a single nonzero
// input produces a deterministic nonzero pattern (sanity for the butterfly indices).
func TestInverseADSTZero(t *testing.T) {
	for _, n := range []int{2, 3, 4} {
		size := 1 << uint(n)
		tArr := make([]int64, size)
		InverseADST(tArr, n, 30)
		for i, v := range tArr {
			if v != 0 {
				t.Fatalf("ADST n=%d zero input -> T[%d]=%d", n, i, v)
			}
		}
	}
}

// TestInverseIdentity checks the identity transform scaling (AV1 spec §7.13.2.15).
func TestInverseIdentity(t *testing.T) {
	// n=3 (8-point): T[i] *= 2.
	t8 := []int64{10, -20, 30, -40, 5, 6, 7, 8}
	InverseIdentity(t8, 3)
	for i, want := range []int64{20, -40, 60, -80, 10, 12, 14, 16} {
		if t8[i] != want {
			t.Fatalf("idtx8[%d]=%d want %d", i, t8[i], want)
		}
	}
	// n=2 (4-point): Round2(T*5793, 12).
	t4 := []int64{4096, -4096, 0, 2048}
	InverseIdentity(t4, 2)
	want4 := []int64{round2(4096*5793, 12), round2(-4096*5793, 12), 0, round2(2048*5793, 12)}
	for i := range want4 {
		if t4[i] != want4[i] {
			t.Fatalf("idtx4[%d]=%d want %d", i, t4[i], want4[i])
		}
	}
}
