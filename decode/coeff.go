package decode

// Coefficient-decode constants (AV1 spec §3).
const (
	numBaseLevels  = 2
	coeffBaseRange = 12
	brCdfSize      = 4
)

// decodeCoeffs decodes the coefficients of a transform block whose all_zero flag
// was 0, returning the dequantized coefficients (a th×tw array) and the transform
// type (AV1 spec §5.11.39 coeffs + §7.12.3 reconstruct). Implemented for DCT_DCT
// transforms with Tx_Size_Sqr_Up == TX_64X64 (i.e. the 64-point DCT path); other
// transform sizes/types return ErrUnsupported.
func (fd *frameDecoder) decodeCoeffs(plane, txSz, x4, y4 int) ([][]int64, int, int, int, error) {
	txSzCtx := (TxSizeSqr[txSz] + TxSizeSqrUp[txSz] + 1) >> 1
	ptype := 0
	if plane > 0 {
		ptype = 1
	}

	// Transform type: luma reads tx_type; chroma derives it from the stored grid.
	if plane == 0 {
		fd.readTransformType(txSz, x4, y4)
	}
	txType := fd.computeTxType(plane, txSz, x4, y4)
	txClass := getTxClass(txType)
	scan := getScan(txSz, txType)
	if scan == nil {
		return nil, 0, 0, 0, ErrUnsupported{"no scan for this tx size/type"}
	}

	// End-of-block position (eob_pt) by transform-area class.
	eobMultisize := mini(TxWidthLog2[txSz], 5) + mini(TxHeightLog2[txSz], 5) - 4
	ptCtx := 0
	if txClass != txClass2D {
		ptCtx = 1
	}
	var eobPt int
	switch eobMultisize {
	case 0:
		eobPt = fd.d.DecodeSymbol(fd.c.eobPt16[ptype][ptCtx]) + 1
	case 1:
		eobPt = fd.d.DecodeSymbol(fd.c.eobPt32[ptype][ptCtx]) + 1
	case 2:
		eobPt = fd.d.DecodeSymbol(fd.c.eobPt64[ptype][ptCtx]) + 1
	case 3:
		eobPt = fd.d.DecodeSymbol(fd.c.eobPt128[ptype][ptCtx]) + 1
	case 4:
		eobPt = fd.d.DecodeSymbol(fd.c.eobPt256[ptype][ptCtx]) + 1
	case 5:
		eobPt = fd.d.DecodeSymbol(fd.c.eobPt512[ptype]) + 1
	default:
		eobPt = fd.d.DecodeSymbol(fd.c.eobPt1024[ptype]) + 1
	}
	eob := eobPt
	if eobPt >= 2 {
		eob = (1 << uint(eobPt-2)) + 1
	}
	if eobShift := maxi(-1, eobPt-3); eobShift >= 0 {
		if fd.d.DecodeSymbol(fd.c.eobExtra[txSzCtx][ptype][eobPt-3]) == 1 {
			eob += 1 << uint(eobShift)
		}
		hi := maxi(0, eobPt-2)
		for i := 1; i < hi; i++ {
			if fd.d.ReadBool() == 1 {
				eob += 1 << uint(hi-1-i)
			}
		}
	}
	fd.tr("coeffs(p%d) eob=%d", plane, eob)

	quant := make([]int, 1024)

	// Coefficient levels, decoded from the highest scan position downwards.
	for c := eob - 1; c >= 0; c-- {
		pos := scan[c]
		var level int
		if c == eob-1 {
			ctx := getCoeffBaseCtx(quant, txSz, txClass, pos, c, true) - sigCoefContexts + sigCoefContextsEob
			level = fd.d.DecodeSymbol(fd.c.coeffBaseEob[txSzCtx][ptype][ctx]) + 1
		} else {
			ctx := getCoeffBaseCtx(quant, txSz, txClass, pos, c, false)
			level = fd.d.DecodeSymbol(fd.c.coeffBase[txSzCtx][ptype][ctx])
		}
		if level > numBaseLevels {
			for idx := 0; idx < coeffBaseRange/(brCdfSize-1); idx++ {
				brCtx := getCoeffBrCtx(quant, txSz, txClass, pos)
				br := fd.d.DecodeSymbol(fd.c.coeffBr[mini(txSzCtx, TX32x32)][ptype][brCtx])
				level += br
				if br < brCdfSize-1 {
					break
				}
			}
		}
		quant[pos] = level
	}

	// Signs and the Exp-Golomb tail, decoded from the lowest scan position upwards.
	dcSignCtx := fd.dcSignContext(plane, x4, y4, TxWidth[txSz]>>2, TxHeight[txSz]>>2)
	culLevel := 0
	dcCategory := 0
	for c := 0; c < eob; c++ {
		pos := scan[c]
		sign := 0
		if quant[pos] != 0 {
			if c == 0 {
				sign = fd.d.DecodeSymbol(fd.c.dcSign[ptype][dcSignCtx])
			} else {
				sign = fd.d.ReadBool()
			}
		}
		if quant[pos] > numBaseLevels+coeffBaseRange {
			length := 0
			for {
				length++
				if fd.d.ReadBool() == 1 {
					break
				}
			}
			x := 1
			for i := length - 2; i >= 0; i-- {
				x = (x << 1) | fd.d.ReadBool()
			}
			quant[pos] = x + coeffBaseRange + numBaseLevels
		}
		if pos == 0 && quant[pos] > 0 {
			if sign == 1 {
				dcCategory = 1
			} else {
				dcCategory = 2
			}
		}
		quant[pos] &= 0xFFFFF
		culLevel += quant[pos]
		if sign == 1 {
			quant[pos] = -quant[pos]
		}
	}
	if culLevel > 63 {
		culLevel = 63
	}

	// Dequantize the top-left tw×th coefficient region (AV1 spec §7.12.3).
	w := 1 << uint(TxWidthLog2[txSz])
	h := 1 << uint(TxHeightLog2[txSz])
	tw := mini(32, w)
	th := mini(32, h)
	bdIdx := (fd.bitDepth - 8) >> 1
	dcQ := dcQlookup[bdIdx][clip3i(0, 255, fd.getQIndex()+fd.dcDeltaQ(plane))]
	acQ := acQlookup[bdIdx][clip3i(0, 255, fd.getQIndex()+fd.acDeltaQ(plane))]
	denom := int64(dqDenom(txSz))
	lo := -(int64(1) << uint(7+fd.bitDepth))
	hi := (int64(1) << uint(7+fd.bitDepth)) - 1
	// Quantizer matrix scaling (AV1 spec §7.12.3): when using_qmatrix, the per-
	// coefficient quantizer is q2 = Round2(q * Quantizer_Matrix[...], 5).
	qmLevel := 15
	if fd.fh.UsingQMatrix && !fd.lossless() {
		switch plane {
		case 0:
			qmLevel = fd.fh.QmY
		case 1:
			qmLevel = fd.fh.QmU
		default:
			qmLevel = fd.fh.QmV
		}
	}
	useQM := qmLevel < 15 && txType < Idtx
	qmBase := 0
	if useQM {
		chroma := 0
		if plane > 0 {
			chroma = 1
		}
		qmBase = (qmLevel*2+chroma)*qmTotalSize + qmOffset[txSz]
	}
	dequant := make([][]int64, th)
	for i := 0; i < th; i++ {
		dequant[i] = make([]int64, tw)
		for j := 0; j < tw; j++ {
			q := acQ
			if i == 0 && j == 0 {
				q = dcQ
			}
			if useQM {
				q = (q*int(quantizerMatrix[qmBase+i*tw+j]) + 16) >> 5
			}
			dq := int64(quant[i*tw+j]) * int64(q)
			sgn := int64(1)
			if dq < 0 {
				sgn = -1
			}
			dq2 := sgn * ((abs64(dq) & 0xFFFFFF) / denom)
			dequant[i][j] = clip64(lo, hi, dq2)
		}
	}
	return dequant, txType, culLevel, dcCategory, nil
}

// dcSignContext computes the dc_sign context from the neighbor DC contexts
// (AV1 spec §8.3).
func (fd *frameDecoder) dcSignContext(plane, x4, y4, w4, h4 int) int {
	subX, subY := 0, 0
	if plane > 0 {
		subX, subY = fd.subX, fd.subY
	}
	maxX4 := fd.miCols >> subX
	maxY4 := fd.miRows >> subY
	dcSign := 0
	for k := 0; k < w4; k++ {
		if x4+k < maxX4 {
			switch fd.aboveDcContext[plane][x4+k] {
			case 1:
				dcSign--
			case 2:
				dcSign++
			}
		}
	}
	for k := 0; k < h4; k++ {
		if y4+k < maxY4 {
			switch fd.leftDcContext[plane][y4+k] {
			case 1:
				dcSign--
			case 2:
				dcSign++
			}
		}
	}
	if dcSign < 0 {
		return 1
	}
	if dcSign > 0 {
		return 2
	}
	return 0
}

// dcDeltaQ / acDeltaQ return the DC/AC quantizer deltas for a plane (AV1 spec,
// get_dc_quant / get_ac_quant). Luma AC has no delta.
func (fd *frameDecoder) dcDeltaQ(plane int) int {
	switch plane {
	case 1:
		return fd.fh.DeltaQUDc
	case 2:
		return fd.fh.DeltaQVDc
	default:
		return fd.fh.DeltaQYDc
	}
}

func (fd *frameDecoder) acDeltaQ(plane int) int {
	switch plane {
	case 1:
		return fd.fh.DeltaQUAc
	case 2:
		return fd.fh.DeltaQVAc
	default:
		return 0
	}
}

// dqDenom returns the dequantization denominator for a transform size (AV1 spec §7.12.3).
func dqDenom(txSz int) int {
	switch txSz {
	case TX32x32, TX16x32, TX32x16, TX16x64, TX64x16:
		return 2
	case TX64x64, TX32x64, TX64x32:
		return 4
	default:
		return 1
	}
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clip3i(lo, hi, v int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clip64(lo, hi, v int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
