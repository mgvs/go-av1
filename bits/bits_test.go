package bits

import "testing"

func TestF(t *testing.T) {
	// 0xB5 = 1011 0101
	r := NewReader([]byte{0xB5, 0x2C})
	if v := r.F(3); v != 0b101 {
		t.Fatalf("F(3)=%b", v)
	}
	if v := r.F(5); v != 0b10101 {
		t.Fatalf("F(5)=%b", v)
	}
	if v := r.F(8); v != 0x2C {
		t.Fatalf("F(8)=%x", v)
	}
}

func TestUvlc(t *testing.T) {
	// uvlc(0) = "1"; uvlc(1) = "010"; uvlc(2)="011"; uvlc(3)="00100".
	// Pack bits MSB-first: 1 010 011 00100 -> 1010 0110 0100 ...
	r := NewReader([]byte{0b10100110, 0b01000000})
	for _, want := range []uint32{0, 1, 2, 3} {
		if got := r.Uvlc(); got != want {
			t.Fatalf("uvlc got %d want %d", got, want)
		}
	}
}

func TestLeb128(t *testing.T) {
	// 1038 = 0b100_0001110 -> LEB128 little-endian: 0x8E, 0x08.
	r := NewReader([]byte{0x8E, 0x08})
	v, n := r.Leb128()
	if v != 1038 || n != 2 {
		t.Fatalf("leb128 got v=%d n=%d", v, n)
	}
}

func TestSu(t *testing.T) {
	// 4-bit: 0b1111 -> -1; 0b0111 -> 7; 0b1000 -> -8.
	for _, c := range []struct {
		bits byte
		want int32
	}{{0b1111_0000, -1}, {0b0111_0000, 7}, {0b1000_0000, -8}} {
		r := NewReader([]byte{c.bits})
		if got := r.Su(4); got != c.want {
			t.Fatalf("su(4) of %04b got %d want %d", c.bits>>4, got, c.want)
		}
	}
}

func TestNs(t *testing.T) {
	// From spec example, n=5: encodings 00,01,10,110,111 -> values 0,1,2,3,4.
	enc := []struct {
		bits  byte
		nbits int
		want  uint32
	}{
		{0b00_000000, 2, 0},
		{0b01_000000, 2, 1},
		{0b10_000000, 2, 2},
		{0b110_00000, 3, 3},
		{0b111_00000, 3, 4},
	}
	for _, c := range enc {
		r := NewReader([]byte{c.bits})
		if got := r.Ns(5); got != c.want {
			t.Fatalf("ns(5) got %d want %d", got, c.want)
		}
	}
}
