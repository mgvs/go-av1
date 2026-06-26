package decode

import "github.com/mgvs/go-av1/cdf"

// cdfContext holds the per-tile CDF tables (mutable copies of the spec defaults;
// they adapt as symbols are decoded). Only the subset needed for intra DC/skip
// blocks is loaded so far (AV1 spec §8.3, init_non_coeff_cdfs / init_coeff_cdfs).
type cdfContext struct {
	skip              [][]uint16     // [ctx][3]
	intrabc           []uint16       // [3] use_intrabc (single context)
	deltaQ            []uint16       // [DELTA_Q_SMALL+2] delta_q_abs (single context)
	deltaLf           []uint16       // [DELTA_LF_SMALL+2] delta_lf_abs (single)
	deltaLfMulti      [][]uint16     // [4][DELTA_LF_SMALL+2] delta_lf_abs (per-plane/dir)
	segmentId         [][]uint16     // [3][MAX_SEGMENTS+1] spatial segment_id
	segIdPredicted    [][]uint16     // [3][2] inter seg_id_predicted
	partitionW8       [][]uint16     // [ctx][5]
	partitionW16      [][]uint16     // [ctx][11]
	partitionW32      [][]uint16     // [ctx][11]
	partitionW64      [][]uint16     // [ctx][11]
	partitionW128     [][]uint16     // [ctx][9]
	intraFrameYMode   [][][]uint16   // [above][left][14]
	uvModeCflNotAllow [][]uint16     // [yMode][14]
	uvModeCflAllow    [][]uint16     // [yMode][15]
	txbSkip           [][][]uint16   // [txSzCtx][ctx][3]
	eobPt1024         [][]uint16     // [ptype][12]
	coeffBaseEob      [][][][]uint16 // [txSzCtx][ptype][ctx][4]
	coeffBr           [][][][]uint16 // [txSzCtx<=3][ptype][ctx][5]
	dcSign            [][][]uint16   // [ptype][ctx][3]
	coeffBase         [][][][]uint16 // [txSzCtx][ptype][ctx][4]
	eobExtra          [][][][]uint16 // [txSzCtx][ptype][eobPt-3][3]
	paletteYMode      [][][]uint16   // [bsizeCtx][ctx][3]
	paletteUvMode     [][]uint16     // [ctx][3]
	paletteYSize      [][]uint16     // [bsizeCtx][PALETTE_SIZES+1]
	paletteUvSize     [][]uint16     // [bsizeCtx][PALETTE_SIZES+1]
	paletteYColor     [9][][]uint16  // [paletteSize 2..8][ctx][size+1]
	paletteUvColor    [9][][]uint16  // [paletteSize 2..8][ctx][size+1]
	filterIntra       [][]uint16     // [miSize][3]
	filterIntraMode   []uint16       // [6]
	intraTxTypeSet1   [][][]uint16   // [txSzSqr][intraDir][8]
	intraTxTypeSet2   [][][]uint16   // [txSzSqr][intraDir][6]
	eobPt16           [][][]uint16   // [ptype][ctx][6]
	eobPt32           [][][]uint16   // [ptype][ctx][7]
	eobPt64           [][][]uint16   // [ptype][ctx][8]
	eobPt128          [][][]uint16   // [ptype][ctx][9]
	eobPt256          [][][]uint16   // [ptype][ctx][10]
	eobPt512          [][]uint16     // [ptype][11]
	angleDelta        [][]uint16     // [mode-V_PRED][8]
	tx8x8             [][]uint16     // [ctx][2]
	tx16x16           [][]uint16     // [ctx][3]
	tx32x32           [][]uint16     // [ctx][3]
	tx64x64           [][]uint16     // [ctx][3]
	cflSign           []uint16       // [9]
	cflAlpha          [][]uint16     // [ctx][17]
	useWiener         []uint16       // [3]
	useSgrproj        []uint16       // [3]
	restorationType   []uint16       // [4]
	// Inter prediction.
	isInter         [][]uint16     // [ctx][3]
	skipMode        [][]uint16     // [ctx][3]
	singleRef       [][][]uint16   // [ctx][p][3]
	newMv           [][]uint16     // [NewMvContext][3]
	zeroMv          [][]uint16     // [ZeroMvContext][3]
	refMvCdf        [][]uint16     // [RefMvContext][3]
	drlMode         [][]uint16     // [ctx][3]
	mvJoint         [][]uint16     // [mvCtx][5]
	mvSign          [][][]uint16   // [mvCtx][comp][3]
	mvClass         [][][]uint16   // [mvCtx][comp][12]
	mvClass0Bit     [][][]uint16   // [mvCtx][comp][3]
	mvClass0Fr      [][][][]uint16 // [mvCtx][comp][cls0bit][5]
	mvClass0Hp      [][][]uint16   // [mvCtx][comp][3]
	mvBit           [][][][]uint16 // [mvCtx][comp][i][3]
	mvFr            [][][]uint16   // [mvCtx][comp][5]
	mvHp            [][][]uint16   // [mvCtx][comp][3]
	txfmSplit       [][]uint16     // [ctx][3]
	interTxType1    [][]uint16     // [txSzSqr][17]
	interTxType2    []uint16       // [13]
	interTxType3    [][]uint16     // [txSzSqr][3]
	useObmc         [][]uint16     // [miSize][3]
	motionModeCdf   [][]uint16     // [miSize][4]
	compMode        [][]uint16     // [ctx][3]
	compRefType     [][]uint16     // [ctx][3]
	uniCompRef      [][][]uint16   // [ctx][p][3]
	compRef         [][][]uint16   // [ctx][p][3]
	compBwdRef      [][][]uint16   // [ctx][p][3]
	compoundMode    [][]uint16     // [ctx][9]
	compGroupIdx    [][]uint16     // [ctx][3]
	compoundIdx     [][]uint16     // [ctx][3]
	yMode           [][]uint16     // [sizeGroup][14]
	compoundType    [][]uint16     // [miSize][3]
	wedgeIdx        [][]uint16     // [miSize][17]
	interIntra      [][]uint16     // [sizeGroup-1][3]
	interIntraMode  [][]uint16     // [sizeGroup-1][5]
	wedgeInterIntra [][]uint16     // [miSize][3]
	interpFilter    [][]uint16     // [ctx][4]
}

// newCDFContext loads the default CDFs. qIdx selects the coeff CDF quantizer
// defaultIntrabcCdf is Default_Intrabc_Cdf (AV1 spec §, use_intrabc).
var defaultIntrabcCdf = []uint16{30531, 32768, 0}

// context (init_coeff_cdfs) for the txb_skip table.
func newCDFContext(baseQIdx int) *cdfContext {
	c := &cdfContext{
		skip:    clone2(cdf.DefaultSkipCdf),
		intrabc: append([]uint16(nil), defaultIntrabcCdf...),
		deltaQ:  []uint16{28160, 32120, 32677, 32768, 0},
		deltaLf: []uint16{28160, 32120, 32677, 32768, 0},
		deltaLfMulti: [][]uint16{
			{28160, 32120, 32677, 32768, 0}, {28160, 32120, 32677, 32768, 0},
			{28160, 32120, 32677, 32768, 0}, {28160, 32120, 32677, 32768, 0},
		},
		segmentId: [][]uint16{
			{5622, 7893, 16093, 18233, 27809, 28373, 32533, 32768, 0},
			{14274, 18230, 22557, 24935, 29980, 30851, 32344, 32768, 0},
			{27527, 28487, 28723, 28890, 32397, 32647, 32679, 32768, 0},
		},
		segIdPredicted: [][]uint16{
			{16384, 32768, 0}, {16384, 32768, 0}, {16384, 32768, 0},
		},
		partitionW8:       clone2(cdf.DefaultPartitionW8Cdf),
		partitionW16:      clone2(cdf.DefaultPartitionW16Cdf),
		partitionW32:      clone2(cdf.DefaultPartitionW32Cdf),
		partitionW64:      clone2(cdf.DefaultPartitionW64Cdf),
		partitionW128:     clone2(cdf.DefaultPartitionW128Cdf),
		intraFrameYMode:   clone3(cdf.DefaultIntraFrameYModeCdf),
		uvModeCflNotAllow: clone2(cdf.DefaultUvModeCflNotAllowedCdf),
		uvModeCflAllow:    clone2(cdf.DefaultUvModeCflAllowedCdf),
		txbSkip:           clone3(cdf.DefaultTxbSkipCdf[coeffQIdx(baseQIdx)]),
		eobPt1024:         clone2(cdf.DefaultEobPt1024Cdf[coeffQIdx(baseQIdx)]),
		coeffBaseEob:      clone4(cdf.DefaultCoeffBaseEobCdf[coeffQIdx(baseQIdx)]),
		coeffBr:           clone4(cdf.DefaultCoeffBrCdf[coeffQIdx(baseQIdx)]),
		dcSign:            clone3(cdf.DefaultDcSignCdf[coeffQIdx(baseQIdx)]),
		coeffBase:         clone4(cdf.DefaultCoeffBaseCdf[coeffQIdx(baseQIdx)]),
		eobExtra:          clone4(cdf.DefaultEobExtraCdf[coeffQIdx(baseQIdx)]),
		paletteYMode:      clone3(cdf.DefaultPaletteYModeCdf),
		paletteUvMode:     clone2(cdf.DefaultPaletteUvModeCdf),
		paletteYSize:      clone2(cdf.DefaultPaletteYSizeCdf),
		paletteUvSize:     clone2(cdf.DefaultPaletteUvSizeCdf),
		filterIntra:       clone2(cdf.DefaultFilterIntraCdf),
		filterIntraMode:   append([]uint16(nil), cdf.DefaultFilterIntraModeCdf...),
		intraTxTypeSet1:   clone3(cdf.DefaultIntraTxTypeSet1Cdf),
		intraTxTypeSet2:   clone3(cdf.DefaultIntraTxTypeSet2Cdf),
		eobPt16:           clone3(cdf.DefaultEobPt16Cdf[coeffQIdx(baseQIdx)]),
		eobPt32:           clone3(cdf.DefaultEobPt32Cdf[coeffQIdx(baseQIdx)]),
		eobPt64:           clone3(cdf.DefaultEobPt64Cdf[coeffQIdx(baseQIdx)]),
		eobPt128:          clone3(cdf.DefaultEobPt128Cdf[coeffQIdx(baseQIdx)]),
		eobPt256:          clone3(cdf.DefaultEobPt256Cdf[coeffQIdx(baseQIdx)]),
		eobPt512:          clone2(cdf.DefaultEobPt512Cdf[coeffQIdx(baseQIdx)]),
		angleDelta:        clone2(cdf.DefaultAngleDeltaCdf),
		tx8x8:             clone2(cdf.DefaultTx8x8Cdf),
		tx16x16:           clone2(cdf.DefaultTx16x16Cdf),
		tx32x32:           clone2(cdf.DefaultTx32x32Cdf),
		tx64x64:           clone2(cdf.DefaultTx64x64Cdf),
		cflSign:           append([]uint16(nil), cdf.DefaultCflSignCdf...),
		cflAlpha:          clone2(cdf.DefaultCflAlphaCdf),
		useWiener:         append([]uint16(nil), cdf.DefaultUseWienerCdf...),
		useSgrproj:        append([]uint16(nil), cdf.DefaultUseSgrprojCdf...),
		restorationType:   append([]uint16(nil), cdf.DefaultRestorationTypeCdf...),
		isInter:           clone2(cdf.DefaultIsInterCdf),
		skipMode:          clone2(cdf.DefaultSkipModeCdf),
		singleRef:         clone3(cdf.DefaultSingleRefCdf),
		newMv:             clone2(cdf.DefaultNewMvCdf),
		zeroMv:            clone2(cdf.DefaultZeroMvCdf),
		refMvCdf:          clone2(cdf.DefaultRefMvCdf),
		drlMode:           clone2(cdf.DefaultDrlModeCdf),
		txfmSplit:         clone2(cdf.DefaultTxfmSplitCdf),
		interTxType1:      clone2(cdf.DefaultInterTxTypeSet1Cdf),
		interTxType2:      append([]uint16(nil), cdf.DefaultInterTxTypeSet2Cdf...),
		interTxType3:      clone2(cdf.DefaultInterTxTypeSet3Cdf),
		useObmc:           clone2(cdf.DefaultUseObmcCdf),
		motionModeCdf:     clone2(cdf.DefaultMotionModeCdf),
		compMode:          clone2(cdf.DefaultCompModeCdf),
		compRefType:       clone2(cdf.DefaultCompRefTypeCdf),
		uniCompRef:        clone3(cdf.DefaultUniCompRefCdf),
		compRef:           clone3(cdf.DefaultCompRefCdf),
		compBwdRef:        clone3(cdf.DefaultCompBwdRefCdf),
		compoundMode:      clone2(cdf.DefaultCompoundModeCdf),
		compGroupIdx:      clone2(cdf.DefaultCompGroupIdxCdf),
		compoundIdx:       clone2(cdf.DefaultCompoundIdxCdf),
		yMode:             clone2(cdf.DefaultYModeCdf),
		compoundType:      clone2(cdf.DefaultCompoundTypeCdf),
		wedgeIdx:          clone2(cdf.DefaultWedgeIndexCdf),
		interpFilter:      clone2(cdf.DefaultInterpFilterCdf),
		interIntra:        clone2(cdf.DefaultInterIntraCdf),
		interIntraMode:    clone2(cdf.DefaultInterIntraModeCdf),
		wedgeInterIntra:   clone2(cdf.DefaultWedgeInterIntraCdf),
	}
	c.paletteYColor[2] = clone2(cdf.DefaultPaletteSize2YColorCdf)
	c.paletteYColor[3] = clone2(cdf.DefaultPaletteSize3YColorCdf)
	c.paletteYColor[4] = clone2(cdf.DefaultPaletteSize4YColorCdf)
	c.paletteYColor[5] = clone2(cdf.DefaultPaletteSize5YColorCdf)
	c.paletteYColor[6] = clone2(cdf.DefaultPaletteSize6YColorCdf)
	c.paletteYColor[7] = clone2(cdf.DefaultPaletteSize7YColorCdf)
	c.paletteYColor[8] = clone2(cdf.DefaultPaletteSize8YColorCdf)
	c.paletteUvColor[2] = clone2(cdf.DefaultPaletteSize2UvColorCdf)
	c.paletteUvColor[3] = clone2(cdf.DefaultPaletteSize3UvColorCdf)
	c.paletteUvColor[4] = clone2(cdf.DefaultPaletteSize4UvColorCdf)
	c.paletteUvColor[5] = clone2(cdf.DefaultPaletteSize5UvColorCdf)
	c.paletteUvColor[6] = clone2(cdf.DefaultPaletteSize6UvColorCdf)
	c.paletteUvColor[7] = clone2(cdf.DefaultPaletteSize7UvColorCdf)
	c.paletteUvColor[8] = clone2(cdf.DefaultPaletteSize8UvColorCdf)
	c.buildMvCdfs()
	return c
}

// buildMvCdfs constructs the per-context (NMV context) / per-component motion
// vector CDFs by replicating the spec default templates (AV1 spec §8.3). There
// are two NMV contexts (normal and intra-block-copy).
func (c *cdfContext) buildMvCdfs() {
	const mvCtxs, comps = 2, 2
	dup := func(s []uint16) []uint16 { return append([]uint16(nil), s...) }
	c.mvJoint = make([][]uint16, mvCtxs)
	c.mvSign = make([][][]uint16, mvCtxs)
	c.mvClass = make([][][]uint16, mvCtxs)
	c.mvClass0Bit = make([][][]uint16, mvCtxs)
	c.mvClass0Fr = make([][][][]uint16, mvCtxs)
	c.mvClass0Hp = make([][][]uint16, mvCtxs)
	c.mvBit = make([][][][]uint16, mvCtxs)
	c.mvFr = make([][][]uint16, mvCtxs)
	c.mvHp = make([][][]uint16, mvCtxs)
	for m := 0; m < mvCtxs; m++ {
		c.mvJoint[m] = dup(cdf.DefaultMvJointCdf)
		c.mvSign[m] = make([][]uint16, comps)
		c.mvClass[m] = make([][]uint16, comps)
		c.mvClass0Bit[m] = make([][]uint16, comps)
		c.mvClass0Fr[m] = make([][][]uint16, comps)
		c.mvClass0Hp[m] = make([][]uint16, comps)
		c.mvBit[m] = make([][][]uint16, comps)
		c.mvFr[m] = make([][]uint16, comps)
		c.mvHp[m] = make([][]uint16, comps)
		for comp := 0; comp < comps; comp++ {
			c.mvSign[m][comp] = dup(cdf.DefaultMvSignCdf)
			c.mvClass[m][comp] = dup(cdf.DefaultMvClassCdf[comp])
			c.mvClass0Bit[m][comp] = dup(cdf.DefaultMvClass0BitCdf)
			c.mvClass0Fr[m][comp] = clone2(cdf.DefaultMvClass0FrCdf[comp])
			c.mvClass0Hp[m][comp] = dup(cdf.DefaultMvClass0HpCdf)
			c.mvBit[m][comp] = clone2(cdf.DefaultMvBitCdf)
			c.mvFr[m][comp] = dup(cdf.DefaultMvFrCdf[comp])
			c.mvHp[m][comp] = dup(cdf.DefaultMvHpCdf)
		}
	}
}

func clone4(src [][][][]uint16) [][][][]uint16 {
	out := make([][][][]uint16, len(src))
	for i := range src {
		out[i] = clone3(src[i])
	}
	return out
}

// coeffQIdx maps base_q_idx to the coeff CDF quantizer context (AV1 spec §8.3,
// init_coeff_cdfs).
func coeffQIdx(baseQIdx int) int {
	switch {
	case baseQIdx <= 20:
		return 0
	case baseQIdx <= 60:
		return 1
	case baseQIdx <= 120:
		return 2
	default:
		return 3
	}
}

func clone2(src [][]uint16) [][]uint16 {
	out := make([][]uint16, len(src))
	for i := range src {
		out[i] = append([]uint16(nil), src[i]...)
	}
	return out
}

func clone3(src [][][]uint16) [][][]uint16 {
	out := make([][][]uint16, len(src))
	for i := range src {
		out[i] = clone2(src[i])
	}
	return out
}

func dup1(s []uint16) []uint16 { return append([]uint16(nil), s...) }

// clone deep-copies the CDF context so a frame can adapt its own copy while the
// saved frame context (for primary_ref_frame loading) stays intact (AV1 spec §8.3).
func (c *cdfContext) clone() *cdfContext {
	n := *c
	n.skip = clone2(c.skip)
	n.intrabc = append([]uint16(nil), c.intrabc...)
	n.deltaQ = append([]uint16(nil), c.deltaQ...)
	n.deltaLf = append([]uint16(nil), c.deltaLf...)
	n.deltaLfMulti = clone2(c.deltaLfMulti)
	n.segmentId = clone2(c.segmentId)
	n.segIdPredicted = clone2(c.segIdPredicted)
	n.partitionW8 = clone2(c.partitionW8)
	n.partitionW16 = clone2(c.partitionW16)
	n.partitionW32 = clone2(c.partitionW32)
	n.partitionW64 = clone2(c.partitionW64)
	n.partitionW128 = clone2(c.partitionW128)
	n.intraFrameYMode = clone3(c.intraFrameYMode)
	n.uvModeCflNotAllow = clone2(c.uvModeCflNotAllow)
	n.uvModeCflAllow = clone2(c.uvModeCflAllow)
	n.txbSkip = clone3(c.txbSkip)
	n.eobPt1024 = clone2(c.eobPt1024)
	n.coeffBaseEob = clone4(c.coeffBaseEob)
	n.coeffBr = clone4(c.coeffBr)
	n.dcSign = clone3(c.dcSign)
	n.coeffBase = clone4(c.coeffBase)
	n.eobExtra = clone4(c.eobExtra)
	n.paletteYMode = clone3(c.paletteYMode)
	n.paletteUvMode = clone2(c.paletteUvMode)
	n.paletteYSize = clone2(c.paletteYSize)
	n.paletteUvSize = clone2(c.paletteUvSize)
	for i := 2; i <= 8; i++ {
		n.paletteYColor[i] = clone2(c.paletteYColor[i])
		n.paletteUvColor[i] = clone2(c.paletteUvColor[i])
	}
	n.filterIntra = clone2(c.filterIntra)
	n.filterIntraMode = dup1(c.filterIntraMode)
	n.intraTxTypeSet1 = clone3(c.intraTxTypeSet1)
	n.intraTxTypeSet2 = clone3(c.intraTxTypeSet2)
	n.eobPt16 = clone3(c.eobPt16)
	n.eobPt32 = clone3(c.eobPt32)
	n.eobPt64 = clone3(c.eobPt64)
	n.eobPt128 = clone3(c.eobPt128)
	n.eobPt256 = clone3(c.eobPt256)
	n.eobPt512 = clone2(c.eobPt512)
	n.angleDelta = clone2(c.angleDelta)
	n.tx8x8 = clone2(c.tx8x8)
	n.tx16x16 = clone2(c.tx16x16)
	n.tx32x32 = clone2(c.tx32x32)
	n.tx64x64 = clone2(c.tx64x64)
	n.cflSign = dup1(c.cflSign)
	n.cflAlpha = clone2(c.cflAlpha)
	n.useWiener = dup1(c.useWiener)
	n.useSgrproj = dup1(c.useSgrproj)
	n.restorationType = dup1(c.restorationType)
	n.isInter = clone2(c.isInter)
	n.skipMode = clone2(c.skipMode)
	n.singleRef = clone3(c.singleRef)
	n.newMv = clone2(c.newMv)
	n.zeroMv = clone2(c.zeroMv)
	n.refMvCdf = clone2(c.refMvCdf)
	n.drlMode = clone2(c.drlMode)
	n.mvJoint = clone2(c.mvJoint)
	n.mvSign = clone3(c.mvSign)
	n.mvClass = clone3(c.mvClass)
	n.mvClass0Bit = clone3(c.mvClass0Bit)
	n.mvClass0Fr = clone4(c.mvClass0Fr)
	n.mvClass0Hp = clone3(c.mvClass0Hp)
	n.mvBit = clone4(c.mvBit)
	n.mvFr = clone3(c.mvFr)
	n.mvHp = clone3(c.mvHp)
	n.txfmSplit = clone2(c.txfmSplit)
	n.interTxType1 = clone2(c.interTxType1)
	n.interTxType2 = dup1(c.interTxType2)
	n.interTxType3 = clone2(c.interTxType3)
	n.useObmc = clone2(c.useObmc)
	n.motionModeCdf = clone2(c.motionModeCdf)
	n.compMode = clone2(c.compMode)
	n.compRefType = clone2(c.compRefType)
	n.uniCompRef = clone3(c.uniCompRef)
	n.compRef = clone3(c.compRef)
	n.compBwdRef = clone3(c.compBwdRef)
	n.compoundMode = clone2(c.compoundMode)
	n.compGroupIdx = clone2(c.compGroupIdx)
	n.compoundIdx = clone2(c.compoundIdx)
	n.yMode = clone2(c.yMode)
	n.compoundType = clone2(c.compoundType)
	n.wedgeIdx = clone2(c.wedgeIdx)
	n.interpFilter = clone2(c.interpFilter)
	n.interIntra = clone2(c.interIntra)
	n.interIntraMode = clone2(c.interIntraMode)
	n.wedgeInterIntra = clone2(c.wedgeInterIntra)
	return &n
}

func reset1(s []uint16) {
	if len(s) > 0 {
		s[len(s)-1] = 0
	}
}
func reset2(s [][]uint16) {
	for _, x := range s {
		reset1(x)
	}
}
func reset3(s [][][]uint16) {
	for _, x := range s {
		reset2(x)
	}
}
func reset4(s [][][][]uint16) {
	for _, x := range s {
		reset3(x)
	}
}

// resetCounts zeroes the symbol counter (last entry) of every CDF, matching
// libaom's av1_reset_cdf_symbol_counters: when a frame context is saved for later
// frames to load, the adaptation counters are reset so the loading frame restarts
// the rate schedule from zero (AV1 spec §7.4 / refresh process).
func (c *cdfContext) resetCounts() {
	reset2(c.skip)
	reset1(c.intrabc)
	reset1(c.deltaQ)
	reset1(c.deltaLf)
	reset2(c.deltaLfMulti)
	reset2(c.segmentId)
	reset2(c.segIdPredicted)
	reset2(c.partitionW8)
	reset2(c.partitionW16)
	reset2(c.partitionW32)
	reset2(c.partitionW64)
	reset2(c.partitionW128)
	reset3(c.intraFrameYMode)
	reset2(c.uvModeCflNotAllow)
	reset2(c.uvModeCflAllow)
	reset3(c.txbSkip)
	reset2(c.eobPt1024)
	reset4(c.coeffBaseEob)
	reset4(c.coeffBr)
	reset3(c.dcSign)
	reset4(c.coeffBase)
	reset4(c.eobExtra)
	reset3(c.paletteYMode)
	reset2(c.paletteUvMode)
	reset2(c.paletteYSize)
	reset2(c.paletteUvSize)
	for i := 2; i <= 8; i++ {
		reset2(c.paletteYColor[i])
		reset2(c.paletteUvColor[i])
	}
	reset2(c.filterIntra)
	reset1(c.filterIntraMode)
	reset3(c.intraTxTypeSet1)
	reset3(c.intraTxTypeSet2)
	reset3(c.eobPt16)
	reset3(c.eobPt32)
	reset3(c.eobPt64)
	reset3(c.eobPt128)
	reset3(c.eobPt256)
	reset2(c.eobPt512)
	reset2(c.angleDelta)
	reset2(c.tx8x8)
	reset2(c.tx16x16)
	reset2(c.tx32x32)
	reset2(c.tx64x64)
	reset1(c.cflSign)
	reset2(c.cflAlpha)
	reset1(c.useWiener)
	reset1(c.useSgrproj)
	reset1(c.restorationType)
	reset2(c.isInter)
	reset2(c.skipMode)
	reset3(c.singleRef)
	reset2(c.newMv)
	reset2(c.zeroMv)
	reset2(c.refMvCdf)
	reset2(c.drlMode)
	reset2(c.mvJoint)
	reset3(c.mvSign)
	reset3(c.mvClass)
	reset3(c.mvClass0Bit)
	reset4(c.mvClass0Fr)
	reset3(c.mvClass0Hp)
	reset4(c.mvBit)
	reset3(c.mvFr)
	reset3(c.mvHp)
	reset2(c.txfmSplit)
	reset2(c.interTxType1)
	reset1(c.interTxType2)
	reset2(c.interTxType3)
	reset2(c.useObmc)
	reset2(c.motionModeCdf)
	reset2(c.compMode)
	reset2(c.compRefType)
	reset3(c.uniCompRef)
	reset3(c.compRef)
	reset3(c.compBwdRef)
	reset2(c.compoundMode)
	reset2(c.compGroupIdx)
	reset2(c.compoundIdx)
	reset2(c.yMode)
	reset2(c.compoundType)
	reset2(c.wedgeIdx)
	reset2(c.interpFilter)
	reset2(c.interIntra)
	reset2(c.interIntraMode)
	reset2(c.wedgeInterIntra)
}
