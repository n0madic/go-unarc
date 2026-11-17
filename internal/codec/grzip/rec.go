package grzip

import "encoding/binary"

// recDecode merges the planes in src (len(src) == len(dst)) back into records (GRZip_Rec_Decode).
//
//	1, 2  the bytes of every 2nd / 4th position were stored one position after the other
//	3     16 bit little-endian words: zigzag deltas to the previous word, high bytes then low bytes
//	4     32 bit words: zigzag deltas, byte 3, then byte 0, 1 and 2 as planes
//
// A trailing partial record is stored as is.
func recDecode(dst, src []byte, mode int) {
	size := len(dst)
	le := binary.LittleEndian
	switch mode {
	case 1, 2:
		stride := 2 * mode
		k := 0
		for first := range stride {
			for i := first; i < size; i += stride {
				dst[i] = src[k]
				k++
			}
		}
	case 3:
		n := size >> 1
		hi, lo := src[:n], src[n:2*n]
		var pred uint16
		for i := range n {
			delta := uint16(hi[i])<<8 | uint16(lo[i])
			if delta&1 != 0 {
				delta = ^(delta >> 1)
			} else {
				delta >>= 1
			}
			pred += delta
			le.PutUint16(dst[2*i:], pred)
		}
		copy(dst[2*n:], src[2*n:])
	case 4:
		n := size >> 2
		b3, b0, b1, b2 := src[:n], src[n:2*n], src[2*n:3*n], src[3*n:4*n]
		var pred uint32
		for i := range n {
			delta := uint32(b3[i])<<24 | uint32(b2[i])<<16 | uint32(b1[i])<<8 | uint32(b0[i])
			if delta&1 != 0 {
				delta = ^(delta >> 1)
			} else {
				delta >>= 1
			}
			pred += delta
			le.PutUint32(dst[4*i:], pred)
		}
		copy(dst[4*n:], src[4*n:])
	}
}
