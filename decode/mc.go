package decode

import (
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
)

// computePrediction performs motion compensation for an inter block over all
// planes (AV1 spec §7.11.3, compute_prediction). For sub-8x8 blocks whose
// co-located luma sub-blocks include an intra block (someUseIntra), chroma is
// predicted per sub-block using the block's own motion vectors.
func (fd *frameDecoder) computePrediction() error {
	// Local-warp parameters are estimated once (luma), reused for chroma
	// (AV1 spec §7.11.3.1 steps 2-3).
	if fd.motionMode == motionModeWarp {
		fd.warpEstimation()
		if fd.localValid {
			ok, _, _, _, _ := setupShear(fd.localWarpParams)
			fd.localValid = ok
		}
	}
	for plane := 0; plane < fd.numPlanes; plane++ {
		if plane > 0 && !fd.hasChroma {
			break
		}
		subX, subY := 0, 0
		if plane > 0 {
			subX, subY = fd.subX, fd.subY
		}
		planeSz := SubsampledSize[fd.miSize][subX][subY]
		num4x4W := predict.Num4x4BlocksWide[planeSz]
		num4x4H := predict.Num4x4BlocksHigh[planeSz]
		baseX := (fd.miCol >> uint(subX)) * 4
		baseY := (fd.miRow >> uint(subY)) * 4
		candRow := (fd.miRow >> uint(subY)) << uint(subY)
		candCol := (fd.miCol >> uint(subX)) << uint(subX)

		predW := predict.BlockWidth(fd.miSize) >> uint(subX)
		predH := predict.BlockHeight(fd.miSize) >> uint(subY)
		someUseIntra := false
		for r := 0; r < (num4x4H<<uint(subY)) && candRow+r < fd.miRows; r++ {
			for c := 0; c < (num4x4W<<uint(subX)) && candCol+c < fd.miCols; c++ {
				if fd.gridRefFrames[candRow+r][candCol+c][0] == IntraFrame {
					someUseIntra = true
				}
			}
		}
		if someUseIntra {
			predW = num4x4W * 4
			predH = num4x4H * 4
			candRow = fd.miRow
			candCol = fd.miCol
		}
		r := 0
		for y := 0; y < num4x4H*4; y += predH {
			c := 0
			for x := 0; x < num4x4W*4; x += predW {
				if err := fd.predictInterBlock(plane, baseX+x, baseY+y, predW, predH, candRow+r, candCol+c); err != nil {
					return err
				}
				c++
			}
			r++
		}
	}
	return nil
}

// predictInterBlock predicts one (sub-)region from one or two reference lists and
// writes it to the plane (AV1 spec §7.11.3.1 predict_inter).
func (fd *frameDecoder) predictInterBlock(plane, x, y, w, h, candRow, candCol int) error {
	isCompound := fd.gridRefFrames[candRow][candCol][1] > IntraFrame
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	preds := make([][][]int, 2)
	for refList := 0; refList <= b2i(isCompound); refList++ {
		var p [][]int
		var err error
		if useWarp := fd.useWarp(w, h, candRow, candCol, refList); useWarp != 0 {
			p, err = fd.predictWarp(useWarp, plane, x, y, w, h, candRow, candCol, refList, isCompound)
		} else {
			p, err = fd.predictInterRefList(plane, x, y, w, h, candRow, candCol, refList, isCompound)
		}
		if err != nil {
			return err
		}
		preds[refList] = p
	}
	hi := (1 << uint(fd.bitDepth)) - 1
	if !isCompound {
		if fd.isInterIntra {
			return fd.blendInterIntra(plane, x, y, w, h, preds[0])
		}
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				fd.planes[plane].SetClip(x+j, y+i, uint16(clip3i(0, hi, preds[0][i][j])))
			}
		}
		return nil
	}
	interPostRound := 4 // 2*FILTER_BITS - (InterRound0 + InterRound1) for 8-bit compound
	if fd.bitDepth == 12 {
		interPostRound = 2
	}
	switch fd.compoundType {
	case compoundAverage:
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				v := round2(preds[0][i][j]+preds[1][i][j], 1+interPostRound)
				fd.planes[plane].SetClip(x+j, y+i, uint16(clip3i(0, hi, v)))
			}
		}
	case compoundWedge, compoundDiffwtd:
		// The mask is built once at luma resolution (plane 0); chroma planes
		// reuse it with subsampling (AV1 spec §7.11.3.14, mask blend process).
		if plane == 0 {
			if fd.compoundType == compoundWedge {
				fd.buildWedgeMask(w, h)
			} else {
				fd.buildDiffwtdMask(preds, w, h)
			}
		}
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				var m int
				switch {
				case subX == 0 && subY == 0:
					m = fd.wedgeMask[i][j]
				case subX != 0 && subY == 0:
					m = round2(fd.wedgeMask[i][2*j]+fd.wedgeMask[i][2*j+1], 1)
				default:
					m = round2(fd.wedgeMask[2*i][2*j]+fd.wedgeMask[2*i][2*j+1]+
						fd.wedgeMask[2*i+1][2*j]+fd.wedgeMask[2*i+1][2*j+1], 2)
				}
				v := round2(m*preds[0][i][j]+(64-m)*preds[1][i][j], 6+interPostRound)
				fd.planes[plane].SetClip(x+j, y+i, uint16(clip3i(0, hi, v)))
			}
		}
	case compoundDistance:
		fwd, bck := fd.distanceWeights(candRow, candCol)
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				v := round2(fwd*preds[0][i][j]+bck*preds[1][i][j], 4+interPostRound)
				fd.planes[plane].SetClip(x+j, y+i, uint16(clip3i(0, hi, v)))
			}
		}
	default:
		return ErrUnsupported{"compound type"}
	}
	return nil
}

// predictInterRefList forms the inter prediction for one reference list, returning
// the w×h array of intermediate (InterRound1-rounded) samples (AV1 spec §7.11.3).
// No reference scaling or warp.
func (fd *frameDecoder) predictInterRefList(plane, x, y, w, h, candRow, candCol, refList int, isCompound bool) ([][]int, error) {
	refFrame := fd.gridRefFrames[candRow][candCol][refList]
	mv := fd.gridMvs[candRow][candCol][refList]
	var refPlane *predict.Plane
	var refWidthLuma, refHeightLuma int
	if refFrame == IntraFrame {
		// Intra block copy: predict from the current frame. The scale must be 1:1,
		// so the reference dimensions are the frame's (upscaled) width/height — NOT
		// the MI-aligned plane size, which would produce xScale != 1<<14 and drift
		// the sample positions (AV1 spec §7.11.3.2/§7.11.3.9; intrabc forbids
		// superres so FrameWidth == UpscaledWidth).
		refPlane = fd.planes[plane]
		refWidthLuma = fd.fh.FrameWidth
		refHeightLuma = fd.fh.FrameHeight
	} else {
		if refFrame < header.LastFrame {
			return nil, ErrUnsupported{"inter prediction: invalid reference frame (desync?)"}
		}
		refIdx := fd.fh.RefFrameIdx[refFrame-header.LastFrame]
		if fd.refs == nil || refIdx < 0 || refIdx >= len(fd.refs) || fd.refs[refIdx] == nil {
			return nil, ErrUnsupported{"inter prediction: missing reference frame"}
		}
		ref := fd.refs[refIdx]
		refPlane = ref.Planes[plane]
		refWidthLuma = ref.Width
		refHeightLuma = ref.Height
	}

	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	interRound0, interRound1 := 3, 11
	if isCompound {
		interRound1 = 7
	}
	if fd.bitDepth == 12 {
		interRound0 += 2
		if !isCompound {
			interRound1 -= 2
		}
	}
	// Clamp reference reads to the visible (upscaled) reference dimensions, not
	// the padded plane size (AV1 spec §7.11.3.2: lastX/lastY from RefUpscaledWidth).
	lastX := ((refWidthLuma + subX) >> uint(subX)) - 1
	lastY := ((refHeightLuma + subY) >> uint(subY)) - 1

	const refScaleShift, subpelBits, scaleSubpelBits = 14, 4, 10
	halfSample := 1 << (subpelBits - 1)
	origX := (x << subpelBits) + ((2 * mv.Col) >> uint(subX)) + halfSample
	origY := (y << subpelBits) + ((2 * mv.Row) >> uint(subY)) + halfSample
	xScale := ((refWidthLuma << refScaleShift) + fd.fh.FrameWidth/2) / fd.fh.FrameWidth
	yScale := ((refHeightLuma << refScaleShift) + fd.fh.FrameHeight/2) / fd.fh.FrameHeight
	baseX := origX*xScale - (halfSample << refScaleShift)
	baseY := origY*yScale - (halfSample << refScaleShift)
	off := (1 << (scaleSubpelBits - subpelBits)) / 2
	startX := round2signed(baseX, refScaleShift+subpelBits-scaleSubpelBits) + off
	startY := round2signed(baseY, refScaleShift+subpelBits-scaleSubpelBits) + off
	stepX := round2signed(xScale, refScaleShift-scaleSubpelBits)
	stepY := round2signed(yScale, refScaleShift-scaleSubpelBits)

	// The interpolation filter belongs to the predicted block (candRow/candCol):
	// for the block's own MC that is itself, but for OBMC it is the neighbour's.
	interp := fd.gridInterpFilters[candRow][candCol]
	filtH := adjustFilter(interp[1], w)
	filtV := adjustFilter(interp[0], h)
	intermediateHeight := (((h-1)*stepY + (1 << scaleSubpelBits) - 1) >> scaleSubpelBits) + 8
	intermediate := make([][]int, intermediateHeight)
	for r := 0; r < intermediateHeight; r++ {
		intermediate[r] = make([]int, w)
		sy := clip3i(0, lastY, (startY>>scaleSubpelBits)+r-3)
		for c := 0; c < w; c++ {
			p := startX + stepX*c
			s := 0
			for t := 0; t < 8; t++ {
				sx := clip3i(0, lastX, (p>>scaleSubpelBits)+t-3)
				s += subpelFilters[filtH][(p>>6)&15][t] * int(refPlane.At(sx, sy))
			}
			intermediate[r][c] = round2(s, interRound0)
		}
	}
	pred := make([][]int, h)
	for r := 0; r < h; r++ {
		pred[r] = make([]int, w)
		p := (startY & 1023) + stepY*r
		for c := 0; c < w; c++ {
			s := 0
			for t := 0; t < 8; t++ {
				s += subpelFilters[filtV][(p>>6)&15][t] * intermediate[(p>>scaleSubpelBits)+t][c]
			}
			pred[r][c] = round2(s, interRound1)
		}
	}
	return pred, nil
}

// OBMC blending masks (AV1 spec §7.11.3.9).
var (
	obmcMask2  = []int{45, 64}
	obmcMask4  = []int{39, 50, 59, 64}
	obmcMask8  = []int{36, 42, 48, 53, 57, 61, 64, 64}
	obmcMask16 = []int{34, 37, 40, 43, 46, 49, 52, 54, 56, 58, 60, 61, 64, 64, 64, 64}
	obmcMask32 = []int{33, 35, 36, 38, 40, 41, 43, 44, 45, 47, 48, 50, 51, 52, 53, 55,
		56, 57, 58, 59, 60, 60, 61, 62, 64, 64, 64, 64, 64, 64, 64, 64}
)

func getObmcMask(length int) []int {
	switch length {
	case 2:
		return obmcMask2
	case 4:
		return obmcMask4
	case 8:
		return obmcMask8
	case 16:
		return obmcMask16
	default:
		return obmcMask32
	}
}

// overlappedMotionComp blends the inter prediction with predictions from the
// above and left neighbors' motion vectors (AV1 spec §7.11.3.9 / §7.11.3.10).
func (fd *frameDecoder) overlappedMotionComp() error {
	for plane := 0; plane < fd.numPlanes; plane++ {
		if plane > 0 && !fd.hasChroma {
			break
		}
		subX, subY := 0, 0
		if plane > 0 {
			subX, subY = fd.subX, fd.subY
		}
		planeSz := SubsampledSize[fd.miSize][subX][subY]
		w := predict.BlockWidth(planeSz)
		h := predict.BlockHeight(planeSz)

		// Pass 0: blend predictions from the row above.
		if fd.availU && fd.getPlaneResidualSize(fd.miSize, plane) >= predict.Block8x8 {
			w4 := predict.Num4x4BlocksWide[fd.miSize]
			x4 := fd.miCol
			nCount, nLimit := 0, min(4, predict.MiWidthLog2[fd.miSize])
			for nCount < nLimit && x4 < min(fd.miCols, fd.miCol+w4) {
				candRow := fd.miRow - 1
				candCol := x4 | 1
				candSz := fd.miSizes[candRow][candCol]
				step4 := clip3int(2, 16, predict.Num4x4BlocksWide[candSz])
				if fd.gridRefFrames[candRow][candCol][0] > IntraFrame {
					nCount++
					predW := min(w, (step4*4)>>subX)
					predH := min(h>>1, 32>>subY)
					mask := getObmcMask(predH)
					if err := fd.predictOverlap(plane, candRow, candCol, x4, fd.miRow, predW, predH, 0, mask, subX, subY); err != nil {
						return err
					}
				}
				x4 += step4
			}
		}
		// Pass 1: blend predictions from the column to the left.
		if fd.availL {
			h4 := predict.Num4x4BlocksHigh[fd.miSize]
			y4 := fd.miRow
			nCount, nLimit := 0, min(4, predict.MiHeightLog2[fd.miSize])
			for nCount < nLimit && y4 < min(fd.miRows, fd.miRow+h4) {
				candCol := fd.miCol - 1
				candRow := y4 | 1
				candSz := fd.miSizes[candRow][candCol]
				step4 := clip3int(2, 16, predict.Num4x4BlocksHigh[candSz])
				if fd.gridRefFrames[candRow][candCol][0] > IntraFrame {
					nCount++
					predW := min(w>>1, 32>>subX)
					predH := min(h, (step4*4)>>subY)
					mask := getObmcMask(predW)
					if err := fd.predictOverlap(plane, candRow, candCol, fd.miCol, y4, predW, predH, 1, mask, subX, subY); err != nil {
						return err
					}
				}
				y4 += step4
			}
		}
	}
	return nil
}

// predictOverlap forms the neighbor prediction and blends it into CurrFrame
// (AV1 spec §7.11.3.9 step "predict_overlap" + §7.11.3.10 blending).
func (fd *frameDecoder) predictOverlap(plane, candRow, candCol, x4, y4, predW, predH, pass int, mask []int, subX, subY int) error {
	predX := (x4 * 4) >> subX
	predY := (y4 * 4) >> subY
	obmcPred, err := fd.predictInterRefList(plane, predX, predY, predW, predH, candRow, candCol, 0, false)
	if err != nil {
		return err
	}
	hi := (1 << uint(fd.bitDepth)) - 1
	pl := fd.planes[plane]
	for i := 0; i < predH && predY+i < pl.AllocH; i++ {
		for j := 0; j < predW && predX+j < pl.AllocW; j++ {
			op := clip3i(0, hi, obmcPred[i][j])
			// pass 0 blends from above (mask indexed by row, length predH);
			// pass 1 blends from left (mask indexed by column, length predW).
			var m int
			if pass == 0 {
				m = mask[i]
			} else {
				m = mask[j]
			}
			cur := int(pl.At(predX+j, predY+i))
			v := round2(m*cur+(64-m)*op, 6)
			pl.Set(predX+j, predY+i, uint16(clip3i(0, hi, v)))
		}
	}
	return nil
}

// adjustFilter substitutes the 4-tap filter variants for narrow blocks (AV1 spec
// §7.11.3.4). EIGHTTAP=0, EIGHTTAP_SMOOTH=1, EIGHTTAP_SHARP=2.
// filterBilinear is the BILINEAR interpolation filter index (subpelFilters[3]).
const filterBilinear = 3

func adjustFilter(interp, dim int) int {
	if dim <= 4 {
		switch interp {
		case 0, 2:
			return 4
		case 1:
			return 5
		}
	}
	return interp
}
