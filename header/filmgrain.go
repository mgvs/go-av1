package header

import "github.com/mgvs/go-av1/bits"

// FilmGrainParams holds the parsed film_grain_params (AV1 spec §5.9.30).
type FilmGrainParams struct {
	ApplyGrain bool
	GrainSeed  int

	NumYPoints    int
	PointYValue   [14]int
	PointYScaling [14]int

	ChromaScalingFromLuma bool

	NumCbPoints    int
	PointCbValue   [10]int
	PointCbScaling [10]int
	NumCrPoints    int
	PointCrValue   [10]int
	PointCrScaling [10]int

	GrainScalingMinus8 int
	ArCoeffLag         int
	ArCoeffsYPlus128   [24]int
	ArCoeffsCbPlus128  [25]int
	ArCoeffsCrPlus128  [25]int
	ArCoeffShiftMinus6 int
	GrainScaleShift    int

	CbMult, CbLumaMult, CbOffset int
	CrMult, CrLumaMult, CrOffset int

	OverlapFlag           bool
	ClipToRestrictedRange bool
}

// filmGrainParams parses film_grain_params (AV1 spec §5.9.30) into f.FilmGrain.
func (f *FrameHeader) filmGrainParams(r *bits.Reader, seq *SequenceHeader, st *State) {
	f.FilmGrain = &FilmGrainParams{}
	if !seq.FilmGrainParamsPresent || (!f.ShowFrame && !f.ShowableFrame) {
		return
	}
	fg := f.FilmGrain
	fg.ApplyGrain = r.F(1) == 1
	if !fg.ApplyGrain {
		return
	}
	fg.GrainSeed = int(r.F(16))
	updateGrain := true
	if f.FrameType == InterFrame {
		updateGrain = r.F(1) == 1
	}
	if !updateGrain {
		refIdx := int(r.F(3))
		seed := fg.GrainSeed
		// load_grain_params: copy from the reference, keeping the new seed.
		*fg = st.SavedFilmGrain[refIdx]
		fg.GrainSeed = seed
		fg.ApplyGrain = true
		return
	}
	fg.NumYPoints = int(r.F(4))
	if fg.NumYPoints > len(fg.PointYValue) {
		fg.NumYPoints = len(fg.PointYValue)
	}
	for i := 0; i < fg.NumYPoints; i++ {
		fg.PointYValue[i] = int(r.F(8))
		fg.PointYScaling[i] = int(r.F(8))
	}
	if !seq.MonoChrome {
		fg.ChromaScalingFromLuma = r.F(1) == 1
	}
	if seq.MonoChrome || fg.ChromaScalingFromLuma ||
		(seq.SubsamplingX == 1 && seq.SubsamplingY == 1 && fg.NumYPoints == 0) {
		fg.NumCbPoints = 0
		fg.NumCrPoints = 0
	} else {
		fg.NumCbPoints = int(r.F(4))
		if fg.NumCbPoints > len(fg.PointCbValue) {
			fg.NumCbPoints = len(fg.PointCbValue)
		}
		for i := 0; i < fg.NumCbPoints; i++ {
			fg.PointCbValue[i] = int(r.F(8))
			fg.PointCbScaling[i] = int(r.F(8))
		}
		fg.NumCrPoints = int(r.F(4))
		if fg.NumCrPoints > len(fg.PointCrValue) {
			fg.NumCrPoints = len(fg.PointCrValue)
		}
		for i := 0; i < fg.NumCrPoints; i++ {
			fg.PointCrValue[i] = int(r.F(8))
			fg.PointCrScaling[i] = int(r.F(8))
		}
	}
	fg.GrainScalingMinus8 = int(r.F(2))
	fg.ArCoeffLag = int(r.F(2))
	numPosLuma := 2 * fg.ArCoeffLag * (fg.ArCoeffLag + 1)
	numPosChroma := numPosLuma
	if fg.NumYPoints > 0 {
		numPosChroma = numPosLuma + 1
		for i := 0; i < numPosLuma; i++ {
			fg.ArCoeffsYPlus128[i] = int(r.F(8))
		}
	}
	if fg.ChromaScalingFromLuma || fg.NumCbPoints > 0 {
		for i := 0; i < numPosChroma; i++ {
			fg.ArCoeffsCbPlus128[i] = int(r.F(8))
		}
	}
	if fg.ChromaScalingFromLuma || fg.NumCrPoints > 0 {
		for i := 0; i < numPosChroma; i++ {
			fg.ArCoeffsCrPlus128[i] = int(r.F(8))
		}
	}
	fg.ArCoeffShiftMinus6 = int(r.F(2))
	fg.GrainScaleShift = int(r.F(2))
	if fg.NumCbPoints > 0 {
		fg.CbMult = int(r.F(8))
		fg.CbLumaMult = int(r.F(8))
		fg.CbOffset = int(r.F(9))
	}
	if fg.NumCrPoints > 0 {
		fg.CrMult = int(r.F(8))
		fg.CrLumaMult = int(r.F(8))
		fg.CrOffset = int(r.F(9))
	}
	fg.OverlapFlag = r.F(1) == 1
	fg.ClipToRestrictedRange = r.F(1) == 1
}
