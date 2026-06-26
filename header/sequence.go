package header

import "github.com/mgvs/go-av1/bits"

// SequenceHeader holds the parsed sequence_header_obu (AV1 spec §5.5). Fields are
// named to match the spec's syntax elements / derived variables.
type SequenceHeader struct {
	SeqProfile                 int
	StillPicture               bool
	ReducedStillPictureHeader  bool
	TimingInfoPresent          bool
	DecoderModelInfoPresent    bool
	InitialDisplayDelayPresent bool
	OperatingPointsCntMinus1   int
	OperatingPointIdc          [32]int
	SeqLevelIdx                [32]int
	SeqTier                    [32]int

	FrameWidthBitsMinus1  int
	FrameHeightBitsMinus1 int
	MaxFrameWidthMinus1   int
	MaxFrameHeightMinus1  int

	FrameIDNumbersPresent      bool
	DeltaFrameIDLengthMinus2   int
	AdditionalFrameIDLenMinus1 int
	Use128x128Superblock       bool
	EnableFilterIntra          bool
	EnableIntraEdgeFilter      bool
	EnableInterintraCompound   bool
	EnableMaskedCompound       bool
	EnableWarpedMotion         bool
	EnableDualFilter           bool
	EnableOrderHint            bool
	EnableJntComp              bool
	EnableRefFrameMVs          bool
	SeqForceScreenContentTools int
	SeqForceIntegerMV          int
	OrderHintBits              int
	EnableSuperres             bool
	EnableCDEF                 bool
	EnableRestoration          bool
	FilmGrainParamsPresent     bool

	// decoder_model_info (only parsed when present).
	BufferDelayLengthMinus1           int
	NumUnitsInDecodingTick            uint32
	BufferRemovalTimeLengthMinus1     int
	FramePresentationTimeLengthMinus1 int

	ColorConfig
}

// ColorConfig holds color_config() results (AV1 spec §5.5.2).
type ColorConfig struct {
	BitDepth                int
	MonoChrome              bool
	NumPlanes               int
	ColorPrimaries          int
	TransferCharacteristics int
	MatrixCoefficients      int
	ColorRange              int
	SubsamplingX            int
	SubsamplingY            int
	ChromaSamplePosition    int
	SeparateUVDeltaQ        bool
}

// ParseSequenceHeader parses a sequence header OBU payload (AV1 spec §5.5.1).
func ParseSequenceHeader(payload []byte) (*SequenceHeader, error) {
	r := bits.NewReader(payload)
	s := &SequenceHeader{}

	s.SeqProfile = int(r.F(3))
	s.StillPicture = r.F(1) == 1
	s.ReducedStillPictureHeader = r.F(1) == 1
	if s.ReducedStillPictureHeader {
		s.OperatingPointsCntMinus1 = 0
		s.OperatingPointIdc[0] = 0
		s.SeqLevelIdx[0] = int(r.F(5))
		s.SeqTier[0] = 0
	} else {
		s.TimingInfoPresent = r.F(1) == 1
		if s.TimingInfoPresent {
			s.parseTimingInfo(r)
			s.DecoderModelInfoPresent = r.F(1) == 1
			if s.DecoderModelInfoPresent {
				s.parseDecoderModelInfo(r)
			}
		}
		s.InitialDisplayDelayPresent = r.F(1) == 1
		s.OperatingPointsCntMinus1 = int(r.F(5))
		for i := 0; i <= s.OperatingPointsCntMinus1; i++ {
			s.OperatingPointIdc[i] = int(r.F(12))
			s.SeqLevelIdx[i] = int(r.F(5))
			if s.SeqLevelIdx[i] > 7 {
				s.SeqTier[i] = int(r.F(1))
			} else {
				s.SeqTier[i] = 0
			}
			if s.DecoderModelInfoPresent {
				if r.F(1) == 1 { // decoder_model_present_for_this_op
					n := s.BufferDelayLengthMinus1 + 1
					r.F(n) // decoder_buffer_delay
					r.F(n) // encoder_buffer_delay
					r.F(1) // low_delay_mode_flag
				}
			}
			if s.InitialDisplayDelayPresent {
				if r.F(1) == 1 { // initial_display_delay_present_for_this_op
					r.F(4) // initial_display_delay_minus_1
				}
			}
		}
	}
	// choose_operating_point(): operating point 0 for non-scalable decode.

	s.FrameWidthBitsMinus1 = int(r.F(4))
	s.FrameHeightBitsMinus1 = int(r.F(4))
	s.MaxFrameWidthMinus1 = int(r.F(s.FrameWidthBitsMinus1 + 1))
	s.MaxFrameHeightMinus1 = int(r.F(s.FrameHeightBitsMinus1 + 1))

	if s.ReducedStillPictureHeader {
		s.FrameIDNumbersPresent = false
	} else {
		s.FrameIDNumbersPresent = r.F(1) == 1
	}
	if s.FrameIDNumbersPresent {
		s.DeltaFrameIDLengthMinus2 = int(r.F(4))
		s.AdditionalFrameIDLenMinus1 = int(r.F(3))
	}

	s.Use128x128Superblock = r.F(1) == 1
	s.EnableFilterIntra = r.F(1) == 1
	s.EnableIntraEdgeFilter = r.F(1) == 1
	if s.ReducedStillPictureHeader {
		s.SeqForceScreenContentTools = SelectScreenContentTools
		s.SeqForceIntegerMV = SelectIntegerMV
		s.OrderHintBits = 0
	} else {
		s.EnableInterintraCompound = r.F(1) == 1
		s.EnableMaskedCompound = r.F(1) == 1
		s.EnableWarpedMotion = r.F(1) == 1
		s.EnableDualFilter = r.F(1) == 1
		s.EnableOrderHint = r.F(1) == 1
		if s.EnableOrderHint {
			s.EnableJntComp = r.F(1) == 1
			s.EnableRefFrameMVs = r.F(1) == 1
		}
		if r.F(1) == 1 { // seq_choose_screen_content_tools
			s.SeqForceScreenContentTools = SelectScreenContentTools
		} else {
			s.SeqForceScreenContentTools = int(r.F(1))
		}
		if s.SeqForceScreenContentTools > 0 {
			if r.F(1) == 1 { // seq_choose_integer_mv
				s.SeqForceIntegerMV = SelectIntegerMV
			} else {
				s.SeqForceIntegerMV = int(r.F(1))
			}
		} else {
			s.SeqForceIntegerMV = SelectIntegerMV
		}
		if s.EnableOrderHint {
			s.OrderHintBits = int(r.F(3)) + 1
		} else {
			s.OrderHintBits = 0
		}
	}
	s.EnableSuperres = r.F(1) == 1
	s.EnableCDEF = r.F(1) == 1
	s.EnableRestoration = r.F(1) == 1
	s.parseColorConfig(r)
	s.FilmGrainParamsPresent = r.F(1) == 1
	return s, nil
}

func (s *SequenceHeader) parseTimingInfo(r *bits.Reader) {
	r.F(32)          // num_units_in_display_tick
	r.F(32)          // time_scale
	if r.F(1) == 1 { // equal_picture_interval
		r.Uvlc() // num_ticks_per_picture_minus_1
	}
}

func (s *SequenceHeader) parseDecoderModelInfo(r *bits.Reader) {
	s.BufferDelayLengthMinus1 = int(r.F(5))
	s.NumUnitsInDecodingTick = r.F(32)
	s.BufferRemovalTimeLengthMinus1 = int(r.F(5))
	s.FramePresentationTimeLengthMinus1 = int(r.F(5))
}

func (s *SequenceHeader) parseColorConfig(r *bits.Reader) {
	highBitdepth := r.F(1) == 1
	switch {
	case s.SeqProfile == 2 && highBitdepth:
		if r.F(1) == 1 { // twelve_bit
			s.BitDepth = 12
		} else {
			s.BitDepth = 10
		}
	case s.SeqProfile <= 2:
		if highBitdepth {
			s.BitDepth = 10
		} else {
			s.BitDepth = 8
		}
	}
	if s.SeqProfile == 1 {
		s.MonoChrome = false
	} else {
		s.MonoChrome = r.F(1) == 1
	}
	if s.MonoChrome {
		s.NumPlanes = 1
	} else {
		s.NumPlanes = 3
	}
	if r.F(1) == 1 { // color_description_present_flag
		s.ColorPrimaries = int(r.F(8))
		s.TransferCharacteristics = int(r.F(8))
		s.MatrixCoefficients = int(r.F(8))
	} else {
		s.ColorPrimaries = CPUnspecified
		s.TransferCharacteristics = TCUnspecified
		s.MatrixCoefficients = MCUnspecified
	}
	if s.MonoChrome {
		s.ColorRange = int(r.F(1))
		s.SubsamplingX = 1
		s.SubsamplingY = 1
		s.ChromaSamplePosition = CSPUnknown
		s.SeparateUVDeltaQ = false
		return
	}
	if s.ColorPrimaries == CPBT709 && s.TransferCharacteristics == TCSRGB && s.MatrixCoefficients == MCIdentity {
		s.ColorRange = 1
		s.SubsamplingX = 0
		s.SubsamplingY = 0
	} else {
		s.ColorRange = int(r.F(1))
		switch s.SeqProfile {
		case 0:
			s.SubsamplingX = 1
			s.SubsamplingY = 1
		case 1:
			s.SubsamplingX = 0
			s.SubsamplingY = 0
		default:
			if s.BitDepth == 12 {
				s.SubsamplingX = int(r.F(1))
				if s.SubsamplingX == 1 {
					s.SubsamplingY = int(r.F(1))
				} else {
					s.SubsamplingY = 0
				}
			} else {
				s.SubsamplingX = 1
				s.SubsamplingY = 0
			}
		}
		if s.SubsamplingX == 1 && s.SubsamplingY == 1 {
			s.ChromaSamplePosition = int(r.F(2))
		}
	}
	s.SeparateUVDeltaQ = r.F(1) == 1
}
