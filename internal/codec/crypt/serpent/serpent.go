// Package serpent implements the Serpent block cipher exactly as FreeArc's serpent.c (Brian Gladman's
// implementation in the LibTomCrypt API) does: 128-bit blocks, keys of 0 to 32 bytes, little-endian words.
package serpent

import (
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"math/bits"
)

const (
	// BlockSize is the Serpent block size in bytes.
	BlockSize = 16
	// MaxKeySize is the largest key in bytes.
	MaxKeySize = 32
	rounds     = 32
	phi        = 0x9e3779b9
)

// KeySizeError is returned for keys longer than MaxKeySize.
type KeySizeError int

func (k KeySizeError) Error() string { return fmt.Sprintf("serpent: invalid key size %d", int(k)) }

// Cipher is a Serpent key schedule.
type Cipher struct {
	k [rounds + 1][4]uint32
}

var _ cipher.Block = (*Cipher)(nil)

// NewCipher expands key. Shorter keys are padded with a single 1 bit, as the Serpent specification says.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) > MaxKeySize {
		return nil, KeySizeError(len(key))
	}
	var l [8 + 4*(rounds+1)]uint32
	var padded [MaxKeySize]byte
	copy(padded[:], key)
	for i := range 8 {
		l[i] = binary.LittleEndian.Uint32(padded[4*i:])
	}
	if n := len(key) * 8; n < 256 {
		i, bit := n/32, uint32(1)<<(n%32)
		l[i] = l[i]&(bit-1) | bit
	}
	for i := range 4 * (rounds + 1) {
		l[i+8] = bits.RotateLeft32(l[i]^l[i+3]^l[i+5]^l[i+7]^phi^uint32(i), 11)
	}
	c := new(Cipher)
	for r := range c.k {
		w := l[8+4*r : 12+4*r]
		c.k[r][0], c.k[r][1], c.k[r][2], c.k[r][3] = sbox(int(35-r)%8, w[0], w[1], w[2], w[3])
	}
	return c, nil
}

// BlockSize returns the block size.
func (*Cipher) BlockSize() int { return BlockSize }

// sbox applies S-box n (the key schedule picks them in a rotating order).
func sbox(n int, a, b, c, d uint32) (uint32, uint32, uint32, uint32) {
	switch n {
	case 0:
		return sb0(a, b, c, d)
	case 1:
		return sb1(a, b, c, d)
	case 2:
		return sb2(a, b, c, d)
	case 3:
		return sb3(a, b, c, d)
	case 4:
		return sb4(a, b, c, d)
	case 5:
		return sb5(a, b, c, d)
	case 6:
		return sb6(a, b, c, d)
	default:
		return sb7(a, b, c, d)
	}
}

// lt is the linear transformation.
func lt(a, b, c, d uint32) (uint32, uint32, uint32, uint32) {
	a = bits.RotateLeft32(a, 13)
	c = bits.RotateLeft32(c, 3)
	d ^= c ^ a<<3
	b ^= a ^ c
	d = bits.RotateLeft32(d, 7)
	b = bits.RotateLeft32(b, 1)
	a ^= b ^ d
	c ^= d ^ b<<7
	a = bits.RotateLeft32(a, 5)
	c = bits.RotateLeft32(c, 22)
	return a, b, c, d
}

// ilt is the inverse linear transformation.
func ilt(a, b, c, d uint32) (uint32, uint32, uint32, uint32) {
	c = bits.RotateLeft32(c, -22)
	a = bits.RotateLeft32(a, -5)
	c ^= d ^ b<<7
	a ^= b ^ d
	d = bits.RotateLeft32(d, -7)
	b = bits.RotateLeft32(b, -1)
	d ^= c ^ a<<3
	b ^= a ^ c
	c = bits.RotateLeft32(c, -3)
	a = bits.RotateLeft32(a, -13)
	return a, b, c, d
}

// Encrypt encrypts the first block of src into dst.
func (s *Cipher) Encrypt(dst, src []byte) {
	if len(src) < BlockSize || len(dst) < BlockSize {
		panic("serpent: input not full block")
	}
	a, b, c, d := load(src)
	k := &s.k
	for r := 0; r < rounds; r += 8 {
		a, b, c, d = sb0(a^k[r][0], b^k[r][1], c^k[r][2], d^k[r][3])
		a, b, c, d = lt(a, b, c, d)
		a, b, c, d = sb1(a^k[r+1][0], b^k[r+1][1], c^k[r+1][2], d^k[r+1][3])
		a, b, c, d = lt(a, b, c, d)
		a, b, c, d = sb2(a^k[r+2][0], b^k[r+2][1], c^k[r+2][2], d^k[r+2][3])
		a, b, c, d = lt(a, b, c, d)
		a, b, c, d = sb3(a^k[r+3][0], b^k[r+3][1], c^k[r+3][2], d^k[r+3][3])
		a, b, c, d = lt(a, b, c, d)
		a, b, c, d = sb4(a^k[r+4][0], b^k[r+4][1], c^k[r+4][2], d^k[r+4][3])
		a, b, c, d = lt(a, b, c, d)
		a, b, c, d = sb5(a^k[r+5][0], b^k[r+5][1], c^k[r+5][2], d^k[r+5][3])
		a, b, c, d = lt(a, b, c, d)
		a, b, c, d = sb6(a^k[r+6][0], b^k[r+6][1], c^k[r+6][2], d^k[r+6][3])
		a, b, c, d = lt(a, b, c, d)
		a, b, c, d = sb7(a^k[r+7][0], b^k[r+7][1], c^k[r+7][2], d^k[r+7][3])
		if r+8 < rounds {
			a, b, c, d = lt(a, b, c, d)
		}
	}
	store(dst, a^k[rounds][0], b^k[rounds][1], c^k[rounds][2], d^k[rounds][3])
}

// Decrypt decrypts the first block of src into dst.
func (s *Cipher) Decrypt(dst, src []byte) {
	if len(src) < BlockSize || len(dst) < BlockSize {
		panic("serpent: input not full block")
	}
	a, b, c, d := load(src)
	k := &s.k
	a, b, c, d = a^k[rounds][0], b^k[rounds][1], c^k[rounds][2], d^k[rounds][3]
	for r := rounds - 8; r >= 0; r -= 8 {
		if r+8 < rounds {
			a, b, c, d = ilt(a, b, c, d)
		}
		a, b, c, d = ib7(a, b, c, d)
		a, b, c, d = ilt(a^k[r+7][0], b^k[r+7][1], c^k[r+7][2], d^k[r+7][3])
		a, b, c, d = ib6(a, b, c, d)
		a, b, c, d = ilt(a^k[r+6][0], b^k[r+6][1], c^k[r+6][2], d^k[r+6][3])
		a, b, c, d = ib5(a, b, c, d)
		a, b, c, d = ilt(a^k[r+5][0], b^k[r+5][1], c^k[r+5][2], d^k[r+5][3])
		a, b, c, d = ib4(a, b, c, d)
		a, b, c, d = ilt(a^k[r+4][0], b^k[r+4][1], c^k[r+4][2], d^k[r+4][3])
		a, b, c, d = ib3(a, b, c, d)
		a, b, c, d = ilt(a^k[r+3][0], b^k[r+3][1], c^k[r+3][2], d^k[r+3][3])
		a, b, c, d = ib2(a, b, c, d)
		a, b, c, d = ilt(a^k[r+2][0], b^k[r+2][1], c^k[r+2][2], d^k[r+2][3])
		a, b, c, d = ib1(a, b, c, d)
		a, b, c, d = ilt(a^k[r+1][0], b^k[r+1][1], c^k[r+1][2], d^k[r+1][3])
		a, b, c, d = ib0(a, b, c, d)
		a, b, c, d = a^k[r][0], b^k[r][1], c^k[r][2], d^k[r][3]
	}
	store(dst, a, b, c, d)
}

func load(p []byte) (a, b, c, d uint32) {
	return binary.LittleEndian.Uint32(p), binary.LittleEndian.Uint32(p[4:]),
		binary.LittleEndian.Uint32(p[8:]), binary.LittleEndian.Uint32(p[12:])
}

func store(p []byte, a, b, c, d uint32) {
	binary.LittleEndian.PutUint32(p, a)
	binary.LittleEndian.PutUint32(p[4:], b)
	binary.LittleEndian.PutUint32(p[8:], c)
	binary.LittleEndian.PutUint32(p[12:], d)
}
