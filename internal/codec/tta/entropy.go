package tta

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

// Entropy coder of TTA: an adaptive Rice code with two parameter sets. Every sample is mapped to an
// unsigned value (see decodeSign), and the code of a value has the form
//
//	unary(u) binary(k bits)
//
// The first set (sum0, k0) models the whole values: when value < 2^k0 the unary part is 0 and the low k0 bits
// follow. Larger values subtract 2^k0, are modelled by the second set (sum1, k1), and are coded as
// unary(1 + value>>k1) binary(k1 bits). A unary part of 50 or more is escaped as unary(50) followed by the
// real unary value in 32 bits. The bits are packed from the least significant bit of each byte.

const (
	initialK     = 10 // both Rice parameters start here
	escapeUnary  = 50 // unary length that announces a 32-bit unary value
	maxRiceK     = 33 // the largest Rice parameter: the C decoder reads outside its tables beyond it
	bitArrayPad  = 16 // zero bytes after the bit array so the 64-bit loads never leave the buffer
	unarySlowMin = 56 // the fast unary path sees at most 57 bits, longer runs are counted by unarySlow
)

// errBitArrayOverrun reports that the samples need more bits than the bit array holds. The C decoder
// reads whatever lies after the array in memory in this case.
var errBitArrayOverrun = errors.New("bit array exhausted")

// errRiceRange reports a Rice parameter above maxRiceK: the values would be around 2^34, which no sample is.
// The C decoder reads past its tables there.
var errRiceRange = errors.New("Rice parameter out of range")

// bitShift[k] is the C table bit_shift: 1<<k, saturated at 1<<31.
var bitShift = func() (t [40]uint32) {
	for k := range t {
		t[k] = 1 << min(k, 31)
	}
	return t
}()

// shift16[k] is the C pointer shift_16 = bit_shift+4, the sum thresholds of the Rice parameter adaption,
// up to the entries the largest parameter looks at.
var shift16 = func() (t [maxRiceK + 2]uint32) {
	for k := range t {
		t[k] = bitShift[k+4]
	}
	return t
}()

// load64 returns the 8 bytes at data[idx:] as a little-endian word.
func load64(data []byte, idx uint64) uint64 {
	return binary.LittleEndian.Uint64(data[idx:])
}

// getBits returns the next n (at most 32) bits at bit position pos.
func getBits(data []byte, pos uint64, n uint32) uint32 {
	w := load64(data, pos>>3) >> (pos & 7)
	return uint32(w & (1<<n - 1))
}

// unarySlow counts the one bits from bit position pos on; the run is longer than a single word.
// The zero padding after the bit array ends every run.
func unarySlow(data []byte, pos uint64) uint64 {
	var n uint64
	for {
		p := pos + n
		sh := p & 7
		ones := uint64(bits.TrailingZeros64(^(load64(data, p>>3) >> sh)))
		n += ones
		if ones < 64-sh {
			return n
		}
	}
}

// decodeSign maps an unsigned value back to a signed sample: the C macro DEC. Odd values are positive.
func decodeSign(v int32) int32 {
	odd := -(v & 1) // all ones for an odd value; selecting by a mask is cheaper than the unpredictable branch
	return (v+1)>>1&odd | -v>>1&^odd
}

// decodeFrame decodes len(out) samples from data, a bit array padded with bitArrayPad zero bytes whose
// meaningful part has endBits bits, starting at bit position pos. It returns the position after the last
// sample. The Rice parameters start afresh for every call, the bit position runs on from channel to channel.
func decodeFrame(out []int32, data []byte, pos, endBits uint64) (uint64, error) {
	k0, k1 := uint32(initialK), uint32(initialK)
	sum0, sum1 := shift16[k0], shift16[k1]

	for i := range out {
		if pos >= endBits {
			return pos, errBitArrayOverrun
		}

		// Unary part: the number of one bits before a zero.
		w := load64(data, pos>>3) >> (pos & 7)
		ones := uint32(bits.TrailingZeros64(^w))
		var unary uint32
		if ones < unarySlowMin {
			unary = ones
			pos += uint64(ones) + 1
		} else {
			n := unarySlow(data, pos)
			unary = uint32(n)
			pos += n + 1
		}
		if unary == escapeUnary {
			unary = getBits(data, pos, 32)
			pos += 32
		}

		// Binary part with the parameter of the set that models this value.
		var k uint32
		deep := unary != 0
		if deep {
			k = k1
			unary--
		} else {
			k = k0
		}
		value := unary
		if k != 0 {
			// C shifts by k, and the processors it runs on take the count modulo 32.
			value = unary<<(k&31) + getBits(data, pos, min(k, 32))
			pos += uint64(k)
		}

		if deep {
			sum1 += value - sum1>>4
			if k1 > 0 && sum1 < shift16[k1] {
				k1--
			} else if sum1 > shift16[k1+1] {
				if k1++; k1 > maxRiceK {
					return pos, errRiceRange
				}
			}
			value += bitShift[k0]
		}
		sum0 += value - sum0>>4
		if k0 > 0 && sum0 < shift16[k0] {
			k0--
		} else if sum0 > shift16[k0+1] {
			if k0++; k0 > maxRiceK {
				return pos, errRiceRange
			}
		}

		out[i] = decodeSign(int32(value))
	}
	if pos > endBits {
		return pos, errBitArrayOverrun
	}
	return pos, nil
}
