package decode

import (
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
)

// Loop restoration types (AV1 spec §6.10.15), mirroring the header constants.
const (
	RestoreNone    = 0
	RestoreWiener  = 1
	RestoreSgrproj = 2
)

var wienerTapsMin = [3]int{-5, -23, -17}
var wienerTapsMax = [3]int{10, 8, 46}
var wienerTapsK = [3]int{1, 2, 3}
var wienerTapsMid = [3]int{3, -7, 15}

func countUnitsInFrame(unitSize, frameSize int) int {
	return max(1, (frameSize+(unitSize>>1))/unitSize)
}

// resetRefLr resets the running Wiener reference coefficients at the start of a tile.
func (fd *frameDecoder) resetRefLr() {
	for p := 0; p < 3; p++ {
		for pass := 0; pass < 2; pass++ {
			for i := 0; i < 3; i++ {
				fd.refLrWiener[p][pass][i] = wienerTapsMid[i]
			}
		}
		for i := 0; i < 2; i++ {
			fd.refSgrXqd[p][i] = sgrprojXqdMid[i]
		}
	}
}

func inverseRecenter(r, v int) int {
	switch {
	case v > 2*r:
		return v
	case v&1 != 0:
		return r - ((v + 1) >> 1)
	default:
		return r + (v >> 1)
	}
}

// decodeSubexpBool reads a subexponential-coded value in [0, numSyms) (AV1 spec §5.9.27).
func (fd *frameDecoder) decodeSubexpBool(numSyms, k int) int {
	i := 0
	mk := 0
	for {
		b2 := k
		if i != 0 {
			b2 = k + i - 1
		}
		a := 1 << uint(b2)
		if numSyms <= mk+3*a {
			return fd.d.ReadNS(numSyms-mk) + mk
		}
		if fd.d.ReadBool() != 0 {
			i++
			mk += a
		} else {
			return int(fd.d.ReadLiteral(b2)) + mk
		}
	}
}

func (fd *frameDecoder) decodeUnsignedSubexpWithRef(mx, k, r int) int {
	v := fd.decodeSubexpBool(mx, k)
	if (r << 1) <= mx {
		return inverseRecenter(r, v)
	}
	return mx - 1 - inverseRecenter(mx-1-r, v)
}

func (fd *frameDecoder) decodeSignedSubexpWithRef(low, high, k, r int) int {
	return fd.decodeUnsignedSubexpWithRef(high-low, k, r-low) + low
}

// readLr reads loop-restoration unit parameters overlapping a superblock (AV1 spec §5.11.57).
func (fd *frameDecoder) readLr(r, c, bSize int) {
	if !fd.fh.UsesLr || fd.fh.AllowIntrabc {
		return
	}
	w := predict.Num4x4BlocksWide[bSize]
	h := predict.Num4x4BlocksHigh[bSize]
	for plane := 0; plane < fd.numPlanes; plane++ {
		if fd.fh.FrameRestorationType[plane] == RestoreNone {
			continue
		}
		subX, subY := 0, 0
		if plane > 0 {
			subX, subY = fd.subX, fd.subY
		}
		unitSize := fd.fh.LoopRestorationSize[plane]
		unitRows := countUnitsInFrame(unitSize, (fd.fh.FrameHeight+subY)>>uint(subY))
		unitCols := countUnitsInFrame(unitSize, (fd.fh.UpscaledWidth+subX)>>uint(subX))
		unitRowStart := (r*(4>>uint(subY)) + unitSize - 1) / unitSize
		unitRowEnd := min(unitRows, ((r+h)*(4>>uint(subY))+unitSize-1)/unitSize)
		numerator := 4 >> uint(subX)
		denominator := unitSize
		if fd.fh.FrameWidth != fd.fh.UpscaledWidth {
			// use_superres: map the coded-resolution superblock columns onto the
			// upscaled-resolution loop-restoration unit grid (AV1 spec §5.11.57).
			numerator = (4 >> uint(subX)) * fd.fh.SuperresDenom
			denominator = unitSize * header.SuperresNum
		}
		unitColStart := (c*numerator + denominator - 1) / denominator
		unitColEnd := min(unitCols, ((c+w)*numerator+denominator-1)/denominator)
		for unitRow := unitRowStart; unitRow < unitRowEnd; unitRow++ {
			for unitCol := unitColStart; unitCol < unitColEnd; unitCol++ {
				fd.readLrUnit(plane, unitRow, unitCol)
			}
		}
	}
}

func (fd *frameDecoder) readLrUnit(plane, unitRow, unitCol int) {
	var rtype int
	switch fd.fh.FrameRestorationType[plane] {
	case RestoreWiener:
		if fd.d.DecodeSymbol(fd.c.useWiener) != 0 {
			rtype = RestoreWiener
		}
	case RestoreSgrproj:
		if fd.d.DecodeSymbol(fd.c.useSgrproj) != 0 {
			rtype = RestoreSgrproj
		}
	default:
		rtype = fd.d.DecodeSymbol(fd.c.restorationType)
	}
	fd.lrType[plane][unitRow][unitCol] = rtype
	if rtype == RestoreWiener {
		for pass := 0; pass < 2; pass++ {
			firstCoeff := 0
			if plane != 0 {
				firstCoeff = 1
				fd.lrWiener[plane][unitRow][unitCol][pass][0] = 0
			}
			for j := firstCoeff; j < 3; j++ {
				v := fd.decodeSignedSubexpWithRef(wienerTapsMin[j], wienerTapsMax[j]+1,
					wienerTapsK[j], fd.refLrWiener[plane][pass][j])
				fd.lrWiener[plane][unitRow][unitCol][pass][j] = v
				fd.refLrWiener[plane][pass][j] = v
			}
		}
	} else if rtype == RestoreSgrproj {
		fd.readSgrParams(plane, unitRow, unitCol)
	}
}

// wienerCoeff expands 3 coded coefficients into the symmetric 7-tap filter (AV1 spec §7.17.5).
func wienerCoeff(coeff [3]int) [7]int {
	var filter [7]int
	filter[3] = 128
	for i := 0; i < 3; i++ {
		c := coeff[i]
		filter[i] = c
		filter[6-i] = c
		filter[3] -= 2 * c
	}
	return filter
}

// loopRestore applies loop restoration over the (deblocked + CDEF'd) frame (AV1 spec §7.17).
func (fd *frameDecoder) loopRestore() {
	if !fd.fh.UsesLr {
		return
	}
	lr := make([]*predict.Plane, fd.numPlanes)
	for p := 0; p < fd.numPlanes; p++ {
		lr[p] = fd.planes[p].Clone()
	}
	for y := 0; y < fd.fh.FrameHeight; y += 4 {
		for x := 0; x < fd.fh.UpscaledWidth; x += 4 {
			for plane := 0; plane < fd.numPlanes; plane++ {
				if fd.fh.FrameRestorationType[plane] != RestoreNone {
					fd.loopRestoreBlock(lr, plane, y>>2, x>>2)
				}
			}
		}
	}
	fd.planes = lr
}

func (fd *frameDecoder) loopRestoreBlock(lr []*predict.Plane, plane, row, col int) {
	lumaY := row * 4
	stripeNum := (lumaY + 8) / 64
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	stripeStartY := (-8 + stripeNum*64) >> uint(subY)
	stripeEndY := stripeStartY + (64 >> uint(subY)) - 1
	unitSize := fd.fh.LoopRestorationSize[plane]
	unitRows := countUnitsInFrame(unitSize, (fd.fh.FrameHeight+subY)>>uint(subY))
	unitCols := countUnitsInFrame(unitSize, (fd.fh.UpscaledWidth+subX)>>uint(subX))
	unitRow := min(unitRows-1, ((row*4+8)>>uint(subY))/unitSize)
	unitCol := min(unitCols-1, ((col*4)>>uint(subX))/unitSize)
	planeEndX := ((fd.fh.UpscaledWidth + subX) >> uint(subX)) - 1
	planeEndY := ((fd.fh.FrameHeight + subY) >> uint(subY)) - 1
	x := (col * 4) >> uint(subX)
	y := (row * 4) >> uint(subY)
	w := min(4>>uint(subX), planeEndX-x+1)
	h := min(4>>uint(subY), planeEndY-y+1)
	if w <= 0 || h <= 0 {
		return
	}
	switch fd.lrType[plane][unitRow][unitCol] {
	case RestoreWiener:
		fd.wienerFilter(lr, plane, unitRow, unitCol, x, y, w, h, stripeStartY, stripeEndY, planeEndX, planeEndY)
	case RestoreSgrproj:
		fd.selfGuidedFilter(lr, plane, unitRow, unitCol, x, y, w, h, stripeStartY, stripeEndY, planeEndX, planeEndY)
	}
}

// getSourceSample fetches a sample for loop restoration, taking in-stripe samples
// from the CDEF frame and out-of-stripe samples from the deblocked frame (AV1 spec §7.17.6).
func (fd *frameDecoder) getSourceSample(plane, x, y, stripeStartY, stripeEndY, planeEndX, planeEndY int) int {
	x = clip3i(0, planeEndX, x)
	y = clip3i(0, planeEndY, y)
	if y < stripeStartY {
		y = max(stripeStartY-2, y)
		return int(fd.deblocked[plane].At(x, y))
	}
	if y > stripeEndY {
		y = min(stripeEndY+2, y)
		return int(fd.deblocked[plane].At(x, y))
	}
	return int(fd.planes[plane].At(x, y))
}

func (fd *frameDecoder) wienerFilter(lr []*predict.Plane, plane, unitRow, unitCol, x, y, w, h,
	stripeStartY, stripeEndY, planeEndX, planeEndY int) {
	bd := fd.bitDepth
	round0, interRound1 := 3, 11
	if bd == 12 {
		round0, interRound1 = 5, 9
	}
	vfilter := wienerCoeff(fd.lrWiener[plane][unitRow][unitCol][0])
	hfilter := wienerCoeff(fd.lrWiener[plane][unitRow][unitCol][1])
	offset := 1 << uint(bd+7-round0-1)
	limit := (1 << uint(bd+1+7-round0)) - 1
	intermediate := make([][]int, h+6)
	for r := 0; r < h+6; r++ {
		intermediate[r] = make([]int, w)
		for c := 0; c < w; c++ {
			s := 0
			for t := 0; t < 7; t++ {
				s += hfilter[t] * fd.getSourceSample(plane, x+c+t-3, y+r-3, stripeStartY, stripeEndY, planeEndX, planeEndY)
			}
			intermediate[r][c] = clip3i(-offset, limit-offset, round2(s, round0))
		}
	}
	hi := (1 << uint(bd)) - 1
	for r := 0; r < h; r++ {
		for c := 0; c < w; c++ {
			s := 0
			for t := 0; t < 7; t++ {
				s += vfilter[t] * intermediate[r+t][c]
			}
			lr[plane].Set(x+c, y+r, uint16(clip3i(0, hi, round2(s, interRound1))))
		}
	}
}
