package predict

// Directional prediction constants and tables (AV1 spec §7.11.2.4, §9.3).
const (
	angleStep     = 3
	maxAngleDelta = 3
	intraEdgeTaps = 5
	bufOff        = 16 // offset so negative edge indices (down to -2) are storable
)

// modeToAngle gives the base prediction angle for each intra mode (0 = non-directional).
var modeToAngle = [13]int{0, 90, 180, 45, 135, 113, 157, 203, 67, 0, 0, 0, 0}

// drIntraDerivative[angle] is the per-row/col step for directional interpolation.
var drIntraDerivative = [90]int{
	0, 0, 0, 1023, 0, 0, 547, 0, 0, 372, 0, 0, 0, 0,
	273, 0, 0, 215, 0, 0, 178, 0, 0, 151, 0, 0, 132, 0, 0,
	116, 0, 0, 102, 0, 0, 0, 90, 0, 0, 80, 0, 0, 71, 0, 0,
	64, 0, 0, 57, 0, 0, 51, 0, 0, 45, 0, 0, 0, 40, 0, 0,
	35, 0, 0, 31, 0, 0, 27, 0, 0, 23, 0, 0, 19, 0, 0,
	15, 0, 0, 0, 0, 11, 0, 0, 7, 0, 0, 3, 0, 0,
}

var intraEdgeKernel = [3][5]int{
	{0, 4, 8, 4, 0},
	{0, 5, 6, 5, 0},
	{2, 4, 4, 4, 2},
}

// IsDirectional reports whether a mode uses the directional predictor.
func IsDirectional(mode int) bool { return mode >= ModeV && mode <= ModeD67 }

// Directional mode ids (AV1 spec §6.10.5).
const (
	ModeV           = 1
	ModeH           = 2
	ModeD67         = 8
	ModeSmoothStart = 9
	ModeSmoothEnd   = 11
)

func clip1i(v, bitDepth int) int {
	hi := (1 << uint(bitDepth)) - 1
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}

// edgeFilterStrength selects the intra edge filter strength (AV1 spec §7.11.2.9).
func edgeFilterStrength(w, h, filterType, delta int) int {
	d := abs(delta)
	blkWh := w + h
	strength := 0
	if filterType == 0 {
		switch {
		case blkWh <= 8:
			if d >= 56 {
				strength = 1
			}
		case blkWh <= 12:
			if d >= 40 {
				strength = 1
			}
		case blkWh <= 16:
			if d >= 40 {
				strength = 1
			}
		case blkWh <= 24:
			if d >= 8 {
				strength = 1
			}
			if d >= 16 {
				strength = 2
			}
			if d >= 32 {
				strength = 3
			}
		case blkWh <= 32:
			strength = 1
			if d >= 4 {
				strength = 2
			}
			if d >= 32 {
				strength = 3
			}
		default:
			strength = 3
		}
	} else {
		switch {
		case blkWh <= 8:
			if d >= 40 {
				strength = 1
			}
			if d >= 64 {
				strength = 2
			}
		case blkWh <= 16:
			if d >= 20 {
				strength = 1
			}
			if d >= 48 {
				strength = 2
			}
		case blkWh <= 24:
			if d >= 4 {
				strength = 3
			}
		default:
			strength = 3
		}
	}
	return strength
}

// upsampleSelect decides whether to upsample an edge (AV1 spec §7.11.2.10).
func upsampleSelect(w, h, filterType, delta int) bool {
	d := abs(delta)
	blkWh := w + h
	if d <= 0 || d >= 40 {
		return false
	}
	if filterType == 0 {
		return blkWh <= 16
	}
	return blkWh <= 8
}

// intraEdgeFilter filters an edge buffer in place (AV1 spec §7.11.2.12). buf is
// indexed with bufOff; valid entries are buf[off-1 .. off+sz-2].
func intraEdgeFilter(buf []int, sz, strength int) {
	if strength == 0 {
		return
	}
	edge := make([]int, sz)
	for i := 0; i < sz; i++ {
		edge[i] = buf[bufOff+i-1]
	}
	for i := 1; i < sz; i++ {
		s := 0
		for j := 0; j < intraEdgeTaps; j++ {
			k := i - 2 + j
			if k < 0 {
				k = 0
			} else if k > sz-1 {
				k = sz - 1
			}
			s += intraEdgeKernel[strength-1][j] * edge[k]
		}
		buf[bufOff+i-1] = (s + 8) >> 4
	}
}

// intraEdgeUpsample upsamples an edge buffer in place (AV1 spec §7.11.2.11). On
// entry buf[off-1 .. off+numPx-1] are valid; on exit buf[off-2 .. off+2*numPx-2].
func intraEdgeUpsample(buf []int, numPx, bitDepth int) {
	dup := make([]int, numPx+3)
	dup[0] = buf[bufOff-1]
	for i := -1; i < numPx; i++ {
		dup[i+2] = buf[bufOff+i]
	}
	dup[numPx+2] = buf[bufOff+numPx-1]
	buf[bufOff-2] = dup[0]
	for i := 0; i < numPx; i++ {
		s := -dup[i] + 9*dup[i+1] + 9*dup[i+2] - dup[i+3]
		s = clip1i((s+8)>>4, bitDepth)
		buf[bufOff+2*i-1] = s
		buf[bufOff+2*i] = dup[i+2]
	}
}

func round2int(x, n int) int { return (x + (1 << uint(n-1))) >> uint(n) }

func round2signed(x, n int) int {
	if x >= 0 {
		return round2int(x, n)
	}
	return -round2int(-x, n)
}

// filterIntra fills pred with the recursive (filter) intra prediction (AV1 spec
// §7.11.2.3), using the edge buffers aboveRow / leftCol (bufOff offset).
func filterIntra(pred [][]int, aboveRow, leftCol []int, w, h, filterMode, bitDepth int) {
	w4 := w >> 2
	h2 := h >> 1
	for i2 := 0; i2 < h2; i2++ {
		for j4 := 0; j4 < w4; j4++ {
			var p [7]int
			for i := 0; i < 7; i++ {
				if i < 5 {
					switch {
					case i2 == 0:
						p[i] = aboveRow[bufOff+(j4<<2)+i-1]
					case j4 == 0 && i == 0:
						p[i] = leftCol[bufOff+(i2<<1)-1]
					default:
						p[i] = pred[(i2<<1)-1][(j4<<2)+i-1]
					}
				} else {
					if j4 == 0 {
						p[i] = leftCol[bufOff+(i2<<1)+i-5]
					} else {
						p[i] = pred[(i2<<1)+i-5][(j4<<2)-1]
					}
				}
			}
			for i1 := 0; i1 < 2; i1++ {
				for j1 := 0; j1 < 4; j1++ {
					pr := 0
					for i := 0; i < 7; i++ {
						pr += intraFilterTaps[filterMode][(i1<<2)+j1][i] * p[i]
					}
					pred[(i2<<1)+i1][(j4<<2)+j1] = clip1i(round2signed(pr, 4), bitDepth)
				}
			}
		}
	}
}

// directional fills pred with the directional intra prediction (AV1 spec §7.11.2.4).
// aboveRow / leftCol are int edge buffers (bufOff offset) holding AboveRow[-1..] and
// LeftCol[-1..]; this function may filter/upsample them in place.
func directional(pred [][]int, aboveRow, leftCol []int, w, h, mode, angleDelta, filterType int,
	haveLeft, haveAbove, enableEdgeFilter bool, x, y, maxX, maxY, bitDepth int) {
	pAngle := modeToAngle[mode] + angleDelta*angleStep
	upAbove, upLeft := 0, 0

	if enableEdgeFilter {
		if pAngle != 90 && pAngle != 180 {
			if pAngle > 90 && pAngle < 180 && (w+h) >= 24 {
				s := 5*leftCol[bufOff+0] + 6*aboveRow[bufOff-1] + 5*aboveRow[bufOff+0]
				corner := round2int(s, 4)
				leftCol[bufOff-1] = corner
				aboveRow[bufOff-1] = corner
			}
			if haveAbove {
				strength := edgeFilterStrength(w, h, filterType, pAngle-90)
				numPx := min(w, maxX-x+1)
				if pAngle < 90 {
					numPx += h
				}
				intraEdgeFilter(aboveRow, numPx+1, strength)
			}
			if haveLeft {
				strength := edgeFilterStrength(w, h, filterType, pAngle-180)
				numPx := min(h, maxY-y+1)
				if pAngle > 180 {
					numPx += w
				}
				intraEdgeFilter(leftCol, numPx+1, strength)
			}
		}
		if upsampleSelect(w, h, filterType, pAngle-90) {
			numPx := w
			if pAngle < 90 {
				numPx += h
			}
			intraEdgeUpsample(aboveRow, numPx, bitDepth)
			upAbove = 1
		}
		if upsampleSelect(w, h, filterType, pAngle-180) {
			numPx := h
			if pAngle > 180 {
				numPx += w
			}
			intraEdgeUpsample(leftCol, numPx, bitDepth)
			upLeft = 1
		}
	}

	dx, dy := 0, 0
	if pAngle < 90 {
		dx = drIntraDerivative[pAngle]
	} else if pAngle > 90 && pAngle < 180 {
		dx = drIntraDerivative[180-pAngle]
		dy = drIntraDerivative[pAngle-90]
	} else if pAngle > 180 {
		dy = drIntraDerivative[270-pAngle]
	}

	ar := func(i int) int { return aboveRow[bufOff+i] }
	lc := func(i int) int { return leftCol[bufOff+i] }

	switch {
	case pAngle < 90:
		maxBaseX := (w + h - 1) << uint(upAbove)
		for i := 0; i < h; i++ {
			idx := (i + 1) * dx
			for j := 0; j < w; j++ {
				base := (idx >> uint(6-upAbove)) + (j << uint(upAbove))
				shift := ((idx << uint(upAbove)) >> 1) & 0x1F
				if base < maxBaseX {
					pred[i][j] = round2int(ar(base)*(32-shift)+ar(base+1)*shift, 5)
				} else {
					pred[i][j] = ar(maxBaseX)
				}
			}
		}
	case pAngle > 90 && pAngle < 180:
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				idx := (j << 6) - (i+1)*dx
				base := idx >> uint(6-upAbove)
				if base >= -(1 << uint(upAbove)) {
					shift := ((idx << uint(upAbove)) >> 1) & 0x1F
					pred[i][j] = round2int(ar(base)*(32-shift)+ar(base+1)*shift, 5)
				} else {
					idx2 := (i << 6) - (j+1)*dy
					base2 := idx2 >> uint(6-upLeft)
					shift := ((idx2 << uint(upLeft)) >> 1) & 0x1F
					pred[i][j] = round2int(lc(base2)*(32-shift)+lc(base2+1)*shift, 5)
				}
			}
		}
	case pAngle > 180:
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				idx := (j + 1) * dy
				base := (idx >> uint(6-upLeft)) + (i << uint(upLeft))
				shift := ((idx << uint(upLeft)) >> 1) & 0x1F
				pred[i][j] = round2int(lc(base)*(32-shift)+lc(base+1)*shift, 5)
			}
		}
	case pAngle == 90:
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				pred[i][j] = ar(j)
			}
		}
	default: // pAngle == 180
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				pred[i][j] = lc(i)
			}
		}
	}
}
