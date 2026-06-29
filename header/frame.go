package header

import (
	"errors"

	"github.com/mgvs/go-av1/bits"
)

// Additional constants (AV1 spec §3) used by the frame header.
const (
	MaxTileWidth = 4096
	MaxTileArea  = 4096 * 2304
	MaxTileCols  = 64
	MaxTileRows  = 64

	RestorationTileSizeMax = 256

	RestoreNone       = 0
	RestoreWiener     = 1
	RestoreSgrproj    = 2
	RestoreSwitchable = 3

	// Interpolation filter.
	Switchable = 4

	// TX modes.
	Only4x4       = 0
	TxModeLargest = 1
	TxModeSelect  = 2
)

// remapLrType maps the 2-bit lr_type to the internal restoration enum.
var remapLrType = [4]int{RestoreNone, RestoreSwitchable, RestoreWiener, RestoreSgrproj}

// ErrInterUnsupported is returned for inter frames, which require reference-frame
// state and inter prediction (milestone M7).
var ErrInterUnsupported = errors.New("header: inter frames not yet supported (M7)")

// State is the cross-frame decoder state the frame header reads/updates: the
// reference-frame slots and current frame id (AV1 spec §7.20, §7.4).
type State struct {
	RefValid     [NumRefFrames]int
	RefOrderHint [NumRefFrames]int
	RefFrameType [NumRefFrames]int
	RefFrameID   [NumRefFrames]int
	// Saved frame dimensions for frame_size_with_refs / reference scaling (§5.9.7).
	RefUpscaledWidth [NumRefFrames]int
	RefFrameWidth    [NumRefFrames]int
	RefFrameHeight   [NumRefFrames]int
	RefRenderWidth   [NumRefFrames]int
	RefRenderHeight  [NumRefFrames]int
	// Saved per-frame state for load_previous (primary_ref_frame, §7.21).
	SavedGmParams             [NumRefFrames][TotalRefsPerFrame][6]int
	SavedLoopFilterRefDeltas  [NumRefFrames][TotalRefsPerFrame]int
	SavedLoopFilterModeDeltas [NumRefFrames][2]int
	SavedFilmGrain            [NumRefFrames]FilmGrainParams
	// Saved segmentation params for !update_data inheritance (§5.9.14).
	SavedFeatureEnabled [NumRefFrames][MaxSegments][SegLvlMax]bool
	SavedFeatureData    [NumRefFrames][MaxSegments][SegLvlMax]int

	CurrentFrameID int
}

// FrameHeader holds the parsed uncompressed_header (AV1 spec §5.9.2). Only the
// keyframe / intra-only path is fully populated for now.
type FrameHeader struct {
	ShowExistingFrame bool
	FrameToShowMapIdx int
	FrameType         int
	FrameIsIntra      bool
	ShowFrame         bool
	ShowableFrame     bool
	ErrorResilient    bool

	DisableCdfUpdate         bool
	AllowScreenContentTools  int
	ForceIntegerMV           int
	FrameSizeOverride        bool
	OrderHint                int
	PrimaryRefFrame          int
	RefreshFrameFlags        int
	AllowIntrabc             bool
	DisableFrameEndUpdateCdf bool

	// Geometry.
	FrameWidth    int
	FrameHeight   int
	UpscaledWidth int
	RenderWidth   int
	RenderHeight  int
	SuperresDenom int
	MiCols        int
	MiRows        int

	// Tiles.
	TileColsLog2        int
	TileRowsLog2        int
	TileCols            int
	TileRows            int
	MiColStarts         []int
	MiRowStarts         []int
	ContextUpdateTileID int
	TileSizeBytes       int

	// Quantization.
	BaseQIdx      int
	DeltaQYDc     int
	DeltaQUDc     int
	DeltaQUAc     int
	DeltaQVDc     int
	DeltaQVAc     int
	UsingQMatrix  bool
	QmY, QmU, QmV int

	// Segmentation.
	SegmentationEnabled        bool
	SegmentationUpdateMap      bool
	SegmentationTemporalUpdate bool
	SegmentationUpdateData     bool
	FeatureEnabled             [MaxSegments][SegLvlMax]bool
	FeatureData                [MaxSegments][SegLvlMax]int
	SegIdPreSkip               int
	LastActiveSegID            int

	// Delta q / lf.
	DeltaQPresent  bool
	DeltaQRes      int
	DeltaLfPresent bool
	DeltaLfRes     int
	DeltaLfMulti   bool

	// Loop filter.
	LoopFilterLevel        [4]int
	LoopFilterSharpness    int
	LoopFilterDeltaEnabled bool
	LoopFilterRefDeltas    [TotalRefsPerFrame]int
	LoopFilterModeDeltas   [2]int

	// CDEF.
	CdefDamping       int
	CdefBits          int
	CdefYPriStrength  [8]int
	CdefYSecStrength  [8]int
	CdefUVPriStrength [8]int
	CdefUVSecStrength [8]int

	// Loop restoration.
	FrameRestorationType [3]int
	LoopRestorationSize  [3]int
	UsesLr               bool

	TxMode          int
	ReferenceSelect bool
	ReducedTxSet    bool

	CodedLossless bool
	AllLossless   bool
	LosslessArray [MaxSegments]bool

	// Inter prediction (non-intra frames).
	RefFrameIdx            [RefsPerFrame]int
	AllowHighPrecisionMV   bool
	InterpolationFilter    int
	IsMotionModeSwitchable bool
	UseRefFrameMvs         bool
	OrderHints             [TotalRefsPerFrame]int
	RefFrameSignBias       [TotalRefsPerFrame]bool
	SkipModePresent        bool
	SkipModeFrame          [2]int
	AllowWarpedMotion      bool
	GmType                 [TotalRefsPerFrame]int
	GmParams               [TotalRefsPerFrame][6]int
	PrevGmParams           [TotalRefsPerFrame][6]int
	FilmGrain              *FilmGrainParams
}

// ParseFrameHeader parses an uncompressed frame header from an OBU payload of type
// FrameHeader or Frame (AV1 spec §5.9.2). seq is the active sequence header; st is
// the cross-frame state; temporalID/spatialID come from the OBU extension header.
// For Frame OBUs, the returned bit offset (in bytes, after byte alignment) marks
// where the tile group begins.
func ParseFrameHeader(seq *SequenceHeader, st *State, payload []byte, temporalID, spatialID int) (*FrameHeader, int, error) {
	r := bits.NewReader(payload)
	f := &FrameHeader{}

	idLen := 0
	if seq.FrameIDNumbersPresent {
		idLen = seq.AdditionalFrameIDLenMinus1 + seq.DeltaFrameIDLengthMinus2 + 3
	}
	allFrames := (1 << NumRefFrames) - 1

	if seq.ReducedStillPictureHeader {
		f.FrameType = KeyFrame
		f.FrameIsIntra = true
		f.ShowFrame = true
	} else {
		f.ShowExistingFrame = r.F(1) == 1
		if f.ShowExistingFrame {
			f.FrameToShowMapIdx = int(r.F(3))
			if seq.DecoderModelInfoPresent && !seq.EqualPictureInterval {
				// temporal_point_info() (AV1 spec §5.9.31).
				r.F(seq.FramePresentationTimeLengthMinus1 + 1)
			}
			if seq.FrameIDNumbersPresent {
				r.F(idLen)
			}
			f.FrameType = st.RefFrameType[f.FrameToShowMapIdx]
			return f, 0, nil
		}
		f.FrameType = int(r.F(2))
		f.FrameIsIntra = f.FrameType == IntraOnlyFrame || f.FrameType == KeyFrame
		f.ShowFrame = r.F(1) == 1
		if f.ShowFrame && seq.DecoderModelInfoPresent && !seq.EqualPictureInterval {
			// temporal_point_info(): frame_presentation_time (AV1 spec §5.9.31).
			r.F(seq.FramePresentationTimeLengthMinus1 + 1)
		}
		if f.ShowFrame {
			f.ShowableFrame = f.FrameType != KeyFrame
		} else {
			f.ShowableFrame = r.F(1) == 1
		}
		if f.FrameType == SwitchFrame || (f.FrameType == KeyFrame && f.ShowFrame) {
			f.ErrorResilient = true
		} else {
			f.ErrorResilient = r.F(1) == 1
		}
	}

	if f.FrameType == KeyFrame && f.ShowFrame {
		for i := 0; i < NumRefFrames; i++ {
			st.RefValid[i] = 0
			st.RefOrderHint[i] = 0
		}
	}

	f.DisableCdfUpdate = r.F(1) == 1
	if seq.SeqForceScreenContentTools == SelectScreenContentTools {
		f.AllowScreenContentTools = int(r.F(1))
	} else {
		f.AllowScreenContentTools = seq.SeqForceScreenContentTools
	}
	if f.AllowScreenContentTools != 0 {
		if seq.SeqForceIntegerMV == SelectIntegerMV {
			f.ForceIntegerMV = int(r.F(1))
		} else {
			f.ForceIntegerMV = seq.SeqForceIntegerMV
		}
	}
	if f.FrameIsIntra {
		f.ForceIntegerMV = 1
	}
	if seq.FrameIDNumbersPresent {
		st.CurrentFrameID = int(r.F(idLen))
	} else {
		st.CurrentFrameID = 0
	}
	if f.FrameType == SwitchFrame {
		f.FrameSizeOverride = true
	} else if seq.ReducedStillPictureHeader {
		f.FrameSizeOverride = false
	} else {
		f.FrameSizeOverride = r.F(1) == 1
	}
	f.OrderHint = int(r.F(seq.OrderHintBits))
	if f.FrameIsIntra || f.ErrorResilient {
		f.PrimaryRefFrame = PrimaryRefNone
	} else {
		f.PrimaryRefFrame = int(r.F(3))
	}
	if seq.DecoderModelInfoPresent {
		if r.F(1) == 1 { // buffer_removal_time_present_flag
			for opNum := 0; opNum <= seq.OperatingPointsCntMinus1; opNum++ {
				if !seq.DecoderModelPresentForOp[opNum] {
					continue
				}
				opPtIdc := seq.OperatingPointIdc[opNum]
				inTemporal := (opPtIdc >> uint(temporalID)) & 1
				inSpatial := (opPtIdc >> uint(spatialID+8)) & 1
				if opPtIdc == 0 || (inTemporal != 0 && inSpatial != 0) {
					r.F(seq.BufferRemovalTimeLengthMinus1 + 1) // buffer_removal_time[opNum]
				}
			}
		}
	}

	if f.FrameType == SwitchFrame || (f.FrameType == KeyFrame && f.ShowFrame) {
		f.RefreshFrameFlags = allFrames
	} else {
		f.RefreshFrameFlags = int(r.F(8))
	}
	if !f.FrameIsIntra || f.RefreshFrameFlags != allFrames {
		if f.ErrorResilient && seq.EnableOrderHint {
			for i := 0; i < NumRefFrames; i++ {
				r.F(seq.OrderHintBits) // ref_order_hint[i]
			}
		}
	}

	if f.FrameIsIntra {
		f.frameSize(r, seq)
		f.renderSize(r)
		if f.AllowScreenContentTools != 0 && f.UpscaledWidth == f.FrameWidth {
			f.AllowIntrabc = r.F(1) == 1
		}
	} else {
		if err := f.interRefs(r, seq, st, idLen); err != nil {
			return nil, 0, err
		}
	}

	if seq.ReducedStillPictureHeader || f.DisableCdfUpdate {
		f.DisableFrameEndUpdateCdf = true
	} else {
		f.DisableFrameEndUpdateCdf = r.F(1) == 1
	}
	if f.PrimaryRefFrame == PrimaryRefNone {
		f.setupPastIndependence()
	} else {
		f.loadPrevious(st)
	}

	f.tileInfo(r, seq)
	f.quantizationParams(r, seq)
	f.segmentationParams(r, st)
	f.deltaQParams(r)
	f.deltaLfParams(r)

	f.computeLossless()

	f.loopFilterParams(r, seq)
	f.cdefParams(r, seq)
	f.lrParams(r, seq)
	f.readTxMode(r)
	f.frameReferenceMode(r)
	f.skipModeParams(r, seq, st)
	if f.FrameIsIntra || f.ErrorResilient || !seq.EnableWarpedMotion {
		f.AllowWarpedMotion = false
	} else {
		f.AllowWarpedMotion = r.F(1) == 1
	}
	f.ReducedTxSet = r.F(1) == 1
	f.globalMotionParams(r)
	// film_grain_params: film_grain_params_present is false in target streams.
	if seq.FilmGrainParamsPresent {
		f.filmGrainParams(r, seq, st)
	}

	r.ByteAlign()
	return f, (r.Pos() + 7) / 8, nil
}

func (f *FrameHeader) frameSize(r *bits.Reader, seq *SequenceHeader) {
	if f.FrameSizeOverride {
		n := seq.FrameWidthBitsMinus1 + 1
		f.FrameWidth = int(r.F(n)) + 1
		n = seq.FrameHeightBitsMinus1 + 1
		f.FrameHeight = int(r.F(n)) + 1
	} else {
		f.FrameWidth = seq.MaxFrameWidthMinus1 + 1
		f.FrameHeight = seq.MaxFrameHeightMinus1 + 1
	}
	f.superresParams(r, seq)
	f.computeImageSize()
}

func (f *FrameHeader) superresParams(r *bits.Reader, seq *SequenceHeader) {
	useSuperres := false
	if seq.EnableSuperres {
		useSuperres = r.F(1) == 1
	}
	if useSuperres {
		codedDenom := int(r.F(SuperresDenomBits))
		f.SuperresDenom = codedDenom + SuperresDenomMin
	} else {
		f.SuperresDenom = SuperresNum
	}
	f.UpscaledWidth = f.FrameWidth
	f.FrameWidth = (f.UpscaledWidth*SuperresNum + f.SuperresDenom/2) / f.SuperresDenom
}

func (f *FrameHeader) computeImageSize() {
	f.MiCols = 2 * ((f.FrameWidth + 7) >> 3)
	f.MiRows = 2 * ((f.FrameHeight + 7) >> 3)
}

func (f *FrameHeader) renderSize(r *bits.Reader) {
	if r.F(1) == 1 { // render_and_frame_size_different
		f.RenderWidth = int(r.F(16)) + 1
		f.RenderHeight = int(r.F(16)) + 1
	} else {
		f.RenderWidth = f.UpscaledWidth
		f.RenderHeight = f.FrameHeight
	}
}

func (f *FrameHeader) setupPastIndependence() {
	// Loop filter delta defaults (AV1 spec §7.20).
	f.LoopFilterRefDeltas = [TotalRefsPerFrame]int{1, 0, 0, 0, -1, 0, -1, -1}
	f.LoopFilterModeDeltas = [2]int{0, 0}
	for ref := 0; ref < TotalRefsPerFrame; ref++ {
		f.PrevGmParams[ref] = identityGmParams()
	}
}

// identityGmParams returns the identity warp model (AV1 spec §7.20).
func identityGmParams() [6]int {
	return [6]int{0, 0, 1 << WarpedModelPrecBits, 0, 0, 1 << WarpedModelPrecBits}
}

// loadPrevious loads gm params and loop filter deltas from the primary reference
// frame (AV1 spec §7.21 load_previous), used when primary_ref_frame != NONE.
func (f *FrameHeader) loadPrevious(st *State) {
	prevFrame := f.RefFrameIdx[f.PrimaryRefFrame]
	f.PrevGmParams = st.SavedGmParams[prevFrame]
	f.LoopFilterRefDeltas = st.SavedLoopFilterRefDeltas[prevFrame]
	f.LoopFilterModeDeltas = st.SavedLoopFilterModeDeltas[prevFrame]
}

func (f *FrameHeader) tileInfo(r *bits.Reader, seq *SequenceHeader) {
	sbShift := 4
	if seq.Use128x128Superblock {
		sbShift = 5
	}
	sbCols := (f.MiCols + (1 << sbShift) - 1) >> sbShift
	sbRows := (f.MiRows + (1 << sbShift) - 1) >> sbShift
	sbSize := sbShift + 2
	maxTileWidthSb := MaxTileWidth >> sbSize
	maxTileAreaSb := MaxTileArea >> (2 * sbSize)
	minLog2TileCols := tileLog2(maxTileWidthSb, sbCols)
	maxLog2TileCols := tileLog2(1, min(sbCols, MaxTileCols))
	maxLog2TileRows := tileLog2(1, min(sbRows, MaxTileRows))
	minLog2Tiles := max(minLog2TileCols, tileLog2(maxTileAreaSb, sbRows*sbCols))

	f.MiColStarts = f.MiColStarts[:0]
	f.MiRowStarts = f.MiRowStarts[:0]

	if r.F(1) == 1 { // uniform_tile_spacing_flag
		f.TileColsLog2 = minLog2TileCols
		for f.TileColsLog2 < maxLog2TileCols {
			if r.F(1) == 1 {
				f.TileColsLog2++
			} else {
				break
			}
		}
		tileWidthSb := (sbCols + (1 << f.TileColsLog2) - 1) >> f.TileColsLog2
		i := 0
		for startSb := 0; startSb < sbCols; startSb += tileWidthSb {
			f.MiColStarts = append(f.MiColStarts, startSb<<sbShift)
			i++
		}
		f.MiColStarts = append(f.MiColStarts, f.MiCols)
		f.TileCols = i

		minLog2TileRows := max(minLog2Tiles-f.TileColsLog2, 0)
		f.TileRowsLog2 = minLog2TileRows
		for f.TileRowsLog2 < maxLog2TileRows {
			if r.F(1) == 1 {
				f.TileRowsLog2++
			} else {
				break
			}
		}
		tileHeightSb := (sbRows + (1 << f.TileRowsLog2) - 1) >> f.TileRowsLog2
		i = 0
		for startSb := 0; startSb < sbRows; startSb += tileHeightSb {
			f.MiRowStarts = append(f.MiRowStarts, startSb<<sbShift)
			i++
		}
		f.MiRowStarts = append(f.MiRowStarts, f.MiRows)
		f.TileRows = i
	} else {
		widestTileSb := 0
		startSb := 0
		i := 0
		for ; startSb < sbCols; i++ {
			f.MiColStarts = append(f.MiColStarts, startSb<<sbShift)
			maxWidth := min(sbCols-startSb, maxTileWidthSb)
			sizeSb := int(r.Ns(maxWidth)) + 1
			widestTileSb = max(sizeSb, widestTileSb)
			startSb += sizeSb
		}
		f.MiColStarts = append(f.MiColStarts, f.MiCols)
		f.TileCols = i
		f.TileColsLog2 = tileLog2(1, f.TileCols)

		if minLog2Tiles > 0 {
			maxTileAreaSb = (sbRows * sbCols) >> (minLog2Tiles + 1)
		} else {
			maxTileAreaSb = sbRows * sbCols
		}
		maxTileHeightSb := max(maxTileAreaSb/max(widestTileSb, 1), 1)
		startSb = 0
		i = 0
		for ; startSb < sbRows; i++ {
			f.MiRowStarts = append(f.MiRowStarts, startSb<<sbShift)
			maxHeight := min(sbRows-startSb, maxTileHeightSb)
			sizeSb := int(r.Ns(maxHeight)) + 1
			startSb += sizeSb
		}
		f.MiRowStarts = append(f.MiRowStarts, f.MiRows)
		f.TileRows = i
		f.TileRowsLog2 = tileLog2(1, f.TileRows)
	}
	if f.TileColsLog2 > 0 || f.TileRowsLog2 > 0 {
		f.ContextUpdateTileID = int(r.F(f.TileRowsLog2 + f.TileColsLog2))
		f.TileSizeBytes = int(r.F(2)) + 1
	} else {
		f.ContextUpdateTileID = 0
	}
}

func (f *FrameHeader) quantizationParams(r *bits.Reader, seq *SequenceHeader) {
	f.BaseQIdx = int(r.F(8))
	f.DeltaQYDc = readDeltaQ(r)
	if seq.NumPlanes > 1 {
		diffUVDelta := false
		if seq.SeparateUVDeltaQ {
			diffUVDelta = r.F(1) == 1
		}
		f.DeltaQUDc = readDeltaQ(r)
		f.DeltaQUAc = readDeltaQ(r)
		if diffUVDelta {
			f.DeltaQVDc = readDeltaQ(r)
			f.DeltaQVAc = readDeltaQ(r)
		} else {
			f.DeltaQVDc = f.DeltaQUDc
			f.DeltaQVAc = f.DeltaQUAc
		}
	}
	f.UsingQMatrix = r.F(1) == 1
	if f.UsingQMatrix {
		f.QmY = int(r.F(4))
		f.QmU = int(r.F(4))
		if !seq.SeparateUVDeltaQ {
			f.QmV = f.QmU
		} else {
			f.QmV = int(r.F(4))
		}
	}
}

func readDeltaQ(r *bits.Reader) int {
	if r.F(1) == 1 { // delta_coded
		return int(r.Su(1 + 6))
	}
	return 0
}

func (f *FrameHeader) segmentationParams(r *bits.Reader, st *State) {
	f.SegmentationEnabled = r.F(1) == 1
	if f.SegmentationEnabled {
		// primary_ref_frame == PRIMARY_REF_NONE for intra: update_map/data = 1.
		segUpdateData := true // for PRIMARY_REF_NONE
		f.SegmentationUpdateMap = true
		f.SegmentationTemporalUpdate = false
		if f.PrimaryRefFrame != PrimaryRefNone {
			f.SegmentationUpdateMap = r.F(1) == 1
			if f.SegmentationUpdateMap {
				f.SegmentationTemporalUpdate = r.F(1) == 1
			}
			segUpdateData = r.F(1) == 1
		}
		f.SegmentationUpdateData = segUpdateData
		if segUpdateData {
			for i := 0; i < MaxSegments; i++ {
				for j := 0; j < SegLvlMax; j++ {
					featureEnabled := r.F(1) == 1
					f.FeatureEnabled[i][j] = featureEnabled
					clippedValue := 0
					if featureEnabled {
						bitsToRead := segmentationFeatureBits[j]
						limit := segmentationFeatureMax[j]
						if segmentationFeatureSigned[j] == 1 {
							featureValue := int(r.Su(1 + bitsToRead))
							clippedValue = clip3(-limit, limit, featureValue)
						} else {
							featureValue := int(r.F(bitsToRead))
							clippedValue = clip3(0, limit, featureValue)
						}
					}
					f.FeatureData[i][j] = clippedValue
				}
			}
		} else if f.PrimaryRefFrame != PrimaryRefNone {
			// !segmentation_update_data: inherit the feature data/enable from the
			// primary reference frame (AV1 spec §5.9.14 / load_previous).
			prevFrame := f.RefFrameIdx[f.PrimaryRefFrame]
			f.FeatureEnabled = st.SavedFeatureEnabled[prevFrame]
			f.FeatureData = st.SavedFeatureData[prevFrame]
		}
	}
	f.SegIdPreSkip = 0
	f.LastActiveSegID = 0
	for i := 0; i < MaxSegments; i++ {
		for j := 0; j < SegLvlMax; j++ {
			if f.FeatureEnabled[i][j] {
				f.LastActiveSegID = i
				if j >= SegLvlRefFrame {
					f.SegIdPreSkip = 1
				}
			}
		}
	}
}

func (f *FrameHeader) deltaQParams(r *bits.Reader) {
	if f.BaseQIdx > 0 {
		f.DeltaQPresent = r.F(1) == 1
	}
	if f.DeltaQPresent {
		f.DeltaQRes = int(r.F(2))
	}
}

func (f *FrameHeader) deltaLfParams(r *bits.Reader) {
	if f.DeltaQPresent {
		if !f.AllowIntrabc {
			f.DeltaLfPresent = r.F(1) == 1
		}
		if f.DeltaLfPresent {
			f.DeltaLfRes = int(r.F(2))
			f.DeltaLfMulti = r.F(1) == 1
		}
	}
}

func (f *FrameHeader) computeLossless() {
	f.CodedLossless = true
	for segmentID := 0; segmentID < MaxSegments; segmentID++ {
		qindex := f.getQIndex(true, segmentID)
		f.LosslessArray[segmentID] = qindex == 0 && f.DeltaQYDc == 0 &&
			f.DeltaQUAc == 0 && f.DeltaQUDc == 0 && f.DeltaQVAc == 0 && f.DeltaQVDc == 0
		if !f.LosslessArray[segmentID] {
			f.CodedLossless = false
		}
	}
	f.AllLossless = f.CodedLossless && (f.FrameWidth == f.UpscaledWidth)
}

// getQIndex computes the q index for a segment (AV1 spec §7.12.2), used here only
// with ignoreDeltaQ = true (the lossless computation).
func (f *FrameHeader) getQIndex(ignoreDeltaQ bool, segmentID int) int {
	if f.SegmentationEnabled && f.FeatureEnabled[segmentID][SegLvlAltQ] {
		data := f.FeatureData[segmentID][SegLvlAltQ]
		qindex := f.BaseQIdx + data
		return clip3(0, 255, qindex)
	}
	return f.BaseQIdx
}

func (f *FrameHeader) loopFilterParams(r *bits.Reader, seq *SequenceHeader) {
	if f.CodedLossless || f.AllowIntrabc {
		f.LoopFilterLevel = [4]int{}
		f.LoopFilterRefDeltas = [TotalRefsPerFrame]int{1, 0, 0, 0, -1, 0, -1, -1}
		f.LoopFilterModeDeltas = [2]int{}
		return
	}
	f.LoopFilterLevel[0] = int(r.F(6))
	f.LoopFilterLevel[1] = int(r.F(6))
	if seq.NumPlanes > 1 {
		if f.LoopFilterLevel[0] != 0 || f.LoopFilterLevel[1] != 0 {
			f.LoopFilterLevel[2] = int(r.F(6))
			f.LoopFilterLevel[3] = int(r.F(6))
		}
	}
	f.LoopFilterSharpness = int(r.F(3))
	f.LoopFilterDeltaEnabled = r.F(1) == 1
	if f.LoopFilterDeltaEnabled {
		if r.F(1) == 1 { // loop_filter_delta_update
			for i := 0; i < TotalRefsPerFrame; i++ {
				if r.F(1) == 1 { // update_ref_delta
					f.LoopFilterRefDeltas[i] = int(r.Su(1 + 6))
				}
			}
			for i := 0; i < 2; i++ {
				if r.F(1) == 1 { // update_mode_delta
					f.LoopFilterModeDeltas[i] = int(r.Su(1 + 6))
				}
			}
		}
	}
}

func (f *FrameHeader) cdefParams(r *bits.Reader, seq *SequenceHeader) {
	if f.CodedLossless || f.AllowIntrabc || !seq.EnableCDEF {
		f.CdefBits = 0
		f.CdefDamping = 3
		return
	}
	f.CdefDamping = int(r.F(2)) + 3
	f.CdefBits = int(r.F(2))
	for i := 0; i < (1 << f.CdefBits); i++ {
		f.CdefYPriStrength[i] = int(r.F(4))
		f.CdefYSecStrength[i] = int(r.F(2))
		if f.CdefYSecStrength[i] == 3 {
			f.CdefYSecStrength[i]++
		}
		if seq.NumPlanes > 1 {
			f.CdefUVPriStrength[i] = int(r.F(4))
			f.CdefUVSecStrength[i] = int(r.F(2))
			if f.CdefUVSecStrength[i] == 3 {
				f.CdefUVSecStrength[i]++
			}
		}
	}
}

func (f *FrameHeader) lrParams(r *bits.Reader, seq *SequenceHeader) {
	f.FrameRestorationType = [3]int{RestoreNone, RestoreNone, RestoreNone}
	if f.AllLossless || f.AllowIntrabc || !seq.EnableRestoration {
		f.UsesLr = false
		return
	}
	usesChromaLr := false
	for i := 0; i < seq.NumPlanes; i++ {
		lrType := int(r.F(2))
		f.FrameRestorationType[i] = remapLrType[lrType]
		if f.FrameRestorationType[i] != RestoreNone {
			f.UsesLr = true
			if i > 0 {
				usesChromaLr = true
			}
		}
	}
	if f.UsesLr {
		var lrUnitShift int
		if seq.Use128x128Superblock {
			lrUnitShift = int(r.F(1)) + 1
		} else {
			lrUnitShift = int(r.F(1))
			if lrUnitShift != 0 {
				lrUnitShift += int(r.F(1))
			}
		}
		f.LoopRestorationSize[0] = RestorationTileSizeMax >> (2 - lrUnitShift)
		lrUvShift := 0
		if seq.SubsamplingX != 0 && seq.SubsamplingY != 0 && usesChromaLr {
			lrUvShift = int(r.F(1))
		}
		f.LoopRestorationSize[1] = f.LoopRestorationSize[0] >> lrUvShift
		f.LoopRestorationSize[2] = f.LoopRestorationSize[0] >> lrUvShift
	}
}

func (f *FrameHeader) readTxMode(r *bits.Reader) {
	if f.CodedLossless {
		f.TxMode = Only4x4
	} else {
		if r.F(1) == 1 { // tx_mode_select
			f.TxMode = TxModeSelect
		} else {
			f.TxMode = TxModeLargest
		}
	}
}

func tileLog2(blkSize, target int) int {
	k := 0
	for (blkSize << k) < target {
		k++
	}
	return k
}

func clip3(lo, hi, v int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
