package decode

// Coefficient context constants and offset tables (AV1 spec §3, §8.3).
const (
	sigCoefContexts    = 42
	sigCoefContextsEob = 4
	sigCoefContexts2D  = 26

	txClass2D    = 0
	txClassHoriz = 1
	txClassVert  = 2
)

// Neighbor offset tables for the coeff_base / coeff_br contexts (AV1 spec §8.3),
// indexed by transform class (2D / horizontal / vertical).
var (
	sigRefDiffOffset = [3][5][2]int{
		{{0, 1}, {1, 0}, {1, 1}, {0, 2}, {2, 0}},
		{{0, 1}, {1, 0}, {0, 2}, {0, 3}, {0, 4}},
		{{0, 1}, {1, 0}, {2, 0}, {3, 0}, {4, 0}},
	}
	magRefOffset = [3][3][2]int{
		{{0, 1}, {1, 0}, {1, 1}},
		{{0, 1}, {1, 0}, {0, 2}},
		{{0, 1}, {1, 0}, {2, 0}},
	}
	coeffBasePosCtxOffset = [3]int{sigCoefContexts2D, sigCoefContexts2D + 5, sigCoefContexts2D + 10}
)

// getCoeffBaseCtx computes the context for coeff_base / coeff_base_eob
// (AV1 spec §8.3, get_coeff_base_ctx). quant holds the levels decoded so far.
func getCoeffBaseCtx(quant []int, txSz, txClass, pos, c int, isEob bool) int {
	adjTxSz := AdjustedTxSize[txSz]
	bwl := TxWidthLog2[adjTxSz]
	width := 1 << uint(bwl)
	height := TxHeight[adjTxSz]

	if isEob {
		switch {
		case c == 0:
			return sigCoefContexts - 4
		case c <= (height<<uint(bwl))/8:
			return sigCoefContexts - 3
		case c <= (height<<uint(bwl))/4:
			return sigCoefContexts - 2
		default:
			return sigCoefContexts - 1
		}
	}

	row := pos >> uint(bwl)
	col := pos - (row << uint(bwl))
	mag := 0
	for idx := 0; idx < 5; idx++ {
		refRow := row + sigRefDiffOffset[txClass][idx][0]
		refCol := col + sigRefDiffOffset[txClass][idx][1]
		if refRow >= 0 && refCol >= 0 && refRow < height && refCol < width {
			mag += mini(absInt(quant[(refRow<<uint(bwl))+refCol]), 3)
		}
	}
	ctx := mini((mag+1)>>1, 4)
	if txClass == txClass2D {
		if row == 0 && col == 0 {
			return 0
		}
		return ctx + coeffBaseCtxOffset[txSz][mini(row, 4)][mini(col, 4)]
	}
	idx := col
	if txClass == txClassVert {
		idx = row
	}
	return ctx + coeffBasePosCtxOffset[mini(idx, 2)]
}

// getCoeffBrCtx computes the context for coeff_br (AV1 spec §8.3).
func getCoeffBrCtx(quant []int, txSz, txClass, pos int) int {
	adjTxSz := AdjustedTxSize[txSz]
	bwl := TxWidthLog2[adjTxSz]
	txw := TxWidth[adjTxSz]
	txh := TxHeight[adjTxSz]
	row := pos >> uint(bwl)
	col := pos - (row << uint(bwl))
	mag := 0
	for idx := 0; idx < 3; idx++ {
		refRow := row + magRefOffset[txClass][idx][0]
		refCol := col + magRefOffset[txClass][idx][1]
		if refRow >= 0 && refCol >= 0 && refRow < txh && refCol < (1<<uint(bwl)) {
			mag += mini(quant[refRow*txw+refCol], coeffBaseRange+numBaseLevels+1)
		}
	}
	mag = mini((mag+1)>>1, 6)
	if pos == 0 {
		return mag
	}
	if txClass == txClass2D {
		if row < 2 && col < 2 {
			return mag + 7
		}
		return mag + 14
	}
	if txClass == txClassHoriz {
		if col == 0 {
			return mag + 7
		}
		return mag + 14
	}
	if row == 0 {
		return mag + 7
	}
	return mag + 14
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
