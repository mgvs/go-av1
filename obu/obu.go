// Package obu parses the AV1 Open Bitstream Unit framing — the outermost layer of
// every AV1 stream (AV1 spec §5.2–5.3). It splits a temporal unit / low-overhead
// bitstream into individual OBUs (type, spatial/temporal id, payload) and decodes
// the unsigned LEB128 sizes. Original implementation from the public AV1 spec.
package obu

import "errors"

// OBU types (AV1 spec §6.2.2).
const (
	TypeSequenceHeader       = 1
	TypeTemporalDelimiter    = 2
	TypeFrameHeader          = 3
	TypeTileGroup            = 4
	TypeMetadata             = 5
	TypeFrame                = 6
	TypeRedundantFrameHeader = 7
	TypeTileList             = 8
	TypePadding              = 15
)

// ErrTruncated is returned when the stream ends inside an OBU header or payload.
var ErrTruncated = errors.New("obu: truncated bitstream")

// OBU is one Open Bitstream Unit.
type OBU struct {
	Type         int
	TemporalID   int
	SpatialID    int
	HasSizeField bool
	Payload      []byte // OBU payload only (header bytes excluded)
}

// ReadLEB128 decodes an unsigned LEB128 value at data[off:] (AV1 spec §4.10.5).
// Returns the value, the number of bytes consumed, and ok. At most 8 bytes.
func ReadLEB128(data []byte, off int) (value uint64, n int, ok bool) {
	for i := 0; i < 8; i++ {
		if off+i >= len(data) {
			return 0, 0, false
		}
		b := data[off+i]
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return value, i + 1, true
		}
	}
	return 0, 0, false // no terminating byte within 8 bytes
}

// AppendLEB128 encodes v as unsigned LEB128 onto dst (helper for tests/muxing).
func AppendLEB128(dst []byte, v uint64) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		dst = append(dst, b)
		if v == 0 {
			return dst
		}
	}
}

// Split iterates the OBUs in a low-overhead bitstream (the format used inside
// MP4/WebM samples), where each OBU carries obu_has_size_field. An OBU without a
// size field is only valid as the final unit and runs to the end of data.
func Split(data []byte) ([]OBU, error) {
	var out []OBU
	pos := 0
	for pos < len(data) {
		o, next, err := parseOne(data, pos)
		if err != nil {
			return out, err
		}
		out = append(out, o)
		pos = next
	}
	return out, nil
}

// parseOne reads a single OBU starting at data[pos] and returns it plus the index
// just past its payload.
func parseOne(data []byte, pos int) (OBU, int, error) {
	if pos >= len(data) {
		return OBU{}, pos, ErrTruncated
	}
	b0 := data[pos]
	if b0&0x80 != 0 { // obu_forbidden_bit must be 0
		return OBU{}, pos, errors.New("obu: forbidden bit set")
	}
	o := OBU{
		Type:         int((b0 >> 3) & 0x0f),
		HasSizeField: (b0>>1)&1 == 1,
	}
	extFlag := (b0>>2)&1 == 1
	hdr := pos + 1
	if extFlag {
		if hdr >= len(data) {
			return OBU{}, pos, ErrTruncated
		}
		e := data[hdr]
		o.TemporalID = int(e >> 5)
		o.SpatialID = int((e >> 3) & 0x03)
		hdr++
	}
	if o.HasSizeField {
		size, n, ok := ReadLEB128(data, hdr)
		if !ok {
			return OBU{}, pos, ErrTruncated
		}
		start := hdr + n
		end := start + int(size)
		if end > len(data) {
			return OBU{}, pos, ErrTruncated
		}
		o.Payload = data[start:end]
		return o, end, nil
	}
	// No size field: payload runs to end of the buffer.
	o.Payload = data[hdr:]
	return o, len(data), nil
}

// AppendOBU writes an OBU (with size field) to dst — helper for tests/muxing.
func AppendOBU(dst []byte, typ, temporalID, spatialID int, payload []byte) []byte {
	ext := temporalID != 0 || spatialID != 0
	b0 := byte(typ&0x0f) << 3
	b0 |= 1 << 1 // obu_has_size_field
	if ext {
		b0 |= 1 << 2 // obu_extension_flag
	}
	dst = append(dst, b0)
	if ext {
		dst = append(dst, byte(temporalID&0x07)<<5|byte(spatialID&0x03)<<3)
	}
	dst = AppendLEB128(dst, uint64(len(payload)))
	return append(dst, payload...)
}
