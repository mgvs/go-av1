package transform

// SINPI constants for the 4-point inverse ADST (AV1 spec §7.13.2.6).
const (
	sinpi1 = 1321
	sinpi2 = 2482
	sinpi3 = 3344
	sinpi4 = 3803
)

// adstInputPermute / adstOutputPermute perform the in-place permutations required
// by the 8- and 16-point inverse ADST (AV1 spec §7.13.2.4–7.13.2.5), 3 <= n <= 4.
func adstInputPermute(t []int64, n int) {
	n0 := 1 << uint(n)
	cp := make([]int64, n0)
	copy(cp, t[:n0])
	for i := 0; i < n0; i++ {
		idx := n0 - i - 1
		if i&1 == 1 {
			idx = i - 1
		}
		t[i] = cp[idx]
	}
}

func adstOutputPermute(t []int64, n int) {
	n0 := 1 << uint(n)
	cp := make([]int64, n0)
	copy(cp, t[:n0])
	for i := 0; i < n0; i++ {
		a := (i >> 3) & 1
		b := ((i >> 2) & 1) ^ ((i >> 3) & 1)
		c := ((i >> 1) & 1) ^ ((i >> 2) & 1)
		d := (i & 1) ^ ((i >> 1) & 1)
		idx := ((d << 3) | (c << 2) | (b << 1) | a) >> uint(4-n)
		if i&1 == 1 {
			t[i] = -cp[idx]
		} else {
			t[i] = cp[idx]
		}
	}
}

// iadst4 performs the 4-point inverse ADST (AV1 spec §7.13.2.6).
func iadst4(t []int64) {
	var s [7]int64
	s[0] = sinpi1 * t[0]
	s[1] = sinpi2 * t[0]
	s[2] = sinpi3 * t[1]
	s[3] = sinpi4 * t[2]
	s[4] = sinpi1 * t[2]
	s[5] = sinpi2 * t[3]
	s[6] = sinpi4 * t[3]
	a7 := t[0] - t[2]
	b7 := a7 + t[3]
	s[0] += s[3]
	s[1] -= s[4]
	s[3] = s[2]
	s[2] = sinpi3 * b7
	s[0] += s[5]
	s[1] -= s[6]
	x0 := s[0] + s[3]
	x1 := s[1] + s[3]
	x2 := s[2]
	x3 := s[0] + s[1] - s[3]
	t[0] = round2(x0, 12)
	t[1] = round2(x1, 12)
	t[2] = round2(x2, 12)
	t[3] = round2(x3, 12)
}

// iadst8 / iadst16 perform the 8- and 16-point inverse ADST (AV1 spec §7.13.2.7–8).
func iadst8(t []int64, r int) {
	adstInputPermute(t, 3)
	for i := 0; i <= 3; i++ {
		bfly(t, 2*i, 2*i+1, 60-16*i, 1)
	}
	for i := 0; i <= 3; i++ {
		had(t, i, 4+i, 0, r)
	}
	for i := 0; i <= 1; i++ {
		bfly(t, 4+3*i, 5+i, 48-32*i, 1)
	}
	for i := 0; i <= 1; i++ {
		for j := 0; j <= 1; j++ {
			had(t, 4*j+i, 2+4*j+i, 0, r)
		}
	}
	for i := 0; i <= 1; i++ {
		bfly(t, 2+4*i, 3+4*i, 32, 1)
	}
	adstOutputPermute(t, 3)
}

func iadst16(t []int64, r int) {
	adstInputPermute(t, 4)
	for i := 0; i <= 7; i++ {
		bfly(t, 2*i, 2*i+1, 62-8*i, 1)
	}
	for i := 0; i <= 7; i++ {
		had(t, i, 8+i, 0, r)
	}
	for i := 0; i <= 1; i++ {
		bfly(t, 8+2*i, 9+2*i, 56-32*i, 1)
		bfly(t, 13+2*i, 12+2*i, 8+32*i, 1)
	}
	for i := 0; i <= 3; i++ {
		for j := 0; j <= 1; j++ {
			had(t, 8*j+i, 4+8*j+i, 0, r)
		}
	}
	for i := 0; i <= 1; i++ {
		for j := 0; j <= 1; j++ {
			bfly(t, 4+8*j+3*i, 5+8*j+i, 48-32*i, 1)
		}
	}
	for i := 0; i <= 1; i++ {
		for j := 0; j <= 3; j++ {
			had(t, 4*j+i, 2+4*j+i, 0, r)
		}
	}
	for i := 0; i <= 3; i++ {
		bfly(t, 2+4*i, 3+4*i, 32, 1)
	}
	adstOutputPermute(t, 4)
}

// InverseADST performs the inverse ADST of length 2^n, for 2 <= n <= 4
// (AV1 spec §7.13.2.9).
func InverseADST(t []int64, n, r int) {
	switch n {
	case 2:
		iadst4(t)
	case 3:
		iadst8(t, r)
	default:
		iadst16(t, r)
	}
}

// InverseIdentity performs the inverse identity transform of length 2^n
// (AV1 spec §7.13.2.15).
func InverseIdentity(t []int64, n int) {
	switch n {
	case 2:
		for i := 0; i < 4; i++ {
			t[i] = round2(t[i]*5793, 12)
		}
	case 3:
		for i := 0; i < 8; i++ {
			t[i] *= 2
		}
	case 4:
		for i := 0; i < 16; i++ {
			t[i] = round2(t[i]*11586, 12)
		}
	default:
		for i := 0; i < 32; i++ {
			t[i] *= 4
		}
	}
}
