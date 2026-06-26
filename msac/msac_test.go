package msac

import (
	"math/rand"
	"testing"
)

// uniformCDF builds a spec-form CDF (length n+1) for an n-symbol alphabet with
// near-uniform probabilities and a zeroed adaptation counter.
func uniformCDF(n int) []uint16 {
	cdf := make([]uint16, n+1)
	for i := 0; i < n-1; i++ {
		cdf[i] = uint16((i + 1) * (1 << 15) / n)
	}
	cdf[n-1] = 1 << 15
	cdf[n] = 0
	return cdf
}

func cloneCDF(c []uint16) []uint16 {
	out := make([]uint16, len(c))
	copy(out, c)
	return out
}

// TestLiteralRoundtrip checks bit-exact encode→decode of fixed-width literals.
func TestLiteralRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	type lit struct {
		v uint32
		n int
	}
	for trial := 0; trial < 200; trial++ {
		var lits []lit
		enc := NewEncoder()
		for i := 0; i < 50; i++ {
			n := 1 + rng.Intn(16)
			v := rng.Uint32() & (1<<uint(n) - 1)
			lits = append(lits, lit{v, n})
			enc.WriteLiteral(v, n)
		}
		data := enc.Finish()
		dec := NewDecoder(data, false)
		for i, l := range lits {
			if got := dec.ReadLiteral(l.n); got != l.v {
				t.Fatalf("trial %d lit %d: got %d want %d (n=%d)", trial, i, got, l.v, l.n)
			}
		}
	}
}

// TestBoolRoundtrip checks equiprobable boolean roundtrip.
func TestBoolRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 200; trial++ {
		bitsSeq := make([]int, 200)
		enc := NewEncoder()
		for i := range bitsSeq {
			bitsSeq[i] = rng.Intn(2)
			enc.WriteBool(bitsSeq[i])
		}
		data := enc.Finish()
		dec := NewDecoder(data, false)
		for i, b := range bitsSeq {
			if got := dec.ReadBool(); got != b {
				t.Fatalf("trial %d bool %d: got %d want %d", trial, i, got, b)
			}
		}
	}
}

// TestSymbolRoundtripNoAdapt checks adaptive-CDF symbols with adaptation disabled
// (static CDFs) — isolates the core range coder from the adaptation logic.
func TestSymbolRoundtripNoAdapt(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, n := range []int{2, 3, 4, 8, 16} {
		base := uniformCDF(n)
		for trial := 0; trial < 100; trial++ {
			syms := make([]int, 300)
			enc := NewEncoder()
			ec := cloneCDF(base)
			for i := range syms {
				syms[i] = rng.Intn(n)
				enc.EncodeSymbol(ec, syms[i])
			}
			data := enc.Finish()
			dec := NewDecoder(data, false)
			dc := cloneCDF(base)
			for i, want := range syms {
				if got := dec.DecodeSymbol(dc); got != want {
					t.Fatalf("n=%d trial %d sym %d: got %d want %d", n, trial, i, got, want)
				}
			}
		}
	}
}

// TestSymbolRoundtripAdapt checks the full path: adaptive CDFs updated identically
// on both sides after every symbol. Encoder and decoder keep independent CDF copies
// that must stay in lockstep.
func TestSymbolRoundtripAdapt(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for _, n := range []int{2, 4, 7, 16} {
		base := uniformCDF(n)
		for trial := 0; trial < 100; trial++ {
			syms := make([]int, 500)
			enc := NewEncoder()
			enc.AllowUpdate = true
			ec := cloneCDF(base)
			// Skew the source so adaptation actually moves the CDF.
			for i := range syms {
				if rng.Intn(3) == 0 {
					syms[i] = rng.Intn(n)
				} else {
					syms[i] = 0
				}
				enc.EncodeSymbol(ec, syms[i])
			}
			data := enc.Finish()
			dec := NewDecoder(data, true)
			dc := cloneCDF(base)
			for i, want := range syms {
				if got := dec.DecodeSymbol(dc); got != want {
					t.Fatalf("n=%d trial %d sym %d: got %d want %d", n, trial, i, got, want)
				}
			}
		}
	}
}

// TestMixedRoundtrip interleaves adaptive symbols, equiprobable bools and literals
// across several CDFs — the realistic usage pattern in a tile decode.
func TestMixedRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for trial := 0; trial < 200; trial++ {
		cdfs := [][]uint16{uniformCDF(4), uniformCDF(8), uniformCDF(3)}
		type op struct {
			kind int // 0 symbol, 1 bool, 2 literal
			idx  int
			v    uint32
			n    int
		}
		var ops []op
		enc := NewEncoder()
		enc.AllowUpdate = true
		ec := make([][]uint16, len(cdfs))
		for i := range cdfs {
			ec[i] = cloneCDF(cdfs[i])
		}
		for i := 0; i < 400; i++ {
			switch rng.Intn(3) {
			case 0:
				idx := rng.Intn(len(ec))
				ncdf := len(ec[idx]) - 1
				s := rng.Intn(ncdf)
				ops = append(ops, op{0, idx, uint32(s), 0})
				enc.EncodeSymbol(ec[idx], s)
			case 1:
				b := rng.Intn(2)
				ops = append(ops, op{1, 0, uint32(b), 0})
				enc.WriteBool(b)
			default:
				n := 1 + rng.Intn(16)
				v := rng.Uint32() & (1<<uint(n) - 1)
				ops = append(ops, op{2, 0, v, n})
				enc.WriteLiteral(v, n)
			}
		}
		data := enc.Finish()
		dec := NewDecoder(data, true)
		dc := make([][]uint16, len(cdfs))
		for i := range cdfs {
			dc[i] = cloneCDF(cdfs[i])
		}
		for i, o := range ops {
			switch o.kind {
			case 0:
				if got := dec.DecodeSymbol(dc[o.idx]); got != int(o.v) {
					t.Fatalf("trial %d op %d symbol: got %d want %d", trial, i, got, o.v)
				}
			case 1:
				if got := dec.ReadBool(); got != int(o.v) {
					t.Fatalf("trial %d op %d bool: got %d want %d", trial, i, got, o.v)
				}
			default:
				if got := dec.ReadLiteral(o.n); got != o.v {
					t.Fatalf("trial %d op %d literal: got %d want %d", trial, i, got, o.v)
				}
			}
		}
	}
}
