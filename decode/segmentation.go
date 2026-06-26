package decode

import (
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
)

// segFeatureActiveIdx reports whether a segmentation feature is active for a
// given segment (AV1 spec §5.11.50).
func (fd *frameDecoder) segFeatureActiveIdx(segID, feature int) bool {
	return fd.fh.SegmentationEnabled && fd.fh.FeatureEnabled[segID][feature]
}

// segFeatureActive reports whether the feature is active for the current block's
// segment.
func (fd *frameDecoder) segFeatureActive(feature int) bool {
	return fd.segFeatureActiveIdx(fd.segmentId, feature)
}

// negDeinterleave maps a coded difference back to a segment id (AV1 spec §5.11.10).
func negDeinterleave(diff, ref, max int) int {
	if ref == 0 {
		return diff
	}
	if ref >= max-1 {
		return max - diff - 1
	}
	if 2*ref < max {
		if diff <= 2*ref {
			if diff&1 != 0 {
				return ref + ((diff + 1) >> 1)
			}
			return ref - (diff >> 1)
		}
		return diff
	}
	if diff <= 2*(max-ref-1) {
		if diff&1 != 0 {
			return ref + ((diff + 1) >> 1)
		}
		return ref - (diff >> 1)
	}
	return max - (diff + 1)
}

// readSegmentId decodes the spatial segment id for the current block (AV1 spec
// §5.11.10), using the neighbouring segment ids as a predictor.
func (fd *frameDecoder) readSegmentId() {
	prevUL, prevU, prevL := -1, -1, -1
	if fd.availU && fd.availL {
		prevUL = fd.segmentIds[fd.miRow-1][fd.miCol-1]
	}
	if fd.availU {
		prevU = fd.segmentIds[fd.miRow-1][fd.miCol]
	}
	if fd.availL {
		prevL = fd.segmentIds[fd.miRow][fd.miCol-1]
	}
	pred := 0
	switch {
	case prevU == -1:
		if prevL != -1 {
			pred = prevL
		}
	case prevL == -1:
		pred = prevU
	default:
		if prevUL == prevU {
			pred = prevU
		} else {
			pred = prevL
		}
	}
	if fd.skip != 0 {
		fd.segmentId = pred
		return
	}
	ctx := 0
	switch {
	case prevUL < 0:
		ctx = 0
	case prevUL == prevU && prevUL == prevL:
		ctx = 2
	case prevUL == prevU || prevUL == prevL || prevU == prevL:
		ctx = 1
	}
	raw := fd.d.DecodeSymbol(fd.c.segmentId[ctx])
	fd.segmentId = negDeinterleave(raw, pred, fd.fh.LastActiveSegID+1)
}

// intraSegmentId reads the segment id for an intra-frame block (AV1 spec §5.11.9).
func (fd *frameDecoder) intraSegmentId() {
	if fd.fh.SegmentationEnabled {
		fd.readSegmentId()
	} else {
		fd.segmentId = 0
	}
}

// getQIndexSeg returns the quantizer index for a segment (AV1 spec §7.12.2),
// honouring per-segment SEG_LVL_ALT_Q and per-superblock delta-q.
func (fd *frameDecoder) getQIndexSeg(ignoreDeltaQ bool, segID int) int {
	if fd.segFeatureActiveIdx(segID, header.SegLvlAltQ) {
		data := fd.fh.FeatureData[segID][header.SegLvlAltQ]
		qindex := fd.fh.BaseQIdx + data
		if !ignoreDeltaQ && fd.fh.DeltaQPresent {
			qindex = fd.currentQIndex + data
		}
		return clip3i(0, 255, qindex)
	}
	if !ignoreDeltaQ && fd.fh.DeltaQPresent {
		return fd.currentQIndex
	}
	return fd.fh.BaseQIdx
}

// getSegmentId returns the predicted segment id from the previous frame's map
// (AV1 spec §5.11.31 get_segment_id), the minimum over the block's footprint.
func (fd *frameDecoder) getSegmentId() int {
	if fd.prevSegmentIds == nil {
		return 0
	}
	bw4 := predict.Num4x4BlocksWide[fd.miSize]
	bh4 := predict.Num4x4BlocksHigh[fd.miSize]
	xMis := min(fd.miCols-fd.miCol, bw4)
	yMis := min(fd.miRows-fd.miRow, bh4)
	seg := 7
	for y := 0; y < yMis; y++ {
		for x := 0; x < xMis; x++ {
			if v := fd.prevSegmentIds[fd.miRow+y][fd.miCol+x]; v < seg {
				seg = v
			}
		}
	}
	return seg
}

// setSegPredContext stores the temporal-prediction flag over the block footprint.
func (fd *frameDecoder) setSegPredContext(v int) {
	bw4 := predict.Num4x4BlocksWide[fd.miSize]
	bh4 := predict.Num4x4BlocksHigh[fd.miSize]
	for i := 0; i < bw4 && fd.miCol+i < len(fd.aboveSegPredContext); i++ {
		fd.aboveSegPredContext[fd.miCol+i] = v
	}
	for i := 0; i < bh4 && fd.miRow+i < len(fd.leftSegPredContext); i++ {
		fd.leftSegPredContext[fd.miRow+i] = v
	}
}

// interSegmentId reads the segment id for an inter-frame block (AV1 spec §5.11.31).
func (fd *frameDecoder) interSegmentId(preSkip bool) {
	if !fd.fh.SegmentationEnabled {
		fd.segmentId = 0
		return
	}
	predictedSegmentId := fd.getSegmentId()
	if !fd.fh.SegmentationUpdateMap {
		fd.segmentId = predictedSegmentId
		return
	}
	if preSkip && fd.fh.SegIdPreSkip == 0 {
		fd.segmentId = 0
		return
	}
	if !preSkip && fd.skip != 0 {
		fd.setSegPredContext(0)
		fd.readSegmentId()
		return
	}
	if fd.fh.SegmentationTemporalUpdate {
		ctx := fd.leftSegPredContext[fd.miRow] + fd.aboveSegPredContext[fd.miCol]
		pred := fd.d.DecodeSymbol(fd.c.segIdPredicted[ctx])
		if pred != 0 {
			fd.segmentId = predictedSegmentId
		} else {
			fd.readSegmentId()
		}
		fd.setSegPredContext(pred)
	} else {
		fd.readSegmentId()
	}
}
