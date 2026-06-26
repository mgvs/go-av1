package header

import "testing"

// seqHeader64 is the real sequence-header OBU payload aomenc 3.13 emits for a
// 64x64 still keyframe (profile 0, 8-bit 4:2:0). Extracted from an IVF temporal
// unit (OBU type 1, 6-byte payload).
var seqHeader64 = []byte{0x18, 0x15, 0x7f, 0xfd, 0xb0, 0x08}

func TestParseSequenceHeader64(t *testing.T) {
	s, err := ParseSequenceHeader(seqHeader64)
	if err != nil {
		t.Fatal(err)
	}
	if s.SeqProfile != 0 {
		t.Errorf("SeqProfile=%d want 0", s.SeqProfile)
	}
	if !s.ReducedStillPictureHeader {
		t.Errorf("ReducedStillPictureHeader=false want true")
	}
	if got := s.MaxFrameWidthMinus1 + 1; got != 64 {
		t.Errorf("width=%d want 64", got)
	}
	if got := s.MaxFrameHeightMinus1 + 1; got != 64 {
		t.Errorf("height=%d want 64", got)
	}
	if s.BitDepth != 8 {
		t.Errorf("BitDepth=%d want 8", s.BitDepth)
	}
	if s.MonoChrome || s.NumPlanes != 3 {
		t.Errorf("mono=%v planes=%d want false,3", s.MonoChrome, s.NumPlanes)
	}
	if s.SubsamplingX != 1 || s.SubsamplingY != 1 {
		t.Errorf("subsampling=%d,%d want 1,1 (4:2:0)", s.SubsamplingX, s.SubsamplingY)
	}
	if s.FilmGrainParamsPresent {
		t.Errorf("FilmGrainParamsPresent=true want false")
	}
}
