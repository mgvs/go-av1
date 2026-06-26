package decode

// Scan tables indexed by transform size (AV1 spec §9.3). nil where a given scan
// flavor does not exist for that size.
var defaultScanByTx = [TXSizesAll][]int{
	TX4x4:   scan_def4x4,
	TX8x8:   scan_def8x8,
	TX16x16: scan_def16x16,
	TX32x32: scan_def32x32,
	TX4x8:   scan_def4x8,
	TX8x4:   scan_def8x4,
	TX8x16:  scan_def8x16,
	TX16x8:  scan_def16x8,
	TX16x32: scan_def16x32,
	TX32x16: scan_def32x16,
	TX4x16:  scan_def4x16,
	TX16x4:  scan_def16x4,
	TX8x32:  scan_def8x32,
	TX32x8:  scan_def32x8,
}

var mrowScanByTx = [TXSizesAll][]int{
	TX4x4:   scan_mrow4x4,
	TX8x8:   scan_mrow8x8,
	TX16x16: scan_mrow16x16,
	TX4x8:   scan_mrow4x8,
	TX8x4:   scan_mrow8x4,
	TX8x16:  scan_mrow8x16,
	TX16x8:  scan_mrow16x8,
	TX4x16:  scan_mrow4x16,
	TX16x4:  scan_mrow16x4,
}

var mcolScanByTx = [TXSizesAll][]int{
	TX4x4:   scan_mcol4x4,
	TX8x8:   scan_mcol8x8,
	TX16x16: scan_mcol16x16,
	TX4x8:   scan_mcol4x8,
	TX8x4:   scan_mcol8x4,
	TX8x16:  scan_mcol8x16,
	TX16x8:  scan_mcol16x8,
	TX4x16:  scan_mcol4x16,
	TX16x4:  scan_mcol16x4,
}

// getScan returns the coefficient scan order for a transform block (AV1 spec
// get_scan). txType is the plane transform type.
func getScan(txSz, txType int) []int {
	switch {
	case txSz == TX16x64:
		return scan_def16x32
	case txSz == TX64x16:
		return scan_def32x16
	case TxSizeSqrUp[txSz] == TX64x64:
		return scan_def32x32
	}
	if txType == Idtx {
		return defaultScanByTx[txSz]
	}
	switch txType {
	case VDct, VAdst, VFlipadst:
		return mrowScanByTx[txSz]
	case HDct, HAdst, HFlipadst:
		return mcolScanByTx[txSz]
	}
	return defaultScanByTx[txSz]
}
