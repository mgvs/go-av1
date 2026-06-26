package decode

// Inter-intra compound prediction (AV1 spec §7.11.3.1 / §7.11.3.13 / §7.11.3.14).

// Inter-intra modes.
const (
	iiDCPred     = 0
	iiVPred      = 1
	iiHPred      = 2
	iiSmoothPred = 3
)

func log2int(n int) int {
	r := 0
	for 1<<uint(r) < n {
		r++
	}
	return r
}

// buildIntraVariantMask returns the per-plane blend mask for a non-wedge
// inter-intra block (AV1 spec §7.11.3.13).
func buildIntraVariantMask(mode, w, h int) [][]int {
	sizeScale := 128 / max(h, w)
	mask := make([][]int, h)
	for i := 0; i < h; i++ {
		mask[i] = make([]int, w)
		for j := 0; j < w; j++ {
			switch mode {
			case iiVPred:
				mask[i][j] = iiWeights1d[i*sizeScale]
			case iiHPred:
				mask[i][j] = iiWeights1d[j*sizeScale]
			case iiSmoothPred:
				mask[i][j] = iiWeights1d[min(i, j)*sizeScale]
			default:
				mask[i][j] = 32
			}
		}
	}
	return mask
}

// blendInterIntra predicts the intra component and blends it with the inter
// prediction for an inter-intra block (AV1 spec §7.11.3.1 IsInterIntra branch).
func (fd *frameDecoder) blendInterIntra(plane, x, y, w, h int, inter [][]int) error {
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	var mode int
	switch fd.interIntraMode {
	case iiVPred:
		mode = VPred
	case iiHPred:
		mode = HPred
	case iiSmoothPred:
		mode = 9 // SMOOTH_PRED
	default:
		mode = DCPred
	}
	haveLeft, haveAbove := fd.availL, fd.availU
	if plane > 0 {
		haveLeft, haveAbove = fd.availLChroma, fd.availUChroma
	}
	row := (y << uint(subY)) >> 2
	col := (x << uint(subX)) >> 2
	sbMask := 15
	if fd.seq.Use128x128Superblock {
		sbMask = 31
	}
	sbRow := row & sbMask
	sbCol := col & sbMask
	stepX := w >> 2
	stepY := h >> 2
	haveAboveRight := fd.bdGet(plane, (sbRow>>uint(subY))-1, (sbCol>>uint(subX))+stepX) == 1
	haveBelowLeft := fd.bdGet(plane, (sbRow>>uint(subY))+stepY, (sbCol>>uint(subX))-1) == 1
	maxX := fd.planes[plane].Width
	maxY := fd.planes[plane].Height
	// Predict the intra component into the plane.
	if err := fd.planes[plane].PredictIntra(x, y, log2int(w), log2int(h),
		haveLeft, haveAbove, haveAboveRight, haveBelowLeft, mode, fd.bitDepth, maxX-1, maxY-1,
		0, fd.getFilterType(plane), fd.seq.EnableIntraEdgeFilter, false, 0); err != nil {
		return ErrUnsupported{err.Error()}
	}
	// Build the blend mask (luma wedge mask, or per-plane intra-variant mask).
	if fd.wedgeInterIntra {
		if plane == 0 {
			fd.buildWedgeMask(w, h)
		}
	} else {
		fd.wedgeMask = buildIntraVariantMask(fd.interIntraMode, w, h)
	}
	hi := (1 << uint(fd.bitDepth)) - 1
	// InterPostRound = 2*FILTER_BITS - InterRound0 - InterRound1 = 0 for the
	// single (non-compound) inter prediction at all bit depths.
	const interPostRound = 0
	for i := 0; i < h; i++ {
		if y+i >= fd.planes[plane].AllocH {
			break
		}
		for j := 0; j < w; j++ {
			if x+j >= fd.planes[plane].AllocW {
				break
			}
			var m int
			if !fd.wedgeInterIntra {
				m = fd.wedgeMask[i][j] // per-plane intra-variant mask
			} else {
				switch {
				case subX == 0 && subY == 0:
					m = fd.wedgeMask[i][j]
				case subX != 0 && subY == 0:
					m = round2(fd.wedgeMask[i][2*j]+fd.wedgeMask[i][2*j+1], 1)
				default:
					m = round2(fd.wedgeMask[2*i][2*j]+fd.wedgeMask[2*i][2*j+1]+
						fd.wedgeMask[2*i+1][2*j]+fd.wedgeMask[2*i+1][2*j+1], 2)
				}
			}
			pred0 := clip3i(0, hi, round2(inter[i][j], interPostRound)) // inter
			pred1 := int(fd.planes[plane].At(x+j, y+i))                 // intra
			v := round2(m*pred1+(64-m)*pred0, 6)
			fd.planes[plane].Set(x+j, y+i, uint16(clip3i(0, hi, v)))
		}
	}
	return nil
}
