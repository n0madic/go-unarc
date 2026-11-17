package tak

import "math/bits"

// crcInit is the starting value of every CRC-24 in a TAK stream.
const crcInit = 0xCE04B7

// crcTable is FFmpeg's AV_CRC_24_IEEE table (polynomial 0x864CFB, most significant bit first), stored the way
// av_crc_init does for big-endian polynomials: byte-swapped, so that the register is processed from its
// lowest byte. A stored CRC (three bytes, big-endian) is compared with the register as it comes out.
var crcTable = func() (t [256]uint32) {
	const poly = 0x864CFB << 8
	for i := range t {
		c := uint32(i) << 24
		for range 8 {
			c = c<<1 ^ poly&uint32(int32(c)>>31)
		}
		t[i] = bits.ReverseBytes32(c)
	}
	return t
}()

func crc24(crc uint32, p []byte) uint32 {
	for _, b := range p {
		crc = crcTable[byte(crc)^b] ^ crc>>8
	}
	return crc
}

// checkCRC verifies a block that ends with its own CRC: the CRC of everything before the last three bytes is
// stored in them, big-endian.
func checkCRC(block []byte) bool {
	if len(block) < 4 {
		return false
	}
	body, sum := block[:len(block)-3], block[len(block)-3:]
	return crc24(crcInit, body) == uint32(sum[0])<<16|uint32(sum[1])<<8|uint32(sum[2])
}
