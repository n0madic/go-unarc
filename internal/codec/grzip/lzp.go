package grzip

const (
	lzpMatchFlag = 0xF2 // LZP_MatchFlag: a match follows; the byte after it holds the match length
	lzpRunFlag   = 0xF3 // LZP_RunFlag: the length continues in the next byte
	lzpXorFlag   = 0x0C // LZP_XorFlag: 0xFF ^ LZP_RunFlag
)

// lzpTable is the hash table of the LZP decoder: for each context hash, the output position that followed the
// last context with that hash. An entry holds generation<<24 | position and counts only when its generation is
// the current one, so starting a new block costs nothing however large the table is (the C decoder clears it,
// which a stream of tiny blocks with a huge table would turn into a slow-down).
type lzpTable struct {
	entries []uint32
	gen     uint32
}

const lzpPositionBits = 24 // block positions are below 2^23 (maxBlockSize)

// reset prepares the table for a new block with 1<<logSize entries.
func (t *lzpTable) reset(logSize uint) {
	n := 1 << logSize
	t.gen++
	if cap(t.entries) < n {
		t.entries, t.gen = make([]uint32, n), 1
	} else if t.gen >= 1<<(32-lzpPositionBits) { // generations used up: forget everything, in the spare capacity too
		clear(t.entries[:cap(t.entries)])
		t.gen = 1
	}
	t.entries = t.entries[:n]
}

// lzpDecode expands LZP output src into dst (GRZip_LZP_Decode); t has been reset to 1<<hashLog entries.
// The decoder predicts the next byte from a hash of the previous four: a match token copies the bytes that
// followed the last occurrence of the same context. dst must be filled exactly.
func lzpDecode(dst, src []byte, minMatch int, hashLog uint, t *lzpTable) error {
	if len(src) < 4 || len(dst) < 4 {
		return errCorrupt("LZP data too short")
	}
	copy(dst, src[:4])
	ctx := uint32(src[0])<<24 | uint32(src[1])<<16 | uint32(src[2])<<8 | uint32(src[3])
	table := t.entries
	mask := uint32(1)<<hashLog - 1
	stamp := t.gen << lzpPositionBits
	i, o := 4, 4
	for i < len(src) {
		h := (ctx>>15 ^ ctx ^ ctx>>3) & mask
		p := 0
		if e := table[h]; e&^(1<<lzpPositionBits-1) == stamp {
			p = int(e & (1<<lzpPositionBits - 1))
		}
		table[h] = stamp | uint32(o)
		c := src[i]
		i++
		if p == 0 || c != lzpMatchFlag {
			if o >= len(dst) {
				return errCorrupt("LZP output exceeds the block size")
			}
			dst[o] = c
			o++
			ctx = ctx<<8 | uint32(c)
			continue
		}
		length := 0
		for {
			if i >= len(src) {
				return errCorrupt("truncated LZP match")
			}
			b := src[i]
			i++
			length += int(b ^ lzpXorFlag)
			if b != lzpRunFlag {
				break
			}
		}
		if length == 0 { // an escaped literal match flag
			if o >= len(dst) {
				return errCorrupt("LZP output exceeds the block size")
			}
			dst[o] = lzpMatchFlag
			o++
			ctx = ctx<<8 | lzpMatchFlag
			continue
		}
		length += minMatch - 1
		if length > len(dst)-o {
			return errCorrupt("LZP output exceeds the block size")
		}
		if length > 0 {
			if p >= o {
				return errCorrupt("LZP match without source") // only with a zero minimum length
			}
			// The source may overlap the destination (a run); copying from a fixed start in growing pieces
			// reproduces the byte-by-byte copy.
			for end := o + length; o < end; {
				o += copy(dst[o:end], dst[p:o])
			}
		}
		ctx = uint32(dst[o-1]) | uint32(dst[o-2])<<8 | uint32(dst[o-3])<<16 | uint32(dst[o-4])<<24
	}
	if o != len(dst) {
		return errCorrupt("LZP produced %d bytes, expected %d", o, len(dst))
	}
	return nil
}
