package predict

import "fmt"

// PredictIntra runs the general intra prediction process for a transform block at
// (x,y) of size 2^log2W × 2^log2H within this plane (AV1 spec §7.11.2). It builds
// the AboveRow / LeftCol reference samples from already-reconstructed neighbors and
// dispatches to the predictor for mode, writing the result into the plane. maxX/maxY
// are the maximum valid sample coordinates in the plane. Only DC / Paeth / Smooth*
// are implemented; directional, CfL and filter-intra modes return an error.
func (p *Plane) PredictIntra(x, y, log2W, log2H int, haveLeft, haveAbove, haveAboveRight, haveBelowLeft bool, mode, bitDepth, maxX, maxY, angleDelta, filterType int, enableEdgeFilter, useFilterIntra bool, filterIntraMode int) error {
	w := 1 << uint(log2W)
	h := 1 << uint(log2H)
	base := 1 << uint(bitDepth-1)

	// Edge buffers (int) holding AboveRow[-2..] and LeftCol[-2..], indexed via bufOff.
	bufSz := 2*(w+h) + 2*bufOff + 8
	aboveRow := make([]int, bufSz)
	leftCol := make([]int, bufSz)

	switch {
	case !haveAbove && haveLeft:
		v := int(p.At(x-1, y))
		for i := 0; i < w+h; i++ {
			aboveRow[bufOff+i] = v
		}
	case !haveAbove && !haveLeft:
		for i := 0; i < w+h; i++ {
			aboveRow[bufOff+i] = base - 1
		}
	default:
		lim := x + w - 1
		if haveAboveRight {
			lim = x + 2*w - 1
		}
		al := min(maxX, lim)
		for i := 0; i < w+h; i++ {
			aboveRow[bufOff+i] = int(p.At(min(al, x+i), y-1))
		}
	}

	switch {
	case !haveLeft && haveAbove:
		v := int(p.At(x, y-1))
		for i := 0; i < w+h; i++ {
			leftCol[bufOff+i] = v
		}
	case !haveLeft && !haveAbove:
		for i := 0; i < w+h; i++ {
			leftCol[bufOff+i] = base + 1
		}
	default:
		lim := y + h - 1
		if haveBelowLeft {
			lim = y + 2*h - 1
		}
		ll := min(maxY, lim)
		for i := 0; i < w+h; i++ {
			leftCol[bufOff+i] = int(p.At(x-1, min(ll, y+i)))
		}
	}

	var tl int
	switch {
	case haveAbove && haveLeft:
		tl = int(p.At(x-1, y-1))
	case haveAbove:
		tl = int(p.At(x, y-1))
	case haveLeft:
		tl = int(p.At(x-1, y))
	default:
		tl = base
	}
	aboveRow[bufOff-1] = tl
	leftCol[bufOff-1] = tl

	pred := make([][]int, h)
	for i := range pred {
		pred[i] = make([]int, w)
	}

	switch {
	case useFilterIntra:
		filterIntra(pred, aboveRow, leftCol, w, h, filterIntraMode, bitDepth)
	case IsDirectional(mode):
		directional(pred, aboveRow, leftCol, w, h, mode, angleDelta, filterType,
			haveLeft, haveAbove, enableEdgeFilter, x, y, maxX, maxY, bitDepth)
	case mode == ModeDC:
		dc := DCValue(aboveRow[bufOff:bufOff+w], leftCol[bufOff:bufOff+h], haveAbove, haveLeft, log2W, log2H, bitDepth)
		for i := range pred {
			for j := range pred[i] {
				pred[i][j] = dc
			}
		}
	case mode == ModePaeth:
		pred = Paeth(aboveRow[bufOff:bufOff+w], leftCol[bufOff:bufOff+h], tl, w, h)
	case mode == ModeSmooth || mode == ModeSmoothV || mode == ModeSmoothH:
		pred = Smooth(mode, log2W, log2H, aboveRow[bufOff:bufOff+w], leftCol[bufOff:bufOff+h])
	default:
		return fmt.Errorf("predict: intra mode %d not implemented", mode)
	}
	// Transform blocks that straddle the frame's right/bottom edge (non-superblock-
	// aligned dimensions) are decoded in full and stored up to the allocated
	// (superblock-aligned) extent so chroma-from-luma can read them.
	for i := 0; i < h && y+i < p.AllocH; i++ {
		for j := 0; j < w && x+j < p.AllocW; j++ {
			p.Set(x+j, y+i, uint16(pred[i][j]))
		}
	}
	return nil
}

// Plane is a reconstructed image plane of 16-bit samples (covers 8/10/12-bit).
type Plane struct {
	Width  int // logical width (mi grid); used for availability/reference clamps
	Height int
	Stride int // = AllocW
	// AllocW/AllocH are the allocated extent (superblock-aligned). Transform
	// reconstruction at a non-aligned frame edge writes the full transform up to
	// these bounds (past Width/Height) so chroma-from-luma reads see the real
	// reconstructed samples beyond the mi grid (matching dav1d/libaom).
	AllocW int
	AllocH int
	Data   []uint16
}

// NewPlane allocates a w×h plane (AllocW/AllocH equal to the logical size).
func NewPlane(w, h int) *Plane {
	return &Plane{Width: w, Height: h, Stride: w, AllocW: w, AllocH: h, Data: make([]uint16, w*h)}
}

// NewPlaneAlloc allocates a plane whose logical size is w×h but whose backing
// buffer spans allocW×allocH (≥ w/h), to hold edge transforms that straddle the
// mi-grid boundary.
func NewPlaneAlloc(w, h, allocW, allocH int) *Plane {
	if allocW < w {
		allocW = w
	}
	if allocH < h {
		allocH = h
	}
	return &Plane{Width: w, Height: h, Stride: allocW, AllocW: allocW, AllocH: allocH, Data: make([]uint16, allocW*allocH)}
}

// At returns the sample at (x,y).
func (p *Plane) At(x, y int) uint16 { return p.Data[y*p.Stride+x] }

// Set writes the sample at (x,y).
func (p *Plane) Set(x, y int, v uint16) { p.Data[y*p.Stride+x] = v }

// setClip writes the sample at (x,y), ignoring writes outside the plane (for
// transform/prediction blocks that straddle a non-aligned frame edge).
func (p *Plane) SetClip(x, y int, v uint16) {
	if x < p.Width && y < p.Height {
		p.Data[y*p.Stride+x] = v
	}
}

// Clone returns a deep copy of the plane (used for film grain, which must not
// modify the grain-free reference samples).
func (p *Plane) Clone() *Plane {
	d := make([]uint16, len(p.Data))
	copy(d, p.Data)
	return &Plane{Data: d, Stride: p.Stride, Width: p.Width, Height: p.Height, AllocW: p.AllocW, AllocH: p.AllocH}
}

// clip1 clamps to the valid sample range for bitDepth (spec Clip1).
func clip1(v, bitDepth int) int {
	hi := (1 << uint(bitDepth)) - 1
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}

// DCValue computes the DC intra prediction fill value (AV1 spec §7.11.2.5). DC
// fills the whole transform block with a single value, so this returns that value.
// above holds the w samples of AboveRow[0..w-1]; left holds the h samples of
// LeftCol[0..h-1]; either may be nil when not available.
func DCValue(above, left []int, haveAbove, haveLeft bool, log2W, log2H, bitDepth int) int {
	w := 1 << uint(log2W)
	h := 1 << uint(log2H)
	switch {
	case haveAbove && haveLeft:
		sum := 0
		for k := 0; k < h; k++ {
			sum += left[k]
		}
		for k := 0; k < w; k++ {
			sum += above[k]
		}
		sum += (w + h) >> 1
		return sum / (w + h)
	case haveLeft:
		sum := 0
		for k := 0; k < h; k++ {
			sum += left[k]
		}
		return clip1((sum+(h>>1))>>uint(log2H), bitDepth)
	case haveAbove:
		sum := 0
		for k := 0; k < w; k++ {
			sum += above[k]
		}
		return clip1((sum+(w>>1))>>uint(log2W), bitDepth)
	default:
		return 1 << uint(bitDepth-1)
	}
}
