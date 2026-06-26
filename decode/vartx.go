package decode

import "github.com/mgvs/go-av1/predict"

const maxVarTxDepth = 2

func findTxSize(w, h int) int {
	for txSz := 0; txSz < TXSizesAll; txSz++ {
		if TxWidth[txSz] == w && TxHeight[txSz] == h {
			return txSz
		}
	}
	return 0
}

// getAboveTxWidth returns the transform width of the block above (AV1 spec §8.3).
func (fd *frameDecoder) getAboveTxWidth(row, col int) int {
	if row == fd.miRow {
		if !fd.availU {
			return 64
		}
		if fd.skips[row-1][col] != 0 && fd.isInterAt(row-1, col) {
			return predict.BlockWidth(fd.miSizes[row-1][col])
		}
	}
	return TxWidth[fd.txSizes[row-1][col]]
}

// getLeftTxHeight returns the transform height of the block to the left.
func (fd *frameDecoder) getLeftTxHeight(row, col int) int {
	if col == fd.miCol {
		if !fd.availL {
			return 64
		}
		if fd.skips[row][col-1] != 0 && fd.isInterAt(row, col-1) {
			return predict.BlockHeight(fd.miSizes[row][col-1])
		}
	}
	return TxHeight[fd.txSizes[row][col-1]]
}

// txfmSplitCtx computes the context for the txfm_split symbol (AV1 spec §8.3).
func (fd *frameDecoder) txfmSplitCtx(row, col, txSz int) int {
	above := b2i(fd.getAboveTxWidth(row, col) < TxWidth[txSz])
	left := b2i(fd.getLeftTxHeight(row, col) < TxHeight[txSz])
	size := min(64, max(predict.BlockWidth(fd.miSize), predict.BlockHeight(fd.miSize)))
	maxTxSz := findTxSize(size, size)
	const txSizes = 5
	return b2i(TxSizeSqrUp[txSz] != maxTxSz)*3 + (txSizes-1-maxTxSz)*6 + above + left
}

// readVarTxSize reads the variable transform tree for an inter block, populating
// the per-4x4 transform sizes in txSizes (AV1 spec §5.11.16).
func (fd *frameDecoder) readVarTxSize(row, col, txSz, depth int) {
	if row >= fd.miRows || col >= fd.miCols {
		return
	}
	split := 0
	if txSz != TX4x4 && depth != maxVarTxDepth {
		ctx := fd.txfmSplitCtx(row, col, txSz)
		split = fd.d.DecodeSymbol(fd.c.txfmSplit[ctx])
	}
	w4 := TxWidth[txSz] >> 2
	h4 := TxHeight[txSz] >> 2
	if split == 1 {
		subTxSz := SplitTxSize[txSz]
		stepW := TxWidth[subTxSz] >> 2
		stepH := TxHeight[subTxSz] >> 2
		for i := 0; i < h4; i += stepH {
			for j := 0; j < w4; j += stepW {
				fd.readVarTxSize(row+i, col+j, subTxSz, depth+1)
			}
		}
	} else {
		for i := 0; i < h4; i++ {
			for j := 0; j < w4; j++ {
				if row+i < fd.miRows && col+j < fd.miCols {
					fd.txSizes[row+i][col+j] = txSz
				}
			}
		}
		fd.txSize = txSz
	}
}

// transformTree walks the inter luma variable transform tree, invoking
// transform_block on each leaf (AV1 spec §5.11.37).
func (fd *frameDecoder) transformTree(startX, startY, w, h int) error {
	maxX := fd.miCols * 4
	maxY := fd.miRows * 4
	if startX >= maxX || startY >= maxY {
		return nil
	}
	row := startY >> 2
	col := startX >> 2
	lumaTxSz := fd.txSizes[row][col]
	lumaW := TxWidth[lumaTxSz]
	lumaH := TxHeight[lumaTxSz]
	if w <= lumaW && h <= lumaH {
		return fd.transformBlock(0, startX, startY, findTxSize(w, h), 0, 0)
	}
	switch {
	case w > h:
		if err := fd.transformTree(startX, startY, w/2, h); err != nil {
			return err
		}
		return fd.transformTree(startX+w/2, startY, w/2, h)
	case w < h:
		if err := fd.transformTree(startX, startY, w, h/2); err != nil {
			return err
		}
		return fd.transformTree(startX, startY+h/2, w, h/2)
	default:
		for _, off := range [4][2]int{{0, 0}, {w / 2, 0}, {0, h / 2}, {w / 2, h / 2}} {
			if err := fd.transformTree(startX+off[0], startY+off[1], w/2, h/2); err != nil {
				return err
			}
		}
		return nil
	}
}
