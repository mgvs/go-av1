// Package transform implements the AV1 inverse transforms (AV1 spec §7.13). The
// inverse DCT is implemented in full (the exact B/H butterfly network); inverse
// ADST and the identity transform are added as the corresponding transform types
// are needed. Arithmetic uses int64 to hold the intermediate products exactly.
package transform

// cos128Lookup[a] = round(4096 * cos(a*pi/128)) for a = 0..64 (AV1 spec §7.13.2.1).
var cos128Lookup = [65]int64{
	4096, 4095, 4091, 4085, 4076, 4065, 4052, 4036,
	4017, 3996, 3973, 3948, 3920, 3889, 3857, 3822,
	3784, 3745, 3703, 3659, 3612, 3564, 3513, 3461,
	3406, 3349, 3290, 3229, 3166, 3102, 3035, 2967,
	2896, 2824, 2751, 2675, 2598, 2520, 2440, 2359,
	2276, 2191, 2106, 2019, 1931, 1842, 1751, 1660,
	1567, 1474, 1380, 1285, 1189, 1092, 995, 897,
	799, 700, 601, 501, 401, 301, 201, 101, 0,
}

func cos128(angle int) int64 {
	a2 := angle & 255
	switch {
	case a2 <= 64:
		return cos128Lookup[a2]
	case a2 <= 128:
		return -cos128Lookup[128-a2]
	case a2 <= 192:
		return -cos128Lookup[a2-128]
	default:
		return cos128Lookup[256-a2]
	}
}

func sin128(angle int) int64 { return cos128(angle - 64) }

// round2 implements Round2(x, n) with arithmetic (floor) right shift.
func round2(x int64, n int) int64 {
	if n == 0 {
		return x
	}
	return (x + (1 << uint(n-1))) >> uint(n)
}

func clip3(lo, hi, x int64) int64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// brev returns the bit-reversal of the low numBits of x (AV1 spec §7.13.2.1).
func brev(numBits, x int) int {
	t := 0
	for i := 0; i < numBits; i++ {
		bit := (x >> uint(i)) & 1
		t += bit << uint(numBits-1-i)
	}
	return t
}

// bfly performs B(a, b, angle, flip) — a butterfly rotation (AV1 spec §7.13.2.1).
func bfly(t []int64, a, b, angle, flip int) {
	ca, sa := cos128(angle), sin128(angle)
	x := t[a]*ca - t[b]*sa
	y := t[a]*sa + t[b]*ca
	t[a] = round2(x, 12)
	t[b] = round2(y, 12)
	if flip == 1 {
		t[a], t[b] = t[b], t[a]
	}
}

// had performs H(a, b, flip, r) — a Hadamard rotation with clamping (AV1 spec §7.13.2.1).
func had(t []int64, a, b, flip, r int) {
	if flip == 1 {
		a, b = b, a
	}
	x, y := t[a], t[b]
	lo := -(int64(1) << uint(r-1))
	hi := (int64(1) << uint(r-1)) - 1
	t[a] = clip3(lo, hi, x+y)
	t[b] = clip3(lo, hi, x-y)
}

// permuteDCT applies the inverse DCT array permutation (AV1 spec §7.13.2.2).
func permuteDCT(t []int64, n int) {
	cp := make([]int64, len(t))
	copy(cp, t)
	for i := 0; i < (1 << uint(n)); i++ {
		t[i] = cp[brev(n, i)]
	}
}

// InverseDCT performs the in-place inverse DCT of the length-2^n array t with
// intermediate clamping range r (AV1 spec §7.13.2.3). 2 <= n <= 6.
func InverseDCT(t []int64, n, r int) {
	permuteDCT(t, n)
	if n == 6 {
		for i := 0; i <= 15; i++ {
			bfly(t, 32+i, 63-i, 63-4*brev(4, i), 0)
		}
	}
	if n >= 5 {
		for i := 0; i <= 7; i++ {
			bfly(t, 16+i, 31-i, 6+(brev(3, 7-i)<<3), 0)
		}
	}
	if n == 6 {
		for i := 0; i <= 15; i++ {
			had(t, 32+i*2, 33+i*2, i&1, r)
		}
	}
	if n >= 4 {
		for i := 0; i <= 3; i++ {
			bfly(t, 8+i, 15-i, 12+(brev(2, 3-i)<<4), 0)
		}
	}
	if n >= 5 {
		for i := 0; i <= 7; i++ {
			had(t, 16+2*i, 17+2*i, i&1, r)
		}
	}
	if n == 6 {
		for i := 0; i <= 3; i++ {
			for j := 0; j <= 1; j++ {
				bfly(t, 62-i*4-j, 33+i*4+j, 60-16*brev(2, i)+64*j, 1)
			}
		}
	}
	if n >= 3 {
		for i := 0; i <= 1; i++ {
			bfly(t, 4+i, 7-i, 56-32*i, 0)
		}
	}
	if n >= 4 {
		for i := 0; i <= 3; i++ {
			had(t, 8+2*i, 9+2*i, i&1, r)
		}
	}
	if n >= 5 {
		for i := 0; i <= 1; i++ {
			for j := 0; j <= 1; j++ {
				bfly(t, 30-4*i-j, 17+4*i+j, 24+(j<<6)+((1-i)<<5), 1)
			}
		}
	}
	if n == 6 {
		for i := 0; i <= 7; i++ {
			for j := 0; j <= 1; j++ {
				had(t, 32+i*4+j, 35+i*4-j, i&1, r)
			}
		}
	}
	for i := 0; i <= 1; i++ {
		bfly(t, 2*i, 2*i+1, 32+16*i, 1-i)
	}
	if n >= 3 {
		for i := 0; i <= 1; i++ {
			had(t, 4+2*i, 5+2*i, i, r)
		}
	}
	if n >= 4 {
		for i := 0; i <= 1; i++ {
			bfly(t, 14-i, 9+i, 48+64*i, 1)
		}
	}
	if n >= 5 {
		for i := 0; i <= 3; i++ {
			for j := 0; j <= 1; j++ {
				had(t, 16+4*i+j, 19+4*i-j, i&1, r)
			}
		}
	}
	if n == 6 {
		for i := 0; i <= 1; i++ {
			for j := 0; j <= 3; j++ {
				bfly(t, 61-i*8-j, 34+i*8+j, 56-i*32+(j>>1)*64, 1)
			}
		}
	}
	for i := 0; i <= 1; i++ {
		had(t, i, 3-i, 0, r)
	}
	if n >= 3 {
		bfly(t, 6, 5, 32, 1)
	}
	if n >= 4 {
		for i := 0; i <= 1; i++ {
			for j := 0; j <= 1; j++ {
				had(t, 8+4*i+j, 11+4*i-j, i, r)
			}
		}
	}
	if n >= 5 {
		for i := 0; i <= 3; i++ {
			bfly(t, 29-i, 18+i, 48+(i>>1)*64, 1)
		}
	}
	if n == 6 {
		for i := 0; i <= 3; i++ {
			for j := 0; j <= 3; j++ {
				had(t, 32+8*i+j, 39+8*i-j, i&1, r)
			}
		}
	}
	if n >= 3 {
		for i := 0; i <= 3; i++ {
			had(t, i, 7-i, 0, r)
		}
	}
	if n >= 4 {
		for i := 0; i <= 1; i++ {
			bfly(t, 13-i, 10+i, 32, 1)
		}
	}
	if n >= 5 {
		for i := 0; i <= 1; i++ {
			for j := 0; j <= 3; j++ {
				had(t, 16+i*8+j, 23+i*8-j, i, r)
			}
		}
	}
	if n == 6 {
		for i := 0; i <= 7; i++ {
			ang := 48
			if i >= 4 {
				ang = 112
			}
			bfly(t, 59-i, 36+i, ang, 1)
		}
	}
	if n >= 4 {
		for i := 0; i <= 7; i++ {
			had(t, i, 15-i, 0, r)
		}
	}
	if n >= 5 {
		for i := 0; i <= 3; i++ {
			bfly(t, 27-i, 20+i, 32, 1)
		}
	}
	if n == 6 {
		for i := 0; i <= 7; i++ {
			had(t, 32+i, 47-i, 0, r)
			had(t, 48+i, 63-i, 1, r)
		}
	}
	if n >= 5 {
		for i := 0; i <= 15; i++ {
			had(t, i, 31-i, 0, r)
		}
	}
	if n == 6 {
		for i := 0; i <= 7; i++ {
			bfly(t, 55-i, 40+i, 32, 1)
		}
		for i := 0; i <= 31; i++ {
			had(t, i, 63-i, 0, r)
		}
	}
}
