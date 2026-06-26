// Package bits is a big-endian (high-to-low) bit reader for the non-arithmetic
// parts of an AV1 bitstream: OBU headers, the sequence header and the uncompressed
// frame header. It implements the AV1 descriptors f(n), uvlc(), le(n), leb128(),
// su(n) and ns(n) exactly as defined in the AV1 spec §4 (Conventions → Descriptors).
// The arithmetic-coded descriptors L(n)/S()/NS(n) live in package msac.
package bits

import "math/bits"

// Reader reads bits high-to-low from a byte slice (AV1 spec §8.1, f(n)).
type Reader struct {
	data []byte
	pos  int // next bit index, MSB-first within each byte
}

// NewReader returns a Reader over data positioned at the first bit.
func NewReader(data []byte) *Reader { return &Reader{data: data} }

// Pos returns the current bit position.
func (r *Reader) Pos() int { return r.pos }

// BitsLeft returns how many bits remain.
func (r *Reader) BitsLeft() int { return len(r.data)*8 - r.pos }

// ReadBit returns the next bit (0 past end of buffer).
func (r *Reader) ReadBit() uint32 {
	var b uint32
	if bytePos := r.pos >> 3; bytePos < len(r.data) {
		b = uint32(r.data[bytePos]>>(7-uint(r.pos&7))) & 1
	}
	r.pos++
	return b
}

// F reads an n-bit unsigned value, high bit first (descriptor f(n), 0 <= n <= 32).
func (r *Reader) F(n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		v = v<<1 | r.ReadBit()
	}
	return v
}

// Uvlc reads a variable-length unsigned number (descriptor uvlc()).
func (r *Reader) Uvlc() uint32 {
	leadingZeros := 0
	for r.ReadBit() == 0 {
		leadingZeros++
		if leadingZeros >= 32 {
			return 1<<32 - 1
		}
	}
	value := r.F(leadingZeros)
	return value + (1<<uint(leadingZeros) - 1)
}

// Le reads an unsigned little-endian n-byte number (descriptor le(n)). The reader
// must be byte-aligned.
func (r *Reader) Le(n int) uint64 {
	var t uint64
	for i := 0; i < n; i++ {
		t += uint64(r.F(8)) << uint(i*8)
	}
	return t
}

// Leb128 reads an unsigned LEB128 value and the number of bytes consumed
// (descriptor leb128()). The reader must be byte-aligned.
func (r *Reader) Leb128() (value uint64, n int) {
	for i := 0; i < 8; i++ {
		b := r.F(8)
		value |= uint64(b&0x7f) << uint(i*7)
		n++
		if b&0x80 == 0 {
			break
		}
	}
	return value, n
}

// Su reads a signed integer from n bits, two's-complement (descriptor su(n)).
func (r *Reader) Su(n int) int32 {
	value := int32(r.F(n))
	signMask := int32(1) << uint(n-1)
	if value&signMask != 0 {
		value -= 2 * signMask
	}
	return value
}

// Ns reads a non-symmetric unsigned integer in 0..n-1 (descriptor ns(n)).
func (r *Reader) Ns(n int) uint32 {
	w := floorLog2(uint32(n)) + 1
	m := uint32(1<<uint(w)) - uint32(n)
	v := r.F(w - 1)
	if v < m {
		return v
	}
	extraBit := r.ReadBit()
	return (v << 1) - m + extraBit
}

// ByteAlign advances to the next byte boundary (spec byte_alignment()).
func (r *Reader) ByteAlign() {
	for r.pos&7 != 0 {
		r.pos++
	}
}

func floorLog2(x uint32) int { return bits.Len32(x) - 1 }
