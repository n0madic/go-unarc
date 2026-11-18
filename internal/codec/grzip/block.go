package grzip

import (
	"encoding/binary"
	"fmt"

	"github.com/n0madic/go-unarc/internal/codec/parallel"
	"github.com/n0madic/go-unarc/internal/method"
)

const (
	headerSize   = 28
	maxBlockSize = 8*1024*1024 - 512 // GRZ_MaxBlockSize
	log2MaxBlock = 23                // GRZ_Log2MaxBlockSize
	// maxHashLog is the largest LZP hash table (2^24 entries, 64 MB) the decoder builds. The C decoder accepts
	// up to 2^31 entries (16 GB of pointers); no encoder setting comes close.
	// TODO: support hash sizes above 2^24 with a sparse table if a real archive ever needs them.
	maxHashLog = 24
	maxNesting = 4 // container blocks inside container blocks (the encoder never nests them)

	modeStored    = -1 // Mode of a stored block
	modeContainer = -2 // Mode of a delta/reorder container

	modeST4 = 0x2 // GRZ_Compression_ST4
	modeMTF = 0x4 // GRZ_Compression_MTF
)

// errCorrupt wraps method.ErrBadData with what is wrong.
func errCorrupt(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrBadData, fmt.Sprintf(format, args...))
}

// header is a decoded 28-byte block header.
type header struct {
	size    int   // +0: decoded size
	mode    int32 // +4
	aux     int32 // +8: unpadded size of the transformed text, container filter, or LZP parameters of a stored block
	fbp     int32 // +12
	payload int   // +16: bytes following the header
}

// parseHeader decodes and validates the header at the start of b (len(b) >= headerSize): the reserved fields
// must be zero and both sizes must be within what the format allows.
func parseHeader(b []byte) (header, error) {
	le := binary.LittleEndian
	if le.Uint32(b[20:]) != 0 || le.Uint32(b[24:]) != 0 {
		return header{}, errCorrupt("bad block signature")
	}
	h := header{
		size:    int(int32(le.Uint32(b[0:]))),
		mode:    int32(le.Uint32(b[4:])),
		aux:     int32(le.Uint32(b[8:])),
		fbp:     int32(le.Uint32(b[12:])),
		payload: int(int32(le.Uint32(b[16:]))),
	}
	if h.size < 0 || h.size > maxBlockSize || h.payload < 0 || h.payload > maxBlockSize {
		return header{}, errCorrupt("block size %d / payload %d out of range", h.size, h.payload)
	}
	return h, nil
}

// plainStored reports whether the block holds its data verbatim.
func (h *header) plainStored() bool { return h.mode == modeStored && h.aux == 0 }

// outLen is the number of bytes the block decodes to. A plain stored block yields its payload as is (the C
// decoder ignores Size there).
func (h *header) outLen() int {
	if h.plainStored() {
		return h.payload
	}
	return h.size
}

// lzpParams extracts the LZP settings packed in a Mode (or in Aux of a stored block):
// Get_LZP_MinMatchLen and log2 of the hash table size (Get_LZP_HT_Size).
func lzpParams(mode int32) (minMatch int, hashLog uint) {
	return int(mode>>16) % 32767, uint(mode>>8) & 0xFF
}

// decoder holds the working memory of one worker, reused from block to block.
type decoder struct {
	env    *method.Env
	ari    *ariDecoder
	mid    []byte   // arithmetic decoder output, then inverse BWT/ST4 in place, then LZP input
	table  []uint32 // BWT / ST4 index table
	st4    *st4Scratch
	hash   lzpTable
	planes [maxNesting][]byte
}

// run decodes the block of j; j.In holds the header followed by the payload.
func (d *decoder) run(j *parallel.Job[header]) {
	j.Out = resize(j.Out, j.Header.outLen())
	j.Err = d.block(&j.Header, j.In[headerSize:], j.Out, 0)
}

// block decodes one block (GRZip_DecompressBlock) into dst, whose length is h.outLen().
func (d *decoder) block(h *header, payload, dst []byte, depth int) error {
	switch {
	case h.mode == modeStored:
		return d.stored(h, payload, dst)
	case h.mode == modeContainer:
		return d.container(h, payload, dst, depth)
	case h.mode < 0:
		return errCorrupt("unknown block mode %d", h.mode)
	}
	return d.compressed(h, payload, dst)
}

// stored decodes a block that was not compressed by the BWT stages: raw bytes, or LZP output only.
func (d *decoder) stored(h *header, payload, dst []byte) error {
	if h.aux == 0 {
		copy(dst, payload)
		return nil
	}
	if h.aux < 0 {
		return errCorrupt("bad LZP parameters %#x", uint32(h.aux))
	}
	return d.lzp(h.aux, payload, dst)
}

// lzp expands src with the LZP parameters packed in mode; it must produce exactly len(dst) bytes.
func (d *decoder) lzp(mode int32, src, dst []byte) error {
	minMatch, hashLog := lzpParams(mode)
	if hashLog > maxHashLog {
		return errCorrupt("LZP hash size 2^%d is not supported", hashLog)
	}
	if err := d.env.CheckDecoderMemory(4 << hashLog); err != nil {
		return err
	}
	d.hash.reset(hashLog)
	return lzpDecode(dst, src, minMatch, hashLog, &d.hash)
}

// compressed decodes a block of the main kind: arithmetic decoder, inverse BWT or ST4, LZP.
func (d *decoder) compressed(h *header, payload, dst []byte) error {
	lzpSize := int(h.aux)
	if lzpSize < 0 || lzpSize > maxBlockSize {
		return errCorrupt("transformed size %d out of range", lzpSize)
	}
	n := (lzpSize + 7) &^ 7 // the encoder pads the transformed text to a multiple of 8
	if err := d.env.CheckDecoderMemory(8 * uint64(n)); err != nil {
		return err
	}
	d.mid = resize(d.mid, n)
	if d.ari == nil {
		d.ari = new(ariDecoder)
	}
	got, err := d.ari.decode(payload, d.mid, h.mode&modeMTF == 0)
	if err != nil {
		return err
	}
	if got != n {
		return errCorrupt("arithmetic decoder produced %d bytes, expected %d", got, n)
	}
	if h.mode&modeST4 != 0 {
		err = d.st4Decode(d.mid, h.fbp)
	} else {
		err = d.bwtDecode(d.mid, h.fbp)
	}
	if err != nil {
		return err
	}
	if h.mode>>8 != 0 { // LZP_Enabled
		return d.lzp(h.mode, d.mid[:lzpSize], dst)
	}
	if lzpSize != len(dst) {
		return errCorrupt("block size %d does not match transformed size %d", len(dst), lzpSize)
	}
	copy(dst, d.mid)
	return nil
}

// container decodes the sub-blocks of a delta/reorder block into a scratch buffer and merges them into dst.
func (d *decoder) container(h *header, payload, dst []byte, depth int) error {
	filter := int(h.aux)
	if filter < 1 || filter > 4 {
		return errCorrupt("unknown reorder filter %d", filter)
	}
	if depth >= maxNesting {
		return errCorrupt("container blocks nested too deeply")
	}
	parts := 4
	if filter&1 != 0 {
		parts = 2
	}
	d.planes[depth] = resize(d.planes[depth], h.size)
	buf := d.planes[depth]
	pos := 0
	for range parts {
		if len(payload) < headerSize {
			return errCorrupt("truncated container block")
		}
		sh, err := parseHeader(payload)
		if err != nil {
			return err
		}
		end := headerSize + sh.payload
		if end > len(payload) {
			return errCorrupt("sub-block exceeds its container")
		}
		n := sh.outLen()
		if n > h.size-pos {
			return errCorrupt("sub-blocks decode to more than %d bytes", h.size)
		}
		if err := d.block(&sh, payload[headerSize:end], buf[pos:pos+n], depth+1); err != nil {
			return err
		}
		pos += n
		payload = payload[end:]
	}
	if pos != h.size {
		return errCorrupt("sub-blocks decode to %d bytes, expected %d", pos, h.size)
	}
	recDecode(dst, buf, filter)
	return nil
}

// resize returns a slice of length n, reusing b's storage when it is large enough. The contents are undefined.
func resize[T any](b []T, n int) []T {
	if cap(b) >= n {
		return b[:n]
	}
	return make([]T, n)
}
