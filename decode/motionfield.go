package decode

import (
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
)

// Temporal motion field projection (AV1 spec §7.9). When use_ref_frame_mvs is
// set, motion vectors saved from reference frames are projected onto an 8x8 grid
// for the current frame and used as temporal candidates in find_mv_stack.

const (
	mfmvStackSize    = 3
	maxOffsetWidth   = 8
	maxOffsetHeight  = 0
	refmvsLimit      = (1 << 12) - 1
	mvBorderTemporal = -1 << 15
)

var divMult = [32]int{
	0, 16384, 8192, 5461, 4096, 3276, 2730, 2340, 2048, 1820, 1638,
	1489, 1365, 1260, 1170, 1092, 1024, 963, 910, 862, 819, 780,
	744, 712, 682, 655, 630, 606, 585, 564, 546, 528,
}

// storeMotionField computes the filtered motion-field vectors for the current
// frame (AV1 spec §7.19 motion field motion vector storage process).
func (fd *frameDecoder) storeMotionField() {
	fd.mfMvs = make([][]MV, fd.miRows)
	fd.mfRefFrames = make([][]int, fd.miRows)
	for row := 0; row < fd.miRows; row++ {
		fd.mfMvs[row] = make([]MV, fd.miCols)
		fd.mfRefFrames[row] = make([]int, fd.miCols)
		for col := 0; col < fd.miCols; col++ {
			fd.mfRefFrames[row][col] = header.NoneFrame
			for list := 0; list < 2; list++ {
				r := fd.gridRefFrames[row][col][list]
				if r <= IntraFrame {
					continue
				}
				refIdx := fd.fh.RefFrameIdx[r-header.LastFrame]
				if fd.refs[refIdx] == nil {
					continue
				}
				dist := fd.relativeDist(fd.refs[refIdx].OrderHint, fd.fh.OrderHint)
				if dist >= 0 {
					continue
				}
				mv := fd.gridMvs[row][col][list]
				if absInt(mv.Row) <= refmvsLimit && absInt(mv.Col) <= refmvsLimit {
					fd.mfRefFrames[row][col] = r
					fd.mfMvs[row][col] = mv
				}
			}
		}
	}
}

// getMvProjection scales a saved motion vector for a different frame distance
// (AV1 spec §7.9.3).
func getMvProjection(mv MV, numerator, denominator int) MV {
	clippedDen := minInt(maxFrameDistance, denominator)
	clippedNum := clip3i(-maxFrameDistance, maxFrameDistance, numerator)
	proj := func(c int) int {
		scaled := round2Signed(c*clippedNum*divMult[clippedDen], 14)
		return clip3i(-(1<<14)+1, (1<<14)-1, scaled)
	}
	return MV{Row: proj(mv.Row), Col: proj(mv.Col)}
}

// getBlockPosition projects a grid location by a motion vector (AV1 spec §7.9.4).
// Returns the projected (x8,y8) and whether it falls within the allowed range.
func (fd *frameDecoder) getBlockPosition(x8, y8, dstSign int, projMv MV) (posX8, posY8 int, valid bool) {
	valid = true
	project := func(v8, delta, max8, maxOff8 int) int {
		base8 := (v8 >> 3) << 3
		var offset8 int
		if delta >= 0 {
			offset8 = delta >> (3 + 1 + 2)
		} else {
			offset8 = -((-delta) >> (3 + 1 + 2))
		}
		v8 += dstSign * offset8
		if v8 < 0 || v8 >= max8 || v8 < base8-maxOff8 || v8 >= base8+8+maxOff8 {
			valid = false
		}
		return v8
	}
	posY8 = project(y8, projMv.Row, fd.miRows>>1, maxOffsetHeight)
	posX8 = project(x8, projMv.Col, fd.miCols>>1, maxOffsetWidth)
	return posX8, posY8, valid
}

// projectMvs projects a reference frame's motion field onto the current frame
// (AV1 spec §7.9.2). Returns whether the source frame was valid.
func (fd *frameDecoder) projectMvs(src, dstSign int) bool {
	srcIdx := fd.fh.RefFrameIdx[src-header.LastFrame]
	w8 := fd.miCols >> 1
	h8 := fd.miRows >> 1
	ref := fd.refs[srcIdx]
	if ref == nil || ref.MiRows != fd.miRows || ref.MiCols != fd.miCols ||
		ref.frameType == header.IntraOnlyFrame || ref.frameType == header.KeyFrame {
		return false
	}
	for y8 := 0; y8 < h8; y8++ {
		for x8 := 0; x8 < w8; x8++ {
			row := 2*y8 + 1
			col := 2*x8 + 1
			srcRef := ref.mfRefFrames[row][col]
			if srcRef <= IntraFrame {
				continue
			}
			refToCur := fd.relativeDist(fd.fh.OrderHints[src], fd.fh.OrderHint)
			refOffset := fd.relativeDist(fd.fh.OrderHints[src], ref.savedOrderHints[srcRef])
			posValid := absInt(refToCur) <= maxFrameDistance &&
				absInt(refOffset) <= maxFrameDistance && refOffset > 0
			if !posValid {
				continue
			}
			mv := ref.mfMvs[row][col]
			projMv := getMvProjection(mv, refToCur*dstSign, refOffset)
			posX8, posY8, ok := fd.getBlockPosition(x8, y8, dstSign, projMv)
			if !ok {
				continue
			}
			for dst := header.LastFrame; dst <= header.AltRefFrame; dst++ {
				refToDst := fd.relativeDist(fd.fh.OrderHint, fd.fh.OrderHints[dst])
				pm := getMvProjection(mv, refToDst, refOffset)
				fd.motionFieldMvs[dst][posY8][posX8] = pm
			}
		}
	}
	return true
}

// motionFieldEstimation builds the temporal motion field for the current frame
// (AV1 spec §7.9.1). Invoked at the start of an inter frame when
// use_ref_frame_mvs is set.
func (fd *frameDecoder) motionFieldEstimation() {
	w8 := fd.miCols >> 1
	h8 := fd.miRows >> 1
	fd.motionFieldMvs = make([][][]MV, header.AltRefFrame+1)
	for ref := header.LastFrame; ref <= header.AltRefFrame; ref++ {
		fd.motionFieldMvs[ref] = make([][]MV, h8)
		for y := 0; y < h8; y++ {
			fd.motionFieldMvs[ref][y] = make([]MV, w8)
			for x := 0; x < w8; x++ {
				fd.motionFieldMvs[ref][y][x] = MV{mvBorderTemporal, mvBorderTemporal}
			}
		}
	}
	lastIdx := fd.fh.RefFrameIdx[0]
	curGoldOrderHint := fd.fh.OrderHints[header.GoldenFrame]
	var lastAltOrderHint int
	if fd.refs[lastIdx] != nil {
		lastAltOrderHint = fd.refs[lastIdx].savedOrderHints[header.AltRefFrame]
	}
	useLast := lastAltOrderHint != curGoldOrderHint
	if useLast {
		fd.projectMvs(header.LastFrame, -1)
	}
	refStamp := mfmvStackSize - 2
	if fd.relativeDist(fd.fh.OrderHints[header.BwdRefFrame], fd.fh.OrderHint) > 0 {
		if fd.projectMvs(header.BwdRefFrame, 1) {
			refStamp--
		}
	}
	if fd.relativeDist(fd.fh.OrderHints[header.AltRef2Frame], fd.fh.OrderHint) > 0 {
		if fd.projectMvs(header.AltRef2Frame, 1) {
			refStamp--
		}
	}
	if fd.relativeDist(fd.fh.OrderHints[header.AltRefFrame], fd.fh.OrderHint) > 0 && refStamp >= 0 {
		if fd.projectMvs(header.AltRefFrame, 1) {
			refStamp--
		}
	}
	if refStamp >= 0 {
		fd.projectMvs(header.Last2Frame, -1)
	}
}

// temporalScan scans the projected motion field for candidates (AV1 spec
// §7.10.2.5). Invoked from find_mv_stack when use_ref_frame_mvs is set.
func (fd *frameDecoder) temporalScan(isCompound bool) {
	bw4 := predict.Num4x4BlocksWide[fd.miSize]
	bh4 := predict.Num4x4BlocksHigh[fd.miSize]
	stepW4 := 2
	if bw4 >= 16 {
		stepW4 = 4
	}
	stepH4 := 2
	if bh4 >= 16 {
		stepH4 = 4
	}
	for deltaRow := 0; deltaRow < minInt(bh4, 16); deltaRow += stepH4 {
		for deltaCol := 0; deltaCol < minInt(bw4, 16); deltaCol += stepW4 {
			fd.addTplRefMv(deltaRow, deltaCol, isCompound)
		}
	}
	allowExtension := bh4 >= 2 && bh4 < 16 && bw4 >= 2 && bw4 < 16
	if allowExtension {
		tplSamplePos := [3][2]int{{bh4, -2}, {bh4, bw4}, {bh4 - 2, bw4}}
		for i := 0; i < 3; i++ {
			deltaRow := tplSamplePos[i][0]
			deltaCol := tplSamplePos[i][1]
			if fd.checkSbBorder(deltaRow, deltaCol) {
				fd.addTplRefMv(deltaRow, deltaCol, isCompound)
			}
		}
	}
}

func (fd *frameDecoder) checkSbBorder(deltaRow, deltaCol int) bool {
	row := (fd.miRow & 15) + deltaRow
	col := (fd.miCol & 15) + deltaCol
	return row >= 0 && row < 16 && col >= 0 && col < 16
}

// addTplRefMv looks up a temporal motion vector from the motion field and adds
// it to the stack (AV1 spec §7.10.2.6 temporal sample process).
func (fd *frameDecoder) addTplRefMv(deltaRow, deltaCol int, isCompound bool) {
	mvRow := (fd.miRow + deltaRow) | 1
	mvCol := (fd.miCol + deltaCol) | 1
	if !fd.isInside(mvRow, mvCol) {
		return
	}
	x8 := mvCol >> 1
	y8 := mvRow >> 1
	if deltaRow == 0 && deltaCol == 0 {
		fd.zeroMvContext = 1
	}
	if !isCompound {
		candMv := fd.motionFieldMvs[fd.refFrame[0]][y8][x8]
		if candMv.Row == mvBorderTemporal {
			return
		}
		fd.lowerMvPrecision(&candMv)
		if deltaRow == 0 && deltaCol == 0 {
			if absInt(candMv.Row-fd.globalMvs[0].Row) >= 16 ||
				absInt(candMv.Col-fd.globalMvs[0].Col) >= 16 {
				fd.zeroMvContext = 1
			} else {
				fd.zeroMvContext = 0
			}
		}
		var idx int
		for idx = 0; idx < fd.numMvFound; idx++ {
			if candMv == fd.refStackMv[idx][0] {
				break
			}
		}
		if idx < fd.numMvFound {
			fd.weightStack[idx] += 2
		} else if fd.numMvFound < maxRefMvStackSize {
			fd.refStackMv[fd.numMvFound][0] = candMv
			fd.weightStack[fd.numMvFound] = 2
			fd.numMvFound++
		}
		return
	}
	candMv0 := fd.motionFieldMvs[fd.refFrame[0]][y8][x8]
	if candMv0.Row == mvBorderTemporal {
		return
	}
	candMv1 := fd.motionFieldMvs[fd.refFrame[1]][y8][x8]
	if candMv1.Row == mvBorderTemporal {
		return
	}
	fd.lowerMvPrecision(&candMv0)
	fd.lowerMvPrecision(&candMv1)
	if deltaRow == 0 && deltaCol == 0 {
		if absInt(candMv0.Row-fd.globalMvs[0].Row) >= 16 ||
			absInt(candMv0.Col-fd.globalMvs[0].Col) >= 16 ||
			absInt(candMv1.Row-fd.globalMvs[1].Row) >= 16 ||
			absInt(candMv1.Col-fd.globalMvs[1].Col) >= 16 {
			fd.zeroMvContext = 1
		} else {
			fd.zeroMvContext = 0
		}
	}
	var idx int
	for idx = 0; idx < fd.numMvFound; idx++ {
		if candMv0 == fd.refStackMv[idx][0] && candMv1 == fd.refStackMv[idx][1] {
			break
		}
	}
	if idx < fd.numMvFound {
		fd.weightStack[idx] += 2
	} else if fd.numMvFound < maxRefMvStackSize {
		fd.refStackMv[fd.numMvFound][0] = candMv0
		fd.refStackMv[fd.numMvFound][1] = candMv1
		fd.weightStack[fd.numMvFound] = 2
		fd.numMvFound++
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func round2Signed(x, n int) int {
	if x >= 0 {
		return round2(x, n)
	}
	return -round2(-x, n)
}
