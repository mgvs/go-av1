package decode

import "github.com/mgvs/go-av1/predict"

const maxLoopFilter = 63

// loopFilter runs the deblocking filter over the reconstructed frame: all vertical
// boundaries then all horizontal boundaries (AV1 spec §7.14).
func (fd *frameDecoder) loopFilter() {
	lf := fd.fh.LoopFilterLevel
	if lf[0] == 0 && lf[1] == 0 {
		return
	}
	for plane := 0; plane < fd.numPlanes; plane++ {
		if plane != 0 && lf[1+plane] == 0 {
			continue
		}
		for pass := 0; pass < 2; pass++ {
			rowStep, colStep := 1, 1
			if plane > 0 {
				rowStep = 1 << uint(fd.subY)
				colStep = 1 << uint(fd.subX)
			}
			for row := 0; row < fd.miRows; row += rowStep {
				for col := 0; col < fd.miCols; col += colStep {
					fd.loopFilterEdge(plane, pass, row, col)
				}
			}
		}
	}
}

// loopFilterEdge filters one 4-sample edge (AV1 spec §7.14.2).
func (fd *frameDecoder) loopFilterEdge(plane, pass, row, col int) {
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	dx, dy := 0, 0
	if pass == 0 {
		dx = 1
	} else {
		dy = 1
	}
	x := col * 4
	y := row * 4
	row |= subY
	col |= subX
	if x >= fd.fh.FrameWidth || y >= fd.fh.FrameHeight {
		return
	}
	if pass == 0 && x == 0 {
		return
	}
	if pass == 1 && y == 0 {
		return
	}
	xP := x >> uint(subX)
	yP := y >> uint(subY)
	prevRow := row - (dy << uint(subY))
	prevCol := col - (dx << uint(subX))
	miSize := fd.miSizes[row][col]
	txSz := fd.lfTxSizes[plane][row>>uint(subY)][col>>uint(subX)]
	planeSize := SubsampledSize[miSize][subX][subY]
	skip := fd.skips[row][col]
	isIntra := fd.gridRefFrames == nil || fd.gridRefFrames[row][col][0] <= IntraFrame
	prevTxSz := fd.lfTxSizes[plane][prevRow>>uint(subY)][prevCol>>uint(subX)]

	isBlockEdge := (pass == 0 && xP%predict.BlockWidth(planeSize) == 0) ||
		(pass == 1 && yP%predict.BlockHeight(planeSize) == 0)
	isTxEdge := (pass == 0 && xP%TxWidth[txSz] == 0) ||
		(pass == 1 && yP%TxHeight[txSz] == 0)
	applyFilter := isTxEdge && (isBlockEdge || skip == 0 || isIntra)

	filterSize := fd.filterSize(txSz, prevTxSz, pass, plane)
	// The edge level comes from the current (q-side) block; if zero, fall back to
	// the adjacent (p-side) block (AV1 spec §7.14.2).
	lvl := fd.filterLevel(plane, pass, row, col)
	if lvl == 0 {
		lvl = fd.filterLevel(plane, pass, prevRow, prevCol)
		if lvl == 0 {
			return
		}
	}
	limit, blimit, thresh := fd.filterLimits(lvl)
	for i := 0; i < 4; i++ {
		if applyFilter {
			fd.sampleFilter(xP+dy*i, yP+dx*i, plane, limit, blimit, thresh, dx, dy, filterSize)
		}
	}
}

// filterSize is the maximum filter width allowed across a boundary (AV1 spec §7.14.3).
func (fd *frameDecoder) filterSize(txSz, prevTxSz, pass, plane int) int {
	var baseSize int
	if pass == 0 {
		baseSize = min(TxWidth[prevTxSz], TxWidth[txSz])
	} else {
		baseSize = min(TxHeight[prevTxSz], TxHeight[txSz])
	}
	if plane == 0 {
		return min(16, baseSize)
	}
	return min(8, baseSize)
}

// adaptiveStrength derives the filter level and limits (AV1 spec §7.14.4/7.14.5).
// Segmentation and delta-LF are disabled in our intra streams (segment 0, deltaLF 0),
// and the reference frame is INTRA_FRAME with intra mode (modeType 0).
func (fd *frameDecoder) filterLevel(plane, pass, row, col int) int {
	i := pass
	if plane > 0 {
		i = plane + 1
	}
	// Per-superblock loop-filter delta (AV1 spec §7.14.4): deltaLF is added before
	// clamping. For delta_lf_multi the index is i (pass for luma, plane+1 for
	// chroma); otherwise index 0.
	deltaLF := 0
	if fd.deltaLFGrid != nil {
		idx := 0
		if fd.fh.DeltaLfMulti {
			idx = i
		}
		deltaLF = int(fd.deltaLFGrid[row][col][idx])
	}
	lvlSeg := clip3i(0, maxLoopFilter, deltaLF+fd.fh.LoopFilterLevel[i])
	if fd.segmentIds != nil {
		seg := fd.segmentIds[row][col]
		if fd.segFeatureActiveIdx(seg, segLvlAltLfYV+i) {
			lvlSeg = clip3i(0, maxLoopFilter, fd.fh.FeatureData[seg][segLvlAltLfYV+i]+lvlSeg)
		}
	}
	if fd.fh.LoopFilterDeltaEnabled {
		nShift := lvlSeg >> 5
		ref := IntraFrame
		if fd.gridRefFrames != nil {
			ref = fd.gridRefFrames[row][col][0]
		}
		if ref <= IntraFrame {
			lvlSeg += fd.fh.LoopFilterRefDeltas[IntraFrame] << uint(nShift)
		} else {
			mode := fd.yModes[row][col]
			modeType := 0
			if mode >= nearestMv && mode != globalMv && mode != globalGlobalMv {
				modeType = 1
			}
			lvlSeg += fd.fh.LoopFilterRefDeltas[ref] << uint(nShift)
			lvlSeg += fd.fh.LoopFilterModeDeltas[modeType] << uint(nShift)
		}
		lvlSeg = clip3i(0, maxLoopFilter, lvlSeg)
	}
	return lvlSeg
}

// filterLimits derives the deblock limit/blimit/thresh from a filter level
// (AV1 spec §7.14.5).
func (fd *frameDecoder) filterLimits(lvl int) (limit, blimit, thresh int) {
	shift := 0
	switch {
	case fd.fh.LoopFilterSharpness > 4:
		shift = 2
	case fd.fh.LoopFilterSharpness > 0:
		shift = 1
	}
	if fd.fh.LoopFilterSharpness > 0 {
		limit = clip3i(1, 9-fd.fh.LoopFilterSharpness, lvl>>uint(shift))
	} else {
		limit = max(1, lvl>>uint(shift))
	}
	blimit = 2*(lvl+2) + limit
	thresh = lvl >> 4
	return
}

// sampleFilter filters one sample column/row across a boundary (AV1 spec §7.14.6.1).
func (fd *frameDecoder) sampleFilter(x, y, plane, limit, blimit, thresh, dx, dy, filterSize int) {
	hev, fmask, flat, flat2 := fd.filterMask(x, y, plane, limit, blimit, thresh, dx, dy, filterSize)
	if !fmask {
		return
	}
	switch {
	case filterSize == 4 || !flat:
		fd.narrowFilter(hev, x, y, plane, dx, dy)
	case filterSize == 8 || !flat2:
		fd.wideFilter(x, y, plane, dx, dy, 3)
	default:
		fd.wideFilter(x, y, plane, dx, dy, 4)
	}
}

// filterMask computes the deblock decision masks (AV1 spec §7.14.6.2).
func (fd *frameDecoder) filterMask(x, y, plane, limit, blimit, thresh, dx, dy, filterSize int) (hev, fmask, flat, flat2 bool) {
	bd := fd.bitDepth
	p := fd.planes[plane]
	g := func(k int) int { return int(p.At(x+dx*k, y+dy*k)) }
	q0, q1, q2, q3 := g(0), g(1), g(2), g(3)
	p0, p1, p2, p3 := g(-1), g(-2), g(-3), g(-4)

	threshBd := thresh << uint(bd-8)
	hev = absInt(p1-p0) > threshBd || absInt(q1-q0) > threshBd

	filterLen := 4
	switch {
	case filterSize == 4:
		filterLen = 4
	case plane != 0:
		filterLen = 6
	case filterSize == 8:
		filterLen = 8
	default:
		filterLen = 16
	}
	limitBd := limit << uint(bd-8)
	blimitBd := blimit << uint(bd-8)
	m := absInt(p1-p0) > limitBd || absInt(q1-q0) > limitBd ||
		absInt(p0-q0)*2+absInt(p1-q1)/2 > blimitBd
	if filterLen >= 6 {
		m = m || absInt(p2-p1) > limitBd || absInt(q2-q1) > limitBd
	}
	if filterLen >= 8 {
		m = m || absInt(p3-p2) > limitBd || absInt(q3-q2) > limitBd
	}
	fmask = !m

	thBd := 1 << uint(bd-8)
	if filterSize >= 8 {
		mm := absInt(p1-p0) > thBd || absInt(q1-q0) > thBd ||
			absInt(p2-p0) > thBd || absInt(q2-q0) > thBd
		if filterLen >= 8 {
			mm = mm || absInt(p3-p0) > thBd || absInt(q3-q0) > thBd
		}
		flat = !mm
	}
	if filterSize >= 16 {
		q4, q5, q6 := g(4), g(5), g(6)
		p4, p5, p6 := g(-5), g(-6), g(-7)
		mm := absInt(p6-p0) > thBd || absInt(q6-q0) > thBd ||
			absInt(p5-p0) > thBd || absInt(q5-q0) > thBd ||
			absInt(p4-p0) > thBd || absInt(q4-q0) > thBd
		flat2 = !mm
	}
	return
}

// narrowFilter modifies up to two samples each side of the boundary (AV1 spec §7.14.6.3).
func (fd *frameDecoder) narrowFilter(hev bool, x, y, plane, dx, dy int) {
	bd := fd.bitDepth
	base := 0x80 << uint(bd-8)
	clamp := func(v int) int { return clip3i(-(1 << uint(bd-1)), (1<<uint(bd-1))-1, v) }
	p := fd.planes[plane]
	q0 := int(p.At(x, y))
	q1 := int(p.At(x+dx, y+dy))
	p0 := int(p.At(x-dx, y-dy))
	p1 := int(p.At(x-2*dx, y-2*dy))
	ps1, ps0, qs0, qs1 := p1-base, p0-base, q0-base, q1-base
	filter := 0
	if hev {
		filter = clamp(ps1 - qs1)
	}
	filter = clamp(filter + 3*(qs0-ps0))
	filter1 := clamp(filter+4) >> 3
	filter2 := clamp(filter+3) >> 3
	p.Set(x, y, uint16(clamp(qs0-filter1)+base))
	p.Set(x-dx, y-dy, uint16(clamp(ps0+filter2)+base))
	if !hev {
		f := round2(filter1, 1)
		p.Set(x+dx, y+dy, uint16(clamp(qs1-f)+base))
		p.Set(x-2*dx, y-2*dy, uint16(clamp(ps1+f)+base))
	}
}

// wideFilter applies the flat-region low-pass filter (AV1 spec §7.14.6.4).
func (fd *frameDecoder) wideFilter(x, y, plane, dx, dy, log2Size int) {
	var n int
	switch {
	case log2Size == 4:
		n = 6
	case plane == 0:
		n = 3
	default:
		n = 2
	}
	n2 := 1
	if log2Size == 3 && plane == 0 {
		n2 = 0
	}
	p := fd.planes[plane]
	F := make([]int, 2*n)
	for i := -n; i < n; i++ {
		t := 0
		for j := -n; j <= n; j++ {
			pp := clip3i(-(n + 1), n, i+j)
			tap := 1
			if absInt(j) <= n2 {
				tap = 2
			}
			t += int(p.At(x+pp*dx, y+pp*dy)) * tap
		}
		F[i+n] = round2(t, log2Size)
	}
	for i := -n; i < n; i++ {
		p.Set(x+i*dx, y+i*dy, uint16(F[i+n]))
	}
}

const segLvlAltLfYV = 1
