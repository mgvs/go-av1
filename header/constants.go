// Package header parses the AV1 sequence header OBU and the uncompressed frame
// header (AV1 spec §5.5 and §5.9) from the non-arithmetic bitstream. These drive
// every later decoding stage: frame geometry, reference management, tiling,
// quantization, segmentation and the in-loop filter parameters.
package header

// Selected constants (AV1 spec §3, "Symbols and abbreviated terms").
const (
	SelectScreenContentTools = 2
	SelectIntegerMV          = 2

	RefsPerFrame       = 7
	TotalRefsPerFrame  = 8
	NumRefFrames       = 8
	PrimaryRefNone     = 7
	MaxSegments        = 8
	SegLvlAltQ         = 0
	SegLvlRefFrame     = 5
	SegLvlSkip         = 6
	SegLvlGlobalMV     = 7
	SegLvlMax          = 8
	MaxLoopFilter      = 63
	SuperresNum        = 8
	SuperresDenomMin   = 9
	SuperresDenomBits  = 3
	MiSize             = 4
	SuperresUnusedFlag = 0
)

// Color description constants (CICP values; AV1 spec §3).
const (
	CPBT709       = 1
	CPUnspecified = 2
	TCSRGB        = 13
	TCUnspecified = 2
	MCIdentity    = 0
	MCUnspecified = 2
	CSPUnknown    = 0
)

// Frame types (AV1 spec §6.8.2).
const (
	KeyFrame       = 0
	InterFrame     = 1
	IntraOnlyFrame = 2
	SwitchFrame    = 3
)

// Reference frame names (AV1 spec §6.10.24).
const (
	NoneFrame    = -1
	IntraFrame   = 0
	LastFrame    = 1
	Last2Frame   = 2
	Last3Frame   = 3
	GoldenFrame  = 4
	BwdRefFrame  = 5
	AltRef2Frame = 6
	AltRefFrame  = 7
)

// Global motion types + precision (AV1 spec §3, §7.11.3.6).
const (
	GmIdentity    = 0
	GmTranslation = 1
	GmRotZoom     = 2
	GmAffine      = 3

	WarpedModelPrecBits = 16
	GmAbsTransBits      = 12
	GmAbsTransOnlyBits  = 9
	GmAbsAlphaBits      = 12
	GmAlphaPrecBits     = 15
	GmTransPrecBits     = 6
	GmTransOnlyPrecBits = 3

	InterpFilterSwitchable = 4
)

// SegLvlMax feature bit counts (AV1 spec, Segmentation_Feature_Bits / _Signed / _Max).
var (
	segmentationFeatureBits   = [SegLvlMax]int{8, 6, 6, 6, 6, 3, 0, 0}
	segmentationFeatureSigned = [SegLvlMax]int{1, 1, 1, 1, 1, 0, 0, 0}
	segmentationFeatureMax    = [SegLvlMax]int{255, MaxLoopFilter, MaxLoopFilter, MaxLoopFilter, MaxLoopFilter, 7, 0, 0}
)
