package decode

import (
	"github.com/mgvs/go-av1/predict"
	"github.com/mgvs/go-av1/transform"
)

// residual decodes the transform blocks of the current block (AV1 spec §5.11.34).
// For intra, prediction and reconstruction happen per transform block. Only the
// all-zero (no coefficient) case is handled; a coded coefficient returns
// ErrUnsupported (milestone M5).
func (fd *frameDecoder) residual() error {
	widthChunks := max1(predict.BlockWidth(fd.miSize) >> 6)
	heightChunks := max1(predict.BlockHeight(fd.miSize) >> 6)
	miSizeChunk := fd.miSize
	if widthChunks > 1 || heightChunks > 1 {
		miSizeChunk = predict.Block64x64
	}

	for chunkY := 0; chunkY < heightChunks; chunkY++ {
		for chunkX := 0; chunkX < widthChunks; chunkX++ {
			miColChunk := fd.miCol + (chunkX << 4)
			miRowChunk := fd.miRow + (chunkY << 4)
			nPlanes := 1
			if fd.hasChroma {
				nPlanes = 3
			}
			for plane := 0; plane < nPlanes; plane++ {
				txSz := fd.getTxSize(plane, fd.txSize)
				stepX := TxWidth[txSz] >> 2
				stepY := TxHeight[txSz] >> 2
				planeSz := fd.getPlaneResidualSize(miSizeChunk, plane)
				num4x4W := predict.Num4x4BlocksWide[planeSz]
				num4x4H := predict.Num4x4BlocksHigh[planeSz]
				subX, subY := 0, 0
				if plane > 0 {
					subX, subY = fd.subX, fd.subY
				}
				baseX := (miColChunk >> subX) * 4
				baseY := (miRowChunk >> subY) * 4
				if fd.isInterFlag && !fd.fh.CodedLossless && plane == 0 {
					if err := fd.transformTree(baseX, baseY, num4x4W*4, num4x4H*4); err != nil {
						return err
					}
					continue
				}
				baseXBlock := (fd.miCol >> subX) * 4
				baseYBlock := (fd.miRow >> subY) * 4
				for y := 0; y < num4x4H; y += stepY {
					for x := 0; x < num4x4W; x += stepX {
						if err := fd.transformBlock(plane, baseXBlock, baseYBlock, txSz,
							x+((chunkX<<4)>>subX), y+((chunkY<<4)>>subY)); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func (fd *frameDecoder) transformBlock(plane, baseX, baseY, txSz, x, y int) error {
	startX := baseX + 4*x
	startY := baseY + 4*y
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	maxX := (fd.miCols * 4) >> subX
	maxY := (fd.miRows * 4) >> subY
	if startX >= maxX || startY >= maxY {
		return nil
	}
	// Record the transform size for deblocking (LoopfilterTxSizes), per plane-MI 4x4.
	for py := startY >> 2; py <= (startY+TxHeight[txSz]-1)>>2 && py < len(fd.lfTxSizes[plane]); py++ {
		for px := startX >> 2; px <= (startX+TxWidth[txSz]-1)>>2 && px < len(fd.lfTxSizes[plane][0]); px++ {
			fd.lfTxSizes[plane][py][px] = txSz
		}
	}

	// Inter blocks are predicted once per block by motion compensation before the
	// residual; here we only add the coded residual to the existing samples.
	if fd.isInterFlag {
		return fd.transformBlockResidual(plane, startX, startY, txSz)
	}

	// Palette-coded planes map color indices to palette colors (AV1 spec §7.11.4),
	// in place of directional intra prediction; the coded residual is added below.
	isPalette := (plane == 0 && fd.paletteSizeY > 0) || (plane > 0 && fd.paletteSizeUV > 0)
	if isPalette {
		fd.predictPalette(plane, startX, startY, baseX, baseY, txSz)
		if plane == 0 {
			fd.maxLumaW = startX + TxWidth[txSz]
			fd.maxLumaH = startY + TxHeight[txSz]
		}
	}

	// Intra prediction for this transform block.
	mode := fd.yMode
	isCfl := false
	if plane > 0 {
		if fd.uvMode == UVCflPred {
			mode = DCPred
			isCfl = true
		} else {
			mode = fd.uvMode
		}
	}
	haveLeft := x > 0
	haveAbove := y > 0
	if plane == 0 {
		haveLeft = haveLeft || fd.availL
		haveAbove = haveAbove || fd.availU
	} else {
		haveLeft = haveLeft || fd.availLChroma
		haveAbove = haveAbove || fd.availUChroma
	}

	// Above-right / below-left availability from the BlockDecoded grid.
	row := (startY << uint(subY)) >> 2
	col := (startX << uint(subX)) >> 2
	sbMask := 15
	if fd.seq.Use128x128Superblock {
		sbMask = 31
	}
	subBlockMiRow := row & sbMask
	subBlockMiCol := col & sbMask
	stepX := TxWidth[txSz] >> 2
	stepY := TxHeight[txSz] >> 2
	haveAboveRight := fd.bdGet(plane, (subBlockMiRow>>uint(subY))-1, (subBlockMiCol>>uint(subX))+stepX) == 1
	haveBelowLeft := fd.bdGet(plane, (subBlockMiRow>>uint(subY))+stepY, (subBlockMiCol>>uint(subX))-1) == 1

	angleDelta := fd.angleDeltaY
	if plane > 0 {
		angleDelta = fd.angleDeltaUV
	}
	useFilterIntra := plane == 0 && fd.useFilterIntra
	if !isPalette {
		if err := fd.planes[plane].PredictIntra(startX, startY, TxWidthLog2[txSz], TxHeightLog2[txSz],
			haveLeft, haveAbove, haveAboveRight, haveBelowLeft, mode, fd.bitDepth, maxX-1, maxY-1,
			angleDelta, fd.getFilterType(plane), fd.seq.EnableIntraEdgeFilter, useFilterIntra, fd.filterIntraMode); err != nil {
			return ErrUnsupported{err.Error()}
		}
		if plane == 0 {
			fd.maxLumaW = startX + TxWidth[txSz]
			fd.maxLumaH = startY + TxHeight[txSz]
		}
		if isCfl {
			fd.predictChromaFromLuma(plane, startX, startY, txSz)
		}
	}

	// Coefficients: read all_zero. all_zero == 1 means the residual is zero and the
	// reconstruction equals the prediction.
	x4 := startX >> 2
	y4 := startY >> 2
	w4 := TxWidth[txSz] >> 2
	h4 := TxHeight[txSz] >> 2
	culLevel, dcCategory := 0, 0
	if fd.skip == 0 && fd.readAllZero(plane, txSz, x4, y4) == 0 {
		dequant, txType, cl, dc, err := fd.decodeCoeffs(plane, txSz, x4, y4)
		if err != nil {
			return err
		}
		culLevel, dcCategory = cl, dc
		var res []int32
		var w, h int
		if fd.fh.CodedLossless {
			res, w, h, err = transform.InverseWHT2D(dequant, fd.bitDepth)
		} else {
			res, w, h, err = transform.Inverse2D(txSz, txType, dequant, fd.bitDepth)
		}
		if err != nil {
			return err
		}
		// FLIPADST flips the residual when adding it to the prediction (AV1 spec §7.12.3).
		flipUD := txType == FlipadstDct || txType == FlipadstAdst || txType == VFlipadst || txType == FlipadstFlipadst
		flipLR := txType == DctFlipadst || txType == AdstFlipadst || txType == HFlipadst || txType == FlipadstFlipadst
		hi := (1 << uint(fd.bitDepth)) - 1
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				xx, yy := j, i
				if flipLR {
					xx = w - 1 - j
				}
				if flipUD {
					yy = h - 1 - i
				}
				if startX+xx >= fd.planes[plane].AllocW || startY+yy >= fd.planes[plane].AllocH {
					continue
				}
				v := int(fd.planes[plane].At(startX+xx, startY+yy)) + int(res[i*w+j])
				if v < 0 {
					v = 0
				} else if v > hi {
					v = hi
				}
				fd.planes[plane].Set(startX+xx, startY+yy, uint16(v))
			}
		}
	}

	// Update the level/DC contexts over this transform block (AV1 spec §5.11.39).
	for i := 0; i < w4; i++ {
		fd.aboveLevelContext[plane][x4+i] = culLevel
		fd.aboveDcContext[plane][x4+i] = dcCategory
	}
	for i := 0; i < h4; i++ {
		fd.leftLevelContext[plane][y4+i] = culLevel
		fd.leftDcContext[plane][y4+i] = dcCategory
	}

	// Mark this transform block's 4x4 positions as decoded.
	for i := 0; i < stepY; i++ {
		for j := 0; j < stepX; j++ {
			fd.bdSet(plane, (subBlockMiRow>>uint(subY))+i, (subBlockMiCol>>uint(subX))+j, 1)
		}
	}
	return nil
}

// transformBlockResidual decodes and adds the coded residual for an inter
// transform block (the motion-compensated prediction is already in the plane).
func (fd *frameDecoder) transformBlockResidual(plane, startX, startY, txSz int) error {
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	x4 := startX >> 2
	y4 := startY >> 2
	w4 := TxWidth[txSz] >> 2
	h4 := TxHeight[txSz] >> 2
	sbMask := 15
	if fd.seq.Use128x128Superblock {
		sbMask = 31
	}
	subBlockMiRow := ((startY << uint(subY)) >> 2) & sbMask
	subBlockMiCol := ((startX << uint(subX)) >> 2) & sbMask
	stepX := TxWidth[txSz] >> 2
	stepY := TxHeight[txSz] >> 2

	culLevel, dcCategory := 0, 0
	if fd.skip == 0 && fd.readAllZero(plane, txSz, x4, y4) == 0 {
		dequant, txType, cl, dc, err := fd.decodeCoeffs(plane, txSz, x4, y4)
		if err != nil {
			return err
		}
		culLevel, dcCategory = cl, dc
		var res []int32
		var w, h int
		if fd.fh.CodedLossless {
			res, w, h, err = transform.InverseWHT2D(dequant, fd.bitDepth)
		} else {
			res, w, h, err = transform.Inverse2D(txSz, txType, dequant, fd.bitDepth)
		}
		if err != nil {
			return err
		}
		flipUD := txType == FlipadstDct || txType == FlipadstAdst || txType == VFlipadst || txType == FlipadstFlipadst
		flipLR := txType == DctFlipadst || txType == AdstFlipadst || txType == HFlipadst || txType == FlipadstFlipadst
		hi := (1 << uint(fd.bitDepth)) - 1
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				xx, yy := j, i
				if flipLR {
					xx = w - 1 - j
				}
				if flipUD {
					yy = h - 1 - i
				}
				if startX+xx >= fd.planes[plane].AllocW || startY+yy >= fd.planes[plane].AllocH {
					continue
				}
				v := int(fd.planes[plane].At(startX+xx, startY+yy)) + int(res[i*w+j])
				if v < 0 {
					v = 0
				} else if v > hi {
					v = hi
				}
				fd.planes[plane].Set(startX+xx, startY+yy, uint16(v))
			}
		}
	}
	for i := 0; i < w4; i++ {
		fd.aboveLevelContext[plane][x4+i] = culLevel
		fd.aboveDcContext[plane][x4+i] = dcCategory
	}
	for i := 0; i < h4; i++ {
		fd.leftLevelContext[plane][y4+i] = culLevel
		fd.leftDcContext[plane][y4+i] = dcCategory
	}
	for i := 0; i < stepY; i++ {
		for j := 0; j < stepX; j++ {
			fd.bdSet(plane, (subBlockMiRow>>uint(subY))+i, (subBlockMiCol>>uint(subX))+j, 1)
		}
	}
	return nil
}

// readAllZero decodes the all_zero (txb_skip) flag for a transform block
// (AV1 spec §5.11.39 / §8.3), deriving the context from the neighbor level contexts.
func (fd *frameDecoder) readAllZero(plane, txSz, x4, y4 int) int {
	txSzCtx := (TxSizeSqr[txSz] + TxSizeSqrUp[txSz] + 1) >> 1
	w := TxWidth[txSz]
	h := TxHeight[txSz]
	w4 := w >> 2
	h4 := h >> 2
	bsize := fd.getPlaneResidualSize(fd.miSize, plane)
	bw := predict.BlockWidth(bsize)
	bh := predict.BlockHeight(bsize)
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	maxX4 := fd.miCols >> subX
	maxY4 := fd.miRows >> subY

	var ctx int
	if plane == 0 {
		top, left := 0, 0
		for k := 0; k < w4; k++ {
			if x4+k < maxX4 {
				top = maxi(top, fd.aboveLevelContext[plane][x4+k])
			}
		}
		for k := 0; k < h4; k++ {
			if y4+k < maxY4 {
				left = maxi(left, fd.leftLevelContext[plane][y4+k])
			}
		}
		top = mini(top, 255)
		left = mini(left, 255)
		switch {
		case bw == w && bh == h:
			ctx = 0
		case top == 0 && left == 0:
			ctx = 1
		case top == 0 || left == 0:
			ctx = 2
			if maxi(top, left) > 3 {
				ctx = 3
			}
		case maxi(top, left) <= 3:
			ctx = 4
		case mini(top, left) <= 3:
			ctx = 5
		default:
			ctx = 6
		}
	} else {
		above, left := 0, 0
		for k := 0; k < w4; k++ {
			if x4+k < maxX4 {
				above |= fd.aboveLevelContext[plane][x4+k]
				above |= fd.aboveDcContext[plane][x4+k]
			}
		}
		for k := 0; k < h4; k++ {
			if y4+k < maxY4 {
				left |= fd.leftLevelContext[plane][y4+k]
				left |= fd.leftDcContext[plane][y4+k]
			}
		}
		ctx = b2i(above != 0) + b2i(left != 0) + 7
		if bw*bh > w*h {
			ctx += 3
		}
	}
	v := fd.d.DecodeSymbol(fd.c.txbSkip[txSzCtx][ctx])
	fd.tr("all_zero(p%d,txSzCtx%d,ctx%d)=%d", plane, txSzCtx, ctx, v)
	return v
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// predictChromaFromLuma applies the chroma-from-luma prediction on top of the DC
// chroma prediction (AV1 spec §7.11.5).
func (fd *frameDecoder) predictChromaFromLuma(plane, startX, startY, txSz int) {
	w := TxWidth[txSz]
	h := TxHeight[txSz]
	subX, subY := fd.subX, fd.subY
	alpha := fd.cflAlphaU
	if plane == 2 {
		alpha = fd.cflAlphaV
	}
	luma := fd.planes[0]
	// MaxLumaW/H is the luma transform edge (spec §7.11.5), but the luma plane is
	// only reconstructed to the mi grid; clamp so beyond-grid CfL samples replicate
	// the last reconstructed luma column/row (matching dav1d) instead of reading OOB.
	maxLW := fd.maxLumaW
	if maxLW > luma.AllocW {
		maxLW = luma.AllocW
	}
	maxLH := fd.maxLumaH
	if maxLH > luma.AllocH {
		maxLH = luma.AllocH
	}
	lumaL := make([][]int, h)
	lumaAvg := 0
	for i := 0; i < h; i++ {
		lumaL[i] = make([]int, w)
		lumaY := (startY + i) << uint(subY)
		if m := maxLH - (1 << uint(subY)); lumaY > m {
			lumaY = m
		}
		for j := 0; j < w; j++ {
			lumaX := (startX + j) << uint(subX)
			if m := maxLW - (1 << uint(subX)); lumaX > m {
				lumaX = m
			}
			t := 0
			for dy := 0; dy <= subY; dy++ {
				for dx := 0; dx <= subX; dx++ {
					t += int(luma.At(lumaX+dx, lumaY+dy))
				}
			}
			v := t << uint(3-subX-subY)
			lumaL[i][j] = v
			lumaAvg += v
		}
	}
	lumaAvg = round2(lumaAvg, TxWidthLog2[txSz]+TxHeightLog2[txSz])
	hi := (1 << uint(fd.bitDepth)) - 1
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			if startX+j >= fd.planes[plane].AllocW || startY+i >= fd.planes[plane].AllocH {
				continue
			}
			dc := int(fd.planes[plane].At(startX+j, startY+i))
			v := dc + round2signed(alpha*(lumaL[i][j]-lumaAvg), 6)
			if v < 0 {
				v = 0
			} else if v > hi {
				v = hi
			}
			fd.planes[plane].Set(startX+j, startY+i, uint16(v))
		}
	}
}

func round2(x, n int) int { return (x + (1 << uint(n-1))) >> uint(n) }

func round2signed(x, n int) int {
	if x >= 0 {
		return round2(x, n)
	}
	return -round2(-x, n)
}

func (fd *frameDecoder) getTxSize(plane, txSz int) int {
	if fd.fh.CodedLossless {
		return TX4x4
	}
	if plane == 0 {
		return txSz
	}
	uvTx := MaxTxSizeRect[fd.getPlaneResidualSize(fd.miSize, plane)]
	if TxWidth[uvTx] == 64 || TxHeight[uvTx] == 64 {
		switch {
		case TxWidth[uvTx] == 16:
			return TX16x32
		case TxHeight[uvTx] == 16:
			return TX32x16
		default:
			return TX32x32
		}
	}
	return uvTx
}

func (fd *frameDecoder) getPlaneResidualSize(subsize, plane int) int {
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	return SubsampledSize[subsize][subX][subY]
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}
