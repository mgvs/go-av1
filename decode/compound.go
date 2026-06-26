package decode

import "github.com/mgvs/go-av1/header"

// Compound reference / type enums (AV1 spec §6.10.24).
const (
	singleReference   = 0
	compoundReference = 1

	unidirCompReference = 0
	bidirCompReference  = 1

	compoundWedge    = 0
	compoundDiffwtd  = 1
	compoundAverage  = 2
	compoundIntra    = 3
	compoundDistance = 4
)

var compoundModeCtxMap = [3][5]int{
	{0, 1, 1, 1, 1},
	{1, 2, 3, 4, 4},
	{4, 4, 5, 6, 7},
}

const compNewmvCtxs = 5

func isSamedirRefPair(ref0, ref1 int) bool {
	return (ref0 >= header.BwdRefFrame) == (ref1 >= header.BwdRefFrame)
}

func checkBackward(ref int) bool {
	return ref >= header.BwdRefFrame && ref <= header.AltRefFrame
}

// compModeCtx computes the comp_mode context (AV1 spec §8.3).
func (fd *frameDecoder) compModeCtx() int {
	a0, l0 := fd.aboveRefFrame[0], fd.leftRefFrame[0]
	switch {
	case fd.availU && fd.availL:
		switch {
		case fd.aboveSingle && fd.leftSingle:
			return b2i(checkBackward(a0)) ^ b2i(checkBackward(l0))
		case fd.aboveSingle:
			return 2 + b2i(checkBackward(a0) || fd.aboveIntra)
		case fd.leftSingle:
			return 2 + b2i(checkBackward(l0) || fd.leftIntra)
		default:
			return 4
		}
	case fd.availU:
		if fd.aboveSingle {
			return b2i(checkBackward(a0))
		}
		return 3
	case fd.availL:
		if fd.leftSingle {
			return b2i(checkBackward(l0))
		}
		return 3
	default:
		return 1
	}
}

// compRefTypeCtx computes the comp_ref_type context (AV1 spec §8.3).
func (fd *frameDecoder) compRefTypeCtx() int {
	a0, a1 := fd.aboveRefFrame[0], fd.aboveRefFrame[1]
	l0, l1 := fd.leftRefFrame[0], fd.leftRefFrame[1]
	aboveCompInter := fd.availU && !fd.aboveIntra && !fd.aboveSingle
	leftCompInter := fd.availL && !fd.leftIntra && !fd.leftSingle
	aboveUniComp := aboveCompInter && isSamedirRefPair(a0, a1)
	leftUniComp := leftCompInter && isSamedirRefPair(l0, l1)
	switch {
	case fd.availU && !fd.aboveIntra && fd.availL && !fd.leftIntra:
		samedir := b2i(isSamedirRefPair(a0, l0))
		switch {
		case !aboveCompInter && !leftCompInter:
			return 1 + 2*samedir
		case !aboveCompInter:
			if !leftUniComp {
				return 1
			}
			return 3 + samedir
		case !leftCompInter:
			if !aboveUniComp {
				return 1
			}
			return 3 + samedir
		default:
			switch {
			case !aboveUniComp && !leftUniComp:
				return 0
			case !aboveUniComp || !leftUniComp:
				return 2
			default:
				return 3 + b2i((a0 == header.BwdRefFrame) == (l0 == header.BwdRefFrame))
			}
		}
	case fd.availU && fd.availL:
		if aboveCompInter {
			return 1 + 2*b2i(aboveUniComp)
		}
		if leftCompInter {
			return 1 + 2*b2i(leftUniComp)
		}
		return 2
	case aboveCompInter:
		return 4 * b2i(aboveUniComp)
	case leftCompInter:
		return 4 * b2i(leftUniComp)
	default:
		return 2
	}
}

func (fd *frameDecoder) singleRefP1Ctx() int {
	cnt := fd.countRefs
	fwd := cnt(header.LastFrame) + cnt(header.Last2Frame) + cnt(header.Last3Frame) + cnt(header.GoldenFrame)
	bwd := cnt(header.BwdRefFrame) + cnt(header.AltRef2Frame) + cnt(header.AltRefFrame)
	return refCountCtx(fwd, bwd)
}

// readCompoundRefs reads the two reference frames of a compound block (AV1 spec §5.11.25).
func (fd *frameDecoder) readCompoundRefs() error {
	cnt := fd.countRefs
	if fd.d.DecodeSymbol(fd.c.compRefType[fd.compRefTypeCtx()]) == unidirCompReference {
		if fd.d.DecodeSymbol(fd.c.uniCompRef[fd.singleRefP1Ctx()][0]) == 1 {
			fd.refFrame[0], fd.refFrame[1] = header.BwdRefFrame, header.AltRefFrame
			return nil
		}
		ctxP1 := refCountCtx(cnt(header.Last2Frame), cnt(header.Last3Frame)+cnt(header.GoldenFrame))
		if fd.d.DecodeSymbol(fd.c.uniCompRef[ctxP1][1]) == 0 {
			fd.refFrame[0], fd.refFrame[1] = header.LastFrame, header.Last2Frame
			return nil
		}
		ctxP2 := refCountCtx(cnt(header.Last3Frame), cnt(header.GoldenFrame))
		if fd.d.DecodeSymbol(fd.c.uniCompRef[ctxP2][2]) == 1 {
			fd.refFrame[0], fd.refFrame[1] = header.LastFrame, header.GoldenFrame
		} else {
			fd.refFrame[0], fd.refFrame[1] = header.LastFrame, header.Last3Frame
		}
		return nil
	}
	// Bidirectional: forward ref via comp_ref, backward ref via comp_bwdref.
	ctxRef := refCountCtx(cnt(header.LastFrame)+cnt(header.Last2Frame), cnt(header.Last3Frame)+cnt(header.GoldenFrame))
	if fd.d.DecodeSymbol(fd.c.compRef[ctxRef][0]) == 0 {
		ctxP1 := refCountCtx(cnt(header.LastFrame), cnt(header.Last2Frame))
		if fd.d.DecodeSymbol(fd.c.compRef[ctxP1][1]) == 1 {
			fd.refFrame[0] = header.Last2Frame
		} else {
			fd.refFrame[0] = header.LastFrame
		}
	} else {
		ctxP2 := refCountCtx(cnt(header.Last3Frame), cnt(header.GoldenFrame))
		if fd.d.DecodeSymbol(fd.c.compRef[ctxP2][2]) == 1 {
			fd.refFrame[0] = header.GoldenFrame
		} else {
			fd.refFrame[0] = header.Last3Frame
		}
	}
	ctxBwd := refCountCtx(cnt(header.BwdRefFrame)+cnt(header.AltRef2Frame), cnt(header.AltRefFrame))
	if fd.d.DecodeSymbol(fd.c.compBwdRef[ctxBwd][0]) == 0 {
		ctxP1 := refCountCtx(cnt(header.BwdRefFrame), cnt(header.AltRef2Frame))
		if fd.d.DecodeSymbol(fd.c.compBwdRef[ctxP1][1]) == 1 {
			fd.refFrame[1] = header.AltRef2Frame
		} else {
			fd.refFrame[1] = header.BwdRefFrame
		}
	} else {
		fd.refFrame[1] = header.AltRefFrame
	}
	return nil
}

// getMode maps the compound Y mode to the per-list prediction mode (AV1 spec §5.11.24).
func (fd *frameDecoder) getMode(refList int) int {
	m := fd.yMode
	if refList == 0 {
		switch {
		case m < nearestNearestMv:
			return m
		case m == newNewMv || m == newNearestMv || m == newNearMv:
			return newMv
		case m == nearestNearestMv || m == nearestNewMv:
			return nearestMv
		case m == nearNearMv || m == nearNewMv:
			return nearMv
		default:
			return globalMv
		}
	}
	switch {
	case m == newNewMv || m == nearestNewMv || m == nearNewMv:
		return newMv
	case m == nearestNearestMv || m == newNearestMv:
		return nearestMv
	case m == nearNearMv || m == newNearMv:
		return nearMv
	default:
		return globalMv
	}
}

func (fd *frameDecoder) compGroupIdxCtx() int {
	ctx := 0
	if fd.availU {
		if !fd.aboveSingle {
			ctx += fd.compGroupIdxs[fd.miRow-1][fd.miCol]
		} else if fd.aboveRefFrame[0] == header.AltRefFrame {
			ctx += 3
		}
	}
	if fd.availL {
		if !fd.leftSingle {
			ctx += fd.compGroupIdxs[fd.miRow][fd.miCol-1]
		} else if fd.leftRefFrame[0] == header.AltRefFrame {
			ctx += 3
		}
	}
	return min(5, ctx)
}

func (fd *frameDecoder) compoundIdxCtx() int {
	fwd := absInt(fd.relativeDist(fd.fh.OrderHints[fd.refFrame[0]], fd.fh.OrderHint))
	bck := absInt(fd.relativeDist(fd.fh.OrderHints[fd.refFrame[1]], fd.fh.OrderHint))
	ctx := 0
	if fwd == bck {
		ctx = 3
	}
	if fd.availU {
		if !fd.aboveSingle {
			ctx += fd.compoundIdxs[fd.miRow-1][fd.miCol]
		} else if fd.aboveRefFrame[0] == header.AltRefFrame {
			ctx++
		}
	}
	if fd.availL {
		if !fd.leftSingle {
			ctx += fd.compoundIdxs[fd.miRow][fd.miCol-1]
		} else if fd.leftRefFrame[0] == header.AltRefFrame {
			ctx++
		}
	}
	return ctx
}

const maxFrameDistance = 31

var quantDistWeight = [4][2]int{{2, 3}, {2, 5}, {2, 7}, {1, maxFrameDistance}}
var quantDistLookup = [4][2]int{{9, 7}, {11, 5}, {12, 4}, {13, 3}}

// distanceWeights computes the forward/backward blend weights for a
// distance-weighted compound block (AV1 spec §7.11.3.15).
func (fd *frameDecoder) distanceWeights(candRow, candCol int) (fwd, bck int) {
	var dist [2]int
	for refList := 0; refList < 2; refList++ {
		h := fd.fh.OrderHints[fd.gridRefFrames[candRow][candCol][refList]]
		dist[refList] = clip3i(0, maxFrameDistance, absInt(fd.relativeDist(h, fd.fh.OrderHint)))
	}
	d0 := dist[1]
	d1 := dist[0]
	order := 0
	if d0 <= d1 {
		order = 1
	}
	if d0 == 0 || d1 == 0 {
		return quantDistLookup[3][order], quantDistLookup[3][1-order]
	}
	i := 0
	for ; i < 3; i++ {
		c0 := quantDistWeight[i][order]
		c1 := quantDistWeight[i][1-order]
		if order != 0 {
			if d0*c0 > d1*c1 {
				break
			}
		} else if d0*c0 < d1*c1 {
			break
		}
	}
	return quantDistLookup[i][order], quantDistLookup[i][1-order]
}

func (fd *frameDecoder) relativeDist(a, b int) int {
	if !fd.seq.EnableOrderHint {
		return 0
	}
	diff := a - b
	m := 1 << uint(fd.seq.OrderHintBits-1)
	return (diff & (m - 1)) - (diff & m)
}

// readCompoundType reads the compound prediction type (AV1 spec §5.11.29).
func (fd *frameDecoder) readCompoundType(isCompound bool) error {
	fd.compoundType = compoundAverage
	if fd.skipModeFlag {
		return nil
	}
	if !isCompound {
		// Single-reference: inter-intra blend, else plain AVERAGE semantics.
		if fd.isInterIntra {
			if fd.wedgeInterIntra {
				fd.compoundType = compoundWedge
			} else {
				fd.compoundType = compoundIntra
			}
		}
		return nil
	}
	fd.compGroupIdxVal = 0
	fd.compoundIdxVal = 1
	if fd.seq.EnableMaskedCompound {
		fd.compGroupIdxVal = fd.d.DecodeSymbol(fd.c.compGroupIdx[fd.compGroupIdxCtx()])
	}
	if fd.compGroupIdxVal == 0 {
		if fd.seq.EnableJntComp {
			fd.compoundIdxVal = fd.d.DecodeSymbol(fd.c.compoundIdx[fd.compoundIdxCtx()])
			if fd.compoundIdxVal != 0 {
				fd.compoundType = compoundAverage
			} else {
				fd.compoundType = compoundDistance
			}
		} else {
			fd.compoundType = compoundAverage
		}
		return nil
	}
	// comp_group_idx == 1: masked compound (wedge or diffwtd).
	n := wedgeBits[fd.miSize]
	if n == 0 {
		fd.compoundType = compoundDiffwtd
	} else {
		if fd.d.DecodeSymbol(fd.c.compoundType[fd.miSize]) != 0 {
			fd.compoundType = compoundDiffwtd
		} else {
			fd.compoundType = compoundWedge
		}
	}
	if fd.compoundType == compoundWedge {
		fd.wedgeIndex = fd.d.DecodeSymbol(fd.c.wedgeIdx[fd.miSize])
		fd.wedgeSign = int(fd.d.ReadLiteral(1))
	} else {
		fd.maskType = int(fd.d.ReadLiteral(1))
	}
	return nil
}
