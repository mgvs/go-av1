package decode

import (
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
)

// MV joint types (AV1 spec §6.10.24).
const (
	mvJointZero   = 0
	mvJointHnzvz  = 1
	mvJointHzvnz  = 2
	mvJointHnzvnz = 3
	class0Size    = 2
)

// interFrameModeInfo reads the mode info for a block in an inter frame
// (AV1 spec §5.11.23). Only the single-reference path needed for the first inter
// test vector is implemented.
func (fd *frameDecoder) interFrameModeInfo() error {
	// Default comp_group_idx/compound_idx so single-reference and intra blocks
	// store the spec defaults (0/1) into the neighbor context grids rather than
	// stale values from a previous compound block (AV1 spec §5.11.24).
	fd.compGroupIdxVal = 0
	fd.compoundIdxVal = 1
	fd.leftRefFrame[0], fd.aboveRefFrame[0] = IntraFrame, IntraFrame
	fd.leftRefFrame[1], fd.aboveRefFrame[1] = header.NoneFrame, header.NoneFrame
	if fd.availL {
		fd.leftRefFrame = fd.gridRefFrames[fd.miRow][fd.miCol-1]
	}
	if fd.availU {
		fd.aboveRefFrame = fd.gridRefFrames[fd.miRow-1][fd.miCol]
	}
	fd.leftIntra = fd.leftRefFrame[0] <= IntraFrame
	fd.aboveIntra = fd.aboveRefFrame[0] <= IntraFrame
	fd.leftSingle = fd.leftRefFrame[1] <= IntraFrame
	fd.aboveSingle = fd.aboveRefFrame[1] <= IntraFrame

	fd.skip = 0
	fd.segmentId = 0
	fd.interSegmentId(true)
	fd.readSkipMode()
	if fd.skipModeFlag {
		fd.skip = 1
	} else {
		fd.readSkip()
	}
	if fd.fh.SegIdPreSkip == 0 {
		fd.interSegmentId(false)
	}
	fd.readCdef()
	fd.readDeltaQIndex()
	if err := fd.readDeltaLF(); err != nil {
		return err
	}
	fd.readDeltas = false
	if err := fd.readIsInter(); err != nil {
		return err
	}
	if fd.isInterFlag {
		return fd.interBlockModeInfo()
	}
	return fd.intraBlockModeInfo()
}

func (fd *frameDecoder) readSkipMode() {
	// Segment features that force skip / reference / global motion suppress
	// skip_mode (AV1 spec §5.11.10).
	if fd.segFeatureActive(header.SegLvlSkip) || fd.segFeatureActive(header.SegLvlRefFrame) ||
		fd.segFeatureActive(header.SegLvlGlobalMV) ||
		!fd.fh.SkipModePresent || predict.BlockWidth(fd.miSize) < 8 || predict.BlockHeight(fd.miSize) < 8 {
		fd.skipModeFlag = false
		return
	}
	ctx := 0
	if fd.availU {
		ctx += fd.skipModes[fd.miRow-1][fd.miCol]
	}
	if fd.availL {
		ctx += fd.skipModes[fd.miRow][fd.miCol-1]
	}
	fd.skipModeFlag = fd.d.DecodeSymbol(fd.c.skipMode[ctx]) == 1
	if fd.skipModeFlag {
		fd.skip = 1
	}
}

func (fd *frameDecoder) readIsInter() error {
	if fd.skipModeFlag {
		fd.isInterFlag = true
		return nil
	}
	// Segment features force the inter/intra decision (AV1 spec §5.11.20).
	if fd.segFeatureActive(header.SegLvlRefFrame) {
		fd.isInterFlag = fd.fh.FeatureData[fd.segmentId][header.SegLvlRefFrame] != IntraFrame
		return nil
	}
	if fd.segFeatureActive(header.SegLvlGlobalMV) {
		fd.isInterFlag = true
		return nil
	}
	leftIntra, aboveIntra := b2i(fd.leftIntra), b2i(fd.aboveIntra)
	var ctx int
	switch {
	case fd.availU && fd.availL:
		if fd.leftIntra && fd.aboveIntra {
			ctx = 3
		} else {
			ctx = b2i(fd.leftIntra || fd.aboveIntra)
		}
	case fd.availU || fd.availL:
		if fd.availU {
			ctx = 2 * aboveIntra
		} else {
			ctx = 2 * leftIntra
		}
	default:
		ctx = 0
	}
	fd.isInterFlag = fd.d.DecodeSymbol(fd.c.isInter[ctx]) == 1
	fd.tr("is_inter(ctx%d)=%v", ctx, fd.isInterFlag)
	return nil
}

// interBlockModeInfo reads the reference frames, MV stack, inter mode and MVs
// (AV1 spec §5.11.24). Single reference only.
func (fd *frameDecoder) interBlockModeInfo() error {
	if err := fd.readRefFrames(); err != nil {
		return err
	}
	isCompound := fd.refFrame[1] > IntraFrame
	fd.findMvStack(isCompound)
	if err := fd.readInterMode(isCompound); err != nil {
		return err
	}
	fd.readDrlIdx()
	if err := fd.assignMv(isCompound); err != nil {
		return err
	}
	fd.readInterIntraMode(isCompound)
	if err := fd.readMotionMode(isCompound); err != nil {
		return err
	}
	if err := fd.readCompoundType(isCompound); err != nil {
		return err
	}
	fd.readInterpFilter(isCompound)
	return nil
}

// readInterpFilter reads the per-direction interpolation filter (AV1 spec §5.11.27).
func (fd *frameDecoder) readInterpFilter(isCompound bool) {
	if fd.fh.InterpolationFilter != header.InterpFilterSwitchable {
		fd.interpFilter[0] = fd.fh.InterpolationFilter
		fd.interpFilter[1] = fd.fh.InterpolationFilter
		return
	}
	dirs := 1
	if fd.seq.EnableDualFilter {
		dirs = 2
	}
	for dir := 0; dir < dirs; dir++ {
		if fd.needsInterpFilter() {
			fd.interpFilter[dir] = fd.d.DecodeSymbol(fd.c.interpFilter[fd.interpFilterCtx(dir)])
		} else {
			fd.interpFilter[dir] = interpEighttap
		}
	}
	if !fd.seq.EnableDualFilter {
		fd.interpFilter[1] = fd.interpFilter[0]
	}
}

const interpEighttap = 0

// needsInterpFilter (AV1 spec §5.11.27).
func (fd *frameDecoder) needsInterpFilter() bool {
	large := min(predict.BlockWidth(fd.miSize), predict.BlockHeight(fd.miSize)) >= 8
	if fd.skipModeFlag || fd.motionMode == motionModeWarp {
		return false
	}
	if large && fd.yMode == globalMv {
		return fd.fh.GmType[fd.refFrame[0]] == GmTranslation
	}
	if large && fd.yMode == globalGlobalMv {
		return fd.fh.GmType[fd.refFrame[0]] == GmTranslation || fd.fh.GmType[fd.refFrame[1]] == GmTranslation
	}
	return true
}

// interpFilterCtx (AV1 spec §8.3).
func (fd *frameDecoder) interpFilterCtx(dir int) int {
	ctx := ((dir&1)*2 + b2i(fd.refFrame[1] > IntraFrame)) * 4
	leftType, aboveType := 3, 3
	if fd.availL {
		lr := fd.gridRefFrames[fd.miRow][fd.miCol-1]
		if lr[0] == fd.refFrame[0] || lr[1] == fd.refFrame[0] {
			leftType = fd.gridInterpFilters[fd.miRow][fd.miCol-1][dir]
		}
	}
	if fd.availU {
		ar := fd.gridRefFrames[fd.miRow-1][fd.miCol]
		if ar[0] == fd.refFrame[0] || ar[1] == fd.refFrame[0] {
			aboveType = fd.gridInterpFilters[fd.miRow-1][fd.miCol][dir]
		}
	}
	switch {
	case leftType == aboveType:
		ctx += leftType
	case leftType == 3:
		ctx += aboveType
	case aboveType == 3:
		ctx += leftType
	default:
		ctx += 3
	}
	return ctx
}

// Motion modes (AV1 spec §6.10.24).
const (
	motionModeSimple = 0
	motionModeObmc   = 1
	motionModeWarp   = 2
)

// hasOverlappableCandidates reports whether an inter neighbor (above or left)
// exists for overlapped motion compensation (AV1 spec §7.10.3).
func (fd *frameDecoder) hasOverlappableCandidates() bool {
	if fd.availU {
		w4 := predict.Num4x4BlocksWide[fd.miSize]
		for x4 := fd.miCol; x4 < min(fd.miCols, fd.miCol+w4); x4 += 2 {
			if fd.gridRefFrames[fd.miRow-1][x4|1][0] > IntraFrame {
				return true
			}
		}
	}
	if fd.availL {
		h4 := predict.Num4x4BlocksHigh[fd.miSize]
		for y4 := fd.miRow; y4 < min(fd.miRows, fd.miRow+h4); y4 += 2 {
			if fd.gridRefFrames[y4|1][fd.miCol-1][0] > IntraFrame {
				return true
			}
		}
	}
	return false
}

// readInterIntraMode reads inter-intra compound prediction signalling (AV1 spec
// §5.11.28). When set, RefFrame[1] becomes INTRA_FRAME and the inter prediction is
// blended with an intra prediction.
func (fd *frameDecoder) readInterIntraMode(isCompound bool) {
	fd.isInterIntra = false
	if fd.skipModeFlag || !fd.seq.EnableInterintraCompound || isCompound ||
		fd.miSize < predict.Block8x8 || fd.miSize > predict.Block32x32 {
		return
	}
	if fd.d.DecodeSymbol(fd.c.interIntra[sizeGroup[fd.miSize]-1]) == 0 {
		return
	}
	fd.isInterIntra = true
	fd.interIntraMode = fd.d.DecodeSymbol(fd.c.interIntraMode[sizeGroup[fd.miSize]-1])
	fd.refFrame[1] = IntraFrame
	fd.angleDeltaY = 0
	fd.angleDeltaUV = 0
	fd.useFilterIntra = false
	fd.wedgeInterIntra = fd.d.DecodeSymbol(fd.c.wedgeInterIntra[fd.miSize]) == 1
	if fd.wedgeInterIntra {
		fd.wedgeIndex = fd.d.DecodeSymbol(fd.c.wedgeIdx[fd.miSize])
		fd.wedgeSign = 0
	}
}

// readMotionMode reads the motion mode for an inter block (AV1 spec §5.11.26).
// Warp and OBMC motion compensation are not yet implemented.
func (fd *frameDecoder) readMotionMode(isCompound bool) error {
	fd.motionMode = motionModeSimple
	if fd.skipModeFlag || !fd.fh.IsMotionModeSwitchable {
		return nil
	}
	if min(predict.BlockWidth(fd.miSize), predict.BlockHeight(fd.miSize)) < 8 {
		return nil
	}
	if fd.fh.ForceIntegerMV == 0 && (fd.yMode == globalMv || fd.yMode == globalGlobalMv) &&
		fd.fh.GmType[fd.refFrame[0]] > GmTranslation {
		return nil
	}
	if isCompound || fd.refFrame[1] == IntraFrame || !fd.hasOverlappableCandidates() {
		return nil
	}
	fd.findWarpSamples()
	if fd.fh.ForceIntegerMV != 0 || fd.numSamples == 0 || !fd.fh.AllowWarpedMotion || fd.isScaled(fd.refFrame[0]) {
		if fd.d.DecodeSymbol(fd.c.useObmc[fd.miSize]) == 1 {
			fd.motionMode = motionModeObmc
		}
		return nil
	}
	fd.motionMode = fd.d.DecodeSymbol(fd.c.motionModeCdf[fd.miSize])
	return nil
}

// isScaled reports whether the reference frame uses scaling (AV1 spec §5.11.26).
// No reference scaling is supported, so references always match the frame size.
func (fd *frameDecoder) isScaled(refFrame int) bool {
	refIdx := fd.fh.RefFrameIdx[refFrame-header.LastFrame]
	if fd.refs == nil || refIdx < 0 || refIdx >= len(fd.refs) || fd.refs[refIdx] == nil {
		return false
	}
	r := fd.refs[refIdx]
	return r.Width != fd.fh.FrameWidth || r.Height != fd.fh.FrameHeight
}

func (fd *frameDecoder) countRefs(frameType int) int {
	c := 0
	if fd.availU {
		if fd.aboveRefFrame[0] == frameType {
			c++
		}
		if fd.aboveRefFrame[1] == frameType {
			c++
		}
	}
	if fd.availL {
		if fd.leftRefFrame[0] == frameType {
			c++
		}
		if fd.leftRefFrame[1] == frameType {
			c++
		}
	}
	return c
}

func refCountCtx(c0, c1 int) int {
	switch {
	case c0 < c1:
		return 0
	case c0 == c1:
		return 1
	default:
		return 2
	}
}

// readRefFrames reads the single reference frame for the block (AV1 spec §5.11.25).
func (fd *frameDecoder) readRefFrames() error {
	if fd.skipModeFlag {
		// skip_mode blocks use the implicit SkipModeFrame references (AV1 spec
		// §5.11.25); no reference syntax is coded.
		fd.refFrame[0] = fd.fh.SkipModeFrame[0]
		fd.refFrame[1] = fd.fh.SkipModeFrame[1]
		return nil
	}
	// Segment features force the reference frame (AV1 spec §5.11.25).
	if fd.segFeatureActive(header.SegLvlRefFrame) {
		fd.refFrame[0] = fd.fh.FeatureData[fd.segmentId][header.SegLvlRefFrame]
		fd.refFrame[1] = header.NoneFrame
		return nil
	}
	if fd.segFeatureActive(header.SegLvlSkip) || fd.segFeatureActive(header.SegLvlGlobalMV) {
		fd.refFrame[0] = header.LastFrame
		fd.refFrame[1] = header.NoneFrame
		return nil
	}
	bw4 := predict.Num4x4BlocksWide[fd.miSize]
	bh4 := predict.Num4x4BlocksHigh[fd.miSize]
	if fd.fh.ReferenceSelect && min(bw4, bh4) >= 2 {
		if fd.d.DecodeSymbol(fd.c.compMode[fd.compModeCtx()]) == compoundReference {
			return fd.readCompoundRefs()
		}
	}
	// SINGLE_REFERENCE: a binary tree over the 7 reference frame names (AV1 spec §5.11.25).
	cnt := fd.countRefs
	fwd := cnt(header.LastFrame) + cnt(header.Last2Frame) + cnt(header.Last3Frame) + cnt(header.GoldenFrame)
	bwd := cnt(header.BwdRefFrame) + cnt(header.AltRef2Frame) + cnt(header.AltRefFrame)
	if fd.d.DecodeSymbol(fd.c.singleRef[refCountCtx(fwd, bwd)][0]) == 1 {
		// Backward references.
		ctxP2 := refCountCtx(cnt(header.BwdRefFrame)+cnt(header.AltRef2Frame), cnt(header.AltRefFrame))
		if fd.d.DecodeSymbol(fd.c.singleRef[ctxP2][1]) == 0 {
			ctxP6 := refCountCtx(cnt(header.BwdRefFrame), cnt(header.AltRef2Frame))
			if fd.d.DecodeSymbol(fd.c.singleRef[ctxP6][5]) == 1 {
				fd.refFrame[0] = header.AltRef2Frame
			} else {
				fd.refFrame[0] = header.BwdRefFrame
			}
		} else {
			fd.refFrame[0] = header.AltRefFrame
		}
	} else {
		ctxP3 := refCountCtx(cnt(header.LastFrame)+cnt(header.Last2Frame), cnt(header.Last3Frame)+cnt(header.GoldenFrame))
		if fd.d.DecodeSymbol(fd.c.singleRef[ctxP3][2]) == 1 {
			ctxP5 := refCountCtx(cnt(header.Last3Frame), cnt(header.GoldenFrame))
			if fd.d.DecodeSymbol(fd.c.singleRef[ctxP5][4]) == 1 {
				fd.refFrame[0] = header.GoldenFrame
			} else {
				fd.refFrame[0] = header.Last3Frame
			}
		} else {
			ctxP4 := refCountCtx(cnt(header.LastFrame), cnt(header.Last2Frame))
			if fd.d.DecodeSymbol(fd.c.singleRef[ctxP4][3]) == 1 {
				fd.refFrame[0] = header.Last2Frame
			} else {
				fd.refFrame[0] = header.LastFrame
			}
		}
	}
	fd.refFrame[1] = header.NoneFrame
	fd.tr("ref_frame=%d", fd.refFrame[0])
	return nil
}

// readInterMode reads the inter Y mode from the MV-stack contexts (AV1 spec §5.11.24).
func (fd *frameDecoder) readInterMode(isCompound bool) error {
	if fd.skipModeFlag {
		fd.yMode = nearestNearestMv
		return nil
	}
	// SEG_LVL_SKIP / SEG_LVL_GLOBALMV force GLOBALMV (AV1 spec §5.11.23).
	if fd.segFeatureActive(header.SegLvlSkip) || fd.segFeatureActive(header.SegLvlGlobalMV) {
		fd.yMode = globalMv
		return nil
	}
	if isCompound {
		ctx := compoundModeCtxMap[fd.refMvContext>>1][min(fd.newMvContext, compNewmvCtxs-1)]
		fd.yMode = nearestNearestMv + fd.d.DecodeSymbol(fd.c.compoundMode[ctx])
		fd.tr("compound_mode=%d", fd.yMode)
		return nil
	}
	newMvSym := fd.d.DecodeSymbol(fd.c.newMv[fd.newMvContext])
	if newMvSym == 0 {
		fd.yMode = newMv
	} else {
		zeroMvSym := fd.d.DecodeSymbol(fd.c.zeroMv[fd.zeroMvContext])
		if zeroMvSym == 0 {
			fd.yMode = globalMv
		} else {
			refMvSym := fd.d.DecodeSymbol(fd.c.refMvCdf[fd.refMvContext])
			if refMvSym == 0 {
				fd.yMode = nearestMv
			} else {
				fd.yMode = nearMv
			}
		}
	}
	fd.tr("inter_mode=%d (newCtx%d)", fd.yMode, fd.newMvContext)
	return nil
}

func (fd *frameDecoder) readDrlIdx() {
	fd.refMvIdx = 0
	if fd.yMode == newMv || fd.yMode == newNewMv {
		for idx := 0; idx < 2; idx++ {
			if fd.numMvFound > idx+1 {
				if fd.d.DecodeSymbol(fd.c.drlMode[fd.drlCtxStack[idx]]) == 0 {
					fd.refMvIdx = idx
					break
				}
				fd.refMvIdx = idx + 1
			}
		}
	} else if hasNearmv(fd.yMode) {
		fd.refMvIdx = 1
		for idx := 1; idx < 3; idx++ {
			if fd.numMvFound > idx+1 {
				if fd.d.DecodeSymbol(fd.c.drlMode[fd.drlCtxStack[idx]]) == 0 {
					fd.refMvIdx = idx
					break
				}
				fd.refMvIdx = idx + 1
			}
		}
	}
}

func (fd *frameDecoder) assignMv(isCompound bool) error {
	lists := 1
	if isCompound {
		lists = 2
	}
	for i := 0; i < lists; i++ {
		compMode := newMv
		if !fd.useIntrabc {
			compMode = fd.getMode(i)
		}
		switch {
		case fd.useIntrabc:
			fd.predMv[0] = fd.refStackMv[0][0]
			if fd.predMv[0] == (MV{}) {
				fd.predMv[0] = fd.refStackMv[1][0]
			}
			if fd.predMv[0] == (MV{}) {
				sbSize4 := fd.sbSize4
				if fd.miRow-sbSize4 < fd.miRowStart {
					fd.predMv[0] = MV{Row: 0, Col: -(sbSize4*miSize4 + intrabcDelayPixels) * 8}
				} else {
					fd.predMv[0] = MV{Row: -(sbSize4 * miSize4 * 8), Col: 0}
				}
			}
		case compMode == globalMv:
			fd.predMv[i] = fd.globalMvs[i]
		default:
			pos := 0
			if compMode != nearestMv {
				pos = fd.refMvIdx
			}
			if compMode == newMv && fd.numMvFound <= 1 {
				pos = 0
			}
			fd.predMv[i] = fd.refStackMv[pos][i]
		}
		if compMode == newMv {
			fd.readMv(i)
		} else {
			fd.mv[i] = fd.predMv[i]
		}
	}
	return nil
}

const (
	intrabcDelayPixels = 256
	miSize4            = 4 // MI_SIZE
)

func (fd *frameDecoder) readMv(ref int) {
	mvCtx := 0
	if fd.useIntrabc {
		mvCtx = 1 // MV_INTRABC_CONTEXT
	}
	joint := fd.d.DecodeSymbol(fd.c.mvJoint[mvCtx])
	diffRow, diffCol := 0, 0
	if joint == mvJointHzvnz || joint == mvJointHnzvnz {
		diffRow = fd.readMvComponent(mvCtx, 0)
	}
	if joint == mvJointHnzvz || joint == mvJointHnzvnz {
		diffCol = fd.readMvComponent(mvCtx, 1)
	}
	fd.mv[ref] = MV{Row: fd.predMv[ref].Row + diffRow, Col: fd.predMv[ref].Col + diffCol}
	fd.tr("read_mv joint=%d d=(%d,%d) mv=(%d,%d)", joint, diffRow, diffCol, fd.mv[ref].Row, fd.mv[ref].Col)
}

func (fd *frameDecoder) readMvComponent(mvCtx, comp int) int {
	sign := fd.d.DecodeSymbol(fd.c.mvSign[mvCtx][comp])
	cls := fd.d.DecodeSymbol(fd.c.mvClass[mvCtx][comp])
	var mag int
	if cls == 0 {
		class0Bit := fd.d.DecodeSymbol(fd.c.mvClass0Bit[mvCtx][comp])
		class0Fr := 3
		if fd.fh.ForceIntegerMV == 0 {
			class0Fr = fd.d.DecodeSymbol(fd.c.mvClass0Fr[mvCtx][comp][class0Bit])
		}
		class0Hp := 1
		if fd.fh.AllowHighPrecisionMV {
			class0Hp = fd.d.DecodeSymbol(fd.c.mvClass0Hp[mvCtx][comp])
		}
		mag = ((class0Bit << 3) | (class0Fr << 1) | class0Hp) + 1
	} else {
		d := 0
		for i := 0; i < cls; i++ {
			d |= fd.d.DecodeSymbol(fd.c.mvBit[mvCtx][comp][i]) << uint(i)
		}
		mag = class0Size << uint(cls+2)
		fr := 3
		if fd.fh.ForceIntegerMV == 0 {
			fr = fd.d.DecodeSymbol(fd.c.mvFr[mvCtx][comp])
		}
		hp := 1
		if fd.fh.AllowHighPrecisionMV {
			hp = fd.d.DecodeSymbol(fd.c.mvHp[mvCtx][comp])
		}
		mag += ((d << 3) | (fr << 1) | hp) + 1
	}
	if sign == 1 {
		return -mag
	}
	return mag
}

// sizeGroup maps a block size to its y_mode CDF context (AV1 spec §9.3).
var sizeGroup = [predict.BlockSizes]int{
	0, 0, 0, 1, 1, 1, 2, 2, 2, 3, 3, 3, 3, 3, 3, 3, 0, 0, 1, 1, 2, 2,
}

// intraBlockModeInfo reads the intra mode of an intra-coded block inside an inter
// frame (AV1 spec §5.11.27).
func (fd *frameDecoder) intraBlockModeInfo() error {
	fd.refFrame[0] = IntraFrame
	fd.refFrame[1] = header.NoneFrame
	fd.yMode = fd.d.DecodeSymbol(fd.c.yMode[sizeGroup[fd.miSize]])
	fd.tr("intra y_mode=%d", fd.yMode)
	fd.angleDeltaY = fd.intraAngleInfo(fd.yMode)

	fd.uvMode = DCPred
	fd.angleDeltaUV = 0
	if fd.hasChroma {
		cflAllowed := fd.cflAllowed()
		var cdf []uint16
		if cflAllowed {
			cdf = fd.c.uvModeCflAllow[fd.yMode]
		} else {
			cdf = fd.c.uvModeCflNotAllow[fd.yMode]
		}
		fd.uvMode = fd.d.DecodeSymbol(cdf)
		if fd.uvMode == UVCflPred {
			fd.readCflAlphas()
		}
		fd.angleDeltaUV = fd.intraAngleInfo(fd.uvMode)
	}

	if fd.miSize >= predict.Block8x8 && predict.BlockWidth(fd.miSize) <= 64 &&
		predict.BlockHeight(fd.miSize) <= 64 && fd.fh.AllowScreenContentTools != 0 {
		if err := fd.paletteModeInfo(); err != nil {
			return err
		}
	}
	fd.filterIntraModeInfo()
	return nil
}
