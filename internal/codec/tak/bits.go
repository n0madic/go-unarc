package tak

import "encoding/binary"

// bitReader reads bit fields least significant bit first, as FFmpeg's little-endian bitstream reader does:
// the first bit of the stream is bit 0 of the first byte. Reads past the end give zero bits and leave the
// position beyond the data, which overrun reports; the decoder checks it where the C code would have read
// into its padding.
type bitReader struct {
	buf []byte
	pos int // position in bits
}

// bitsLeft is the number of bits still in the buffer, negative after an overrun.
func (r *bitReader) bitsLeft() int { return len(r.buf)*8 - r.pos }

func (r *bitReader) overrun() bool { return r.pos > len(r.buf)*8 }

// read returns the next n bits, n <= 32.
func (r *bitReader) read(n uint) uint32 {
	if n == 0 {
		return 0
	}
	i, shift := r.pos>>3, uint(r.pos&7)
	var v uint64
	if i+8 <= len(r.buf) {
		v = binary.LittleEndian.Uint64(r.buf[i:])
	} else {
		for k := 0; k < 8 && i+k < len(r.buf); k++ {
			v |= uint64(r.buf[i+k]) << (8 * k)
		}
	}
	r.pos += int(n)
	return uint32((v >> shift) & (1<<n - 1))
}

func (r *bitReader) bit() bool { return r.read(1) != 0 }

// readSigned returns the next n bits as a two's complement number, n <= 32.
func (r *bitReader) readSigned(n uint) int32 {
	if n == 0 {
		return 0
	}
	return int32(r.read(n)<<(32-n)) >> (32 - n)
}

// read64 returns the next n bits, n <= 64: the low 32 bits come first.
func (r *bitReader) read64(n uint) uint64 {
	if n <= 32 {
		return uint64(r.read(n))
	}
	lo := uint64(r.read(32))
	return lo | uint64(r.read(n-32))<<32
}

func (r *bitReader) skip(n int) { r.pos += n }

// align moves to the next byte boundary.
func (r *bitReader) align() { r.pos = (r.pos + 7) &^ 7 }

// unary counts the bits that differ from stop, up to limit (the terminating bit is consumed when it is seen).
func (r *bitReader) unary(stop uint32, limit int) int {
	i := 0
	for i < limit && r.read(1) != stop {
		i++
	}
	return i
}

// escape4 reads a flag and, if it is set, 4 more bits plus one: the C code's get_bits_esc4.
func (r *bitReader) escape4() int {
	if r.bit() {
		return int(r.read(4)) + 1
	}
	return 0
}
