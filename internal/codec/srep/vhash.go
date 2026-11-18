package srep

import (
	"crypto/aes"
	"encoding/binary"
	"math/bits"
)

// VHASH-128 is the universal hash in draft-krovetz-vmac-01, section 5. SREP uses
// the public-domain Krovetz/Dai implementation's AES-256, 4096-byte NH and
// one-byte KDF counter. It stores just VHASH, without VMAC's nonce/pad layer.
const (
	nhSize   = 4096
	polyMask = 0x1fffffff1fffffff
	prime64  = ^uint64(0) - 256 // 2^64 - 257
	mask63   = 1<<63 - 1
)

type uint128 struct{ hi, lo uint64 }

func (x uint128) add(y uint128) uint128 {
	lo, carry := bits.Add64(x.lo, y.lo, 0)
	return uint128{x.hi + y.hi + carry, lo}
}

type vhashKey struct {
	nh       [nhSize/8 + 2]uint64 // each successive tag word starts two words later
	poly     [2]uint128
	l3       [2]uint128
	oldARM64 bool
}

func newVHash(key [32]byte, oldARM64 bool) *vhashKey {
	cipher, _ := aes.NewCipher(key[:])
	derive := func(domain, counter byte) uint128 {
		var in, out [16]byte
		in[0], in[15] = domain, counter
		cipher.Encrypt(out[:], in[:])
		if oldARM64 {
			return uint128{binary.LittleEndian.Uint64(out[:8]), binary.LittleEndian.Uint64(out[8:])}
		}
		return uint128{binary.BigEndian.Uint64(out[:8]), binary.BigEndian.Uint64(out[8:])}
	}
	k := &vhashKey{oldARM64: oldARM64}
	for i := 0; i < len(k.nh); i += 2 {
		x := derive(128, byte(i/2))
		k.nh[i], k.nh[i+1] = x.hi, x.lo
	}
	for i := range k.poly {
		x := derive(192, byte(i))
		k.poly[i] = uint128{x.hi & polyMask, x.lo & polyMask}
	}
	lanes := 0
	for counter := 0; counter < 256*len(k.l3); counter++ {
		x := derive(224, byte(counter))
		if x.hi < prime64 && x.lo < prime64 {
			k.l3[lanes] = x
			lanes++
			if lanes == len(k.l3) {
				return k
			}
		}
	}
	return nil
}

func (k *vhashKey) sum(p []byte) (tag [16]byte) {
	remBits := uint64(len(p)%nhSize) * 8
	y := k.poly
	first := true
	for len(p) != 0 {
		n := min(len(p), nhSize)
		chunk := p[:n]
		p = p[n:]
		var hash [2]uint128
		for i := 0; len(chunk) != 0; i += 2 {
			pair := chunk
			var tail [16]byte
			if len(chunk) < 16 {
				copy(tail[:], chunk)
				pair = tail[:]
			}
			a := binary.LittleEndian.Uint64(pair[:8])
			b := binary.LittleEndian.Uint64(pair[8:16])
			if k.oldARM64 {
				a, b = bits.ReverseBytes64(a), bits.ReverseBytes64(b)
			}
			h0, l0 := bits.Mul64(a+k.nh[i], b+k.nh[i+1])
			h1, l1 := bits.Mul64(a+k.nh[i+2], b+k.nh[i+3])
			hash[0] = hash[0].add(uint128{h0, l0})
			hash[1] = hash[1].add(uint128{h1, l1})
			chunk = chunk[min(16, len(chunk)):]
		}
		for i := range hash {
			hash[i].hi &= 1<<62 - 1
			switch {
			case first:
				y[i] = y[i].add(hash[i])
			case k.oldARM64:
				y[i] = oldARM64Poly(y[i], k.poly[i], hash[i])
			default:
				y[i] = mul127(y[i], k.poly[i]).add(hash[i]).mod127()
			}
		}
		first = false
	}
	for i := range y {
		x := y[i].add(uint128{hi: remBits}).mod127()
		a, b := bits.Div64(x.hi, x.lo, 1<<64-1<<32)
		hi, lo := bits.Mul64(add64Mod(a, k.l3[i].hi), add64Mod(b, k.l3[i].lo))
		_, val := bits.Div64(hi, lo, prime64)
		binary.LittleEndian.PutUint64(tag[8*i:], val)
	}
	return tag
}

// The misdetected arm64 implementation also swapped the two base-2^32 digits of
// every 64-bit operand of the portable polynomial step (except m.hi). Keep the
// result partially reduced, since the next step splits its digits again.
func oldARM64Poly(a, k, m uint128) uint128 {
	digits := func(x uint128) [4]uint64 {
		return [4]uint64{x.lo >> 32, uint64(uint32(x.lo)), x.hi >> 32, uint64(uint32(x.hi))}
	}
	var c [7]uint64
	for i, x := range digits(a) {
		for j, y := range digits(k) {
			c[i+j] += x * y
		}
	}
	// Fold coefficients of 2^128 and above at twice their value, then bit 127.
	c2 := c[2] + 2*c[6] + m.hi
	c3 := c[3] + (c2 >> 32)
	c0 := c[0] + 2*c[4] + (m.lo >> 32) + (c3 >> 31)
	c1 := c[1] + 2*c[5] + uint64(uint32(m.lo)) + (c0 >> 32)
	hi := uint64(uint32(c2)) | (c3&(1<<31-1))<<32
	return uint128{hi + (c1 >> 32), c1<<32 | uint64(uint32(c0))}
}

func add64Mod(a, b uint64) uint64 {
	x, carry := bits.Add64(a, b, 0)
	x += 257 * carry // 2^64 ≡ 257 (mod prime64)
	if x >= prime64 {
		x -= prime64
	}
	return x
}

func (x uint128) mod127() uint128 {
	// Fold bits above bit 126 down to the low end: 2^127 ≡ 1 (mod 2^127 - 1).
	high := x.hi >> 63
	x.hi &= mask63
	x = x.add(uint128{lo: high})
	if x.hi > mask63 {
		return uint128{lo: 1} // only possible at exactly 2^127
	}
	if x.hi == mask63 && x.lo == ^uint64(0) {
		return uint128{}
	}
	return x
}

func mul127(a, b uint128) uint128 {
	w1, w0 := bits.Mul64(a.lo, b.lo)
	w3, w2 := bits.Mul64(a.hi, b.hi)
	for _, p := range [2]uint128{{a.hi, b.lo}, {a.lo, b.hi}} {
		hi, lo := bits.Mul64(p.hi, p.lo)
		var carry uint64
		w1, carry = bits.Add64(w1, lo, 0)
		w2, carry = bits.Add64(w2, hi, carry)
		w3 += carry
	}
	low := uint128{w1 & mask63, w0}
	high := uint128{w3<<1 | w2>>63, w2<<1 | w1>>63}
	return low.add(high).mod127()
}
