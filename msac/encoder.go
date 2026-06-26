package msac

import "math/bits"

// Encoder is the arithmetic dual of Decoder: an AV1 range encoder producing a
// bitstream that Decoder reads back to the identical symbol sequence. It is a port
// of the BSD-2 od_ec range encoder (libaom / rav1e WriterEncoder) — the reference
// AV1 entropy encoder — operating on the same spec-form CDFs as the decoder, so the
// adaptation stays in lockstep. The AV1 spec defines only the decoder; this encoder
// exists to validate the decoder by roundtrip and to synthesize test bitstreams.
type Encoder struct {
	low      uint32   // ec_window: pending low bits
	rng      uint16   // current range, in [2^15, 2^16)
	cnt      int      // bits buffered in low, biased (starts at -9)
	precarry []uint16 // output words; bit 8 of each carries into the previous word

	// AllowUpdate enables CDF adaptation after each adaptive symbol; it must match
	// the decoder's setting for adaptive streams.
	AllowUpdate bool
}

// NewEncoder returns a fresh range encoder (od_ec_enc_reset).
func NewEncoder() *Encoder {
	return &Encoder{rng: 0x8000, cnt: -9}
}

// EncodeSymbol encodes symbol against cdf (the inverse of Decoder.DecodeSymbol) and,
// if AllowUpdate is set, adapts cdf with the same update as the decoder.
func (e *Encoder) EncodeSymbol(cdf []uint16, symbol int) {
	n := len(cdf) - 1 // number of symbols
	// Map spec-form cumulative CDF to the od_ec "inverse" boundaries fl > fh:
	// fl is the upper interval edge (cdf[symbol-1]; full range when symbol==0),
	// fh is the lower edge (cdf[symbol]).
	fl := uint16(1 << 15)
	if symbol > 0 {
		fl = 1<<15 - cdf[symbol-1]
	}
	fh := 1<<15 - cdf[symbol]
	e.store(fl, fh, n-symbol)
	if e.AllowUpdate {
		updateCDF(cdf, symbol, n)
	}
}

// store narrows the range to the chosen sub-interval and renormalizes, emitting
// output words as bits settle (od_ec_encode_q15 + od_ec_enc_normalize). nms is
// (N - symbol); fl/fh are the inverse-CDF edges with fl > fh.
func (e *Encoder) store(fl, fh uint16, nms int) {
	r := uint32(e.rng)
	u := ((r>>8)*(uint32(fl)>>ecProbShift))>>(7-ecProbShift) + ecMinProb*uint32(nms)
	if fl >= 1<<15 {
		u = r
	}
	v := ((r>>8)*(uint32(fh)>>ecProbShift))>>(7-ecProbShift) + ecMinProb*uint32(nms-1)
	newRng := uint16(u - v) // narrowed range, in (0, 2^16)

	low := (r - u) + e.low
	c := e.cnt
	d := bits.LeadingZeros16(newRng) // renormalization shift = 15 - floorLog2(newRng)
	s := c + d
	if s >= 0 {
		c += 16
		m := uint32(1)<<uint(c) - 1
		if s >= 8 {
			e.precarry = append(e.precarry, uint16(low>>uint(c)))
			low &= m
			c -= 8
			m >>= 8
		}
		e.precarry = append(e.precarry, uint16(low>>uint(c)))
		s = c + d - 24
		low &= m
	}
	e.low = low << uint(d)
	e.rng = newRng << uint(d)
	e.cnt = s
}

// WriteBool encodes one equiprobable bit (dual of Decoder.ReadBool), no adaptation.
func (e *Encoder) WriteBool(b int) {
	cdf := equiCDF
	save := e.AllowUpdate
	e.AllowUpdate = false
	e.EncodeSymbol(cdf[:], b)
	e.AllowUpdate = save
}

// WriteLiteral encodes an n-bit unsigned literal, MSB-first (dual of ReadLiteral).
func (e *Encoder) WriteLiteral(x uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		e.WriteBool(int((x >> uint(i)) & 1))
	}
}

// Finish flushes the coder and resolves carries, returning the encoded bytes
// (od_ec_enc_done). The encoder must not be used afterwards.
func (e *Encoder) Finish() []byte {
	l := e.low
	c := e.cnt
	s := 10 + c
	m := uint32(0x3FFF)
	ev := ((l + m) &^ m) | (m + 1)
	if s > 0 {
		n := uint32(1)<<uint(c+16) - 1
		for {
			e.precarry = append(e.precarry, uint16(ev>>uint(c+16)))
			ev &= n
			s -= 8
			c -= 8
			n >>= 8
			if s <= 0 {
				break
			}
		}
	}
	// Resolve carries: each precarry word may set bit 8, carrying into the word
	// before it. Walk backwards accumulating the carry.
	var carry uint32
	out := make([]byte, len(e.precarry))
	for i := len(e.precarry) - 1; i >= 0; i-- {
		carry += uint32(e.precarry[i])
		out[i] = byte(carry)
		carry >>= 8
	}
	return out
}
