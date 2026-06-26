package decode

import (
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/predict"
)

// applyFilmGrain synthesises film grain noise onto a copy of the frame and
// returns the grain-applied output (AV1 spec §7.18.3). The reference frames keep
// the grain-free samples, so the input planes are not modified.
func (dec *Decoder) applyFilmGrain(frame *Frame, fg *header.FilmGrainParams) *Frame {
	if fg == nil || !fg.ApplyGrain {
		return frame
	}
	seq := dec.seq
	bd := frame.BitDepth
	subX, subY := frame.SubX, frame.SubY

	grainCenter := 128 << uint(bd-8)
	grainMin := -grainCenter
	grainMax := (256 << uint(bd-8)) - 1 - grainCenter

	g := &grainState{
		fg: fg, bd: bd, subX: subX, subY: subY,
		grainMin: grainMin, grainMax: grainMax,
		monoChrome: seq.MonoChrome, numPlanes: frame.NumPlanes,
	}
	g.generateGrain()
	g.initScalingLut()

	// Copy planes so reference storage keeps grain-free samples.
	out := make([]*predict.Plane, len(frame.Planes))
	for p := range frame.Planes {
		out[p] = frame.Planes[p].Clone()
	}
	g.addNoise(out, frame.Planes[0].Width, frame.Planes[0].Height, seq.MatrixCoefficients)

	return &Frame{
		Planes: out, NumPlanes: frame.NumPlanes,
		SubX: subX, SubY: subY, BitDepth: bd, Trace: frame.Trace,
	}
}

type grainState struct {
	fg                 *header.FilmGrainParams
	bd, subX, subY     int
	grainMin, grainMax int
	monoChrome         bool
	numPlanes          int
	randomRegister     int

	lumaGrain [73][82]int
	cbGrain   [38][44]int
	crGrain   [38][44]int
	scaling   [3][256]int
}

func (g *grainState) getRandomNumber(bits int) int {
	r := g.randomRegister
	bit := ((r >> 0) ^ (r >> 1) ^ (r >> 3) ^ (r >> 12)) & 1
	r = (r >> 1) | (bit << 15)
	result := (r >> uint(16-bits)) & ((1 << uint(bits)) - 1)
	g.randomRegister = r
	return result
}

func (g *grainState) generateGrain() {
	fg := g.fg
	shift := 12 - g.bd + fg.GrainScaleShift
	g.randomRegister = fg.GrainSeed
	for y := 0; y < 73; y++ {
		for x := 0; x < 82; x++ {
			v := 0
			if fg.NumYPoints > 0 {
				v = gaussianSequence[g.getRandomNumber(11)]
			}
			g.lumaGrain[y][x] = round2(v, shift)
		}
	}
	arShift := fg.ArCoeffShiftMinus6 + 6
	lag := fg.ArCoeffLag
	for y := 3; y < 73; y++ {
		for x := 3; x < 82-3; x++ {
			s, pos := 0, 0
			done := false
			for dr := -lag; dr <= 0 && !done; dr++ {
				for dc := -lag; dc <= lag; dc++ {
					if dr == 0 && dc == 0 {
						done = true
						break
					}
					c := fg.ArCoeffsYPlus128[pos] - 128
					s += g.lumaGrain[y+dr][x+dc] * c
					pos++
				}
			}
			g.lumaGrain[y][x] = clip3i(g.grainMin, g.grainMax, g.lumaGrain[y][x]+round2(s, arShift))
		}
	}
	if g.monoChrome {
		return
	}
	chromaW := 82
	if g.subX == 1 {
		chromaW = 44
	}
	chromaH := 73
	if g.subY == 1 {
		chromaH = 38
	}
	g.randomRegister = fg.GrainSeed ^ 0xb524
	for y := 0; y < chromaH; y++ {
		for x := 0; x < chromaW; x++ {
			v := 0
			if fg.NumCbPoints > 0 || fg.ChromaScalingFromLuma {
				v = gaussianSequence[g.getRandomNumber(11)]
			}
			g.cbGrain[y][x] = round2(v, shift)
		}
	}
	g.randomRegister = fg.GrainSeed ^ 0x49d8
	for y := 0; y < chromaH; y++ {
		for x := 0; x < chromaW; x++ {
			v := 0
			if fg.NumCrPoints > 0 || fg.ChromaScalingFromLuma {
				v = gaussianSequence[g.getRandomNumber(11)]
			}
			g.crGrain[y][x] = round2(v, shift)
		}
	}
	for y := 3; y < chromaH; y++ {
		for x := 3; x < chromaW-3; x++ {
			s0, s1, pos := 0, 0, 0
			done := false
			for dr := -lag; dr <= 0 && !done; dr++ {
				for dc := -lag; dc <= lag; dc++ {
					c0 := fg.ArCoeffsCbPlus128[pos] - 128
					c1 := fg.ArCoeffsCrPlus128[pos] - 128
					if dr == 0 && dc == 0 {
						if fg.NumYPoints > 0 {
							luma := 0
							lumaX := ((x - 3) << uint(g.subX)) + 3
							lumaY := ((y - 3) << uint(g.subY)) + 3
							for i := 0; i <= g.subY; i++ {
								for j := 0; j <= g.subX; j++ {
									luma += g.lumaGrain[lumaY+i][lumaX+j]
								}
							}
							luma = round2(luma, g.subX+g.subY)
							s0 += luma * c0
							s1 += luma * c1
						}
						done = true
						break
					}
					s0 += g.cbGrain[y+dr][x+dc] * c0
					s1 += g.crGrain[y+dr][x+dc] * c1
					pos++
				}
			}
			g.cbGrain[y][x] = clip3i(g.grainMin, g.grainMax, g.cbGrain[y][x]+round2(s0, arShift))
			g.crGrain[y][x] = clip3i(g.grainMin, g.grainMax, g.crGrain[y][x]+round2(s1, arShift))
		}
	}
}

func (g *grainState) getX(plane, i int) int {
	if plane == 0 || g.fg.ChromaScalingFromLuma {
		return g.fg.PointYValue[i]
	} else if plane == 1 {
		return g.fg.PointCbValue[i]
	}
	return g.fg.PointCrValue[i]
}

func (g *grainState) getY(plane, i int) int {
	if plane == 0 || g.fg.ChromaScalingFromLuma {
		return g.fg.PointYScaling[i]
	} else if plane == 1 {
		return g.fg.PointCbScaling[i]
	}
	return g.fg.PointCrScaling[i]
}

func (g *grainState) initScalingLut() {
	for plane := 0; plane < g.numPlanes; plane++ {
		var numPoints int
		if plane == 0 || g.fg.ChromaScalingFromLuma {
			numPoints = g.fg.NumYPoints
		} else if plane == 1 {
			numPoints = g.fg.NumCbPoints
		} else {
			numPoints = g.fg.NumCrPoints
		}
		if numPoints == 0 {
			for x := 0; x < 256; x++ {
				g.scaling[plane][x] = 0
			}
			continue
		}
		for x := 0; x < g.getX(plane, 0); x++ {
			g.scaling[plane][x] = g.getY(plane, 0)
		}
		for i := 0; i < numPoints-1; i++ {
			deltaY := g.getY(plane, i+1) - g.getY(plane, i)
			deltaX := g.getX(plane, i+1) - g.getX(plane, i)
			delta := deltaY * ((65536 + (deltaX >> 1)) / deltaX)
			for x := 0; x < deltaX; x++ {
				v := g.getY(plane, i) + ((x*delta + 32768) >> 16)
				g.scaling[plane][g.getX(plane, i)+x] = v
			}
		}
		for x := g.getX(plane, numPoints-1); x < 256; x++ {
			g.scaling[plane][x] = g.getY(plane, numPoints-1)
		}
	}
}

func (g *grainState) scaleLut(plane, index int) int {
	shift := g.bd - 8
	x := index >> uint(shift)
	rem := index - (x << uint(shift))
	if g.bd == 8 || x == 255 {
		return g.scaling[plane][x]
	}
	start := g.scaling[plane][x]
	end := g.scaling[plane][x+1]
	return start + round2((end-start)*rem, shift)
}

// addNoise constructs the noise image from grain blocks (with optional overlap
// blending) and adds it to the frame (AV1 spec §7.18.3 add noise process).
func (g *grainState) addNoise(out []*predict.Plane, w, h, matrixCoeffs int) {
	fg := g.fg
	subX, subY := g.subX, g.subY
	overlap := fg.OverlapFlag
	gmin, gmax := g.grainMin, g.grainMax

	psub := func(plane int) (int, int) {
		if plane > 0 {
			return subX, subY
		}
		return 0, 0
	}
	grainAt := func(plane, y, x int) int {
		switch plane {
		case 0:
			return g.lumaGrain[y][x]
		case 1:
			return g.cbGrain[y][x]
		default:
			return g.crGrain[y][x]
		}
	}

	numStripes := 0
	for y := 0; y < (h+1)/2; y += 16 {
		numStripes++
	}
	// noiseStripe[lumaNum][plane][i][x]
	noiseStripe := make([][][][]int, numStripes)
	for s := range noiseStripe {
		noiseStripe[s] = make([][][]int, g.numPlanes)
		for plane := 0; plane < g.numPlanes; plane++ {
			pSubX, pSubY := psub(plane)
			ph := 34 >> uint(pSubY)
			pw := ((w + pSubX) >> uint(pSubX)) + 64
			noiseStripe[s][plane] = make([][]int, ph)
			for i := 0; i < ph; i++ {
				noiseStripe[s][plane][i] = make([]int, pw)
			}
		}
	}

	lumaNum := 0
	for y := 0; y < (h+1)/2; y += 16 {
		g.randomRegister = fg.GrainSeed
		g.randomRegister ^= ((lumaNum*37 + 178) & 255) << 8
		g.randomRegister ^= (lumaNum*173 + 105) & 255
		for x := 0; x < (w+1)/2; x += 16 {
			rand := g.getRandomNumber(8)
			offsetX := rand >> 4
			offsetY := rand & 15
			for plane := 0; plane < g.numPlanes; plane++ {
				planeSubX, planeSubY := psub(plane)
				planeOffsetX := 9 + offsetX*2
				if planeSubX == 1 {
					planeOffsetX = 6 + offsetX
				}
				planeOffsetY := 9 + offsetY*2
				if planeSubY == 1 {
					planeOffsetY = 6 + offsetY
				}
				row := noiseStripe[lumaNum][plane]
				for i := 0; i < 34>>uint(planeSubY); i++ {
					for j := 0; j < 34>>uint(planeSubX); j++ {
						gg := grainAt(plane, planeOffsetY+i, planeOffsetX+j)
						if planeSubX == 0 {
							idx := x*2 + j
							if j < 2 && overlap && x > 0 {
								old := row[i][idx]
								if j == 0 {
									gg = old*27 + gg*17
								} else {
									gg = old*17 + gg*27
								}
								gg = clip3i(gmin, gmax, round2(gg, 5))
							}
							row[i][idx] = gg
						} else {
							idx := x + j
							if j == 0 && overlap && x > 0 {
								old := row[i][idx]
								gg = old*23 + gg*22
								gg = clip3i(gmin, gmax, round2(gg, 5))
							}
							row[i][idx] = gg
						}
					}
				}
			}
		}
		lumaNum++
	}

	// noiseImage[plane][y][x] — blend stripes vertically.
	noiseImage := make([][][]int, g.numPlanes)
	for plane := 0; plane < g.numPlanes; plane++ {
		planeSubX, planeSubY := psub(plane)
		ph := (h + planeSubY) >> uint(planeSubY)
		pw := (w + planeSubX) >> uint(planeSubX)
		noiseImage[plane] = make([][]int, ph)
		for y := 0; y < ph; y++ {
			noiseImage[plane][y] = make([]int, pw)
			ln := y >> uint(5-planeSubY)
			i := y - (ln << uint(5-planeSubY))
			for x := 0; x < pw; x++ {
				gg := noiseStripe[ln][plane][i][x]
				if planeSubY == 0 {
					if i < 2 && ln > 0 && overlap {
						old := noiseStripe[ln-1][plane][i+32][x]
						if i == 0 {
							gg = old*27 + gg*17
						} else {
							gg = old*17 + gg*27
						}
						gg = clip3i(gmin, gmax, round2(gg, 5))
					}
				} else {
					if i < 1 && ln > 0 && overlap {
						old := noiseStripe[ln-1][plane][i+16][x]
						gg = old*23 + gg*22
						gg = clip3i(gmin, gmax, round2(gg, 5))
					}
				}
				noiseImage[plane][y][x] = gg
			}
		}
	}

	// Final blend with the image.
	minValue, maxLuma, maxChroma := 0, (256<<uint(g.bd-8))-1, (256<<uint(g.bd-8))-1
	if fg.ClipToRestrictedRange {
		minValue = 16 << uint(g.bd-8)
		maxLuma = 235 << uint(g.bd-8)
		if matrixCoeffs == header.MCIdentity {
			maxChroma = maxLuma
		} else {
			maxChroma = 240 << uint(g.bd-8)
		}
	}
	clip1 := func(v int) int { return clip3i(0, (1<<uint(g.bd))-1, v) }
	scalingShift := fg.GrainScalingMinus8 + 8

	outY := out[0]
	chromaActive := g.numPlanes > 1 && (fg.NumCbPoints > 0 || fg.NumCrPoints > 0 || fg.ChromaScalingFromLuma)
	if chromaActive {
		cw := (w + subX) >> uint(subX)
		ch := (h + subY) >> uint(subY)
		outU, outV := out[1], out[2]
		for y := 0; y < ch; y++ {
			for x := 0; x < cw; x++ {
				lumaX := x << uint(subX)
				lumaY := y << uint(subY)
				lumaNextX := minInt(lumaX+1, w-1)
				averageLuma := int(outY.At(lumaX, lumaY))
				if subX == 1 {
					averageLuma = round2(int(outY.At(lumaX, lumaY))+int(outY.At(lumaNextX, lumaY)), 1)
				}
				if fg.NumCbPoints > 0 || fg.ChromaScalingFromLuma {
					orig := int(outU.At(x, y))
					merged := averageLuma
					if !fg.ChromaScalingFromLuma {
						combined := averageLuma*(fg.CbLumaMult-128) + orig*(fg.CbMult-128)
						merged = clip1((combined >> 6) + ((fg.CbOffset - 256) << uint(g.bd-8)))
					}
					noise := round2(g.scaleLut(1, merged)*noiseImage[1][y][x], scalingShift)
					outU.Set(x, y, uint16(clip3i(minValue, maxChroma, orig+noise)))
				}
				if fg.NumCrPoints > 0 || fg.ChromaScalingFromLuma {
					orig := int(outV.At(x, y))
					merged := averageLuma
					if !fg.ChromaScalingFromLuma {
						combined := averageLuma*(fg.CrLumaMult-128) + orig*(fg.CrMult-128)
						merged = clip1((combined >> 6) + ((fg.CrOffset - 256) << uint(g.bd-8)))
					}
					noise := round2(g.scaleLut(2, merged)*noiseImage[2][y][x], scalingShift)
					outV.Set(x, y, uint16(clip3i(minValue, maxChroma, orig+noise)))
				}
			}
		}
	}
	if fg.NumYPoints > 0 {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				orig := int(outY.At(x, y))
				noise := round2(g.scaleLut(0, orig)*noiseImage[0][y][x], scalingShift)
				outY.Set(x, y, uint16(clip3i(minValue, maxLuma, orig+noise)))
			}
		}
	}
}
