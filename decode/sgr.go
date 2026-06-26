package decode

import "github.com/mgvs/go-av1/predict"

// Self-guided restoration constants (AV1 spec §3).
const (
	sgrprojParamsBits = 4
	sgrprojPrjSubexpK = 4
	sgrprojPrjBits    = 7
	sgrprojRstBits    = 4
	sgrprojMtableBits = 20
	sgrprojRecipBits  = 12
	sgrprojSgrBits    = 8
)

var sgrprojXqdMin = [2]int{-96, -32}
var sgrprojXqdMax = [2]int{31, 95}
var sgrprojXqdMid = [2]int{-32, 31}

// sgrParams[set] = {r0, eps0, r1, eps1} (AV1 spec §7.17.3).
var sgrParams = [16][4]int{
	{2, 12, 1, 4}, {2, 15, 1, 6}, {2, 18, 1, 8}, {2, 21, 1, 9},
	{2, 24, 1, 10}, {2, 29, 1, 11}, {2, 36, 1, 12}, {2, 45, 1, 13},
	{2, 56, 1, 14}, {2, 68, 1, 15}, {0, 0, 1, 5}, {0, 0, 1, 8},
	{0, 0, 1, 11}, {0, 0, 1, 14}, {2, 30, 0, 0}, {2, 75, 0, 0},
}

// readSgrParams reads the self-guided projection parameters (AV1 spec §5.11.58).
func (fd *frameDecoder) readSgrParams(plane, unitRow, unitCol int) {
	set := int(fd.d.ReadLiteral(sgrprojParamsBits))
	fd.lrSgrSet[plane][unitRow][unitCol] = set
	for i := 0; i < 2; i++ {
		radius := sgrParams[set][i*2]
		mn, mx := sgrprojXqdMin[i], sgrprojXqdMax[i]
		var v int
		if radius != 0 {
			v = fd.decodeSignedSubexpWithRef(mn, mx+1, sgrprojPrjSubexpK, fd.refSgrXqd[plane][i])
		} else {
			v = 0
			if i == 1 {
				v = clip3i(mn, mx, (1<<sgrprojPrjBits)-fd.refSgrXqd[plane][0])
			}
		}
		fd.lrSgrXqd[plane][unitRow][unitCol][i] = v
		fd.refSgrXqd[plane][i] = v
	}
}

func roundN(x, n int) int {
	if n <= 0 {
		return x
	}
	return (x + (1 << uint(n-1))) >> uint(n)
}

// selfGuidedFilter applies self-guided (SGR) loop restoration (AV1 spec §7.17.2).
func (fd *frameDecoder) selfGuidedFilter(lr []*predict.Plane, plane, unitRow, unitCol, x, y, w, h,
	stripeStartY, stripeEndY, planeEndX, planeEndY int) {
	set := fd.lrSgrSet[plane][unitRow][unitCol]
	flt0 := fd.boxFilter(plane, x, y, w, h, set, 0, stripeStartY, stripeEndY, planeEndX, planeEndY)
	flt1 := fd.boxFilter(plane, x, y, w, h, set, 1, stripeStartY, stripeEndY, planeEndX, planeEndY)
	w0 := fd.lrSgrXqd[plane][unitRow][unitCol][0]
	w1 := fd.lrSgrXqd[plane][unitRow][unitCol][1]
	w2 := (1 << sgrprojPrjBits) - w0 - w1
	r0 := sgrParams[set][0]
	r1 := sgrParams[set][2]
	cdef := fd.planes[plane]
	hi := (1 << uint(fd.bitDepth)) - 1
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			u := int(cdef.At(x+j, y+i)) << sgrprojRstBits
			v := w1 * u
			if r0 != 0 {
				v += w0 * flt0[i][j]
			} else {
				v += w0 * u
			}
			if r1 != 0 {
				v += w2 * flt1[i][j]
			} else {
				v += w2 * u
			}
			s := roundN(v, sgrprojRstBits+sgrprojPrjBits)
			lr[plane].Set(x+j, y+i, uint16(clip3i(0, hi, s)))
		}
	}
}

// boxFilter computes one self-guided box-filtered output array (AV1 spec §7.17.3).
func (fd *frameDecoder) boxFilter(plane, x, y, w, h, set, pass,
	stripeStartY, stripeEndY, planeEndX, planeEndY int) [][]int {
	r := sgrParams[set][pass*2+0]
	if r == 0 {
		return nil
	}
	eps := sgrParams[set][pass*2+1]
	bd := fd.bitDepth
	n := (2*r + 1) * (2*r + 1)
	n2e := n * n * eps
	s := ((1 << sgrprojMtableBits) + n2e/2) / n2e
	// A and B span -1..h and -1..w; store with a +1 offset.
	A := make([][]int, h+2)
	B := make([][]int, h+2)
	for i := range A {
		A[i] = make([]int, w+2)
		B[i] = make([]int, w+2)
	}
	for i := -1; i < h+1; i++ {
		for j := -1; j < w+1; j++ {
			a, b := 0, 0
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					c := fd.getSourceSample(plane, x+j+dx, y+i+dy, stripeStartY, stripeEndY, planeEndX, planeEndY)
					a += c * c
					b += c
				}
			}
			a = roundN(a, 2*(bd-8))
			d := roundN(b, bd-8)
			p := max(0, a*n-d*d)
			z := roundN(p*s, sgrprojMtableBits)
			var a2 int
			switch {
			case z >= 255:
				a2 = 256
			case z == 0:
				a2 = 1
			default:
				a2 = ((z << sgrprojSgrBits) + (z / 2)) / (z + 1)
			}
			oneOverN := ((1 << sgrprojRecipBits) + (n / 2)) / n
			b2 := ((1 << sgrprojSgrBits) - a2) * b * oneOverN
			A[i+1][j+1] = a2
			B[i+1][j+1] = roundN(b2, sgrprojRecipBits)
		}
	}
	cdef := fd.planes[plane]
	F := make([][]int, h)
	for i := 0; i < h; i++ {
		F[i] = make([]int, w)
		shift := 5
		if pass == 0 && (i&1) != 0 {
			shift = 4
		}
		for j := 0; j < w; j++ {
			a, b := 0, 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					var weight int
					if pass == 0 {
						if ((i + dy) & 1) != 0 {
							if dx == 0 {
								weight = 6
							} else {
								weight = 5
							}
						}
					} else {
						if dx == 0 || dy == 0 {
							weight = 4
						} else {
							weight = 3
						}
					}
					a += weight * A[i+dy+1][j+dx+1]
					b += weight * B[i+dy+1][j+dx+1]
				}
			}
			v := a*int(cdef.At(x+j, y+i)) + b
			F[i][j] = roundN(v, sgrprojSgrBits+shift-sgrprojRstBits)
		}
	}
	return F
}
