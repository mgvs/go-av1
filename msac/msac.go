// Package msac implements the AV1 symbol decoder — the multi-symbol arithmetic
// (range) coder that every AV1 tile is entropy-coded with (AV1 spec §8.2,
// "Symbol decoding process"). It decodes adaptive CDF-coded symbols, equiprobable
// booleans and literals, and adapts the CDFs after each symbol.
//
// The decoder follows the spec representation directly (SymbolValue / SymbolRange /
// SymbolMaxBits over a 15-bit range, MSB-first bit input). A matching encoder lives
// in encoder.go (a port of the BSD-2 od_ec range encoder from libaom / rav1e); the
// two are exact arithmetic duals and are validated against each other by roundtrip
// tests. Original implementation from the public AV1 spec.
package msac

import "math/bits"

// Range-coder constants (AV1 spec §8.2). EC_PROB_SHIFT reduces CDF precision in the
// interval split; EC_MIN_PROB guarantees every symbol a non-zero sub-interval.
const (
	ecProbShift = 6
	ecMinProb   = 4
)

// CDF convention (shared by decoder and encoder).
//
// A CDF for an N-symbol alphabet is a []uint16 of length N+1:
//   - cdf[0..N-1] are cumulative frequencies in q15, strictly increasing, with
//     cdf[N-1] == 1<<15 (32768). Symbol s owns the interval [cdf[s-1], cdf[s])
//     (with cdf[-1] == 0).
//   - cdf[N] is the adaptation counter (0..32), used to slow adaptation over time.
//
// This is the AV1-spec form. updateCDF adapts cdf[0..N-2] toward the observed
// symbol and bumps the counter; cdf[N-1] stays pinned at 32768.

// Decoder is an AV1 symbol decoder reading from a byte buffer (one tile / partition
// worth of entropy-coded data).
type Decoder struct {
	data   []byte
	bitPos int // next bit to read, MSB-first within each byte

	// Spec decoder state (§8.2.2).
	symbolValue   uint32
	symbolRange   uint32
	symbolMaxBits int

	// AllowUpdate enables CDF adaptation after each adaptive symbol (the spec's
	// disable_cdf_update flag, inverted). Equiprobable bools/literals never adapt.
	AllowUpdate bool
}

// NewDecoder starts a symbol decoder over data (the init_symbol process, §8.2.2).
// allowUpdate corresponds to !disable_cdf_update.
func NewDecoder(data []byte, allowUpdate bool) *Decoder {
	d := &Decoder{data: data, AllowUpdate: allowUpdate}
	sz := len(data)
	numBits := sz * 8
	if numBits > 15 {
		numBits = 15
	}
	buf := d.readBits(numBits)
	paddedBuf := buf << (15 - numBits)
	d.symbolValue = (uint32(1)<<15 - 1) ^ paddedBuf
	d.symbolRange = 1 << 15
	d.symbolMaxBits = 8*sz - 15
	return d
}

// readBits returns the next n bits, MSB-first. Bits past the end of the buffer read
// as zero (the spec caps reads via SymbolMaxBits, so this only pads the tail).
func (d *Decoder) readBits(n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		var bit uint32
		if bytePos := d.bitPos >> 3; bytePos < len(d.data) {
			bit = uint32(d.data[bytePos]>>(7-uint(d.bitPos&7))) & 1
		}
		v = v<<1 | bit
		d.bitPos++
	}
	return v
}

// DecodeSymbol decodes one adaptive symbol against cdf (the decode_symbol process,
// §8.2) and, if AllowUpdate is set, adapts cdf. Returns the symbol in [0, N).
func (d *Decoder) DecodeSymbol(cdf []uint16) int {
	n := len(cdf) - 1 // number of symbols
	cur := d.symbolRange
	var prev uint32
	symbol := -1
	for {
		symbol++
		prev = cur
		f := uint32(1)<<15 - uint32(cdf[symbol])
		cur = ((d.symbolRange >> 8) * (f >> ecProbShift)) >> (7 - ecProbShift)
		cur += ecMinProb * uint32(n-symbol-1)
		if d.symbolValue >= cur {
			break
		}
	}
	d.symbolRange = prev - cur
	d.symbolValue -= cur
	d.renorm()
	if d.AllowUpdate {
		updateCDF(cdf, symbol, n)
	}
	return symbol
}

// renorm rescales the range back into [2^15, 2^16) and refills SymbolValue, reading
// fresh bits where available (the renormalization process, §8.2).
func (d *Decoder) renorm() {
	b := 15 - floorLog2(d.symbolRange)
	d.symbolRange <<= uint(b)
	numBits := b
	if maxb := d.symbolMaxBits; numBits > maxb {
		if maxb < 0 {
			maxb = 0
		}
		numBits = maxb
	}
	newData := d.readBits(numBits)
	paddedData := newData << uint(b-numBits)
	d.symbolValue = paddedData ^ (((d.symbolValue + 1) << uint(b)) - 1)
	d.symbolMaxBits -= b
}

// equiCDF is the fixed, non-adaptive CDF for an equiprobable boolean (spec read_bool).
var equiCDF = [3]uint16{1 << 14, 1 << 15, 0}

// ReadBool decodes one equiprobable bit (spec read_bool), without CDF adaptation.
func (d *Decoder) ReadBool() int {
	cdf := equiCDF // local copy; never updated
	save := d.AllowUpdate
	d.AllowUpdate = false
	s := d.DecodeSymbol(cdf[:])
	d.AllowUpdate = save
	return s
}

// ReadLiteral decodes an n-bit unsigned literal, MSB-first (spec read_literal).
func (d *Decoder) ReadLiteral(n int) uint32 {
	var x uint32
	for i := 0; i < n; i++ {
		x = x<<1 | uint32(d.ReadBool())
	}
	return x
}

// ReadNS decodes a non-symmetric unsigned value in [0, n) (spec ns(n) / read_ns).
func (d *Decoder) ReadNS(n int) int {
	w := 0
	for (1 << uint(w+1)) <= n {
		w++ // w = FloorLog2(n)
	}
	w++ // FloorLog2(n) + 1
	m := (1 << uint(w)) - n
	v := int(d.ReadLiteral(w - 1))
	if v < m {
		return v
	}
	return (v << 1) - m + d.ReadBool()
}

// updateCDF adapts cdf toward the observed symbol (the CDF update process, §8.2).
// N is the number of symbols; cdf[N] is the adaptation counter.
func updateCDF(cdf []uint16, symbol, n int) {
	rate := 3
	if cdf[n] > 15 {
		rate++
	}
	if cdf[n] > 31 {
		rate++
	}
	if l := floorLog2(uint32(n)); l < 2 {
		rate += l
	} else {
		rate += 2
	}
	var tmp uint16
	for i := 0; i < n-1; i++ {
		if i == symbol {
			tmp = 1 << 15
		}
		if tmp < cdf[i] {
			cdf[i] -= (cdf[i] - tmp) >> uint(rate)
		} else {
			cdf[i] += (tmp - cdf[i]) >> uint(rate)
		}
	}
	if cdf[n] < 32 {
		cdf[n]++
	}
}

// floorLog2 returns floor(log2(x)) for x >= 1.
func floorLog2(x uint32) int {
	return bits.Len32(x) - 1
}

// State returns the raw decoder state (for cross-decoder desync debugging).
func (d *Decoder) State() (rng, val uint32, maxBits, bitPos int) {
	return d.symbolRange, d.symbolValue, d.symbolMaxBits, d.bitPos
}
