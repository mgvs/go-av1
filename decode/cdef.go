package decode

import (
	"github.com/mgvs/go-av1/predict"
)

var cdefDivTable = [9]int{0, 840, 420, 280, 210, 168, 140, 120, 105}

var cdefDirections = [8][2][2]int{
	{{-1, 1}, {-2, 2}},
	{{0, 1}, {-1, 2}},
	{{0, 1}, {0, 2}},
	{{0, 1}, {1, 2}},
	{{1, 1}, {2, 2}},
	{{1, 0}, {2, 1}},
	{{1, 0}, {2, 0}},
	{{1, 0}, {2, -1}},
}

var cdefPriTaps = [2][2]int{{4, 2}, {3, 3}}
var cdefSecTaps = [2][2]int{{2, 1}, {2, 1}}

var cdefUvDir = [2][2][8]int{
	{{0, 1, 2, 3, 4, 5, 6, 7}, {1, 2, 2, 2, 3, 4, 6, 0}},
	{{7, 0, 2, 4, 5, 6, 6, 6}, {0, 1, 2, 3, 4, 5, 6, 7}},
}

func floorLog2(x int) int {
	s := 0
	for x != 0 {
		x >>= 1
		s++
	}
	return s - 1
}

// cdef applies the CDEF deringing filter to the (already deblocked) frame
// (AV1 spec §7.15). Reads from a snapshot of the deblocked planes and writes the
// filtered result back.
func (fd *frameDecoder) cdef() {
	// The deblocked frame is the loop-restoration source for out-of-stripe samples.
	fd.deblocked = make([]*predict.Plane, fd.numPlanes)
	for p := 0; p < fd.numPlanes; p++ {
		fd.deblocked[p] = fd.planes[p].Clone()
	}
	if !fd.seq.EnableCDEF || fd.fh.CodedLossless || fd.fh.AllowIntrabc {
		return
	}
	// CdefFrame starts as a copy of the deblocked CurrFrame; filtering reads the
	// snapshot (src) and writes the copy (dst).
	src := make([]*predict.Plane, fd.numPlanes)
	dst := make([]*predict.Plane, fd.numPlanes)
	for p := 0; p < fd.numPlanes; p++ {
		src[p] = fd.planes[p]
		dst[p] = src[p].Clone()
	}

	const step4 = 2 // Num_4x4_Blocks_Wide[BLOCK_8X8]
	const cdefSize4 = 16
	const cdefMask4 = ^(cdefSize4 - 1)
	coeffShift := fd.bitDepth - 8
	for r := 0; r < fd.miRows; r += step4 {
		for c := 0; c < fd.miCols; c += step4 {
			idx := fd.cdefIdx[r&cdefMask4][c&cdefMask4]
			if idx == -1 {
				continue
			}
			skip := fd.skipAt(r, c) && fd.skipAt(r+1, c) && fd.skipAt(r, c+1) && fd.skipAt(r+1, c+1)
			if skip {
				continue
			}
			yDir, variance := fd.cdefDirection(src[0], r, c)

			priStr := fd.fh.CdefYPriStrength[idx] << uint(coeffShift)
			secStr := fd.fh.CdefYSecStrength[idx] << uint(coeffShift)
			dir := 0
			if priStr != 0 {
				dir = yDir
			}
			varStr := 0
			if variance>>6 != 0 {
				varStr = min(floorLog2(variance>>6), 12)
			}
			if variance != 0 {
				priStr = (priStr*(4+varStr) + 8) >> 4
			} else {
				priStr = 0
			}
			damping := fd.fh.CdefDamping + coeffShift
			fd.cdefFilter(src, dst, 0, r, c, priStr, secStr, damping, dir)
			if fd.numPlanes == 1 {
				continue
			}
			priStr = fd.fh.CdefUVPriStrength[idx] << uint(coeffShift)
			secStr = fd.fh.CdefUVSecStrength[idx] << uint(coeffShift)
			dir = 0
			if priStr != 0 {
				dir = cdefUvDir[fd.subX][fd.subY][yDir]
			}
			damping = fd.fh.CdefDamping + coeffShift - 1
			fd.cdefFilter(src, dst, 1, r, c, priStr, secStr, damping, dir)
			fd.cdefFilter(src, dst, 2, r, c, priStr, secStr, damping, dir)
		}
	}
	fd.planes = dst
}

func (fd *frameDecoder) skipAt(r, c int) bool {
	if r >= fd.miRows || c >= fd.miCols {
		return true
	}
	return fd.skips[r][c] != 0
}

// cdefDirection detects the dominant edge direction and variance of an 8x8 luma
// block (AV1 spec §7.15.2).
func (fd *frameDecoder) cdefDirection(luma *predict.Plane, r, c int) (yDir, variance int) {
	var cost [8]int
	var partial [8][15]int
	x0 := c << 2
	y0 := r << 2
	for i := 0; i < 8; i++ {
		for j := 0; j < 8; j++ {
			x := (int(luma.At(x0+j, y0+i)) >> uint(fd.bitDepth-8)) - 128
			partial[0][i+j] += x
			partial[1][i+j/2] += x
			partial[2][i] += x
			partial[3][3+i-j/2] += x
			partial[4][7+i-j] += x
			partial[5][3-i/2+j] += x
			partial[6][j] += x
			partial[7][i/2+j] += x
		}
	}
	for i := 0; i < 8; i++ {
		cost[2] += partial[2][i] * partial[2][i]
		cost[6] += partial[6][i] * partial[6][i]
	}
	cost[2] *= cdefDivTable[8]
	cost[6] *= cdefDivTable[8]
	for i := 0; i < 7; i++ {
		cost[0] += (partial[0][i]*partial[0][i] + partial[0][14-i]*partial[0][14-i]) * cdefDivTable[i+1]
		cost[4] += (partial[4][i]*partial[4][i] + partial[4][14-i]*partial[4][14-i]) * cdefDivTable[i+1]
	}
	cost[0] += partial[0][7] * partial[0][7] * cdefDivTable[8]
	cost[4] += partial[4][7] * partial[4][7] * cdefDivTable[8]
	for i := 1; i < 8; i += 2 {
		for j := 0; j < 5; j++ {
			cost[i] += partial[i][3+j] * partial[i][3+j]
		}
		cost[i] *= cdefDivTable[8]
		for j := 0; j < 3; j++ {
			cost[i] += (partial[i][j]*partial[i][j] + partial[i][10-j]*partial[i][10-j]) * cdefDivTable[2*j+2]
		}
	}
	bestCost := 0
	for i := 0; i < 8; i++ {
		if cost[i] > bestCost {
			bestCost = cost[i]
			yDir = i
		}
	}
	variance = (bestCost - cost[(yDir+4)&7]) >> 10
	return
}

func cdefConstrain(diff, threshold, damping int) int {
	if threshold == 0 {
		return 0
	}
	dampingAdj := max(0, damping-floorLog2(threshold))
	sign := 1
	if diff < 0 {
		sign = -1
	}
	return sign * clip3i(0, absInt(diff), threshold-(absInt(diff)>>uint(dampingAdj)))
}

// cdefFilter filters one 8x8 (sub-sampled) block (AV1 spec §7.15.3).
func (fd *frameDecoder) cdefFilter(src, dst []*predict.Plane, plane, r, c, priStr, secStr, damping, dir int) {
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	coeffShift := fd.bitDepth - 8
	x0 := (c * 4) >> uint(subX)
	y0 := (r * 4) >> uint(subY)
	w := 8 >> uint(subX)
	h := 8 >> uint(subY)
	sp := src[plane]
	priIdx := (priStr >> uint(coeffShift)) & 1
	get := func(i, j, d, k, sign int) (int, bool) {
		y := y0 + i + sign*cdefDirections[d][k][0]
		x := x0 + j + sign*cdefDirections[d][k][1]
		candR := (y << uint(subY)) >> 2
		candC := (x << uint(subX)) >> 2
		if candR >= 0 && candR < fd.miRows && candC >= 0 && candC < fd.miCols {
			return int(sp.At(x, y)), true
		}
		return 0, false
	}
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			x := int(sp.At(x0+j, y0+i))
			sum := 0
			mx, mn := x, x
			for k := 0; k < 2; k++ {
				for sign := -1; sign <= 1; sign += 2 {
					if p, ok := get(i, j, dir, k, sign); ok {
						sum += cdefPriTaps[priIdx][k] * cdefConstrain(p-x, priStr, damping)
						mx = max(p, mx)
						mn = min(p, mn)
					}
					for dirOff := -2; dirOff <= 2; dirOff += 4 {
						if s, ok := get(i, j, (dir+dirOff)&7, k, sign); ok {
							sum += cdefSecTaps[priIdx][k] * cdefConstrain(s-x, secStr, damping)
							mx = max(s, mx)
							mn = min(s, mn)
						}
					}
				}
			}
			lessZero := 0
			if sum < 0 {
				lessZero = 1
			}
			dst[plane].Set(x0+j, y0+i, uint16(clip3i(mn, mx, x+((8+sum-lessZero)>>4))))
		}
	}
}
