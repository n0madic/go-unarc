package tta

import "math/bits"

// combineInt undoes the channel decorrelation of the encoder (combine_int in C). The planes have the same
// length. The last channel holds its difference from the one before and every other channel the difference
// from its successor, with the last channel's value halved on the way.
func combineInt(planes [][]int32) {
	n := len(planes) - 1
	if n < 1 {
		return
	}
	if n == 1 {
		l, r := planes[0], planes[1]
		r = r[:len(l)]
		for i, a := range l {
			b := r[i] + a/2
			r[i] = b
			l[i] = b - a
		}
		return
	}
	for i := range planes[0] {
		planes[n][i] += planes[n-1][i] / 2
		for j := n; j > 0; j-- {
			planes[j-1][i] = planes[j][i] - planes[j-1][i]
		}
	}
}

// floatBias is the part of the IEEE 754 single precision exponent the encoder subtracts (0x3F80 are the top
// 16 bits of 1.0f without the sign).
const floatBias = 0x3F80

// combineFloat joins the first numChan planes (sign-less top half of the float, biased) with the
// second numChan ones (signed, offset bit-reversed low half) into 32-bit floats (combine_float in C).
// The result is left in the first numChan planes.
func combineFloat(planes [][]int32, numChan int) {
	for j := range numChan {
		hi, lo := planes[j], planes[j+numChan]
		lo = lo[:len(hi)]
		for i, l := range lo {
			negative := uint32(l) & 0x80000000
			if l < 0 {
				l = -l
			}
			dataLo := uint32(l) - 1
			dataHi := uint32(hi[i]) + floatBias
			hi[i] = int32(dataHi<<16 | uint32(bits.Reverse16(uint16(dataLo))) | negative)
		}
	}
}
