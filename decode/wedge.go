package decode

import (
	"sync"

	"github.com/mgvs/go-av1/predict"
)

// Masked-compound (wedge) mask generation (AV1 spec §7.11.3.11).

const maskMasterSize = 64
const wedgeTypes = 16

// Wedge_Bits[BLOCK_SIZES]: number of wedge index bits (0 = no wedge for this size).
var wedgeBits = [predict.BlockSizes]int{
	0, 0, 0, 4, 4, 4, 4, 4, 4, 4, 0,
	0, 0, 0, 0, 0, 0, 0, 4, 4, 0, 0,
}

// Wedge direction constants.
const (
	wedgeHorizontal = 0
	wedgeVertical   = 1
	wedgeOblique27  = 2
	wedgeOblique63  = 3
	wedgeOblique117 = 4
	wedgeOblique153 = 5
)

var wedgeMasterObliqueOdd = [maskMasterSize]int{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 6, 18,
	37, 53, 60, 63, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64,
	64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64,
}
var wedgeMasterObliqueEven = [maskMasterSize]int{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 4, 11, 27,
	46, 58, 62, 63, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64,
	64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64,
}
var wedgeMasterVertical = [maskMasterSize]int{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 7, 21,
	43, 57, 62, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64,
	64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64,
}

// Wedge_Codebook[3][16][3] = {direction, xoff, yoff}.
var wedgeCodebook = [3][16][3]int{
	{
		{wedgeOblique27, 4, 4}, {wedgeOblique63, 4, 4},
		{wedgeOblique117, 4, 4}, {wedgeOblique153, 4, 4},
		{wedgeHorizontal, 4, 2}, {wedgeHorizontal, 4, 4},
		{wedgeHorizontal, 4, 6}, {wedgeVertical, 4, 4},
		{wedgeOblique27, 4, 2}, {wedgeOblique27, 4, 6},
		{wedgeOblique153, 4, 2}, {wedgeOblique153, 4, 6},
		{wedgeOblique63, 2, 4}, {wedgeOblique63, 6, 4},
		{wedgeOblique117, 2, 4}, {wedgeOblique117, 6, 4},
	},
	{
		{wedgeOblique27, 4, 4}, {wedgeOblique63, 4, 4},
		{wedgeOblique117, 4, 4}, {wedgeOblique153, 4, 4},
		{wedgeVertical, 2, 4}, {wedgeVertical, 4, 4},
		{wedgeVertical, 6, 4}, {wedgeHorizontal, 4, 4},
		{wedgeOblique27, 4, 2}, {wedgeOblique27, 4, 6},
		{wedgeOblique153, 4, 2}, {wedgeOblique153, 4, 6},
		{wedgeOblique63, 2, 4}, {wedgeOblique63, 6, 4},
		{wedgeOblique117, 2, 4}, {wedgeOblique117, 6, 4},
	},
	{
		{wedgeOblique27, 4, 4}, {wedgeOblique63, 4, 4},
		{wedgeOblique117, 4, 4}, {wedgeOblique153, 4, 4},
		{wedgeHorizontal, 4, 2}, {wedgeHorizontal, 4, 6},
		{wedgeVertical, 2, 4}, {wedgeVertical, 6, 4},
		{wedgeOblique27, 4, 2}, {wedgeOblique27, 4, 6},
		{wedgeOblique153, 4, 2}, {wedgeOblique153, 4, 6},
		{wedgeOblique63, 2, 4}, {wedgeOblique63, 6, 4},
		{wedgeOblique117, 2, 4}, {wedgeOblique117, 6, 4},
	},
}

// wedgeMasks[bsize][sign][wedge][i][j], generated once.
var (
	wedgeMasks     [predict.BlockSizes][2][wedgeTypes][][]int
	wedgeMasksOnce sync.Once
)

func blockShape(bsize int) int {
	w4 := predict.Num4x4BlocksWide[bsize]
	h4 := predict.Num4x4BlocksHigh[bsize]
	switch {
	case h4 > w4:
		return 0
	case h4 < w4:
		return 1
	default:
		return 2
	}
}

func clip3int(lo, hi, v int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// initWedgeMasks builds the WedgeMasks lookup table (AV1 spec §7.11.3.11).
func initWedgeMasks() {
	var master [6][maskMasterSize][maskMasterSize]int
	w, h := maskMasterSize, maskMasterSize
	for j := 0; j < w; j++ {
		shift := maskMasterSize / 4
		for i := 0; i < h; i += 2 {
			master[wedgeOblique63][i][j] = wedgeMasterObliqueEven[clip3int(0, maskMasterSize-1, j-shift)]
			shift--
			master[wedgeOblique63][i+1][j] = wedgeMasterObliqueOdd[clip3int(0, maskMasterSize-1, j-shift)]
			master[wedgeVertical][i][j] = wedgeMasterVertical[j]
			master[wedgeVertical][i+1][j] = wedgeMasterVertical[j]
		}
	}
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			msk := master[wedgeOblique63][i][j]
			master[wedgeOblique27][j][i] = msk
			master[wedgeOblique117][i][w-1-j] = 64 - msk
			master[wedgeOblique153][w-1-j][i] = 64 - msk
			master[wedgeHorizontal][j][i] = master[wedgeVertical][i][j]
		}
	}
	for bsize := predict.Block8x8; bsize < predict.BlockSizes; bsize++ {
		if wedgeBits[bsize] == 0 {
			continue
		}
		bw := predict.BlockWidth(bsize)
		bh := predict.BlockHeight(bsize)
		for wedge := 0; wedge < wedgeTypes; wedge++ {
			dir := wedgeCodebook[blockShape(bsize)][wedge][0]
			xoff := maskMasterSize/2 - ((wedgeCodebook[blockShape(bsize)][wedge][1] * bw) >> 3)
			yoff := maskMasterSize/2 - ((wedgeCodebook[blockShape(bsize)][wedge][2] * bh) >> 3)
			sum := 0
			for i := 0; i < bw; i++ {
				sum += master[dir][yoff][xoff+i]
			}
			for i := 1; i < bh; i++ {
				sum += master[dir][yoff+i][xoff]
			}
			avg := (sum + (bw+bh-1)/2) / (bw + bh - 1)
			flip := 0
			if avg < 32 {
				flip = 1
			}
			m0 := make([][]int, bh)
			m1 := make([][]int, bh)
			for i := 0; i < bh; i++ {
				m0[i] = make([]int, bw)
				m1[i] = make([]int, bw)
				for j := 0; j < bw; j++ {
					v := master[dir][yoff+i][xoff+j]
					m0[i][j] = v
					m1[i][j] = 64 - v
				}
			}
			wedgeMasks[bsize][flip][wedge] = m0
			wedgeMasks[bsize][1-flip][wedge] = m1
		}
	}
}

// buildDiffwtdMask fills fd.wedgeMask from the difference between the two luma
// predictions (AV1 spec §7.11.3.12).
func (fd *frameDecoder) buildDiffwtdMask(preds [][][]int, w, h int) {
	interPostRound := 4
	if fd.bitDepth == 12 {
		interPostRound = 2
	}
	mask := make([][]int, h)
	for i := 0; i < h; i++ {
		mask[i] = make([]int, w)
		for j := 0; j < w; j++ {
			diff := absInt(preds[0][i][j] - preds[1][i][j])
			diff = round2(diff, (fd.bitDepth-8)+interPostRound)
			m := clip3int(0, 64, 38+diff/16)
			if fd.maskType != 0 {
				m = 64 - m
			}
			mask[i][j] = m
		}
	}
	fd.wedgeMask = mask
}

// buildWedgeMask fills fd.wedgeMask (w×h luma blend weights) for the current block.
func (fd *frameDecoder) buildWedgeMask(w, h int) {
	wedgeMasksOnce.Do(initWedgeMasks)
	src := wedgeMasks[fd.miSize][fd.wedgeSign][fd.wedgeIndex]
	mask := make([][]int, h)
	for i := 0; i < h; i++ {
		mask[i] = make([]int, w)
		copy(mask[i], src[i])
	}
	fd.wedgeMask = mask
}
