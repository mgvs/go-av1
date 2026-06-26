package predict

// Intra prediction modes (AV1 spec §6.10.5).
const (
	ModeDC      = 0
	ModePaeth   = 12
	ModeSmooth  = 9
	ModeSmoothV = 10
	ModeSmoothH = 11
)

// smWeights returns the smooth-prediction weight table for a given log2 dimension
// (AV1 spec §7.11.2.6).
func smWeights(log2 int) []int {
	switch log2 {
	case 2:
		return smWeights4
	case 3:
		return smWeights8
	case 4:
		return smWeights16
	case 5:
		return smWeights32
	default:
		return smWeights64
	}
}

func round2(x, n int) int { return (x + (1 << uint(n-1))) >> uint(n) }

// Paeth fills the w×h block with the Paeth predictor (AV1 spec §7.11.2.2, the basic
// intra prediction process). above holds AboveRow[0..w-1], left holds LeftCol[0..h-1]
// and topLeft is AboveRow[-1].
func Paeth(above, left []int, topLeft int, w, h int) [][]int {
	pred := make([][]int, h)
	tl := topLeft
	for i := 0; i < h; i++ {
		pred[i] = make([]int, w)
		l := left[i]
		for j := 0; j < w; j++ {
			a := above[j]
			base := a + l - tl
			pLeft := abs(base - l)
			pTop := abs(base - a)
			pTopLeft := abs(base - tl)
			switch {
			case pLeft <= pTop && pLeft <= pTopLeft:
				pred[i][j] = l
			case pTop <= pTopLeft:
				pred[i][j] = a
			default:
				pred[i][j] = tl
			}
		}
	}
	return pred
}

// Smooth fills the w×h block with the SMOOTH / SMOOTH_V / SMOOTH_H predictor
// (AV1 spec §7.11.2.6). w = 1<<log2W, h = 1<<log2H.
func Smooth(mode, log2W, log2H int, above, left []int) [][]int {
	w := 1 << uint(log2W)
	h := 1 << uint(log2H)
	pred := make([][]int, h)
	for i := range pred {
		pred[i] = make([]int, w)
	}
	switch mode {
	case ModeSmooth:
		wx := smWeights(log2W)
		wy := smWeights(log2H)
		bottomLeft := left[h-1]
		topRight := above[w-1]
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				s := wy[i]*above[j] + (256-wy[i])*bottomLeft +
					wx[j]*left[i] + (256-wx[j])*topRight
				pred[i][j] = round2(s, 9)
			}
		}
	case ModeSmoothV:
		wt := smWeights(log2H)
		bottomLeft := left[h-1]
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				s := wt[i]*above[j] + (256-wt[i])*bottomLeft
				pred[i][j] = round2(s, 8)
			}
		}
	default: // ModeSmoothH
		wt := smWeights(log2W)
		topRight := above[w-1]
		for i := 0; i < h; i++ {
			for j := 0; j < w; j++ {
				s := wt[j]*left[i] + (256-wt[j])*topRight
				pred[i][j] = round2(s, 8)
			}
		}
	}
	return pred
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
