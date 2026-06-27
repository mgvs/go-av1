package decode

// Transform types (AV1 spec §6.10.4).
const (
	DctDct = iota
	AdstDct
	DctAdst
	AdstAdst
	FlipadstDct
	DctFlipadst
	FlipadstFlipadst
	AdstFlipadst
	FlipadstAdst
	Idtx
	VDct
	HDct
	VAdst
	HAdst
	VFlipadst
	HFlipadst
)

// Transform sets (AV1 spec §6.10.4).
const (
	txSetDctOnly = 0
	txSetIntra1  = 1
	txSetIntra2  = 2
)

// modeToTxfm maps a (UV) intra mode to its implied transform type (AV1 spec §9.3).
var modeToTxfm = [14]int{
	DctDct, AdstDct, DctAdst, DctDct, AdstAdst, AdstDct, DctAdst,
	DctAdst, AdstDct, AdstAdst, AdstDct, DctAdst, AdstAdst, DctDct,
}

// Inverse maps from the decoded tx_type index to the transform type.
var (
	txTypeIntraInvSet1 = []int{Idtx, DctDct, VDct, HDct, AdstAdst, AdstDct, DctAdst}
	txTypeIntraInvSet2 = []int{Idtx, DctDct, AdstAdst, AdstDct, DctAdst}
	txTypeInterInvSet1 = []int{Idtx, VDct, HDct, VAdst, HAdst, VFlipadst, HFlipadst,
		DctDct, AdstDct, DctAdst, FlipadstDct, DctFlipadst, AdstAdst, FlipadstFlipadst, AdstFlipadst, FlipadstAdst}
	txTypeInterInvSet2 = []int{Idtx, VDct, HDct, DctDct, AdstDct, DctAdst, FlipadstDct,
		DctFlipadst, AdstAdst, FlipadstFlipadst, AdstFlipadst, FlipadstAdst}
	txTypeInterInvSet3 = []int{Idtx, DctDct}
)

// getTxSet returns the transform set for a transform of size txSz, for either an
// intra or inter block (AV1 spec §5.11.48). Inter returns INTER_1/2/3 (1..3),
// intra returns INTRA_1/2 (1..2) or DCTONLY (0).
func (fd *frameDecoder) getTxSet(txSz int) int {
	sqr := TxSizeSqr[txSz]
	sqrUp := TxSizeSqrUp[txSz]
	if sqrUp > TX32x32 {
		return txSetDctOnly
	}
	if fd.isInterFlag {
		if fd.fh.ReducedTxSet || sqrUp == TX32x32 {
			return 3 // TX_SET_INTER_3
		}
		if sqr == TX16x16 {
			return 2 // TX_SET_INTER_2
		}
		return 1 // TX_SET_INTER_1
	}
	if sqrUp == TX32x32 {
		return txSetDctOnly
	}
	if fd.fh.ReducedTxSet || sqr == TX16x16 {
		return txSetIntra2
	}
	return txSetIntra1
}

func (fd *frameDecoder) txTypeInSet(set, txType int) bool {
	if fd.isInterFlag {
		switch set {
		case txSetDctOnly:
			return txType == DctDct
		case 1:
			return contains(txTypeInterInvSet1, txType)
		case 2:
			return contains(txTypeInterInvSet2, txType)
		default:
			return contains(txTypeInterInvSet3, txType)
		}
	}
	switch set {
	case txSetDctOnly:
		return txType == DctDct
	case txSetIntra1:
		return contains(txTypeIntraInvSet1, txType)
	default:
		return contains(txTypeIntraInvSet2, txType)
	}
}

// getTxClass returns the transform class (2D / horizontal / vertical) for a
// transform type (AV1 spec get_tx_class).
func getTxClass(txType int) int {
	switch txType {
	case VDct, VAdst, VFlipadst:
		return txClassVert
	case HDct, HAdst, HFlipadst:
		return txClassHoriz
	default:
		return txClass2D
	}
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// filterIntraModeToIntraDir maps a filter-intra mode to the intra direction used
// for intra_tx_type CDF selection (AV1 spec §9.3): {DC, V, H, D157, DC}.
var filterIntraModeToIntraDir = [5]int{DCPred, VPred, HPred, D157Pred, DCPred}

// readTransformType reads intra_tx_type/inter_tx_type for a luma transform block
// (AV1 spec §5.11.47). The tx_type is coded only when the (per-segment) qindex > 0.
func (fd *frameDecoder) readTransformType(txSz, x4, y4 int) int {
	set := fd.getTxSet(txSz)
	txType := DctDct
	// The tx_type is coded only when the quantizer is non-zero. With segmentation
	// the per-segment qindex (SEG_LVL_ALT_Q, ignoring delta-q) is used — NOT the
	// frame base_q_idx, which can be 0 while a segment raises the block above it
	// (AV1 spec §5.11.47).
	qidx := fd.fh.BaseQIdx
	if fd.fh.SegmentationEnabled {
		qidx = fd.getQIndexSeg(true, fd.segmentId)
	}
	if set > 0 && qidx > 0 {
		txSzSqr := TxSizeSqr[txSz]
		if fd.isInterFlag {
			switch set {
			case 1:
				txType = txTypeInterInvSet1[fd.d.DecodeSymbol(fd.c.interTxType1[txSzSqr])]
			case 2:
				txType = txTypeInterInvSet2[fd.d.DecodeSymbol(fd.c.interTxType2)]
			default:
				txType = txTypeInterInvSet3[fd.d.DecodeSymbol(fd.c.interTxType3[txSzSqr])]
			}
		} else {
			// intraDir selects the CDF: the filter-intra direction when filter-intra
			// is active, else the luma mode (AV1 spec §9.3, intra_tx_type).
			intraDir := fd.yMode
			if fd.useFilterIntra {
				intraDir = filterIntraModeToIntraDir[fd.filterIntraMode]
			}
			if set == txSetIntra1 {
				txType = txTypeIntraInvSet1[fd.d.DecodeSymbol(fd.c.intraTxTypeSet1[txSzSqr][intraDir])]
			} else {
				txType = txTypeIntraInvSet2[fd.d.DecodeSymbol(fd.c.intraTxTypeSet2[txSzSqr][intraDir])]
			}
		}
	}
	for i := 0; i < TxWidth[txSz]>>2; i++ {
		for j := 0; j < TxHeight[txSz]>>2; j++ {
			if y4+j < len(fd.txTypes) && x4+i < len(fd.txTypes[0]) {
				fd.txTypes[y4+j][x4+i] = txType
			}
		}
	}
	return txType
}

// computeTxType derives the transform type for a transform block (AV1 spec
// compute_tx_type), reading the stored per-4x4 luma transform type for chroma.
func (fd *frameDecoder) computeTxType(plane, txSz, x4, y4 int) int {
	if fd.lossless() || TxSizeSqrUp[txSz] > TX32x32 {
		return DctDct
	}
	set := fd.getTxSet(txSz)
	if plane == 0 {
		return fd.txTypes[y4][x4]
	}
	if fd.isInterFlag {
		lx := max(fd.miCol, x4<<uint(fd.subX))
		ly := max(fd.miRow, y4<<uint(fd.subY))
		txType := fd.txTypes[ly][lx]
		if !fd.txTypeInSet(set, txType) {
			return DctDct
		}
		return txType
	}
	txType := modeToTxfm[fd.uvMode]
	if !fd.txTypeInSet(set, txType) {
		return DctDct
	}
	return txType
}
