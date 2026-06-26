package decode

import (
	"sort"

	"github.com/mgvs/go-av1/predict"
)

// Palette color context tables (AV1 spec §9.3 / §10).
var paletteColorContext = [9]int{-1, -1, 0, -1, -1, 4, 3, 2, 1}
var paletteColorHashMultipliers = [3]int{1, 2, 2}

const paletteColors = 8 // PALETTE_COLORS

// paletteModeInfo reads the palette flags, sizes and colors (AV1 spec §5.11.46).
func (fd *frameDecoder) paletteModeInfo() error {
	fd.paletteSizeY, fd.paletteSizeUV = 0, 0
	bd := fd.bitDepth
	bsizeCtx := predict.MiWidthLog2[fd.miSize] + predict.MiHeightLog2[fd.miSize] - 2

	if fd.yMode == DCPred {
		ctx := 0
		if fd.availU && fd.paletteSizesGrid[0][fd.miRow-1][fd.miCol] > 0 {
			ctx++
		}
		if fd.availL && fd.paletteSizesGrid[0][fd.miRow][fd.miCol-1] > 0 {
			ctx++
		}
		if fd.d.DecodeSymbol(fd.c.paletteYMode[bsizeCtx][ctx]) == 1 {
			fd.paletteSizeY = fd.d.DecodeSymbol(fd.c.paletteYSize[bsizeCtx]) + 2
			cache := fd.getPaletteCache(0)
			cols := fd.paletteColorsY[:0]
			idx := 0
			for i := 0; i < len(cache) && idx < fd.paletteSizeY; i++ {
				if fd.d.ReadLiteral(1) == 1 {
					fd.paletteColorsY[idx] = cache[i]
					idx++
				}
			}
			if idx < fd.paletteSizeY {
				fd.paletteColorsY[idx] = int(fd.d.ReadLiteral(bd))
				idx++
			}
			if idx < fd.paletteSizeY {
				paletteBits := bd - 3 + int(fd.d.ReadLiteral(2))
				for idx < fd.paletteSizeY {
					delta := int(fd.d.ReadLiteral(paletteBits)) + 1
					fd.paletteColorsY[idx] = clip1(fd.paletteColorsY[idx-1]+delta, bd)
					rng := (1 << uint(bd)) - fd.paletteColorsY[idx] - 1
					paletteBits = mini(paletteBits, ceilLog2(rng))
					idx++
				}
			}
			_ = cols
			sort.Ints(fd.paletteColorsY[:fd.paletteSizeY])
		}
	}

	if fd.hasChroma && fd.uvMode == DCPred {
		ctx := 0
		if fd.paletteSizeY > 0 {
			ctx = 1
		}
		if fd.d.DecodeSymbol(fd.c.paletteUvMode[ctx]) == 1 {
			fd.paletteSizeUV = fd.d.DecodeSymbol(fd.c.paletteUvSize[bsizeCtx]) + 2
			// U plane colors.
			cache := fd.getPaletteCache(1)
			idx := 0
			for i := 0; i < len(cache) && idx < fd.paletteSizeUV; i++ {
				if fd.d.ReadLiteral(1) == 1 {
					fd.paletteColorsU[idx] = cache[i]
					idx++
				}
			}
			if idx < fd.paletteSizeUV {
				fd.paletteColorsU[idx] = int(fd.d.ReadLiteral(bd))
				idx++
			}
			if idx < fd.paletteSizeUV {
				paletteBits := bd - 3 + int(fd.d.ReadLiteral(2))
				for idx < fd.paletteSizeUV {
					delta := int(fd.d.ReadLiteral(paletteBits))
					fd.paletteColorsU[idx] = clip1(fd.paletteColorsU[idx-1]+delta, bd)
					rng := (1 << uint(bd)) - fd.paletteColorsU[idx]
					paletteBits = mini(paletteBits, ceilLog2(rng))
					idx++
				}
			}
			sort.Ints(fd.paletteColorsU[:fd.paletteSizeUV])
			// V plane colors.
			if fd.d.ReadLiteral(1) == 1 {
				paletteBits := bd - 4 + int(fd.d.ReadLiteral(2))
				maxVal := 1 << uint(bd)
				fd.paletteColorsV[0] = int(fd.d.ReadLiteral(bd))
				for i := 1; i < fd.paletteSizeUV; i++ {
					delta := int(fd.d.ReadLiteral(paletteBits))
					if delta != 0 && fd.d.ReadLiteral(1) == 1 {
						delta = -delta
					}
					val := fd.paletteColorsV[i-1] + delta
					if val < 0 {
						val += maxVal
					}
					if val >= maxVal {
						val -= maxVal
					}
					fd.paletteColorsV[i] = clip1(val, bd)
				}
			} else {
				for i := 0; i < fd.paletteSizeUV; i++ {
					fd.paletteColorsV[i] = int(fd.d.ReadLiteral(bd))
				}
			}
		}
	}
	return nil
}

// getPaletteCache merges the above and left palettes into a sorted cache
// (AV1 spec §5.11.47 get_palette_cache).
func (fd *frameDecoder) getPaletteCache(plane int) []int {
	aboveN := 0
	if (fd.miRow*4)%64 != 0 && fd.miRow > 0 {
		aboveN = fd.paletteSizesGrid[plane][fd.miRow-1][fd.miCol]
	}
	leftN := 0
	if fd.availL {
		leftN = fd.paletteSizesGrid[plane][fd.miRow][fd.miCol-1]
	}
	var above, left []int
	if aboveN > 0 {
		above = fd.paletteColorsGrid[plane][fd.miRow-1][fd.miCol]
	}
	if leftN > 0 {
		left = fd.paletteColorsGrid[plane][fd.miRow][fd.miCol-1]
	}
	cache := make([]int, 0, aboveN+leftN)
	aboveIdx, leftIdx := 0, 0
	push := func(v int) {
		if len(cache) == 0 || v != cache[len(cache)-1] {
			cache = append(cache, v)
		}
	}
	for aboveIdx < aboveN && leftIdx < leftN {
		aboveC := above[aboveIdx]
		leftC := left[leftIdx]
		if leftC < aboveC {
			push(leftC)
			leftIdx++
		} else {
			push(aboveC)
			aboveIdx++
			if leftC == aboveC {
				leftIdx++
			}
		}
	}
	for aboveIdx < aboveN {
		push(above[aboveIdx])
		aboveIdx++
	}
	for leftIdx < leftN {
		push(left[leftIdx])
		leftIdx++
	}
	return cache
}

// paletteTokens decodes the color index maps (AV1 spec §5.11.49).
func (fd *frameDecoder) paletteTokens() {
	blockHeight := predict.BlockHeight(fd.miSize)
	blockWidth := predict.BlockWidth(fd.miSize)
	onscreenHeight := mini(blockHeight, (fd.miRows-fd.miRow)*4)
	onscreenWidth := mini(blockWidth, (fd.miCols-fd.miCol)*4)

	if fd.paletteSizeY > 0 {
		m := makeIntGrid(blockHeight, blockWidth)
		m[0][0] = fd.d.ReadNS(fd.paletteSizeY)
		fd.decodeColorMap(m, onscreenHeight, onscreenWidth, blockHeight, blockWidth, fd.paletteSizeY, 0)
		fd.colorMapY = m
	}
	if fd.paletteSizeUV > 0 {
		bh := blockHeight >> fd.subY
		bw := blockWidth >> fd.subX
		oh := onscreenHeight >> fd.subY
		ow := onscreenWidth >> fd.subX
		if bw < 4 {
			bw += 2
			ow += 2
		}
		if bh < 4 {
			bh += 2
			oh += 2
		}
		m := makeIntGrid(bh, bw)
		m[0][0] = fd.d.ReadNS(fd.paletteSizeUV)
		fd.decodeColorMap(m, oh, ow, bh, bw, fd.paletteSizeUV, 1)
		fd.colorMapUV = m
	}
}

// decodeColorMap fills the diagonal wavefront of a color index map and replicates
// the off-screen edges (AV1 spec §5.11.49).
func (fd *frameDecoder) decodeColorMap(m [][]int, onscreenHeight, onscreenWidth, blockHeight, blockWidth, paletteSize, plane int) {
	for i := 1; i < onscreenHeight+onscreenWidth-1; i++ {
		for j := mini(i, onscreenWidth-1); j >= maxi(0, i-onscreenHeight+1); j-- {
			ctx, colorOrder := getPaletteColorContext(m, i-j, j, paletteSize)
			var cdf []uint16
			if plane == 0 {
				cdf = fd.c.paletteYColor[paletteSize][ctx]
			} else {
				cdf = fd.c.paletteUvColor[paletteSize][ctx]
			}
			idx := fd.d.DecodeSymbol(cdf)
			m[i-j][j] = colorOrder[idx]
		}
	}
	for i := 0; i < onscreenHeight; i++ {
		for j := onscreenWidth; j < blockWidth; j++ {
			m[i][j] = m[i][onscreenWidth-1]
		}
	}
	for i := onscreenHeight; i < blockHeight; i++ {
		for j := 0; j < blockWidth; j++ {
			m[i][j] = m[onscreenHeight-1][j]
		}
	}
}

// getPaletteColorContext derives the color context hash and color ordering for a
// color map position (AV1 spec §5.11.50).
func getPaletteColorContext(colorMap [][]int, r, c, n int) (ctx int, colorOrder [paletteColors]int) {
	var scores [paletteColors]int
	for i := 0; i < paletteColors; i++ {
		colorOrder[i] = i
	}
	if c > 0 {
		scores[colorMap[r][c-1]] += 2
	}
	if r > 0 && c > 0 {
		scores[colorMap[r-1][c-1]]++
	}
	if r > 0 {
		scores[colorMap[r-1][c]] += 2
	}
	for i := 0; i < 3; i++ { // PALETTE_NUM_NEIGHBORS
		maxScore := scores[i]
		maxIdx := i
		for j := i + 1; j < n; j++ {
			if scores[j] > maxScore {
				maxScore = scores[j]
				maxIdx = j
			}
		}
		if maxIdx != i {
			maxScore = scores[maxIdx]
			maxColorOrder := colorOrder[maxIdx]
			for k := maxIdx; k > i; k-- {
				scores[k] = scores[k-1]
				colorOrder[k] = colorOrder[k-1]
			}
			scores[i] = maxScore
			colorOrder[i] = maxColorOrder
		}
	}
	hash := 0
	for i := 0; i < 3; i++ {
		hash += scores[i] * paletteColorHashMultipliers[i]
	}
	return paletteColorContext[hash], colorOrder
}

func makeIntGrid(h, w int) [][]int {
	m := make([][]int, h)
	for i := range m {
		m[i] = make([]int, w)
	}
	return m
}

func clip1(v, bitDepth int) int { return clip3i(0, (1<<uint(bitDepth))-1, v) }

func ceilLog2(x int) int {
	if x < 2 {
		return 0
	}
	i, p := 1, 2
	for p < x {
		i++
		p <<= 1
	}
	return i
}

// predictPalette maps the per-pixel color indices to palette colors for one
// transform block (AV1 spec §7.11.4, palette prediction process).
func (fd *frameDecoder) predictPalette(plane, startX, startY, baseX, baseY, txSz int) {
	w := TxWidth[txSz]
	h := TxHeight[txSz]
	var pal []int
	var cmap [][]int
	switch plane {
	case 0:
		pal = fd.paletteColorsY[:fd.paletteSizeY]
		cmap = fd.colorMapY
	case 1:
		pal = fd.paletteColorsU[:fd.paletteSizeUV]
		cmap = fd.colorMapUV
	default:
		pal = fd.paletteColorsV[:fd.paletteSizeUV]
		cmap = fd.colorMapUV
	}
	offY := startY - baseY
	offX := startX - baseX
	pl := fd.planes[plane]
	for i := 0; i < h && startY+i < pl.AllocH; i++ {
		for j := 0; j < w && startX+j < pl.AllocW; j++ {
			pl.Set(startX+j, startY+i, uint16(pal[cmap[offY+i][offX+j]]))
		}
	}
}
