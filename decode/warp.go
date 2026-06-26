package decode

import (
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
)

// Warped motion (AV1 spec §7.10.4 find_warp_samples; §7.11.3.5-8 warp prediction).

const (
	lsSamplesMax        = 8 // LEAST_SQUARES_SAMPLES_MAX
	lsMvMax             = 256
	warpModelTransClamp = 1 << 23 // WARPEDMODEL_TRANS_CLAMP
	warpModelNondiag    = 1 << 13 // WARPEDMODEL_NONDIAGAFFINE_CLAMP
	warpParamReduceBits = 6
	warpedDiffPrecBits  = 10
	warpedPixelPrecSh   = 64 // WARPEDPIXEL_PREC_SHIFTS
	divLutBits          = 8
	divLutPrecBits      = 14
)

// findWarpSamples gathers neighbor MV samples for local-warp estimation
// (AV1 spec §7.10.4), populating fd.candList and fd.numSamples.
func (fd *frameDecoder) findWarpSamples() {
	fd.numSamples = 0
	fd.numSamplesScanned = 0
	w4 := predict.Num4x4BlocksWide[fd.miSize]
	h4 := predict.Num4x4BlocksHigh[fd.miSize]
	doTopLeft, doTopRight := true, true
	if fd.availU {
		srcSize := fd.miSizes[fd.miRow-1][fd.miCol]
		srcW := predict.Num4x4BlocksWide[srcSize]
		if w4 <= srcW {
			colOffset := -(fd.miCol & (srcW - 1))
			if colOffset < 0 {
				doTopLeft = false
			}
			if colOffset+srcW > w4 {
				doTopRight = false
			}
			fd.addSample(-1, 0)
		} else {
			for i := 0; i < min(w4, fd.miCols-fd.miCol); {
				srcSize := fd.miSizes[fd.miRow-1][fd.miCol+i]
				srcW := predict.Num4x4BlocksWide[srcSize]
				miStep := min(w4, srcW)
				fd.addSample(-1, i)
				i += miStep
			}
		}
	}
	if fd.availL {
		srcSize := fd.miSizes[fd.miRow][fd.miCol-1]
		srcH := predict.Num4x4BlocksHigh[srcSize]
		if h4 <= srcH {
			rowOffset := -(fd.miRow & (srcH - 1))
			if rowOffset < 0 {
				doTopLeft = false
			}
			fd.addSample(0, -1)
		} else {
			for i := 0; i < min(h4, fd.miRows-fd.miRow); {
				srcSize := fd.miSizes[fd.miRow+i][fd.miCol-1]
				srcH := predict.Num4x4BlocksHigh[srcSize]
				miStep := min(h4, srcH)
				fd.addSample(i, -1)
				i += miStep
			}
		}
	}
	if doTopLeft {
		fd.addSample(-1, -1)
	}
	if doTopRight && max(w4, h4) <= 16 {
		fd.addSample(-1, w4)
	}
	if fd.numSamples == 0 && fd.numSamplesScanned > 0 {
		fd.numSamples = 1
	}
}

// addSample adds a candidate at (deltaRow, deltaCol) if valid (AV1 spec §7.10.4.2).
func (fd *frameDecoder) addSample(deltaRow, deltaCol int) {
	if fd.numSamplesScanned >= lsSamplesMax {
		return
	}
	mvRow := fd.miRow + deltaRow
	mvCol := fd.miCol + deltaCol
	if !fd.isInside(mvRow, mvCol) {
		return
	}
	if !fd.miWrittenGrid[mvRow][mvCol] {
		return
	}
	if fd.gridRefFrames[mvRow][mvCol][0] != fd.refFrame[0] {
		return
	}
	if fd.gridRefFrames[mvRow][mvCol][1] != header.NoneFrame {
		return
	}
	candSz := fd.miSizes[mvRow][mvCol]
	candW4 := predict.Num4x4BlocksWide[candSz]
	candH4 := predict.Num4x4BlocksHigh[candSz]
	candRow := mvRow &^ (candH4 - 1)
	candCol := mvCol &^ (candW4 - 1)
	midY := candRow*4 + candH4*2 - 1
	midX := candCol*4 + candW4*2 - 1
	threshold := clip3i(16, 112, max(predict.BlockWidth(fd.miSize), predict.BlockHeight(fd.miSize)))
	candMv := fd.gridMvs[candRow][candCol][0]
	mvDiffRow := absInt(candMv.Row - fd.mv[0].Row)
	mvDiffCol := absInt(candMv.Col - fd.mv[0].Col)
	valid := (mvDiffRow + mvDiffCol) <= threshold
	cand := [4]int{midY * 8, midX * 8, midY*8 + candMv.Row, midX*8 + candMv.Col}
	fd.numSamplesScanned++
	if !valid && fd.numSamplesScanned > 1 {
		return
	}
	fd.candList[fd.numSamples] = cand
	if valid {
		fd.numSamples++
	}
}

func lsProduct(a, b int) int { return ((a * b) >> 2) + (a + b) }

// warpEstimation computes LocalWarpParams via least-squares (AV1 spec §7.11.3.8).
func (fd *frameDecoder) warpEstimation() {
	var A [2][2]int
	var Bx, By [2]int
	w4 := predict.Num4x4BlocksWide[fd.miSize]
	h4 := predict.Num4x4BlocksHigh[fd.miSize]
	midY := fd.miRow*4 + h4*2 - 1
	midX := fd.miCol*4 + w4*2 - 1
	suy := midY * 8
	sux := midX * 8
	duy := suy + fd.mv[0].Row
	dux := sux + fd.mv[0].Col
	for i := 0; i < fd.numSamples; i++ {
		sy := fd.candList[i][0] - suy
		sx := fd.candList[i][1] - sux
		dy := fd.candList[i][2] - duy
		dx := fd.candList[i][3] - dux
		if absInt(sx-dx) < lsMvMax && absInt(sy-dy) < lsMvMax {
			A[0][0] += lsProduct(sx, sx) + 8
			A[0][1] += lsProduct(sx, sy) + 4
			A[1][1] += lsProduct(sy, sy) + 8
			Bx[0] += lsProduct(sx, dx) + 8
			Bx[1] += lsProduct(sy, dx) + 4
			By[0] += lsProduct(sx, dy) + 4
			By[1] += lsProduct(sy, dy) + 8
		}
	}
	det := A[0][0]*A[1][1] - A[0][1]*A[0][1]
	fd.localValid = det != 0
	if det == 0 {
		return
	}
	divShift, divFactor := resolveDivisor(det)
	const prec = WarpedModelPrecBits
	divShift -= prec
	if divShift < 0 {
		divFactor = divFactor << uint(-divShift)
		divShift = 0
	}
	diag := func(v int) int {
		return clip3i((1<<prec)-warpModelNondiag+1, (1<<prec)+warpModelNondiag-1, round2signed(v*divFactor, divShift))
	}
	nondiag := func(v int) int {
		return clip3i(-warpModelNondiag+1, warpModelNondiag-1, round2signed(v*divFactor, divShift))
	}
	fd.localWarpParams[2] = diag(A[1][1]*Bx[0] - A[0][1]*Bx[1])
	fd.localWarpParams[3] = nondiag(-A[0][1]*Bx[0] + A[0][0]*Bx[1])
	fd.localWarpParams[4] = nondiag(A[1][1]*By[0] - A[0][1]*By[1])
	fd.localWarpParams[5] = diag(-A[0][1]*By[0] + A[0][0]*By[1])
	mvx := fd.mv[0].Col
	mvy := fd.mv[0].Row
	vx := mvx*(1<<(prec-3)) - (midX*(fd.localWarpParams[2]-(1<<prec)) + midY*fd.localWarpParams[3])
	vy := mvy*(1<<(prec-3)) - (midX*fd.localWarpParams[4] + midY*(fd.localWarpParams[5]-(1<<prec)))
	fd.localWarpParams[0] = clip3i(-warpModelTransClamp, warpModelTransClamp-1, vx)
	fd.localWarpParams[1] = clip3i(-warpModelTransClamp, warpModelTransClamp-1, vy)
}

// resolveDivisor approximates division by d (AV1 spec §7.11.3.7).
func resolveDivisor(d int) (divShift, divFactor int) {
	ad := absInt(d)
	n := floorLog2(ad)
	e := ad - (1 << uint(n))
	var f int
	if n > divLutBits {
		f = round2(e, n-divLutBits)
	} else {
		f = e << uint(divLutBits-n)
	}
	divShift = n + divLutPrecBits
	if d < 0 {
		divFactor = -divLut[f]
	} else {
		divFactor = divLut[f]
	}
	return
}

// useWarp derives whether warped motion compensation applies (AV1 spec §7.11.3.1
// step 7): 1 for local warp, 2 for global warp, 0 for translational.
func (fd *frameDecoder) useWarp(w, h, candRow, candCol, refList int) int {
	if w < 8 || h < 8 || fd.fh.ForceIntegerMV != 0 {
		return 0
	}
	if fd.motionMode == motionModeWarp && fd.localValid {
		return 1
	}
	refFrame := fd.gridRefFrames[candRow][candCol][refList]
	if (fd.yMode == globalMv || fd.yMode == globalGlobalMv) &&
		fd.fh.GmType[refFrame] > GmTranslation && !fd.isScaled(refFrame) {
		if ok, _, _, _, _ := setupShear(fd.fh.GmParams[refFrame]); ok {
			return 2
		}
	}
	return 0
}

// predictWarp forms the warped prediction for a reference list, returning the
// w×h array of intermediate (InterRound1-rounded) samples (AV1 spec §7.11.3.5).
// useWarp is 1 for local warp (LocalWarpParams) or 2 for global warp (gm_params).
func (fd *frameDecoder) predictWarp(useWarp, plane, x, y, w, h, candRow, candCol, refList int, isCompound bool) ([][]int, error) {
	refFrame := fd.gridRefFrames[candRow][candCol][refList]
	refIdx := fd.fh.RefFrameIdx[refFrame-header.LastFrame]
	if fd.refs == nil || refIdx < 0 || refIdx >= len(fd.refs) || fd.refs[refIdx] == nil {
		return nil, ErrUnsupported{"warp prediction: missing reference frame"}
	}
	refPlane := fd.refs[refIdx].Planes[plane]
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
	var warpParams [6]int
	if useWarp == 1 {
		warpParams = fd.localWarpParams
	} else {
		warpParams = fd.fh.GmParams[refFrame]
	}
	_, alpha, beta, gamma, delta := setupShear(warpParams)
	lastX := refPlane.Width - 1
	lastY := refPlane.Height - 1
	const prec = WarpedModelPrecBits
	const precMask = (1 << prec) - 1

	pred := make([][]int, h)
	for i := range pred {
		pred[i] = make([]int, w)
	}
	for i8 := 0; i8 <= (h-1)>>3; i8++ {
		for j8 := 0; j8 <= (w-1)>>3; j8++ {
			srcX := (x + j8*8 + 4) << subX
			srcY := (y + i8*8 + 4) << subY
			dstX := warpParams[2]*srcX + warpParams[3]*srcY + warpParams[0]
			dstY := warpParams[4]*srcX + warpParams[5]*srcY + warpParams[1]
			x4 := dstX >> subX
			y4 := dstY >> subY
			ix4 := x4 >> prec
			sx4 := x4 & precMask
			iy4 := y4 >> prec
			sy4 := y4 & precMask
			var inter [15][8]int
			for i1 := -7; i1 < 8; i1++ {
				for i2 := -4; i2 < 4; i2++ {
					sx := sx4 + alpha*i2 + beta*i1
					offs := round2(sx, warpedDiffPrecBits) + warpedPixelPrecSh
					s := 0
					for i3 := 0; i3 < 8; i3++ {
						sy := clip3i(0, lastY, iy4+i1)
						sxx := clip3i(0, lastX, ix4+i2-3+i3)
						s += warpedFilters[offs][i3] * int(refPlane.At(sxx, sy))
					}
					inter[i1+7][i2+4] = round2(s, interRound0)
				}
			}
			for i1 := -4; i1 < min(4, h-i8*8-4); i1++ {
				for i2 := -4; i2 < min(4, w-j8*8-4); i2++ {
					sy := sy4 + gamma*i2 + delta*i1
					offs := round2(sy, warpedDiffPrecBits) + warpedPixelPrecSh
					s := 0
					for i3 := 0; i3 < 8; i3++ {
						s += warpedFilters[offs][i3] * inter[i1+i3+4][i2+4]
					}
					pred[i8*8+i1+4][j8*8+i2+4] = round2(s, interRound1)
				}
			}
		}
	}
	return pred, nil
}

// setupShear decomposes the affine warp into two shears (AV1 spec §7.11.3.6).
func setupShear(warpParams [6]int) (warpValid bool, alpha, beta, gamma, delta int) {
	const prec = WarpedModelPrecBits
	alpha0 := clip3i(-32768, 32767, warpParams[2]-(1<<prec))
	beta0 := clip3i(-32768, 32767, warpParams[3])
	divShift, divFactor := resolveDivisor(warpParams[2])
	v := warpParams[4] << prec
	gamma0 := clip3i(-32768, 32767, round2signed(v*divFactor, divShift))
	w := warpParams[3] * warpParams[4]
	delta0 := clip3i(-32768, 32767, warpParams[5]-round2signed(w*divFactor, divShift)-(1<<prec))
	alpha = round2signed(alpha0, warpParamReduceBits) << warpParamReduceBits
	beta = round2signed(beta0, warpParamReduceBits) << warpParamReduceBits
	gamma = round2signed(gamma0, warpParamReduceBits) << warpParamReduceBits
	delta = round2signed(delta0, warpParamReduceBits) << warpParamReduceBits
	warpValid = true
	if 4*absInt(alpha)+7*absInt(beta) >= (1 << prec) {
		warpValid = false
	}
	if 4*absInt(gamma)+4*absInt(delta) >= (1 << prec) {
		warpValid = false
	}
	return
}
