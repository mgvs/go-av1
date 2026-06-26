package header

import (
	"fmt"

	"github.com/mgvs/go-av1/bits"
)

// getRelativeDist returns the signed circular distance between two order hints
// (AV1 spec §5.9.3). Returns 0 when order hints are disabled.
func getRelativeDist(seq *SequenceHeader, a, b int) int {
	if !seq.EnableOrderHint {
		return 0
	}
	diff := a - b
	m := 1 << uint(seq.OrderHintBits-1)
	return (diff & (m - 1)) - (diff & m)
}

// interRefs reads the reference-frame portion of a non-intra frame header
// (AV1 spec §5.9.2).
func (f *FrameHeader) interRefs(r *bits.Reader, seq *SequenceHeader, st *State, idLen int) error {
	frameRefsShortSignaling := false
	if seq.EnableOrderHint {
		frameRefsShortSignaling = r.F(1) == 1
		if frameRefsShortSignaling {
			return fmt.Errorf("header: frame_refs_short_signaling not yet implemented")
		}
	}
	for i := 0; i < RefsPerFrame; i++ {
		if !frameRefsShortSignaling {
			f.RefFrameIdx[i] = int(r.F(3))
		}
		if seq.FrameIDNumbersPresent {
			r.F(seq.DeltaFrameIDLengthMinus2 + 2) // delta_frame_id_minus_1
		}
	}
	if f.FrameSizeOverride && !f.ErrorResilient {
		f.frameSizeWithRefs(r, seq, st)
	} else {
		f.frameSize(r, seq)
		f.renderSize(r)
	}

	if f.ForceIntegerMV != 0 {
		f.AllowHighPrecisionMV = false
	} else {
		f.AllowHighPrecisionMV = r.F(1) == 1
	}
	f.readInterpolationFilter(r)
	f.IsMotionModeSwitchable = r.F(1) == 1
	if f.ErrorResilient || !seq.EnableRefFrameMVs {
		f.UseRefFrameMvs = false
	} else {
		f.UseRefFrameMvs = r.F(1) == 1
	}
	for i := 0; i < RefsPerFrame; i++ {
		refFrame := LastFrame + i
		hint := st.RefOrderHint[f.RefFrameIdx[i]]
		f.OrderHints[refFrame] = hint
		f.RefFrameSignBias[refFrame] = getRelativeDist(seq, hint, f.OrderHint) > 0
	}
	return nil
}

func (f *FrameHeader) readInterpolationFilter(r *bits.Reader) {
	if r.F(1) == 1 {
		f.InterpolationFilter = InterpFilterSwitchable
	} else {
		f.InterpolationFilter = int(r.F(2))
	}
}

func (f *FrameHeader) frameReferenceMode(r *bits.Reader) {
	if f.FrameIsIntra {
		f.ReferenceSelect = false
	} else {
		f.ReferenceSelect = r.F(1) == 1
	}
}

// skipModeParams derives skip_mode_present (AV1 spec §5.9.22).
func (f *FrameHeader) skipModeParams(r *bits.Reader, seq *SequenceHeader, st *State) {
	skipModeAllowed := false
	if !f.FrameIsIntra && f.ReferenceSelect && seq.EnableOrderHint {
		forwardIdx, backwardIdx := -1, -1
		forwardHint, backwardHint := 0, 0
		for i := 0; i < RefsPerFrame; i++ {
			refHint := st.RefOrderHint[f.RefFrameIdx[i]]
			if getRelativeDist(seq, refHint, f.OrderHint) < 0 {
				if forwardIdx < 0 || getRelativeDist(seq, refHint, forwardHint) > 0 {
					forwardIdx, forwardHint = i, refHint
				}
			} else if getRelativeDist(seq, refHint, f.OrderHint) > 0 {
				if backwardIdx < 0 || getRelativeDist(seq, refHint, backwardHint) < 0 {
					backwardIdx, backwardHint = i, refHint
				}
			}
		}
		switch {
		case forwardIdx < 0:
			skipModeAllowed = false
		case backwardIdx >= 0:
			skipModeAllowed = true
			f.SkipModeFrame[0] = LastFrame + min(forwardIdx, backwardIdx)
			f.SkipModeFrame[1] = LastFrame + max(forwardIdx, backwardIdx)
		default:
			secondForwardIdx, secondForwardHint := -1, 0
			for i := 0; i < RefsPerFrame; i++ {
				refHint := st.RefOrderHint[f.RefFrameIdx[i]]
				if getRelativeDist(seq, refHint, forwardHint) < 0 {
					if secondForwardIdx < 0 || getRelativeDist(seq, refHint, secondForwardHint) > 0 {
						secondForwardIdx, secondForwardHint = i, refHint
					}
				}
			}
			if secondForwardIdx < 0 {
				skipModeAllowed = false
			} else {
				skipModeAllowed = true
				f.SkipModeFrame[0] = LastFrame + min(forwardIdx, secondForwardIdx)
				f.SkipModeFrame[1] = LastFrame + max(forwardIdx, secondForwardIdx)
			}
		}
	}
	if skipModeAllowed {
		f.SkipModePresent = r.F(1) == 1
	} else {
		f.SkipModePresent = false
	}
}

// globalMotionParams reads the per-reference global motion models (AV1 spec §5.9.24).
func (f *FrameHeader) globalMotionParams(r *bits.Reader) {
	for ref := LastFrame; ref <= AltRefFrame; ref++ {
		f.GmType[ref] = GmIdentity
		for i := 0; i < 6; i++ {
			if i%3 == 2 {
				f.GmParams[ref][i] = 1 << WarpedModelPrecBits
			} else {
				f.GmParams[ref][i] = 0
			}
		}
	}
	if f.FrameIsIntra {
		return
	}
	for ref := LastFrame; ref <= AltRefFrame; ref++ {
		typ := GmIdentity
		if r.F(1) == 1 { // is_global
			if r.F(1) == 1 { // is_rot_zoom
				typ = GmRotZoom
			} else if r.F(1) == 1 { // is_translation
				typ = GmTranslation
			} else {
				typ = GmAffine
			}
		}
		f.GmType[ref] = typ
		if typ >= GmRotZoom {
			f.readGlobalParam(r, typ, ref, 2)
			f.readGlobalParam(r, typ, ref, 3)
			if typ == GmAffine {
				f.readGlobalParam(r, typ, ref, 4)
				f.readGlobalParam(r, typ, ref, 5)
			} else {
				f.GmParams[ref][4] = -f.GmParams[ref][3]
				f.GmParams[ref][5] = f.GmParams[ref][2]
			}
		}
		if typ >= GmTranslation {
			f.readGlobalParam(r, typ, ref, 0)
			f.readGlobalParam(r, typ, ref, 1)
		}
	}
}

func (f *FrameHeader) readGlobalParam(r *bits.Reader, typ, ref, idx int) {
	absBits := GmAbsAlphaBits
	precBits := GmAlphaPrecBits
	if idx < 2 {
		if typ == GmTranslation {
			hp := 0
			if !f.AllowHighPrecisionMV {
				hp = 1
			}
			absBits = GmAbsTransOnlyBits - hp
			precBits = GmTransOnlyPrecBits - hp
		} else {
			absBits = GmAbsTransBits
			precBits = GmTransPrecBits
		}
	}
	precDiff := WarpedModelPrecBits - precBits
	round := 0
	sub := 0
	if idx%3 == 2 {
		round = 1 << WarpedModelPrecBits
		sub = 1 << uint(precBits)
	}
	mx := 1 << uint(absBits)
	prev := f.PrevGmParams[ref][idx]
	ref0 := (prev >> uint(precDiff)) - sub
	v := decodeSignedSubexpWithRef(r, -mx, mx+1, ref0)
	f.GmParams[ref][idx] = (v << uint(precDiff)) + round
}

// decodeSignedSubexpWithRef reads a subexponential value from the raw bitstream
// (AV1 spec §5.9.27, the non-arithmetic-coded variant).
func decodeSignedSubexpWithRef(r *bits.Reader, low, high, ref int) int {
	x := decodeUnsignedSubexpWithRef(r, high-low, ref-low)
	return x + low
}

func decodeUnsignedSubexpWithRef(r *bits.Reader, mx, ref int) int {
	v := decodeSubexp(r, mx)
	if (ref << 1) <= mx {
		return inverseRecenter(ref, v)
	}
	return mx - 1 - inverseRecenter(mx-1-ref, v)
}

func decodeSubexp(r *bits.Reader, numSyms int) int {
	i, mk, k := 0, 0, 3
	for {
		b2 := k
		if i != 0 {
			b2 = k + i - 1
		}
		a := 1 << uint(b2)
		if numSyms <= mk+3*a {
			return readNS(r, numSyms-mk) + mk
		}
		if r.F(1) == 1 {
			i++
			mk += a
		} else {
			return int(r.F(b2)) + mk
		}
	}
}

func readNS(r *bits.Reader, n int) int {
	if n <= 1 {
		return 0
	}
	w := 0
	for (1 << uint(w+1)) <= n {
		w++
	}
	w++
	m := (1 << uint(w)) - n
	v := int(r.F(w - 1))
	if v < m {
		return v
	}
	return (v << 1) - m + int(r.F(1))
}

func inverseRecenter(ref, v int) int {
	switch {
	case v > 2*ref:
		return v
	case v&1 != 0:
		return ref - ((v + 1) >> 1)
	default:
		return ref + (v >> 1)
	}
}

// frameSizeWithRefs reads frame_size_with_refs (AV1 spec §5.9.7): a frame may
// inherit its size from a reference, otherwise it signals its own.
func (f *FrameHeader) frameSizeWithRefs(r *bits.Reader, seq *SequenceHeader, st *State) {
	foundRef := false
	for i := 0; i < RefsPerFrame; i++ {
		if r.F(1) == 1 { // found_ref
			idx := f.RefFrameIdx[i]
			f.UpscaledWidth = st.RefUpscaledWidth[idx]
			f.FrameWidth = f.UpscaledWidth
			f.FrameHeight = st.RefFrameHeight[idx]
			f.RenderWidth = st.RefRenderWidth[idx]
			f.RenderHeight = st.RefRenderHeight[idx]
			foundRef = true
			break
		}
	}
	if !foundRef {
		f.frameSize(r, seq)
		f.renderSize(r)
	} else {
		f.superresParams(r, seq)
		f.computeImageSize()
	}
}
