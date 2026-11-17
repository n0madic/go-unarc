package crypt

import (
	"crypto/cipher"
	"crypto/subtle"
)

// keystreamBlocks is how many counter blocks ctr encrypts at a time.
const keystreamBlocks = 32

// ctr is LibTomCrypt's CTR mode with CTR_COUNTER_LITTLE_ENDIAN: the whole block is a counter whose first
// byte is the least significant, and the first keystream block encrypts the IV itself.
// (cipher.NewCTR counts big-endian.)
type ctr struct {
	b       cipher.Block
	counter []byte
	stream  []byte // keystream; stream[used:] is still unused
	used    int
}

func newCTR(b cipher.Block, iv []byte) *ctr {
	bs := b.BlockSize()
	c := &ctr{b: b, counter: append([]byte(nil), iv[:bs]...), stream: make([]byte, bs*keystreamBlocks)}
	c.used = len(c.stream)
	return c
}

// refill encrypts the next counters; the counter is incremented after each block.
func (c *ctr) refill() {
	bs := len(c.counter)
	for i := 0; i < len(c.stream); i += bs {
		c.b.Encrypt(c.stream[i:i+bs], c.counter)
		for j := range c.counter {
			c.counter[j]++
			if c.counter[j] != 0 {
				break
			}
		}
	}
	c.used = 0
}

func (c *ctr) XORKeyStream(dst, src []byte) {
	for len(src) > 0 {
		if c.used == len(c.stream) {
			c.refill()
		}
		n := subtle.XORBytes(dst, src, c.stream[c.used:])
		c.used += n
		dst, src = dst[n:], src[n:]
	}
}

// cfb is full-block CFB decryption (LibTomCrypt cfb_start/cfb_decrypt): the register starts as E(IV), and
// each full block of ciphertext is encrypted to give the next one.
type cfb struct {
	b        cipher.Block
	register []byte // keystream for the current block
	feedback []byte // ciphertext of the current block
	used     int
}

func newCFB(b cipher.Block, iv []byte) *cfb {
	bs := b.BlockSize()
	c := &cfb{b: b, register: make([]byte, bs), feedback: make([]byte, bs)}
	b.Encrypt(c.register, iv[:bs])
	return c
}

func (c *cfb) XORKeyStream(dst, src []byte) {
	for len(src) > 0 {
		if c.used == len(c.register) {
			c.b.Encrypt(c.register, c.feedback)
			c.used = 0
		}
		n := copy(c.feedback[c.used:], src) // before dst overwrites src when they are the same buffer
		subtle.XORBytes(dst[:n], src[:n], c.register[c.used:])
		c.used += n
		dst, src = dst[n:], src[n:]
	}
}
