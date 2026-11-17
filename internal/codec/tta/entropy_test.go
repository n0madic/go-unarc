package tta

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// bitWriter packs bits from the least significant bit of each byte, like put_binary and put_unary of the
// C encoder.
type bitWriter struct {
	buf  []byte
	bits uint64
}

func (w *bitWriter) putBit(b uint32) {
	if w.bits&7 == 0 {
		w.buf = append(w.buf, 0)
	}
	w.buf[len(w.buf)-1] |= byte(b&1) << (w.bits & 7)
	w.bits++
}

func (w *bitWriter) putBinary(v uint32, n uint32) {
	for i := range n {
		w.putBit(v >> i)
	}
}

func (w *bitWriter) putUnary(n uint32) {
	for range n {
		w.putBit(1)
	}
	w.putBit(0)
}

// encodeFrame is encode_frame of the C encoder: the values are int32 samples, which must stay within
// +-2^28 so that neither the unsigned mapping nor the sums of the Rice parameters overflow.
func (w *bitWriter) encodeFrame(data []int32) {
	k0, k1 := uint32(initialK), uint32(initialK)
	sum0, sum1 := shift16[k0], shift16[k1]
	for _, s := range data {
		var value uint32
		if s > 0 {
			value = uint32(s)<<1 - 1
		} else {
			value = uint32(-s) << 1
		}
		k := k0
		sum0 += value - sum0>>4
		if k0 > 0 && sum0 < shift16[k0] {
			k0--
		} else if sum0 > shift16[k0+1] {
			k0++
		}
		var unary uint32
		if value >= bitShift[k] {
			value -= bitShift[k]
			k = k1
			sum1 += value - sum1>>4
			if k1 > 0 && sum1 < shift16[k1] {
				k1--
			} else if sum1 > shift16[k1+1] {
				k1++
			}
			unary = 1 + value>>k
		}
		if unary >= escapeUnary {
			w.putUnary(escapeUnary)
			w.putBinary(unary, 32)
		} else {
			w.putUnary(unary)
		}
		if k != 0 {
			w.putBinary(value&(1<<k-1), k)
		}
	}
}

// padded returns the bit array the way the decoder keeps it.
func (w *bitWriter) padded() []byte {
	return append(slices.Clone(w.buf), make([]byte, bitArrayPad)...)
}

func TestDecodeFrameRoundTrip(t *testing.T) {
	t.Parallel()
	gens := map[string]func(r *rand.Rand) int32{
		"small":  func(r *rand.Rand) int32 { return int32(r.IntN(41) - 20) },
		"medium": func(r *rand.Rand) int32 { return int32(r.IntN(1<<17) - 1<<16) },
		"large":  func(r *rand.Rand) int32 { return int32(r.IntN(1<<26) - 1<<25) },
		"zero":   func(r *rand.Rand) int32 { return 0 },
		// mostly tiny with rare huge values: long unary parts and the escape of unary >= 50
		"heavy tail": func(r *rand.Rand) int32 {
			if r.IntN(60) == 0 {
				return int32(r.IntN(1<<25)) - 1<<24
			}
			return int32(r.IntN(201) - 100)
		},
		"extremes": func(r *rand.Rand) int32 { return [...]int32{1<<26 - 1, -(1<<26 - 1), 1, -1, 0}[r.IntN(5)] },
	}
	for name, gen := range gens {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := rand.New(rand.NewPCG(1, uint64(len(name))))
			for _, n := range []int{0, 1, 2, 7, 100, 5000} {
				// two planes in one bit array: the bit position runs on from one to the next
				a, b := make([]int32, n), make([]int32, n)
				for i := range a {
					a[i], b[i] = gen(r), gen(r)
				}
				var w bitWriter
				w.encodeFrame(a)
				w.encodeFrame(b)
				data := w.padded()

				got := make([]int32, 2*n)
				pos := uint64(0)
				var err error
				for p := range 2 {
					if pos, err = decodeFrame(got[p*n:(p+1)*n], data, pos, w.bits); err != nil {
						t.Fatalf("n=%d plane %d: %v", n, p, err)
					}
				}
				if pos != w.bits {
					t.Errorf("n=%d: ended at bit %d, want %d", n, pos, w.bits)
				}
				if !slices.Equal(got[:n], a) || !slices.Equal(got[n:], b) {
					t.Fatalf("n=%d: decoded values differ", n)
				}
			}
		})
	}
}

// The unary escape: exactly 50 ones announce a 32-bit unary value, longer runs of ones are plain values.
func TestDecodeFrameUnaryRuns(t *testing.T) {
	t.Parallel()
	for _, ones := range []uint32{0, 1, 49, 50, 51, 55, 56, 57, 63, 64, 65, 100, 200} {
		var w bitWriter
		w.putUnary(ones)
		if ones == escapeUnary {
			w.putBinary(1234, 32) // the real unary value
		}
		w.putBinary(0x155, 10) // k = 10 binary part, whichever set is used
		data := w.padded()

		var want uint32
		switch {
		case ones == 0:
			want = 0x155
		case ones == escapeUnary:
			want = (1234-1)<<10 + 0x155
		default:
			want = (ones-1)<<10 + 0x155
		}
		want += bitShift[10] * btoi(ones != 0)
		got := make([]int32, 1)
		if _, err := decodeFrame(got, data, 0, w.bits); err != nil {
			t.Fatalf("%d ones: %v", ones, err)
		}
		if exp := decodeSign(int32(want)); got[0] != exp {
			t.Errorf("%d ones: got %d, want %d", ones, got[0], exp)
		}
	}
}

func btoi(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

func TestDecodeFrameOverrun(t *testing.T) {
	t.Parallel()
	var w bitWriter
	w.encodeFrame([]int32{5, -3, 100, 7})
	data := w.padded()

	for _, tt := range []struct {
		name    string
		n       int
		endBits uint64
		wantErr bool
	}{
		{"exact", 4, w.bits, false},
		{"one sample too many", 5, w.bits, true},
		{"cut short", 4, w.bits - 1, true},
		{"empty array", 1, 0, true},
		{"nothing wanted", 0, 0, false},
		{"all ones", -1, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			arr, endBits, n := data, tt.endBits, tt.n
			if tt.name == "all ones" {
				// A run of ones without its terminating zero up to the end of the array.
				arr = append(slices.Repeat([]byte{0xFF}, 40), make([]byte, bitArrayPad)...)
				endBits, n = 40*8, 1
			}
			_, err := decodeFrame(make([]int32, n), arr, 0, endBits)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, want error: %v", err, tt.wantErr)
			}
		})
	}
}

func TestDecodeSign(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ in, want int32 }{
		{0, 0}, {1, 1}, {2, -1}, {3, 2}, {4, -2}, {-1 << 31, -1 << 30}, {1<<31 - 1, -1 << 30},
	} {
		if got := decodeSign(tt.in); got != tt.want {
			t.Errorf("decodeSign(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
