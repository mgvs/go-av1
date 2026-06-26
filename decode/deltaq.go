package decode

import "github.com/mgvs/go-av1/predict"

const deltaQSmall = 3

// sbSizeBlock returns the superblock block size for the current sequence.
func (fd *frameDecoder) sbSizeBlock() int {
	if fd.seq.Use128x128Superblock {
		return predict.Block128x128
	}
	return predict.Block64x64
}

// readDeltaQIndex reads a per-superblock delta quantizer index and updates
// CurrentQIndex (AV1 spec §5.11.36).
func (fd *frameDecoder) readDeltaQIndex() {
	if fd.miSize == fd.sbSizeBlock() && fd.skip != 0 {
		return
	}
	if !fd.readDeltas {
		return
	}
	deltaQAbs := fd.d.DecodeSymbol(fd.c.deltaQ)
	if deltaQAbs == deltaQSmall {
		remBits := int(fd.d.ReadLiteral(3)) + 1
		absBits := int(fd.d.ReadLiteral(remBits))
		deltaQAbs = absBits + (1 << uint(remBits)) + 1
	}
	if deltaQAbs != 0 {
		reduced := deltaQAbs
		if fd.d.ReadLiteral(1) == 1 {
			reduced = -deltaQAbs
		}
		fd.currentQIndex = clip3i(1, 255, fd.currentQIndex+(reduced<<uint(fd.fh.DeltaQRes)))
	}
}

const deltaLfSmall = 3

// readDeltaLF reads per-superblock loop-filter level deltas (AV1 spec §5.11.37),
// updating currentDeltaLF cumulatively.
func (fd *frameDecoder) readDeltaLF() error {
	if fd.miSize == fd.sbSizeBlock() && fd.skip != 0 {
		return nil
	}
	if !fd.readDeltas || !fd.fh.DeltaLfPresent {
		return nil
	}
	frameLfCount := 1
	if fd.fh.DeltaLfMulti {
		frameLfCount = frameLfCountAll
		if fd.numPlanes == 1 {
			frameLfCount = frameLfCountAll - 2
		}
	}
	for i := 0; i < frameLfCount; i++ {
		cdf := fd.c.deltaLf
		if fd.fh.DeltaLfMulti {
			cdf = fd.c.deltaLfMulti[i]
		}
		deltaLfAbs := fd.d.DecodeSymbol(cdf)
		if deltaLfAbs == deltaLfSmall {
			remBits := int(fd.d.ReadLiteral(3)) + 1
			absBits := int(fd.d.ReadLiteral(remBits))
			deltaLfAbs = absBits + (1 << uint(remBits)) + 1
		}
		if deltaLfAbs != 0 {
			reduced := deltaLfAbs
			if fd.d.ReadLiteral(1) == 1 {
				reduced = -deltaLfAbs
			}
			fd.currentDeltaLF[i] = clip3i(-maxLoopFilter, maxLoopFilter,
				fd.currentDeltaLF[i]+(reduced<<uint(fd.fh.DeltaLfRes)))
		}
	}
	return nil
}

const frameLfCountAll = 4

// getQIndex returns the quantizer index for the current block, honouring the
// per-superblock delta-q (AV1 spec §7.12.2, segment 0 only).
func (fd *frameDecoder) getQIndex() int {
	return fd.getQIndexSeg(false, fd.segmentId)
}
