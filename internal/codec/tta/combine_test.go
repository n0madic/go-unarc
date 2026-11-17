package tta

import (
	"math"
	"math/bits"
	"math/rand/v2"
	"slices"
	"testing"
)

// splitInt is split_int of the C encoder for planes of equal length.
func splitInt(planes [][]int32) {
	n := len(planes) - 1
	if n < 1 {
		return
	}
	for i := range planes[0] {
		for j := range n {
			planes[j][i] = planes[j+1][i] - planes[j][i]
		}
		planes[n][i] -= planes[n-1][i] / 2
	}
}

// splitFloat is split_float of the C encoder: planes[:numChan] get the biased top half, the rest the signed
// bit-reversed bottom half plus one.
func splitFloat(planes [][]int32, numChan int) {
	for j := range numChan {
		for i := range planes[j] {
			t := uint32(planes[j][i])
			negative := uint32(1)
			if t&0x80000000 != 0 {
				negative = ^uint32(0)
			}
			hi := (t & 0x7FFF0000) >> 16
			lo := t & 0xFFFF
			planes[j][i] = int32(hi - floatBias)
			planes[j+numChan][i] = int32((uint32(bits.Reverse16(uint16(lo))) + 1) * negative)
		}
	}
}

func randomPlanes(r *rand.Rand, n, length int, wide bool) [][]int32 {
	planes := make([][]int32, n)
	for j := range planes {
		planes[j] = make([]int32, length)
		for i := range planes[j] {
			if wide {
				planes[j][i] = int32(r.Uint32())
			} else {
				planes[j][i] = int32(r.IntN(1<<16) - 1<<15)
			}
		}
	}
	return planes
}

func clonePlanes(p [][]int32) [][]int32 {
	out := make([][]int32, len(p))
	for i := range p {
		out[i] = slices.Clone(p[i])
	}
	return out
}

func TestCombineIntInvertsSplit(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(3, 4))
	for _, n := range []int{1, 2, 3, 4, 6, 255} {
		// the encoder halves with wrap-around, so wide values round-trip too
		for _, wide := range []bool{false, true} {
			orig := randomPlanes(r, n, 500, wide)
			work := clonePlanes(orig)
			splitInt(work)
			combineInt(work)
			for j := range orig {
				if !slices.Equal(work[j], orig[j]) {
					t.Fatalf("%d channels (wide %v): channel %d differs", n, wide, j)
				}
			}
		}
	}
}

func TestCombineFloatInvertsSplit(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(5, 6))
	for _, n := range []int{1, 2, 5} {
		planes := randomPlanes(r, 2*n, 2000, true)
		// special values: zeros, infinities, NaN, denormals, negative zero
		for i, f := range []float32{0, float32(math.Copysign(0, -1)), float32(math.Inf(1)), float32(math.Inf(-1)),
			float32(math.NaN()), math.SmallestNonzeroFloat32, 1, -1} {
			planes[0][i] = int32(math.Float32bits(f))
		}
		orig := clonePlanes(planes[:n])
		splitFloat(planes, n)
		combineFloat(planes, n)
		for j := range n {
			if !slices.Equal(planes[j], orig[j]) {
				t.Fatalf("%d channels: channel %d differs", n, j)
			}
		}
	}
}
