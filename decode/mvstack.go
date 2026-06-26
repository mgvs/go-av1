package decode

import "github.com/mgvs/go-av1/predict"

// Inter prediction constants (AV1 spec §3); IntraFrame / GM values mirror header.
const (
	IntraFrame          = 0
	GmIdentity          = 0
	GmTranslation       = 1
	WarpedModelPrecBits = 16

	maxRefMvStackSize = 8
	refCatLevel       = 640
	mvBorder          = 128

	// Inter Y modes (AV1 spec §6.10.24).
	nearestMv        = 13
	nearMv           = 14
	globalMv         = 15
	newMv            = 16
	nearestNearestMv = 17
	nearNearMv       = 18
	nearestNewMv     = 19
	newNearestMv     = 20
	nearNewMv        = 21
	newNearMv        = 22
	globalGlobalMv   = 23
	newNewMv         = 24
)

func hasNewmv(mode int) bool {
	switch mode {
	case newMv, newNewMv, nearNewMv, newNearMv, nearestNewMv, newNearestMv:
		return true
	}
	return false
}

func hasNearmv(mode int) bool {
	switch mode {
	case nearMv, nearNearMv, nearNewMv, newNearMv:
		return true
	}
	return false
}

// isInterAt reports whether the candidate 4x4 location holds an inter block.
func (fd *frameDecoder) isInterAt(r, c int) bool {
	if fd.isInters != nil {
		return fd.isInters[r][c]
	}
	return fd.gridRefFrames[r][c][0] > IntraFrame
}

// miWritten reports whether a 4x4 mode-info location has been decoded this frame.
func (fd *frameDecoder) miWritten(r, c int) bool {
	return fd.miWrittenGrid != nil && fd.miWrittenGrid[r][c]
}

// setupGlobalMv derives the block's global-motion MV for a reference list (AV1
// spec §7.10.2.1). Identity / intra references yield a zero MV.
func (fd *frameDecoder) setupGlobalMv(refList int) MV {
	ref := fd.refFrame[refList]
	if ref == IntraFrame {
		return MV{}
	}
	typ := fd.fh.GmType[ref]
	gm := fd.fh.GmParams[ref]
	switch {
	case typ == GmIdentity:
		return MV{}
	case typ == GmTranslation:
		return MV{
			Row: gm[0] >> uint(WarpedModelPrecBits-3),
			Col: gm[1] >> uint(WarpedModelPrecBits-3),
		}
	default:
		bw := predict.BlockWidth(fd.miSize)
		bh := predict.BlockHeight(fd.miSize)
		x := fd.miCol*4 + bw/2 - 1
		y := fd.miRow*4 + bh/2 - 1
		xc := (gm[2]-(1<<WarpedModelPrecBits))*x + gm[3]*y + gm[0]
		yc := gm[4]*x + (gm[5]-(1<<WarpedModelPrecBits))*y + gm[1]
		var mv MV
		if fd.fh.AllowHighPrecisionMV {
			mv = MV{Row: round2signed(yc, WarpedModelPrecBits-3), Col: round2signed(xc, WarpedModelPrecBits-3)}
		} else {
			mv = MV{Row: round2signed(yc, WarpedModelPrecBits-2) * 2, Col: round2signed(xc, WarpedModelPrecBits-2) * 2}
		}
		fd.lowerMvPrecision(&mv)
		return mv
	}
}

// lowerMvPrecision removes MV precision the frame is not allowed to use
// (AV1 spec §7.10.2.10).
func (fd *frameDecoder) lowerMvPrecision(mv *MV) {
	if fd.fh.AllowHighPrecisionMV {
		return
	}
	comp := [2]*int{&mv.Row, &mv.Col}
	for _, p := range comp {
		v := *p
		if fd.fh.ForceIntegerMV != 0 {
			a := absInt(v)
			aInt := (a + 3) >> 3
			if v > 0 {
				*p = aInt << 3
			} else {
				*p = -(aInt << 3)
			}
		} else if v&1 != 0 {
			if v > 0 {
				*p = v - 1
			} else {
				*p = v + 1
			}
		}
	}
}

// findMvStack builds the reference MV candidate stack and the inter-mode contexts
// (AV1 spec §7.10.2). Spatial candidates only (temporal scan handled separately when
// use_ref_frame_mvs is set).
func (fd *frameDecoder) findMvStack(isCompound bool) {
	bw4 := predict.Num4x4BlocksWide[fd.miSize]
	bh4 := predict.Num4x4BlocksHigh[fd.miSize]
	fd.numMvFound = 0
	fd.newMvCount = 0
	fd.globalMvs[0] = fd.setupGlobalMv(0)
	if isCompound {
		fd.globalMvs[1] = fd.setupGlobalMv(1)
	}
	fd.foundMatch = 0
	fd.scanRow(-1, isCompound)
	foundAboveMatch := fd.foundMatch
	fd.foundMatch = 0
	fd.scanCol(-1, isCompound)
	foundLeftMatch := fd.foundMatch
	fd.foundMatch = 0
	if max(bw4, bh4) <= 16 {
		fd.scanPoint(-1, bw4, isCompound)
	}
	if fd.foundMatch == 1 {
		foundAboveMatch = 1
	}
	fd.closeMatches = foundAboveMatch + foundLeftMatch
	numNearest := fd.numMvFound
	numNew := fd.newMvCount
	if numNearest > 0 {
		for idx := 0; idx < numNearest; idx++ {
			fd.weightStack[idx] += refCatLevel
		}
	}
	fd.zeroMvContext = 0
	if fd.fh.UseRefFrameMvs {
		fd.temporalScan(isCompound)
	}
	fd.foundMatch = 0
	fd.scanPoint(-1, -1, isCompound)
	if fd.foundMatch == 1 {
		foundAboveMatch = 1
	}
	fd.foundMatch = 0
	fd.scanRow(-3, isCompound)
	if fd.foundMatch == 1 {
		foundAboveMatch = 1
	}
	fd.foundMatch = 0
	fd.scanCol(-3, isCompound)
	if fd.foundMatch == 1 {
		foundLeftMatch = 1
	}
	fd.foundMatch = 0
	if bh4 > 1 {
		fd.scanRow(-5, isCompound)
		if fd.foundMatch == 1 {
			foundAboveMatch = 1
		}
		fd.foundMatch = 0
	}
	if bw4 > 1 {
		fd.scanCol(-5, isCompound)
		if fd.foundMatch == 1 {
			foundLeftMatch = 1
		}
		fd.foundMatch = 0
	}
	fd.totalMatches = foundAboveMatch + foundLeftMatch
	fd.sortMvStack(0, numNearest, isCompound)
	fd.sortMvStack(numNearest, fd.numMvFound, isCompound)
	if fd.numMvFound < 2 {
		fd.extraSearch(isCompound)
	}
	fd.contextAndClamping(isCompound, numNew)
}

func (fd *frameDecoder) scanRow(deltaRow int, isCompound bool) {
	bw4 := predict.Num4x4BlocksWide[fd.miSize]
	end4 := min(min(bw4, fd.miCols-fd.miCol), 16)
	deltaCol := 0
	useStep16 := bw4 >= 16
	dr := deltaRow
	if absInt(deltaRow) > 1 {
		dr += fd.miRow & 1
		deltaCol = 1 - (fd.miCol & 1)
	}
	for i := 0; i < end4; {
		mvRow := fd.miRow + dr
		mvCol := fd.miCol + deltaCol + i
		if !fd.isInside(mvRow, mvCol) {
			break
		}
		length := min(bw4, predict.Num4x4BlocksWide[fd.miSizes[mvRow][mvCol]])
		if absInt(deltaRow) > 1 {
			length = max(2, length)
		}
		if useStep16 {
			length = max(4, length)
		}
		fd.addRefMvCandidate(mvRow, mvCol, isCompound, length*2)
		i += length
	}
}

func (fd *frameDecoder) scanCol(deltaCol int, isCompound bool) {
	bh4 := predict.Num4x4BlocksHigh[fd.miSize]
	end4 := min(min(bh4, fd.miRows-fd.miRow), 16)
	deltaRow := 0
	useStep16 := bh4 >= 16
	dc := deltaCol
	if absInt(deltaCol) > 1 {
		deltaRow = 1 - (fd.miRow & 1)
		dc += fd.miCol & 1
	}
	for i := 0; i < end4; {
		mvRow := fd.miRow + deltaRow + i
		mvCol := fd.miCol + dc
		if !fd.isInside(mvRow, mvCol) {
			break
		}
		length := min(bh4, predict.Num4x4BlocksHigh[fd.miSizes[mvRow][mvCol]])
		if absInt(deltaCol) > 1 {
			length = max(2, length)
		}
		if useStep16 {
			length = max(4, length)
		}
		fd.addRefMvCandidate(mvRow, mvCol, isCompound, length*2)
		i += length
	}
}

func (fd *frameDecoder) scanPoint(deltaRow, deltaCol int, isCompound bool) {
	mvRow := fd.miRow + deltaRow
	mvCol := fd.miCol + deltaCol
	if fd.isInside(mvRow, mvCol) && fd.miWritten(mvRow, mvCol) {
		fd.addRefMvCandidate(mvRow, mvCol, isCompound, 4)
	}
}

func (fd *frameDecoder) addRefMvCandidate(mvRow, mvCol int, isCompound bool, weight int) {
	if !fd.isInterAt(mvRow, mvCol) {
		return
	}
	if !isCompound {
		for candList := 0; candList < 2; candList++ {
			if fd.gridRefFrames[mvRow][mvCol][candList] == fd.refFrame[0] {
				fd.searchStack(mvRow, mvCol, candList, weight)
			}
		}
	} else if fd.gridRefFrames[mvRow][mvCol][0] == fd.refFrame[0] &&
		fd.gridRefFrames[mvRow][mvCol][1] == fd.refFrame[1] {
		fd.compoundSearchStack(mvRow, mvCol, weight)
	}
}

func (fd *frameDecoder) searchStack(mvRow, mvCol, candList, weight int) {
	candMode := fd.yModes[mvRow][mvCol]
	candSize := fd.miSizes[mvRow][mvCol]
	large := min(predict.BlockWidth(candSize), predict.BlockHeight(candSize)) >= 8
	var candMv MV
	if (candMode == globalMv || candMode == globalGlobalMv) &&
		fd.fh.GmType[fd.refFrame[0]] > GmTranslation && large {
		candMv = fd.globalMvs[0]
	} else {
		candMv = fd.gridMvs[mvRow][mvCol][candList]
	}
	fd.lowerMvPrecision(&candMv)
	if hasNewmv(candMode) {
		fd.newMvCount++
	}
	fd.foundMatch = 1
	for idx := 0; idx < fd.numMvFound; idx++ {
		if fd.refStackMv[idx][0] == candMv {
			fd.weightStack[idx] += weight
			return
		}
	}
	if fd.numMvFound < maxRefMvStackSize {
		fd.refStackMv[fd.numMvFound][0] = candMv
		fd.weightStack[fd.numMvFound] = weight
		fd.numMvFound++
	}
}

func (fd *frameDecoder) compoundSearchStack(mvRow, mvCol, weight int) {
	candMvs := fd.gridMvs[mvRow][mvCol]
	candMode := fd.yModes[mvRow][mvCol]
	if candMode == globalGlobalMv {
		for refList := 0; refList < 2; refList++ {
			if fd.fh.GmType[fd.refFrame[refList]] > GmTranslation {
				candMvs[refList] = fd.globalMvs[refList]
			}
		}
	}
	for i := 0; i < 2; i++ {
		fd.lowerMvPrecision(&candMvs[i])
	}
	fd.foundMatch = 1
	for idx := 0; idx < fd.numMvFound; idx++ {
		if fd.refStackMv[idx][0] == candMvs[0] && fd.refStackMv[idx][1] == candMvs[1] {
			fd.weightStack[idx] += weight
			if hasNewmv(candMode) {
				fd.newMvCount++
			}
			return
		}
	}
	if fd.numMvFound < maxRefMvStackSize {
		fd.refStackMv[fd.numMvFound][0] = candMvs[0]
		fd.refStackMv[fd.numMvFound][1] = candMvs[1]
		fd.weightStack[fd.numMvFound] = weight
		fd.numMvFound++
	}
	if hasNewmv(candMode) {
		fd.newMvCount++
	}
}

func (fd *frameDecoder) sortMvStack(start, end int, isCompound bool) {
	for end > start {
		newEnd := start
		for idx := start + 1; idx < end; idx++ {
			if fd.weightStack[idx-1] < fd.weightStack[idx] {
				fd.weightStack[idx-1], fd.weightStack[idx] = fd.weightStack[idx], fd.weightStack[idx-1]
				lists := 1
				if isCompound {
					lists = 2
				}
				for list := 0; list < lists; list++ {
					fd.refStackMv[idx-1][list], fd.refStackMv[idx][list] = fd.refStackMv[idx][list], fd.refStackMv[idx-1][list]
				}
				newEnd = idx
			}
		}
		end = newEnd
	}
}

func (fd *frameDecoder) extraSearch(isCompound bool) {
	for l := 0; l < 2; l++ {
		fd.refIdCount[l] = 0
		fd.refDiffCount[l] = 0
	}
	w4 := min(min(16, predict.Num4x4BlocksWide[fd.miSize]), fd.miCols-fd.miCol)
	h4 := min(min(16, predict.Num4x4BlocksHigh[fd.miSize]), fd.miRows-fd.miRow)
	num4x4 := min(w4, h4)
	for pass := 0; pass < 2; pass++ {
		idx := 0
		for idx < num4x4 && fd.numMvFound < 2 {
			var mvRow, mvCol int
			if pass == 0 {
				mvRow, mvCol = fd.miRow-1, fd.miCol+idx
			} else {
				mvRow, mvCol = fd.miRow+idx, fd.miCol-1
			}
			if !fd.isInside(mvRow, mvCol) {
				break
			}
			fd.addExtraMvCandidate(mvRow, mvCol, isCompound)
			if pass == 0 {
				idx += predict.Num4x4BlocksWide[fd.miSizes[mvRow][mvCol]]
			} else {
				idx += predict.Num4x4BlocksHigh[fd.miSizes[mvRow][mvCol]]
			}
		}
	}
	if !isCompound {
		for idx := fd.numMvFound; idx < 2; idx++ {
			fd.refStackMv[idx][0] = fd.globalMvs[0]
		}
		return
	}
	// Combine the RefId/RefDiff candidates into the stack (AV1 spec §7.10.2.12).
	var combined [2][2]MV
	for list := 0; list < 2; list++ {
		compCount := 0
		for i := 0; i < fd.refIdCount[list]; i++ {
			combined[compCount][list] = fd.refIdMvs[list][i]
			compCount++
		}
		for i := 0; i < fd.refDiffCount[list] && compCount < 2; i++ {
			combined[compCount][list] = fd.refDiffMvs[list][i]
			compCount++
		}
		for compCount < 2 {
			combined[compCount][list] = fd.globalMvs[list]
			compCount++
		}
	}
	if fd.numMvFound == 1 {
		if combined[0][0] == fd.refStackMv[0][0] && combined[0][1] == fd.refStackMv[0][1] {
			fd.refStackMv[fd.numMvFound][0] = combined[1][0]
			fd.refStackMv[fd.numMvFound][1] = combined[1][1]
		} else {
			fd.refStackMv[fd.numMvFound][0] = combined[0][0]
			fd.refStackMv[fd.numMvFound][1] = combined[0][1]
		}
		fd.weightStack[fd.numMvFound] = 2
		fd.numMvFound++
	} else {
		for idx := 0; idx < 2; idx++ {
			fd.refStackMv[fd.numMvFound][0] = combined[idx][0]
			fd.refStackMv[fd.numMvFound][1] = combined[idx][1]
			fd.weightStack[fd.numMvFound] = 2
			fd.numMvFound++
		}
	}
}

func (fd *frameDecoder) addExtraMvCandidate(mvRow, mvCol int, isCompound bool) {
	if isCompound {
		for candList := 0; candList < 2; candList++ {
			candRef := fd.gridRefFrames[mvRow][mvCol][candList]
			if candRef <= IntraFrame {
				continue
			}
			for list := 0; list < 2; list++ {
				candMv := fd.gridMvs[mvRow][mvCol][candList]
				if candRef == fd.refFrame[list] && fd.refIdCount[list] < 2 {
					fd.refIdMvs[list][fd.refIdCount[list]] = candMv
					fd.refIdCount[list]++
				} else if fd.refDiffCount[list] < 2 {
					if fd.fh.RefFrameSignBias[candRef] != fd.fh.RefFrameSignBias[fd.refFrame[list]] {
						candMv.Row *= -1
						candMv.Col *= -1
					}
					fd.refDiffMvs[list][fd.refDiffCount[list]] = candMv
					fd.refDiffCount[list]++
				}
			}
		}
		return
	}
	for candList := 0; candList < 2; candList++ {
		candRef := fd.gridRefFrames[mvRow][mvCol][candList]
		if candRef <= IntraFrame {
			continue
		}
		candMv := fd.gridMvs[mvRow][mvCol][candList]
		if fd.fh.RefFrameSignBias[candRef] != fd.fh.RefFrameSignBias[fd.refFrame[0]] {
			candMv.Row *= -1
			candMv.Col *= -1
		}
		found := false
		for idx := 0; idx < fd.numMvFound; idx++ {
			if fd.refStackMv[idx][0] == candMv {
				found = true
				break
			}
		}
		if !found && fd.numMvFound < maxRefMvStackSize {
			fd.refStackMv[fd.numMvFound][0] = candMv
			fd.weightStack[fd.numMvFound] = 2
			fd.numMvFound++
		}
	}
}

func (fd *frameDecoder) contextAndClamping(isCompound bool, numNew int) {
	bw := predict.BlockWidth(fd.miSize)
	bh := predict.BlockHeight(fd.miSize)
	numLists := 1
	if isCompound {
		numLists = 2
	}
	for idx := 0; idx < fd.numMvFound; idx++ {
		z := 0
		if idx+1 < fd.numMvFound {
			w0 := fd.weightStack[idx]
			w1 := fd.weightStack[idx+1]
			if w0 >= refCatLevel {
				if w1 < refCatLevel {
					z = 1
				}
			} else {
				z = 2
			}
		}
		fd.drlCtxStack[idx] = z
	}
	for list := 0; list < numLists; list++ {
		for idx := 0; idx < fd.numMvFound; idx++ {
			fd.refStackMv[idx][list].Row = fd.clampMvRow(fd.refStackMv[idx][list].Row, mvBorder+bh*8)
			fd.refStackMv[idx][list].Col = fd.clampMvCol(fd.refStackMv[idx][list].Col, mvBorder+bw*8)
		}
	}
	switch {
	case fd.closeMatches == 0:
		fd.newMvContext = min(fd.totalMatches, 1)
		fd.refMvContext = fd.totalMatches
	case fd.closeMatches == 1:
		fd.newMvContext = 3 - min(numNew, 1)
		fd.refMvContext = 2 + fd.totalMatches
	default:
		fd.newMvContext = 5 - min(numNew, 1)
		fd.refMvContext = 5
	}
}

func (fd *frameDecoder) clampMvRow(mvec, border int) int {
	bh4 := predict.Num4x4BlocksHigh[fd.miSize]
	mbToTop := -(fd.miRow * 4 * 8)
	mbToBottom := (fd.miRows - bh4 - fd.miRow) * 4 * 8
	return clip3i(mbToTop-border, mbToBottom+border, mvec)
}

func (fd *frameDecoder) clampMvCol(mvec, border int) int {
	bw4 := predict.Num4x4BlocksWide[fd.miSize]
	mbToLeft := -(fd.miCol * 4 * 8)
	mbToRight := (fd.miCols - bw4 - fd.miCol) * 4 * 8
	return clip3i(mbToLeft-border, mbToRight+border, mvec)
}
