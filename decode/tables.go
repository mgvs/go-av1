// Package decode is the AV1 tile reconstruction pipeline: partition tree, intra
// mode info, residual entry and intra prediction (milestones M3–M5). It currently
// reconstructs intra keyframes whose blocks are DC-predicted with all-zero residual
// (the simplest matching-frame case) and returns a clear error for anything that
// needs the full coefficient decode or other prediction modes.
package decode

import "github.com/mgvs/go-av1/predict"

// Transform sizes (AV1 spec §6.10.4, TX_*). 0..4 are square; 5..18 rectangular.
const (
	TX4x4 = iota
	TX8x8
	TX16x16
	TX32x32
	TX64x64
	TX4x8
	TX8x4
	TX8x16
	TX16x8
	TX16x32
	TX32x16
	TX32x64
	TX64x32
	TX4x16
	TX16x4
	TX8x32
	TX32x8
	TX16x64
	TX64x16
	TXSizesAll
)

// Transform-size geometry (AV1 spec §9.3), indexed by TX size.
var (
	TxWidth      = [TXSizesAll]int{4, 8, 16, 32, 64, 4, 8, 8, 16, 16, 32, 32, 64, 4, 16, 8, 32, 16, 64}
	TxHeight     = [TXSizesAll]int{4, 8, 16, 32, 64, 8, 4, 16, 8, 32, 16, 64, 32, 16, 4, 32, 8, 64, 16}
	TxWidthLog2  = [TXSizesAll]int{2, 3, 4, 5, 6, 2, 3, 3, 4, 4, 5, 5, 6, 2, 4, 3, 5, 4, 6}
	TxHeightLog2 = [TXSizesAll]int{2, 3, 4, 5, 6, 3, 2, 4, 3, 5, 4, 6, 5, 4, 2, 5, 3, 6, 4}
	TxSizeSqr    = [TXSizesAll]int{0, 1, 2, 3, 4, 0, 0, 1, 1, 2, 2, 3, 3, 0, 0, 1, 1, 2, 2}
	TxSizeSqrUp  = [TXSizesAll]int{0, 1, 2, 3, 4, 1, 1, 2, 2, 3, 3, 4, 4, 2, 2, 3, 3, 4, 4}
)

// MaxTxSizeRect[bSize] is the largest transform (square or rectangular) for a luma
// block of size bSize (AV1 spec §9.3).
var MaxTxSizeRect = [predict.BlockSizes]int{
	TX4x4, TX4x8, TX8x4, TX8x8, TX8x16, TX16x8, TX16x16, TX16x32,
	TX32x16, TX32x32, TX32x64, TX64x32, TX64x64, TX64x64, TX64x64, TX64x64,
	TX4x16, TX16x4, TX8x32, TX32x8, TX16x64, TX64x16,
}

// IntraModeContext maps an intra mode to its neighbor-context index for the
// intra_frame_y_mode CDF (AV1 spec §8.3).
var IntraModeContext = [13]int{0, 1, 2, 3, 4, 4, 4, 4, 3, 0, 1, 2, 0}

// SplitTxSize[txSz] halves a transform size (AV1 spec §9.3).
var SplitTxSize = [TXSizesAll]int{
	TX4x4, TX4x4, TX8x8, TX16x16, TX32x32, TX4x4, TX4x4, TX8x8, TX8x8,
	TX16x16, TX16x16, TX32x32, TX32x32, TX4x8, TX8x4, TX8x16, TX16x8, TX16x32, TX32x16,
}

// MaxTxDepth[bSize] is the maximum transform depth for a block size (AV1 spec §9.3).
var MaxTxDepth = [predict.BlockSizes]int{
	0, 1, 1, 1, 2, 2, 2, 3, 3, 3, 4, 4, 4, 4, 4, 4, 2, 2, 3, 3, 4, 4,
}

// AdjustedTxSize[txSz] clamps 64-dimensioned transforms to 32 for coefficient
// addressing/scan/context purposes (AV1 spec §9.3, Adjusted_Tx_Size).
var AdjustedTxSize = [TXSizesAll]int{
	TX4x4, TX8x8, TX16x16, TX32x32, TX32x32, TX4x8, TX8x4, TX8x16, TX16x8,
	TX16x32, TX32x16, TX32x32, TX32x32, TX4x16, TX16x4, TX8x32, TX32x8, TX16x32, TX32x16,
}

// SubsampledSize[bSize][subX][subY] gives the residual block size for a plane
// (AV1 spec §6.10.4, Subsampled_Size). -1 is BLOCK_INVALID.
var SubsampledSize = [predict.BlockSizes][2][2]int{
	{{predict.Block4x4, predict.Block4x4}, {predict.Block4x4, predict.Block4x4}},
	{{predict.Block4x8, predict.Block4x4}, {-1, predict.Block4x4}},
	{{predict.Block8x4, -1}, {predict.Block4x4, predict.Block4x4}},
	{{predict.Block8x8, predict.Block8x4}, {predict.Block4x8, predict.Block4x4}},
	{{predict.Block8x16, predict.Block8x8}, {-1, predict.Block4x8}},
	{{predict.Block16x8, -1}, {predict.Block8x8, predict.Block8x4}},
	{{predict.Block16x16, predict.Block16x8}, {predict.Block8x16, predict.Block8x8}},
	{{predict.Block16x32, predict.Block16x16}, {-1, predict.Block8x16}},
	{{predict.Block32x16, -1}, {predict.Block16x16, predict.Block16x8}},
	{{predict.Block32x32, predict.Block32x16}, {predict.Block16x32, predict.Block16x16}},
	{{predict.Block32x64, predict.Block32x32}, {-1, predict.Block16x32}},
	{{predict.Block64x32, -1}, {predict.Block32x32, predict.Block32x16}},
	{{predict.Block64x64, predict.Block64x32}, {predict.Block32x64, predict.Block32x32}},
	{{predict.Block64x128, predict.Block64x64}, {-1, predict.Block32x64}},
	{{predict.Block128x64, -1}, {predict.Block64x64, predict.Block64x32}},
	{{predict.Block128x128, predict.Block128x64}, {predict.Block64x128, predict.Block64x64}},
	{{predict.Block4x16, predict.Block4x8}, {-1, predict.Block4x8}},
	{{predict.Block16x4, -1}, {predict.Block8x4, predict.Block8x4}},
	{{predict.Block8x32, predict.Block8x16}, {-1, predict.Block4x16}},
	{{predict.Block32x8, -1}, {predict.Block16x8, predict.Block16x4}},
	{{predict.Block16x64, predict.Block16x32}, {-1, predict.Block8x32}},
	{{predict.Block64x16, -1}, {predict.Block32x16, predict.Block32x8}},
}

// Intra prediction modes (AV1 spec §6.10.5) used here.
const (
	DCPred    = 0
	VPred     = 1
	HPred     = 2
	D157Pred  = 6
	D67Pred   = 8
	UVCflPred = 13
)

// isDirectional reports whether an intra mode is a directional predictor (reads an
// angle delta). V_PRED..D67_PRED are directional.
func isDirectional(mode int) bool { return mode >= VPred && mode <= D67Pred }
