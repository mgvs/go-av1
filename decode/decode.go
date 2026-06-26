package decode

import (
	"fmt"

	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/msac"
	"github.com/mgvs/go-av1/predict"
	"github.com/mgvs/go-av1/tile"
)

// ErrUnsupported marks a block that needs decoding features not yet implemented
// (non-DC intra prediction, non-zero residual coefficients, tx_mode select, etc.).
// The matching-frame milestone deliberately covers only DC/skip/all-zero blocks.
type ErrUnsupported struct{ What string }

func (e ErrUnsupported) Error() string { return "decode: unsupported (needs M4/M5): " + e.What }

// Frame holds the reconstructed planes (16-bit samples; luma is plane 0).
var SkipFilters = false

type Frame struct {
	Planes    []*predict.Plane
	NumPlanes int
	SubX      int
	SubY      int
	BitDepth  int
	// Trace records the decoded entropy symbols (name=value) in order, for
	// cross-checking against the libaom inspect oracle.
	Trace []string
	// Per-4x4 motion field, retained for temporal MV projection by later frames.
	mvs         [][]MV
	refFrame0   [][]int
	mfMvs       [][]MV      // filtered motion-field MVs (AV1 spec §7.19)
	mfRefFrames [][]int     // filtered motion-field reference frames
	cdfs        *cdfContext // saved frame CDF context (for primary_ref_frame loading)
	segmentIds  [][]int     // saved segmentation map (for temporal prediction)
}

type frameDecoder struct {
	seq *header.SequenceHeader
	fh  *header.FrameHeader
	d   *msac.Decoder
	c   *cdfContext

	planes         []*predict.Plane
	numPlanes      int
	subX, subY     int
	bitDepth       int
	refs           []*RefFrame // reference frame store (nil for intra-only)
	mvs            [][]MV      // per-4x4 motion field (this frame)
	refFrames0     [][]int     // per-4x4 primary reference frame
	mfMvs          [][]MV      // filtered motion-field MVs (spec §7.19)
	mfRefFrames    [][]int     // filtered motion-field reference frames
	motionFieldMvs [][][]MV    // [ref][y8][x8] temporal projected motion field
	// Inter neighbor grids (per 4x4): two MV lists + two reference frames.
	gridMvs           [][][2]MV
	gridRefFrames     [][][2]int
	gridInterpFilters [][][2]int
	miWrittenGrid     [][]bool
	isInters          [][]bool

	// Per-block find_mv_stack state.
	refFrame      [2]int
	globalMvs     [2]MV
	refStackMv    [maxRefMvStackSize][2]MV
	weightStack   [maxRefMvStackSize]int
	numMvFound    int
	newMvCount    int
	foundMatch    int
	closeMatches  int
	totalMatches  int
	newMvContext  int
	refMvContext  int
	zeroMvContext int
	drlCtxStack   [maxRefMvStackSize]int
	// Compound extra-search accumulators (AV1 spec §7.10.2.12/13).
	refIdMvs     [2][2]MV
	refIdCount   [2]int
	refDiffMvs   [2][2]MV
	refDiffCount [2]int
	// Warped motion state (AV1 spec §7.10.4 / §7.11.3.5-8).
	numSamples        int
	numSamplesScanned int
	candList          [lsSamplesMax][4]int
	localWarpParams   [6]int
	localValid        bool

	miCols, miRows int
	// Neighbor state, indexed [miRow][miCol].
	miSizes [][]int
	yModes  [][]int
	uvModes [][]int
	skips   [][]int
	txSizes [][]int
	// lfTxSizes[plane][planeMiRow][planeMiCol] — transform size per 4x4 for deblocking.
	lfTxSizes [3][][]int
	cdefIdx   [][]int // per-64x64 CDEF parameter index (-1 = unset)
	txTypes   [][]int // per-4x4 transform type (for inter chroma derivation)
	// Loop restoration state.
	lrType      [3][][]int       // [plane][unitRow][unitCol]
	lrWiener    [3][][][2][3]int // [plane][unitRow][unitCol][pass][coeff]
	refLrWiener [3][2][3]int
	lrSgrSet    [3][][]int    // [plane][unitRow][unitCol]
	lrSgrXqd    [3][][][2]int // [plane][unitRow][unitCol][i]
	refSgrXqd   [3][2]int
	deblocked   []*predict.Plane // pre-CDEF (deblocked) snapshot for LR source

	// Tile mode-info bounds.
	miRowStart, miRowEnd, miColStart, miColEnd int

	// CDF frame-context management.
	frameInitialCdfs *cdfContext
	frameContextCdfs *cdfContext

	sbSize4 int
	// blockDecoded[plane][y+1][x+1] tracks which 4x4 positions in the current
	// superblock are reconstructed (for haveAboveRight/haveBelowLeft).
	blockDecoded [3][][]int

	// Coefficient level/DC sign contexts (AV1 spec §5.11.39). Above arrays span the
	// frame width; Left arrays span the frame height and are cleared per SB row.
	aboveLevelContext [3][]int
	aboveDcContext    [3][]int
	leftLevelContext  [3][]int
	leftDcContext     [3][]int

	trace []string

	// Current block.
	miRow, miCol, miSize       int
	availU, availL             bool
	availUChroma, availLChroma bool
	hasChroma                  bool
	yMode, uvMode, txSize      int
	angleDeltaY, angleDeltaUV  int
	useFilterIntra             bool
	filterIntraMode            int
	cflAlphaU, cflAlphaV       int
	maxLumaW, maxLumaH         int
	skip                       int
	// Delta-Q state (AV1 spec §5.11.36): CurrentQIndex is reset to base_q_idx at
	// each tile start and updated cumulatively per superblock; readDeltas gates
	// the per-superblock read.
	currentQIndex int
	readDeltas    bool
	// Per-superblock loop-filter deltas (AV1 spec §5.11.37). currentDeltaLF is
	// reset to 0 at each tile start; deltaLFGrid stores it per 4x4 for the loop
	// filter (allocated only when delta_lf_present).
	currentDeltaLF [4]int
	deltaLFGrid    [][][4]int16
	// Segmentation (AV1 spec §5.11.9/§5.11.31). segmentId is the current block's
	// id; segmentIds[miRow][miCol] holds the per-4x4 map for the loop filter and
	// later-frame temporal prediction (allocated when segmentation_enabled).
	segmentId           int
	segmentIds          [][]int
	prevSegmentIds      [][]int // primary-ref segment map (temporal prediction)
	aboveSegPredContext []int
	leftSegPredContext  []int
	// Inter block state.
	skipModeFlag            bool
	isInterFlag             bool
	leftRefFrame            [2]int
	aboveRefFrame           [2]int
	leftIntra, aboveIntra   bool
	leftSingle, aboveSingle bool
	compoundType            int
	compGroupIdxVal         int
	compoundIdxVal          int
	compGroupIdxs           [][]int
	compoundIdxs            [][]int
	mv, predMv              [2]MV
	refMvIdx                int
	motionMode              int
	interpFilter            [2]int
	wedgeIndex              int
	wedgeSign               int
	maskType                int
	wedgeMask               [][]int // luma blend weights for COMPOUND_WEDGE/DIFFWTD
	isInterIntra            bool
	interIntraMode          int
	wedgeInterIntra         bool
	skipModes               [][]int
	useIntrabc              bool
	// Palette mode state (AV1 spec §5.11.46-49).
	paletteSizeY, paletteSizeUV int
	paletteColorsY              [8]int
	paletteColorsU              [8]int
	paletteColorsV              [8]int
	colorMapY                   [][]int
	colorMapUV                  [][]int
	paletteSizesGrid            [2][][]int   // [plane 0=Y/1=UV][miRow][miCol]
	paletteColorsGrid           [2][][][]int // [plane][miRow][miCol] -> colors
}

// getFilterType returns the intra edge filter type for a plane: 1 if the block
// above or to the left uses a smooth prediction mode (AV1 spec §7.11.2.8).
func (fd *frameDecoder) getFilterType(plane int) int {
	availU, availL := fd.availU, fd.availL
	if plane > 0 {
		availU, availL = fd.availUChroma, fd.availLChroma
	}
	smooth := func(r, c int) bool {
		var m int
		if plane == 0 {
			m = fd.yModes[r][c]
		} else {
			// is_smooth returns 0 for an inter chroma neighbour (AV1 spec §7.11.2.8).
			if fd.gridRefFrames != nil && fd.gridRefFrames[r][c][0] > IntraFrame {
				return false
			}
			m = fd.uvModes[r][c]
		}
		return m >= predict.ModeSmoothStart && m <= predict.ModeSmoothEnd
	}
	if availU {
		r, c := fd.miRow-1, fd.miCol
		if plane > 0 {
			if fd.subX != 0 && fd.miCol&1 == 0 {
				c++
			}
			if fd.subY != 0 && fd.miRow&1 == 1 {
				r--
			}
		}
		if smooth(r, c) {
			return 1
		}
	}
	if availL {
		r, c := fd.miRow, fd.miCol-1
		if plane > 0 {
			if fd.subX != 0 && fd.miCol&1 == 1 {
				c--
			}
			if fd.subY != 0 && fd.miRow&1 == 0 {
				r++
			}
		}
		if smooth(r, c) {
			return 1
		}
	}
	return 0
}

// DecodeFrame reconstructs an intra keyframe from its tiles (AV1 spec §7.4/§7.11).
// Only the DC/skip/all-zero intra path is implemented; other content returns
// ErrUnsupported.
func DecodeFrame(seq *header.SequenceHeader, fh *header.FrameHeader, tiles []tile.Tile) (*Frame, error) {
	return decodeFrameInternal(seq, fh, tiles, nil)
}

// decodeFrameInternal reconstructs a frame; refs holds the reference frame store
// (nil for a standalone intra frame).
func decodeFrameInternal(seq *header.SequenceHeader, fh *header.FrameHeader, tiles []tile.Tile, refs []*RefFrame) (*Frame, error) {
	fd := &frameDecoder{
		seq: seq, fh: fh,
		numPlanes: seq.NumPlanes,
		subX:      seq.SubsamplingX,
		subY:      seq.SubsamplingY,
		bitDepth:  seq.BitDepth,
		miCols:    fh.MiCols,
		miRows:    fh.MiRows,
		refs:      refs,
	}
	// Allocate planes at frame resolution (luma) and subsampled chroma.
	fd.planes = make([]*predict.Plane, fd.numPlanes)
	// Allocate planes superblock-aligned so transforms that straddle the (non-SB-
	// aligned) frame edge reconstruct fully past the mi grid; chroma-from-luma then
	// reads the real reconstructed samples there. Width/Height stay at the mi grid.
	sbMi := 16
	if fd.seq.Use128x128Superblock {
		sbMi = 32
	}
	allocMiCols := ((fh.MiCols + sbMi - 1) / sbMi) * sbMi
	allocMiRows := ((fh.MiRows + sbMi - 1) / sbMi) * sbMi
	for p := 0; p < fd.numPlanes; p++ {
		sx, sy := 0, 0
		if p > 0 {
			sx, sy = fd.subX, fd.subY
		}
		// Decode at the mi-grid resolution (FrameWidth/Height rounded up to the
		// superblock grid); superres/crop produces the final UpscaledWidth output.
		fd.planes[p] = predict.NewPlaneAlloc((fh.MiCols*4+sx)>>sx, (fh.MiRows*4+sy)>>sy,
			(allocMiCols*4)>>sx, (allocMiRows*4)>>sy)
	}
	fd.miSizes = makeGrid(fd.miRows, fd.miCols)
	if fh.DeltaLfPresent {
		fd.deltaLFGrid = make([][][4]int16, fd.miRows)
		for r := range fd.deltaLFGrid {
			fd.deltaLFGrid[r] = make([][4]int16, fd.miCols)
		}
	}
	if fh.SegmentationEnabled {
		fd.segmentIds = makeGrid(fd.miRows, fd.miCols)
		fd.aboveSegPredContext = make([]int, fd.miCols)
		fd.leftSegPredContext = make([]int, fd.miRows)
	}
	fd.yModes = makeGrid(fd.miRows, fd.miCols)
	fd.uvModes = makeGrid(fd.miRows, fd.miCols)
	fd.skips = makeGrid(fd.miRows, fd.miCols)
	fd.txSizes = makeGrid(fd.miRows, fd.miCols)
	fd.txTypes = makeGrid(fd.miRows, fd.miCols)
	fd.refFrames0 = makeGrid(fd.miRows, fd.miCols) // 0 = INTRA_FRAME for an intra frame
	fd.skipModes = makeGrid(fd.miRows, fd.miCols)
	fd.compGroupIdxs = makeGrid(fd.miRows, fd.miCols)
	fd.compoundIdxs = makeGrid(fd.miRows, fd.miCols)
	fd.mvs = make([][]MV, fd.miRows)
	fd.gridMvs = make([][][2]MV, fd.miRows)
	fd.gridRefFrames = make([][][2]int, fd.miRows)
	fd.gridInterpFilters = make([][][2]int, fd.miRows)
	fd.miWrittenGrid = make([][]bool, fd.miRows)
	fd.isInters = make([][]bool, fd.miRows)
	for pl := 0; pl < 2; pl++ {
		fd.paletteSizesGrid[pl] = make([][]int, fd.miRows)
		fd.paletteColorsGrid[pl] = make([][][]int, fd.miRows)
	}
	for i := range fd.mvs {
		fd.mvs[i] = make([]MV, fd.miCols)
		fd.gridMvs[i] = make([][2]MV, fd.miCols)
		fd.gridRefFrames[i] = make([][2]int, fd.miCols)
		fd.gridInterpFilters[i] = make([][2]int, fd.miCols)
		fd.miWrittenGrid[i] = make([]bool, fd.miCols)
		fd.isInters[i] = make([]bool, fd.miCols)
		for pl := 0; pl < 2; pl++ {
			fd.paletteSizesGrid[pl][i] = make([]int, fd.miCols)
			fd.paletteColorsGrid[pl][i] = make([][]int, fd.miCols)
		}
		for j := range fd.gridRefFrames[i] {
			fd.gridRefFrames[i][j] = [2]int{IntraFrame, header.NoneFrame}
		}
	}
	for p := 0; p < fd.numPlanes; p++ {
		sy := 0
		sx := 0
		if p > 0 {
			sx, sy = fd.subX, fd.subY
		}
		fd.lfTxSizes[p] = makeGrid((fd.miRows>>sy)+1, (fd.miCols>>sx)+1)
	}
	fd.cdefIdx = makeGrid(fd.miRows, fd.miCols)
	for i := range fd.cdefIdx {
		for j := range fd.cdefIdx[i] {
			fd.cdefIdx[i][j] = -1
		}
	}
	if fh.UsesLr {
		for p := 0; p < fd.numPlanes; p++ {
			sx, sy := 0, 0
			if p > 0 {
				sx, sy = fd.subX, fd.subY
			}
			unitRows := countUnitsInFrame(fh.LoopRestorationSize[p], (fh.FrameHeight+sy)>>uint(sy))
			unitCols := countUnitsInFrame(fh.LoopRestorationSize[p], (fh.UpscaledWidth+sx)>>uint(sx))
			fd.lrType[p] = makeGrid(unitRows, unitCols)
			fd.lrWiener[p] = make([][][2][3]int, unitRows)
			fd.lrSgrSet[p] = makeGrid(unitRows, unitCols)
			fd.lrSgrXqd[p] = make([][][2]int, unitRows)
			for i := range fd.lrWiener[p] {
				fd.lrWiener[p][i] = make([][2][3]int, unitCols)
				fd.lrSgrXqd[p][i] = make([][2]int, unitCols)
			}
		}
	}

	// Load the frame's initial CDFs: fresh defaults, or the saved frame context of
	// the primary reference frame (AV1 spec §7.4, load_cdfs / setup_past_independence).
	if fh.PrimaryRefFrame == header.PrimaryRefNone {
		fd.frameInitialCdfs = newCDFContext(fh.BaseQIdx)
	} else {
		refIdx := fh.RefFrameIdx[fh.PrimaryRefFrame]
		if refs == nil || refIdx < 0 || refIdx >= len(refs) || refs[refIdx] == nil || refs[refIdx].cdfs == nil {
			return nil, ErrUnsupported{"primary_ref_frame CDFs unavailable"}
		}
		fd.frameInitialCdfs = refs[refIdx].cdfs
		// load_previous_segment_ids (AV1 spec §7.4): inherit the primary ref's
		// segmentation map when dimensions match, else leave nil (zeros).
		if fh.SegmentationEnabled && refs[refIdx].MiCols == fh.MiCols && refs[refIdx].MiRows == fh.MiRows {
			fd.prevSegmentIds = refs[refIdx].segmentIds
		}
	}

	// Temporal motion field projection (AV1 spec §7.9), used by find_mv_stack.
	if fh.UseRefFrameMvs {
		fd.motionFieldEstimation()
	}

	for i := range tiles {
		if err := fd.decodeTile(&tiles[i]); err != nil {
			return nil, err
		}
		// The saved frame context (frame_end_update_cdf) comes from the tile whose
		// index equals context_update_tile_id, not necessarily the last tile.
		if i == fh.ContextUpdateTileID {
			fd.frameContextCdfs = fd.c
		}
	}
	// segmentation_update_map == 0: the saved map is inherited unchanged from the
	// previous frame (AV1 spec §7.4 step 7).
	if fd.fh.SegmentationEnabled && !fd.fh.SegmentationUpdateMap && fd.prevSegmentIds != nil {
		for r := 0; r < fd.miRows; r++ {
			copy(fd.segmentIds[r], fd.prevSegmentIds[r])
		}
	}
	if !SkipFilters {
		fd.loopFilter()
		fd.cdef()
		fd.superresUpscale()
		fd.loopRestore()
	}

	// Save the frame's CDF context for later frames (AV1 spec §7.4, frame_end_update_cdf).
	frameCdfs := fd.frameContextCdfs
	if fh.DisableFrameEndUpdateCdf || frameCdfs == nil {
		frameCdfs = fd.frameInitialCdfs.clone()
	}
	// Reset adaptation counters before saving, matching libaom's
	// av1_reset_cdf_symbol_counters: a loading frame restarts the rate schedule.
	frameCdfs.resetCounts()
	fd.storeMotionField()
	return &Frame{
		Planes: fd.planes, NumPlanes: fd.numPlanes, SubX: fd.subX, SubY: fd.subY,
		BitDepth: fd.bitDepth, Trace: fd.trace, mvs: fd.mvs, refFrame0: fd.refFrames0,
		mfMvs: fd.mfMvs, mfRefFrames: fd.mfRefFrames,
		cdfs: frameCdfs, segmentIds: fd.segmentIds,
	}, nil
}

func (fd *frameDecoder) tr(format string, args ...any) {
	fd.trace = append(fd.trace, fmt.Sprintf(format, args...))
}

func makeGrid(rows, cols int) [][]int {
	g := make([][]int, rows)
	for i := range g {
		g[i] = make([]int, cols)
	}
	return g
}

func (fd *frameDecoder) decodeTile(t *tile.Tile) error {
	fd.d = msac.NewDecoder(t.Data, !fd.fh.DisableCdfUpdate)
	fd.c = fd.frameInitialCdfs.clone()
	fd.miRowStart, fd.miRowEnd = t.MiRowStart, t.MiRowEnd
	fd.miColStart, fd.miColEnd = t.MiColStart, t.MiColEnd

	sbSize := predict.Block64x64
	if fd.seq.Use128x128Superblock {
		sbSize = predict.Block128x128
	}
	fd.sbSize4 = predict.Num4x4BlocksWide[sbSize]
	fd.clearAboveContext()
	fd.resetRefLr()
	fd.currentQIndex = fd.fh.BaseQIdx
	fd.currentDeltaLF = [4]int{}
	for r := fd.miRowStart; r < fd.miRowEnd; r += fd.sbSize4 {
		fd.clearLeftContext()
		for c := fd.miColStart; c < fd.miColEnd; c += fd.sbSize4 {
			fd.clearBlockDecodedFlags(r, c)
			fd.readDeltas = fd.fh.DeltaQPresent
			fd.readLr(r, c, sbSize)
			if err := fd.decodePartition(r, c, sbSize); err != nil {
				return err
			}
		}
	}
	return nil
}

// clearAboveContext / clearLeftContext reset the coefficient level/DC contexts
// (AV1 spec §5.11.3). Above spans the frame width (per tile); Left spans the frame
// height and is reset per superblock row. A small pad avoids edge bounds checks.
func (fd *frameDecoder) clearAboveContext() {
	for p := 0; p < fd.numPlanes; p++ {
		subX := 0
		if p > 0 {
			subX = fd.subX
		}
		n := (fd.miCols >> subX) + 16
		fd.aboveLevelContext[p] = make([]int, n)
		fd.aboveDcContext[p] = make([]int, n)
	}
}

func (fd *frameDecoder) clearLeftContext() {
	for p := 0; p < fd.numPlanes; p++ {
		subY := 0
		if p > 0 {
			subY = fd.subY
		}
		n := (fd.miRows >> subY) + 16
		fd.leftLevelContext[p] = make([]int, n)
		fd.leftDcContext[p] = make([]int, n)
	}
}

// clearBlockDecodedFlags initializes the BlockDecoded grid for a superblock at
// (r,c) (AV1 spec §5.11.33). The grid is offset by +1 so index -1 is storable.
func (fd *frameDecoder) clearBlockDecodedFlags(r, c int) {
	for plane := 0; plane < fd.numPlanes; plane++ {
		subX, subY := 0, 0
		if plane > 0 {
			subX, subY = fd.subX, fd.subY
		}
		sbWidth4 := (fd.miColEnd - c) >> subX
		sbHeight4 := (fd.miRowEnd - r) >> subY
		w4 := fd.sbSize4 >> subX
		h4 := fd.sbSize4 >> subY
		grid := make([][]int, h4+2)
		for i := range grid {
			grid[i] = make([]int, w4+2)
		}
		for y := -1; y <= h4; y++ {
			for x := -1; x <= w4; x++ {
				v := 0
				if y < 0 && x < sbWidth4 {
					v = 1
				} else if x < 0 && y < sbHeight4 {
					v = 1
				}
				grid[y+1][x+1] = v
			}
		}
		grid[h4+1][0] = 0 // BlockDecoded[plane][sbSize4>>subY][-1] = 0
		fd.blockDecoded[plane] = grid
	}
}

func (fd *frameDecoder) bdGet(plane, y, x int) int {
	g := fd.blockDecoded[plane]
	if y+1 < 0 || y+1 >= len(g) || x+1 < 0 || x+1 >= len(g[0]) {
		return 0
	}
	return g[y+1][x+1]
}

func (fd *frameDecoder) bdSet(plane, y, x, v int) {
	g := fd.blockDecoded[plane]
	if y+1 >= 0 && y+1 < len(g) && x+1 >= 0 && x+1 < len(g[0]) {
		g[y+1][x+1] = v
	}
}

func (fd *frameDecoder) isInside(r, c int) bool {
	return c >= fd.miColStart && c < fd.miColEnd && r >= fd.miRowStart && r < fd.miRowEnd
}

func (fd *frameDecoder) decodePartition(r, c, bSize int) error {
	if r >= fd.miRows || c >= fd.miCols {
		return nil
	}
	availU := fd.isInside(r-1, c)
	availL := fd.isInside(r, c-1)
	num4x4 := predict.Num4x4BlocksWide[bSize]
	half := num4x4 >> 1
	quarter := half >> 1
	hasRows := (r + half) < fd.miRows
	hasCols := (c + half) < fd.miCols

	partition := predict.PartitionNone
	if bSize >= predict.Block8x8 {
		switch {
		case hasRows && hasCols:
			ctx := fd.partitionCtx(r, c, bSize, availU, availL)
			partition = fd.d.DecodeSymbol(fd.partitionCDF(bSize, ctx))
			fd.tr("partition(r%d,c%d,bs%d,ctx%d)=%d", r, c, bSize, ctx, partition)
		case hasCols:
			ctx := fd.partitionCtx(r, c, bSize, availU, availL)
			pcdf := fd.partitionCDF(bSize, ctx)
			cdf := splitGatherCDF(pcdf, bSize, true)
			if fd.d.DecodeSymbol(cdf) == 1 {
				partition = predict.PartitionSplit
			} else {
				partition = predict.PartitionHorz
			}
		case hasRows:
			ctx := fd.partitionCtx(r, c, bSize, availU, availL)
			pcdf := fd.partitionCDF(bSize, ctx)
			cdf := splitGatherCDF(pcdf, bSize, false)
			if fd.d.DecodeSymbol(cdf) == 1 {
				partition = predict.PartitionSplit
			} else {
				partition = predict.PartitionVert
			}
		default:
			partition = predict.PartitionSplit
		}
	}
	subSize := predict.PartitionSubsize[partition][bSize]
	splitSize := predict.PartitionSubsize[predict.PartitionSplit][bSize]
	_ = splitSize

	switch partition {
	case predict.PartitionNone:
		return fd.decodeBlock(r, c, subSize)
	case predict.PartitionHorz:
		if err := fd.decodeBlock(r, c, subSize); err != nil {
			return err
		}
		if hasRows {
			return fd.decodeBlock(r+half, c, subSize)
		}
		return nil
	case predict.PartitionVert:
		if err := fd.decodeBlock(r, c, subSize); err != nil {
			return err
		}
		if hasCols {
			return fd.decodeBlock(r, c+half, subSize)
		}
		return nil
	case predict.PartitionSplit:
		for _, rc := range [4][2]int{{r, c}, {r, c + half}, {r + half, c}, {r + half, c + half}} {
			if err := fd.decodePartition(rc[0], rc[1], subSize); err != nil {
				return err
			}
		}
		return nil
	case predict.PartitionHorzA:
		return fd.decodeBlocks([][3]int{{r, c, splitSize}, {r, c + half, splitSize}, {r + half, c, subSize}})
	case predict.PartitionHorzB:
		return fd.decodeBlocks([][3]int{{r, c, subSize}, {r + half, c, splitSize}, {r + half, c + half, splitSize}})
	case predict.PartitionVertA:
		return fd.decodeBlocks([][3]int{{r, c, splitSize}, {r + half, c, splitSize}, {r, c + half, subSize}})
	case predict.PartitionVertB:
		return fd.decodeBlocks([][3]int{{r, c, subSize}, {r, c + half, splitSize}, {r + half, c + half, splitSize}})
	case predict.PartitionHorz4:
		for k := 0; k < 4; k++ {
			rr := r + quarter*k
			if k == 3 && rr >= fd.miRows {
				break
			}
			if err := fd.decodeBlock(rr, c, subSize); err != nil {
				return err
			}
		}
		return nil
	default: // PartitionVert4
		for k := 0; k < 4; k++ {
			cc := c + quarter*k
			if k == 3 && cc >= fd.miCols {
				break
			}
			if err := fd.decodeBlock(r, cc, subSize); err != nil {
				return err
			}
		}
		return nil
	}
}

// decodeBlocks decodes a list of (row, col, size) sub-blocks for the extended
// partition types (AV1 spec §5.11.4).
func (fd *frameDecoder) decodeBlocks(blocks [][3]int) error {
	for _, b := range blocks {
		if err := fd.decodeBlock(b[0], b[1], b[2]); err != nil {
			return err
		}
	}
	return nil
}

func (fd *frameDecoder) partitionCtx(r, c, bSize int, availU, availL bool) int {
	bsl := predict.MiWidthLog2[bSize]
	above, left := 0, 0
	if availU && predict.MiWidthLog2[fd.miSizes[r-1][c]] < bsl {
		above = 1
	}
	if availL && predict.MiHeightLog2[fd.miSizes[r][c-1]] < bsl {
		left = 1
	}
	return left*2 + above
}

func (fd *frameDecoder) partitionCDF(bSize, ctx int) []uint16 {
	switch predict.MiWidthLog2[bSize] {
	case 1:
		return fd.c.partitionW8[ctx]
	case 2:
		return fd.c.partitionW16[ctx]
	case 3:
		return fd.c.partitionW32[ctx]
	case 4:
		return fd.c.partitionW64[ctx]
	default:
		return fd.c.partitionW128[ctx]
	}
}

// splitGatherCDF builds the 2-symbol CDF for split_or_horz (horz=true) or
// split_or_vert (horz=false) at a frame edge, by gathering the split-like
// partition probabilities from the full partition CDF (AV1 spec §9.3).
func splitGatherCDF(pcdf []uint16, bSize int, horz bool) []uint16 {
	prob := func(x int) int { return int(pcdf[x]) - int(pcdf[x-1]) }
	const (
		pHorz  = 1
		pVert  = 2
		pSplit = 3
		pHorzA = 4
		pHorzB = 5
		pVertA = 6
		pVertB = 7
		pHorz4 = 8
		pVert4 = 9
	)
	var psum int
	if horz { // split_or_horz: probabilities producing a vertical-like split
		psum = prob(pVert) + prob(pSplit) + prob(pHorzA) + prob(pVertA) + prob(pVertB)
		if bSize != predict.Block128x128 {
			psum += prob(pVert4)
		}
	} else { // split_or_vert: probabilities producing a horizontal-like split
		psum = prob(pHorz) + prob(pSplit) + prob(pHorzA) + prob(pHorzB) + prob(pVertA)
		if bSize != predict.Block128x128 {
			psum += prob(pHorz4)
		}
	}
	return []uint16{uint16((1 << 15) - psum), 1 << 15, 0}
}

// superresUpscale horizontally upscales the decoded frame from the coded width
// to UpscaledWidth (AV1 spec §7.16) and crops to FrameWidth/FrameHeight. When
// use_superres is off this is a plain crop of the mi-grid decode buffer.
func (fd *frameDecoder) superresUpscale() {
	fh := fd.fh
	r2 := func(x, n int) int {
		if n == 0 {
			return x
		}
		return (x + 1) >> 1
	}
	hi := (1 << uint(fd.bitDepth)) - 1
	up := func(src *predict.Plane, plane int) *predict.Plane {
		subX, subY := 0, 0
		if plane > 0 {
			subX, subY = fd.subX, fd.subY
		}
		downW := r2(fh.FrameWidth, subX)
		upW := r2(fh.UpscaledWidth, subX)
		planeH := r2(fh.FrameHeight, subY)
		dst := predict.NewPlane(upW, planeH)
		if fh.FrameWidth == fh.UpscaledWidth {
			for y := 0; y < planeH; y++ {
				for x := 0; x < upW; x++ {
					dst.Set(x, y, src.At(x, y))
				}
			}
			return dst
		}
		const scaleBits, extraBits, mask = 14, 8, (1 << 14) - 1
		stepX := ((downW << scaleBits) + (upW / 2)) / upW
		errv := (upW * stepX) - (downW << scaleBits)
		initialSubpelX := (-((upW-downW)<<(scaleBits-1))+upW/2)/upW + (1 << (extraBits - 1)) - errv/2
		initialSubpelX &= mask
		maxX := (fd.miCols>>uint(subX))*4 - 1
		for y := 0; y < planeH; y++ {
			for x := 0; x < upW; x++ {
				srcX := -(1 << scaleBits) + initialSubpelX + x*stepX
				srcXPx := srcX >> scaleBits
				srcXSubpel := (srcX & mask) >> extraBits
				sum := 0
				for k := 0; k < 8; k++ {
					sampleX := clip3i(0, maxX, srcXPx+(k-3))
					sum += int(src.At(sampleX, y)) * upscaleFilter[srcXSubpel][k]
				}
				dst.Set(x, y, uint16(clip3i(0, hi, round2(sum, 7))))
			}
		}
		return dst
	}
	for plane := 0; plane < fd.numPlanes; plane++ {
		fd.planes[plane] = up(fd.planes[plane], plane)
		if fd.deblocked != nil {
			fd.deblocked[plane] = up(fd.deblocked[plane], plane)
		}
	}
}
