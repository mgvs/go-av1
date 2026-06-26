package decode

import (
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
	"github.com/mgvs/go-av1/tile"
)

// RefFrame is a decoded reference frame retained for inter prediction.
type RefFrame struct {
	Planes    []*predict.Plane
	OrderHint int
	MiCols    int
	MiRows    int
	Width     int
	Height    int
	// MVs / reference info for temporal motion projection are added with
	// find_mv_stack; intra reference frames carry none.
	mvs        [][]MV  // [miRow][miCol] motion vector (0,0 for intra)
	refFrame0  [][]int // [miRow][miCol] primary reference (INTRA_FRAME for intra)
	cdfs       *cdfContext
	segmentIds [][]int // saved segmentation map (SavedSegmentIds)
	// Motion-field motion vectors (AV1 spec §7.19) saved for temporal projection.
	mfMvs           [][]MV
	mfRefFrames     [][]int
	savedOrderHints [header.TotalRefsPerFrame]int
	frameType       int
}

// MV is a motion vector in 1/8-pel units (row, col), matching the spec's Mv array.
type MV struct{ Row, Col int }

// Decoder holds cross-frame state for decoding a coded video sequence: the active
// sequence header, the header-level reference state and the decoded reference
// frame buffers. Decode frames in coded order through DecodeFrame.
type Decoder struct {
	seq  *header.SequenceHeader
	st   *header.State
	refs [header.NumRefFrames]*RefFrame
}

// NewDecoder creates a sequence decoder for the given sequence header.
func NewDecoder(seq *header.SequenceHeader) *Decoder {
	return &Decoder{seq: seq, st: &header.State{}}
}

// State returns the cross-frame header state (for ParseFrameHeader).
func (dec *Decoder) State() *header.State { return dec.st }

// DecodeFrame decodes one frame (given its parsed header and tiles) and updates
// the reference frame store according to refresh_frame_flags.
func (dec *Decoder) DecodeFrame(fh *header.FrameHeader, tiles []tile.Tile) (*Frame, error) {
	if fh.ShowExistingFrame {
		return dec.showExisting(fh)
	}
	frame, err := decodeFrameInternal(dec.seq, fh, tiles, dec.refs[:])
	if err != nil {
		return nil, err
	}
	dec.updateRefs(fh, frame)
	return dec.applyFilmGrain(frame, fh.FilmGrain), nil
}

// showExisting outputs a previously decoded frame referenced by
// frame_to_show_map_idx (AV1 spec §7.4, show_existing_frame path). No tiles are
// decoded. Showing a saved KEY_FRAME reloads its state into all reference slots
// (reference frame loading §7.21 + update §7.20 with refresh_frame_flags = all).
func (dec *Decoder) showExisting(fh *header.FrameHeader) (*Frame, error) {
	idx := fh.FrameToShowMapIdx
	ref := dec.refs[idx]
	if ref == nil {
		return nil, ErrUnsupported{"show_existing_frame: empty reference slot"}
	}
	frame := &Frame{
		Planes:    ref.Planes,
		NumPlanes: dec.seq.NumPlanes,
		SubX:      dec.seq.SubsamplingX,
		SubY:      dec.seq.SubsamplingY,
		BitDepth:  dec.seq.BitDepth,
	}
	if fh.FrameType == header.KeyFrame {
		dec.st.CurrentFrameID = dec.st.RefFrameID[idx]
		for i := 0; i < header.NumRefFrames; i++ {
			dec.refs[i] = ref
			dec.st.RefValid[i] = 1
			dec.st.RefOrderHint[i] = dec.st.RefOrderHint[idx]
			dec.st.RefFrameType[i] = header.KeyFrame
			dec.st.RefFrameID[i] = dec.st.RefFrameID[idx]
			dec.st.RefUpscaledWidth[i] = dec.st.RefUpscaledWidth[idx]
			dec.st.RefFrameWidth[i] = dec.st.RefFrameWidth[idx]
			dec.st.RefFrameHeight[i] = dec.st.RefFrameHeight[idx]
			dec.st.RefRenderWidth[i] = dec.st.RefRenderWidth[idx]
			dec.st.RefRenderHeight[i] = dec.st.RefRenderHeight[idx]
		}
	}
	fg := dec.st.SavedFilmGrain[idx]
	return dec.applyFilmGrain(frame, &fg), nil
}

// updateRefs stores the decoded frame into every reference slot selected by
// refresh_frame_flags (AV1 spec §7.20, reference frame update process).
func (dec *Decoder) updateRefs(fh *header.FrameHeader, frame *Frame) {
	ref := &RefFrame{
		Planes:          frame.Planes,
		OrderHint:       fh.OrderHint,
		MiCols:          fh.MiCols,
		MiRows:          fh.MiRows,
		Width:           fh.UpscaledWidth, // RefUpscaledWidth — the stored (upscaled) plane width
		Height:          fh.FrameHeight,
		mvs:             frame.mvs,
		refFrame0:       frame.refFrame0,
		cdfs:            frame.cdfs,
		segmentIds:      frame.segmentIds,
		mfMvs:           frame.mfMvs,
		mfRefFrames:     frame.mfRefFrames,
		savedOrderHints: fh.OrderHints,
		frameType:       fh.FrameType,
	}
	for i := 0; i < header.NumRefFrames; i++ {
		if fh.RefreshFrameFlags&(1<<uint(i)) != 0 {
			dec.refs[i] = ref
			dec.st.RefValid[i] = 1
			dec.st.RefOrderHint[i] = fh.OrderHint
			dec.st.RefUpscaledWidth[i] = fh.UpscaledWidth
			dec.st.RefFrameWidth[i] = fh.FrameWidth
			dec.st.RefFrameHeight[i] = fh.FrameHeight
			dec.st.RefRenderWidth[i] = fh.RenderWidth
			dec.st.RefRenderHeight[i] = fh.RenderHeight
			dec.st.RefFrameType[i] = fh.FrameType
			dec.st.RefFrameID[i] = dec.st.CurrentFrameID
			dec.st.SavedGmParams[i] = fh.GmParams
			dec.st.SavedLoopFilterRefDeltas[i] = fh.LoopFilterRefDeltas
			dec.st.SavedLoopFilterModeDeltas[i] = fh.LoopFilterModeDeltas
			dec.st.SavedFeatureEnabled[i] = fh.FeatureEnabled
			dec.st.SavedFeatureData[i] = fh.FeatureData
			if fh.FilmGrain != nil {
				dec.st.SavedFilmGrain[i] = *fh.FilmGrain
			}
		}
	}
}
