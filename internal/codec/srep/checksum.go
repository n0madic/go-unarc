package srep

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/binary"
	"io"
	"math/bits"

	"github.com/n0madic/go-unarc/internal/codec/frame"
)

const maxTagSize = sha512.Size

type blockHash struct {
	id            byte
	tagLen        int
	sip           [2]uint64
	seed          [32]byte
	vhash         *vhashKey
	vhashSelected bool
}

func readHash(src io.Reader, id byte, seedLen, tagLen int) (blockHash, error) {
	// ID 1 disables checksums. Some encoders still reserve 16 bytes in each block header.
	seeds := [...]int{0, 0, 0, 0, 32, 16}
	tags := [...]int{md5.Size, md5.Size, sha1.Size, sha512.Size, 16, 8}
	if int(id) >= len(seeds) || seedLen != seeds[id] || tagLen > tags[id] || (id != 1 && tagLen == 0) {
		return blockHash{}, badData("invalid or unsupported block checksum")
	}
	h := blockHash{id: id, tagLen: tagLen}
	var seed [32]byte
	if _, err := io.ReadFull(src, seed[:seedLen]); err != nil {
		return blockHash{}, frame.Truncated(err)
	}
	switch id {
	case 4:
		h.seed = seed
		h.vhash = newVHash(seed, false)
		if h.vhash == nil {
			return blockHash{}, badData("invalid VHASH key")
		}
	case 5:
		h.sip = [2]uint64{binary.LittleEndian.Uint64(seed[:]), binary.LittleEndian.Uint64(seed[8:])}
	}
	return h, nil
}

func (h *blockHash) matches(p, tag []byte) bool {
	switch h.id {
	case 0:
		got := md5.Sum(p)
		return bytes.Equal(got[:len(tag)], tag)
	case 1:
		return true
	case 2:
		got := sha1.Sum(p)
		return bytes.Equal(got[:len(tag)], tag)
	case 3:
		got := sha512.Sum512(p)
		return bytes.Equal(got[:len(tag)], tag)
	case 4:
		got := h.vhash.sum(p)
		if !h.vhashSelected && !bytes.Equal(got[:len(tag)], tag) {
			// The old macOS arm64 build misdetected its byte order. There is no marker
			// in the stream, so try that variant on the first block only.
			legacy := newVHash(h.seed, true)
			if legacy == nil {
				return false
			}
			h.vhash = legacy
			got = h.vhash.sum(p)
		}
		h.vhashSelected = true
		return bytes.Equal(got[:len(tag)], tag)
	case 5:
		var got [8]byte
		binary.LittleEndian.PutUint64(got[:], sipHash24(h.sip, p))
		return bytes.Equal(got[:len(tag)], tag)
	}
	return false
}

// SipHash-2-4, from the Aumasson/Bernstein specification (ePrint 2012/351).
func sipHash24(key [2]uint64, p []byte) uint64 {
	a := key[0] ^ 0x736f6d6570736575
	b := key[1] ^ 0x646f72616e646f6d
	c := key[0] ^ 0x6c7967656e657261
	d := key[1] ^ 0x7465646279746573
	round := func() {
		a += b
		b = bits.RotateLeft64(b, 13) ^ a
		a = bits.RotateLeft64(a, 32)
		c += d
		d = bits.RotateLeft64(d, 16) ^ c
		a += d
		d = bits.RotateLeft64(d, 21) ^ a
		c += b
		b = bits.RotateLeft64(b, 17) ^ c
		c = bits.RotateLeft64(c, 32)
	}
	last := uint64(len(p)) << 56
	for len(p) >= 8 {
		word := binary.LittleEndian.Uint64(p)
		p = p[8:]
		d ^= word
		round()
		round()
		a ^= word
	}
	for i, v := range p {
		last |= uint64(v) << (8 * i)
	}
	d ^= last
	round()
	round()
	a ^= last
	c ^= 0xff
	for range 4 {
		round()
	}
	return a ^ b ^ c ^ d
}
