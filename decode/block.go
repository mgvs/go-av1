package decode

import (
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
)

func (fd *frameDecoder) decodeBlock(r, c, bSize int) error {
	fd.miRow, fd.miCol, fd.miSize = r, c, bSize
	bw4 := predict.Num4x4BlocksWide[bSize]
	bh4 := predict.Num4x4BlocksHigh[bSize]

	switch {
	case bh4 == 1 && fd.subY != 0 && (r&1) == 0:
		fd.hasChroma = false
	case bw4 == 1 && fd.subX != 0 && (c&1) == 0:
		fd.hasChroma = false
	default:
		fd.hasChroma = fd.numPlanes > 1
	}
	fd.availU = fd.isInside(r-1, c)
	fd.availL = fd.isInside(r, c-1)
	fd.availUChroma, fd.availLChroma = fd.availU, fd.availL
	if fd.hasChroma {
		if fd.subY != 0 && bh4 == 1 {
			fd.availUChroma = fd.isInside(r-2, c)
		}
		if fd.subX != 0 && bw4 == 1 {
			fd.availLChroma = fd.isInside(r, c-2)
		}
	} else {
		fd.availUChroma, fd.availLChroma = false, false
	}

	fd.isInterFlag = false
	fd.refFrame = [2]int{IntraFrame, header.NoneFrame}
	fd.mv = [2]MV{}
	fd.paletteSizeY, fd.paletteSizeUV = 0, 0
	if fd.fh.FrameIsIntra {
		if err := fd.intraFrameModeInfo(); err != nil {
			return err
		}
	} else {
		if err := fd.interFrameModeInfo(); err != nil {
			return err
		}
	}
	if fd.paletteSizeY > 0 || fd.paletteSizeUV > 0 {
		fd.paletteTokens()
	}

	// Update neighbor state over the block's 4x4 footprint (before residual so MC
	// reads the block's own motion vectors via candRow/candCol).
	for y := 0; y < bh4 && r+y < fd.miRows; y++ {
		for x := 0; x < bw4 && c+x < fd.miCols; x++ {
			fd.yModes[r+y][c+x] = fd.yMode
			fd.uvModes[r+y][c+x] = fd.uvMode
			fd.skips[r+y][c+x] = fd.skip
			if fd.deltaLFGrid != nil {
				fd.deltaLFGrid[r+y][c+x] = [4]int16{int16(fd.currentDeltaLF[0]), int16(fd.currentDeltaLF[1]), int16(fd.currentDeltaLF[2]), int16(fd.currentDeltaLF[3])}
			}
			if fd.segmentIds != nil {
				fd.segmentIds[r+y][c+x] = fd.segmentId
			}
			fd.miSizes[r+y][c+x] = bSize
			fd.paletteSizesGrid[0][r+y][c+x] = fd.paletteSizeY
			fd.paletteSizesGrid[1][r+y][c+x] = fd.paletteSizeUV
			if fd.paletteSizeY > 0 {
				fd.paletteColorsGrid[0][r+y][c+x] = append([]int(nil), fd.paletteColorsY[:fd.paletteSizeY]...)
			}
			if fd.paletteSizeUV > 0 {
				fd.paletteColorsGrid[1][r+y][c+x] = append([]int(nil), fd.paletteColorsU[:fd.paletteSizeUV]...)
			}
			if fd.gridRefFrames != nil {
				fd.gridRefFrames[r+y][c+x] = fd.refFrame
				fd.gridMvs[r+y][c+x] = fd.mv
				fd.miWrittenGrid[r+y][c+x] = true
				fd.isInters[r+y][c+x] = fd.isInterFlag
				fd.skipModes[r+y][c+x] = b2i(fd.skipModeFlag)
				fd.mvs[r+y][c+x] = fd.mv[0]
				fd.refFrames0[r+y][c+x] = fd.refFrame[0]
				fd.compGroupIdxs[r+y][c+x] = fd.compGroupIdxVal
				fd.compoundIdxs[r+y][c+x] = fd.compoundIdxVal
				fd.gridInterpFilters[r+y][c+x] = fd.interpFilter
			}
		}
	}

	if err := fd.readBlockTxSize(); err != nil {
		return err
	}
	if fd.isInterFlag {
		if err := fd.computePrediction(); err != nil {
			return err
		}
		if fd.motionMode == motionModeObmc {
			if err := fd.overlappedMotionComp(); err != nil {
				return err
			}
		}
	}
	if err := fd.residual(); err != nil {
		return err
	}
	return nil
}

func (fd *frameDecoder) intraFrameModeInfo() error {
	fd.skip = 0
	fd.segmentId = 0
	if fd.fh.SegIdPreSkip == 1 {
		fd.intraSegmentId()
	}
	fd.readSkip()
	if fd.fh.SegIdPreSkip == 0 {
		fd.intraSegmentId()
	}

	fd.readCdef()
	fd.readDeltaQIndex()
	if err := fd.readDeltaLF(); err != nil {
		return err
	}
	fd.readDeltas = false
	fd.useIntrabc = false
	if fd.fh.AllowIntrabc {
		fd.useIntrabc = fd.d.DecodeSymbol(fd.c.intrabc) == 1
		fd.tr("use_intrabc=%d", b2i(fd.useIntrabc))
		if fd.useIntrabc {
			return fd.intrabcModeInfo()
		}
	}

	// intra_frame_y_mode.
	am := IntraModeContext[DCPred]
	lm := IntraModeContext[DCPred]
	if fd.availU {
		am = IntraModeContext[fd.yModes[fd.miRow-1][fd.miCol]]
	}
	if fd.availL {
		lm = IntraModeContext[fd.yModes[fd.miRow][fd.miCol-1]]
	}
	fd.yMode = fd.d.DecodeSymbol(fd.c.intraFrameYMode[am][lm])
	fd.tr("y_mode(a%d,l%d)=%d", am, lm, fd.yMode)
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
		fd.tr("uv_mode(y%d)=%d", fd.yMode, fd.uvMode)
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

// readCdef reads the CDEF index for the 64x64 region of this block (AV1 spec §5.11.56).
func (fd *frameDecoder) readCdef() {
	if fd.skip != 0 || fd.fh.CodedLossless || !fd.seq.EnableCDEF || fd.fh.AllowIntrabc {
		return
	}
	const cdefSize4 = 16
	const cdefMask4 = ^(cdefSize4 - 1)
	r := fd.miRow & cdefMask4
	c := fd.miCol & cdefMask4
	if fd.cdefIdx[r][c] == -1 {
		idx := int(fd.d.ReadLiteral(fd.fh.CdefBits))
		w4 := predict.Num4x4BlocksWide[fd.miSize]
		h4 := predict.Num4x4BlocksHigh[fd.miSize]
		for i := r; i < r+h4 && i < fd.miRows; i += cdefSize4 {
			for j := c; j < c+w4 && j < fd.miCols; j += cdefSize4 {
				fd.cdefIdx[i][j] = idx
			}
		}
	}
}

// readCflAlphas reads the chroma-from-luma alphas (AV1 spec §5.11.45).
func (fd *frameDecoder) readCflAlphas() {
	signs := fd.d.DecodeSymbol(fd.c.cflSign)
	signU := (signs + 1) / 3
	signV := (signs + 1) % 3
	fd.cflAlphaU, fd.cflAlphaV = 0, 0
	if signU != 0 { // CFL_SIGN_ZERO
		fd.cflAlphaU = 1 + fd.d.DecodeSymbol(fd.c.cflAlpha[signs-2])
		if signU == 1 { // CFL_SIGN_NEG
			fd.cflAlphaU = -fd.cflAlphaU
		}
	}
	if signV != 0 {
		fd.cflAlphaV = 1 + fd.d.DecodeSymbol(fd.c.cflAlpha[(signV-1)*3+signU])
		if signV == 1 {
			fd.cflAlphaV = -fd.cflAlphaV
		}
	}
}

// intraAngleInfo reads angle_delta for a directional mode (AV1 spec §5.11.43).
func (fd *frameDecoder) intraAngleInfo(mode int) int {
	if fd.miSize >= predict.Block8x8 && isDirectional(mode) {
		sym := fd.d.DecodeSymbol(fd.c.angleDelta[mode-VPred])
		return sym - 3 // MAX_ANGLE_DELTA
	}
	return 0
}

// filterIntraModeInfo reads use_filter_intra (AV1 spec §5.11.45). The recursive
// filter-intra predictor is not yet implemented; the flag is usually 0.
func (fd *frameDecoder) filterIntraModeInfo() {
	fd.useFilterIntra = false
	fd.filterIntraMode = 0
	if fd.seq.EnableFilterIntra && fd.yMode == DCPred && fd.paletteSizeY == 0 &&
		maxi(predict.BlockWidth(fd.miSize), predict.BlockHeight(fd.miSize)) <= 32 {
		fd.useFilterIntra = fd.d.DecodeSymbol(fd.c.filterIntra[fd.miSize]) == 1
		if fd.useFilterIntra {
			fd.filterIntraMode = fd.d.DecodeSymbol(fd.c.filterIntraMode)
		}
	}
}

func (fd *frameDecoder) readSkip() {
	// SEG_LVL_SKIP (when read before the segment id) forces skip (AV1 spec §5.11.11).
	if fd.fh.SegIdPreSkip == 1 && fd.segFeatureActive(header.SegLvlSkip) {
		fd.skip = 1
		return
	}
	ctx := 0
	if fd.availU {
		ctx += fd.skips[fd.miRow-1][fd.miCol]
	}
	if fd.availL {
		ctx += fd.skips[fd.miRow][fd.miCol-1]
	}
	fd.skip = fd.d.DecodeSymbol(fd.c.skip[ctx])
	fd.tr("skip(ctx%d)=%d", ctx, fd.skip)
}

// lossless reports whether the current block's segment is coded losslessly
// (AV1 spec Lossless = LosslessArray[segment_id]). This is PER-SEGMENT — distinct
// from the frame-level CodedLossless (the AND over all segments). With base_q_idx=0
// and segmentation a block may be lossless while the frame is not (and vice versa),
// which changes its transform size (forced TX_4X4), transform type (DCT_DCT) and the
// inverse transform (Walsh–Hadamard). Frame-level decisions (CDEF, loop filter, loop
// restoration) keep using CodedLossless.
func (fd *frameDecoder) lossless() bool {
	if fd.segmentId < 0 || fd.segmentId >= header.MaxSegments {
		return fd.fh.CodedLossless
	}
	return fd.fh.LosslessArray[fd.segmentId]
}

// readBlockTxSize reads the transform size for an intra block (AV1 spec §5.11.15).
func (fd *frameDecoder) readBlockTxSize() error {
	// Inter blocks with a coded residual use the var-tx transform tree.
	if fd.fh.TxMode == header.TxModeSelect && fd.miSize > predict.Block4x4 &&
		fd.isInterFlag && fd.skip == 0 && !fd.lossless() {
		maxTxSz := MaxTxSizeRect[fd.miSize]
		txW4 := TxWidth[maxTxSz] >> 2
		txH4 := TxHeight[maxTxSz] >> 2
		bw4 := predict.Num4x4BlocksWide[fd.miSize]
		bh4 := predict.Num4x4BlocksHigh[fd.miSize]
		for row := fd.miRow; row < fd.miRow+bh4; row += txH4 {
			for col := fd.miCol; col < fd.miCol+bw4; col += txW4 {
				fd.readVarTxSize(row, col, maxTxSz, 0)
			}
		}
		return nil
	}
	allowSelect := fd.skip == 0 || !fd.isInterFlag
	if fd.lossless() {
		fd.txSize = TX4x4
	} else {
		maxRectTxSize := MaxTxSizeRect[fd.miSize]
		fd.txSize = maxRectTxSize
		if allowSelect && fd.miSize > predict.Block4x4 && fd.fh.TxMode == header.TxModeSelect {
			ctx := fd.txDepthCtx(maxRectTxSize)
			var cdf []uint16
			switch MaxTxDepth[fd.miSize] {
			case 4:
				cdf = fd.c.tx64x64[ctx]
			case 3:
				cdf = fd.c.tx32x32[ctx]
			case 2:
				cdf = fd.c.tx16x16[ctx]
			default:
				cdf = fd.c.tx8x8[ctx]
			}
			txDepth := fd.d.DecodeSymbol(cdf)
			fd.tr("tx_depth(ctx%d)=%d", ctx, txDepth)
			for i := 0; i < txDepth; i++ {
				fd.txSize = SplitTxSize[fd.txSize]
			}
		}
	}
	// Record this block's transform size over its 4x4 footprint (InterTxSizes).
	bw4 := predict.Num4x4BlocksWide[fd.miSize]
	bh4 := predict.Num4x4BlocksHigh[fd.miSize]
	for y := 0; y < bh4 && fd.miRow+y < fd.miRows; y++ {
		for x := 0; x < bw4 && fd.miCol+x < fd.miCols; x++ {
			fd.txSizes[fd.miRow+y][fd.miCol+x] = fd.txSize
		}
	}
	return nil
}

// txDepthCtx computes the tx_depth context from neighbor transform sizes (AV1 spec §8.3).
func (fd *frameDecoder) txDepthCtx(maxRectTxSize int) int {
	maxTxWidth := TxWidth[maxRectTxSize]
	maxTxHeight := TxHeight[maxRectTxSize]
	aboveW := 0
	if fd.availU && fd.isInterAt(fd.miRow-1, fd.miCol) {
		aboveW = predict.BlockWidth(fd.miSizes[fd.miRow-1][fd.miCol])
	} else if fd.availU {
		aboveW = fd.getAboveTxWidth(fd.miRow, fd.miCol)
	}
	leftH := 0
	if fd.availL && fd.isInterAt(fd.miRow, fd.miCol-1) {
		leftH = predict.BlockHeight(fd.miSizes[fd.miRow][fd.miCol-1])
	} else if fd.availL {
		leftH = fd.getLeftTxHeight(fd.miRow, fd.miCol)
	}
	ctx := 0
	if aboveW >= maxTxWidth {
		ctx++
	}
	if leftH >= maxTxHeight {
		ctx++
	}
	return ctx
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// intrabcModeInfo handles an intra block-copy block (AV1 spec §5.11.34, use_intrabc
// path): the block is coded inter-style with a block vector that copies from an
// already-decoded region of the current frame.
func (fd *frameDecoder) intrabcModeInfo() error {
	fd.isInterFlag = true
	fd.skipModeFlag = false
	fd.yMode = DCPred
	fd.uvMode = DCPred
	fd.angleDeltaY = 0
	fd.angleDeltaUV = 0
	fd.useFilterIntra = false
	fd.cflAlphaU, fd.cflAlphaV = 0, 0
	fd.motionMode = motionModeSimple
	fd.compoundType = compoundAverage
	fd.compGroupIdxVal = 0
	fd.compoundIdxVal = 1
	fd.interpFilter = [2]int{filterBilinear, filterBilinear}
	fd.refFrame = [2]int{IntraFrame, header.NoneFrame}
	fd.refMvIdx = 0
	fd.findMvStack(false)
	return fd.assignMv(false)
}

// cflAllowed implements is_cfl_allowed (AV1 spec §5.11.45). For lossless blocks
// CfL is allowed only when the chroma residual size is BLOCK_4X4; otherwise it is
// allowed for blocks up to 32x32.
func (fd *frameDecoder) cflAllowed() bool {
	if fd.lossless() {
		return fd.getPlaneResidualSize(fd.miSize, 1) == predict.Block4x4
	}
	return predict.BlockWidth(fd.miSize) <= 32 && predict.BlockHeight(fd.miSize) <= 32
}
