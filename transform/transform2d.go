package transform

// Transform types (AV1 spec §6.10.4).
const (
	DCTDCT = iota
	ADSTDCT
	DCTADST
	ADSTADST
	FLIPADSTDCT
	DCTFLIPADST
	FLIPADSTFLIPADST
	ADSTFLIPADST
	FLIPADSTADST
	IDTX
	VDCT
	HDCT
	VADST
	HADST
	VFLIPADST
	HFLIPADST
)

// Transform kinds for one dimension.
const (
	kindDCT = iota
	kindADST
	kindIdentity
)

// rowKind / colKind classify a 2D transform type into its 1D row and column
// transforms (AV1 spec §7.13.3). FLIPADST uses the ADST transform; the flip itself
// is applied during reconstruction.
func rowKind(txType int) int {
	switch txType {
	case DCTDCT, ADSTDCT, FLIPADSTDCT, HDCT:
		return kindDCT
	case IDTX, VDCT, VADST, VFLIPADST:
		return kindIdentity
	default:
		return kindADST
	}
}

func colKind(txType int) int {
	switch txType {
	case DCTDCT, DCTADST, DCTFLIPADST, VDCT:
		return kindDCT
	case IDTX, HDCT, HADST, HFLIPADST:
		return kindIdentity
	default:
		return kindADST
	}
}

func apply1D(t []int64, kind, n, r int) {
	switch kind {
	case kindDCT:
		InverseDCT(t, n, r)
	case kindADST:
		InverseADST(t, n, r)
	default:
		InverseIdentity(t, n)
	}
}

// transformRowShift[txSz] is the rounding shift after the row transform
// (AV1 spec §7.13.3, Transform_Row_Shift). Column shift is always 4.
var transformRowShift = [19]int{0, 1, 2, 2, 2, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2}

// txWidthLog2 / txHeightLog2 for the transform sizes (AV1 spec §9.3).
var txWidthLog2 = [19]int{2, 3, 4, 5, 6, 2, 3, 3, 4, 4, 5, 5, 6, 2, 4, 3, 5, 4, 6}
var txHeightLog2 = [19]int{2, 3, 4, 5, 6, 3, 2, 4, 3, 5, 4, 6, 5, 4, 2, 5, 3, 6, 4}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Inverse2D performs the 2D inverse transform of the dequantized coefficients
// (AV1 spec §7.13.3) and returns the residual as a row-major h×w int32 slice.
// dequant[i][j] holds Dequant[i][j] for i in 0..th-1, j in 0..tw-1 (the up-to-32×32
// nonzero region). Only the DCT_DCT type is supported so far.
func Inverse2D(txSz, txType int, dequant [][]int64, bitDepth int) ([]int32, int, int, error) {
	rk := rowKind(txType)
	ck := colKind(txType)
	log2W, log2H := txWidthLog2[txSz], txHeightLog2[txSz]
	w, h := 1<<uint(log2W), 1<<uint(log2H)
	rowShift := transformRowShift[txSz]
	colShift := 4
	rowClamp := bitDepth + 8
	colClamp := bitDepth + 6
	if colClamp < 16 {
		colClamp = 16
	}
	rectScale := absInt(log2W-log2H) == 1

	res := make([]int32, w*h)

	// Row transforms.
	row := make([]int64, w)
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			if i < 32 && j < 32 && i < len(dequant) && j < len(dequant[i]) {
				row[j] = dequant[i][j]
			} else {
				row[j] = 0
			}
		}
		if rectScale {
			for j := 0; j < w; j++ {
				row[j] = round2(row[j]*2896, 12)
			}
		}
		apply1D(row, rk, log2W, rowClamp)
		for j := 0; j < w; j++ {
			res[i*w+j] = int32(round2(row[j], rowShift))
		}
	}
	// Inter-stage clamp.
	lo := -(int64(1) << uint(colClamp-1))
	hi := (int64(1) << uint(colClamp-1)) - 1
	for k := range res {
		res[k] = int32(clip3(lo, hi, int64(res[k])))
	}
	// Column transforms.
	col := make([]int64, h)
	for j := 0; j < w; j++ {
		for i := 0; i < h; i++ {
			col[i] = int64(res[i*w+j])
		}
		apply1D(col, ck, log2H, colClamp)
		for i := 0; i < h; i++ {
			res[i*w+j] = int32(round2(col[i], colShift))
		}
	}
	return res, w, h, nil
}

// invWHT performs the in-place inverse Walsh-Hadamard transform on a length-4
// array with the given pre-scaling shift (AV1 spec §7.13.2.10).
func invWHT(t []int64, shift uint) {
	a := t[0] >> shift
	c := t[1] >> shift
	d := t[2] >> shift
	b := t[3] >> shift
	a += c
	d -= b
	e := (a - d) >> 1
	b = e - b
	c = e - c
	a -= b
	d += c
	t[0] = a
	t[1] = b
	t[2] = c
	t[3] = d
}

// InverseWHT2D performs the 2D inverse Walsh-Hadamard transform used for lossless
// coding (always a 4x4 block): rows with shift 2, columns with shift 0, no
// row/column rounding shifts (AV1 spec §7.13.3, Lossless path).
func InverseWHT2D(dequant [][]int64, bitDepth int) ([]int32, int, int, error) {
	const w, h = 4, 4
	colClamp := bitDepth + 6
	if colClamp < 16 {
		colClamp = 16
	}
	res := make([]int32, w*h)
	t := make([]int64, 4)
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			if i < len(dequant) && j < len(dequant[i]) {
				t[j] = dequant[i][j]
			} else {
				t[j] = 0
			}
		}
		invWHT(t, 2)
		for j := 0; j < w; j++ {
			res[i*w+j] = int32(t[j])
		}
	}
	lo := -(int64(1) << uint(colClamp-1))
	hi := (int64(1) << uint(colClamp-1)) - 1
	for k := range res {
		res[k] = int32(clip3(lo, hi, int64(res[k])))
	}
	for j := 0; j < w; j++ {
		for i := 0; i < h; i++ {
			t[i] = int64(res[i*w+j])
		}
		invWHT(t, 0)
		for i := 0; i < h; i++ {
			res[i*w+j] = int32(t[i])
		}
	}
	return res, w, h, nil
}
