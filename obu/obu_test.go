package obu

import (
	"bytes"
	"math/rand"
	"testing"
)

// TestLEB128Roundtrip checks LEB128 encode→decode over a wide value range.
func TestLEB128Roundtrip(t *testing.T) {
	vals := []uint64{0, 1, 127, 128, 129, 255, 256, 16383, 16384, 1 << 20, 1<<35 - 1}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		vals = append(vals, rng.Uint64()&(1<<56-1))
	}
	for _, v := range vals {
		buf := AppendLEB128(nil, v)
		got, n, ok := ReadLEB128(buf, 0)
		if !ok || got != v || n != len(buf) {
			t.Fatalf("v=%d: got=%d n=%d ok=%v buf=%x", v, got, n, ok, buf)
		}
	}
}

// TestSplitOBUs builds a temporal unit (delimiter + seq header + frame, the last
// two with extension ids) and checks Split recovers every field and payload.
func TestSplitOBUs(t *testing.T) {
	seq := []byte{0x0a, 0x0b, 0x0c, 0x0d}
	frame := bytes.Repeat([]byte{0xee}, 300) // forces 2-byte LEB128 size
	var stream []byte
	stream = AppendOBU(stream, TypeTemporalDelimiter, 0, 0, nil)
	stream = AppendOBU(stream, TypeSequenceHeader, 0, 0, seq)
	stream = AppendOBU(stream, TypeFrame, 3, 1, frame)

	obus, err := Split(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(obus) != 3 {
		t.Fatalf("got %d OBUs, want 3", len(obus))
	}
	if obus[0].Type != TypeTemporalDelimiter || len(obus[0].Payload) != 0 {
		t.Fatalf("OBU0 = %+v", obus[0])
	}
	if obus[1].Type != TypeSequenceHeader || !bytes.Equal(obus[1].Payload, seq) {
		t.Fatalf("OBU1 payload mismatch: %x", obus[1].Payload)
	}
	if obus[2].Type != TypeFrame || obus[2].TemporalID != 3 || obus[2].SpatialID != 1 {
		t.Fatalf("OBU2 ids: temporal=%d spatial=%d", obus[2].TemporalID, obus[2].SpatialID)
	}
	if !bytes.Equal(obus[2].Payload, frame) {
		t.Fatal("OBU2 payload mismatch")
	}
}

// TestTruncated ensures partial input is reported, not panicked on.
func TestTruncated(t *testing.T) {
	full := AppendOBU(nil, TypeSequenceHeader, 0, 0, bytes.Repeat([]byte{1}, 50))
	for cut := 1; cut < len(full); cut++ {
		if _, err := Split(full[:cut]); err == nil {
			t.Fatalf("cut %d: expected truncation error", cut)
		}
	}
}

// TestNoSizeField checks an OBU without a size field consumes the remainder.
func TestNoSizeField(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 5}
	stream := append([]byte{byte(TypeFrame<<3) | 0x00}, payload...) // has_size_field=0
	obus, err := Split(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(obus) != 1 || obus[0].Type != TypeFrame || !bytes.Equal(obus[0].Payload, payload) {
		t.Fatalf("got %+v", obus)
	}
}
