package lzp

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/bits"
	"strings"

	"github.com/n0madic/go-unarc/internal/codec/frame"
	"github.com/n0madic/go-unarc/internal/method"
)

const (
	// Defaults of the method parameters (the LZP_METHOD constructor).
	defaultBlockSize      = 8 * method.MB
	defaultMinCompression = 100
	defaultMinMatchLen    = 64
	defaultHashSizeLog    = 18
	defaultSmallestLen    = 32

	// matchFlag is the byte that stands for a match when the hash table predicts one (LZP_MATCH_FLAG).
	matchFlag = 0xB5
	// literalFlag is the length record that says the flag byte was an ordinary literal.
	literalFlag = 255
	// prefixLen is the number of bytes at the start of a block that are copied verbatim.
	prefixLen = 12
	// maxHashBits is the largest table the decoder builds: C asks for 8<<bits bytes and fails for bits >= 31.
	maxHashBits = 30
	// shiftMask reduces HashSizeLog the way the machine shift of "1<<HashSizeLog" in C does.
	shiftMask = 31
	// maxN1 is the largest value of n1, the interval at which the table is updated, see decoder.block.
	maxN1 = 7
)

func init() { method.Register(Parse) }

// codec is a parsed "lzp" method.
type codec struct {
	blockSize      uint32 // b: largest size of a decoded block
	minCompression int32  // N%: encoder only
	minMatchLen    int32  // l, or a bare number: match length for short distances
	hashSizeLog    int32  // h: log2 of the number of hash table entries
	barrier        int32  // d: distance from which smallestLen is used instead of minMatchLen
	smallestLen    int32  // s
}

// Parse mirrors parse_LZP.
func Parse(params []string, _ *method.Env) (method.Codec, bool) {
	if params[0] != "lzp" {
		return nil, false
	}
	c := codec{
		blockSize:      defaultBlockSize,
		minCompression: defaultMinCompression,
		minMatchLen:    defaultMinMatchLen,
		hashSizeLog:    defaultHashSizeLog,
		barrier:        math.MaxInt32,
		smallestLen:    defaultSmallestLen,
	}
	for _, param := range params[1:] {
		if !c.set(param) {
			return nil, false
		}
	}
	return c, true
}

// set applies one parameter, reporting false when it is invalid.
func (c *codec) set(param string) bool {
	if param != "" {
		value := param[1:]
		switch param[0] {
		case 'b':
			return setMem(&c.blockSize, value)
		case 'l':
			return setInt(&c.minMatchLen, value)
		case 'h':
			return setInt(&c.hashSizeLog, value)
		case 'd':
			return setMemInt(&c.barrier, value)
		case 's':
			return setInt(&c.smallestLen, value)
		}
	}
	// "N%" is the minimal compression percentage, a bare number is the minimal match length,
	// anything else must be the block size.
	if percent, ok := strings.CutSuffix(param, "%"); ok && setInt(&c.minCompression, percent) {
		return true
	}
	if setInt(&c.minMatchLen, param) {
		return true
	}
	return setMem(&c.blockSize, param)
}

// setInt, setMem and setMemInt store a parsed number only if the text was valid, so that a failed
// attempt (the parameter may be tried as something else) leaves the field alone.
func setInt(dst *int32, s string) bool {
	n, ok := method.ParseInt(s)
	if ok {
		*dst = int32(n)
	}
	return ok
}

func setMem(dst *uint32, s string) bool {
	n, ok := method.ParseMem(s)
	if ok {
		*dst = n
	}
	return ok
}

func setMemInt(dst *int32, s string) bool {
	n, ok := method.ParseMem(s)
	if ok {
		*dst = int32(n)
	}
	return ok
}

// hashBits is log2 of the hash table size as the C code computes it: "1<<HashSizeLog" in int arithmetic.
func (c codec) hashBits() uint32 { return uint32(c.hashSizeLog) & shiftMask }

// DecompressionMem is what GetDecompressionMem returns: input and output buffers of BlockSize each (the C
// decoder allocates both) plus the hash table of 8-byte pointers, all in 32-bit arithmetic.
func (c codec) DecompressionMem() uint64 {
	table := int64(int32(1) << c.hashBits()) // negative for 2^31, as in C
	return uint64(c.blockSize*2 + uint32(table*8))
}

// Dictionary mirrors LZP_METHOD::GetDictionary: the block size.
func (c codec) Dictionary() uint64 { return uint64(c.blockSize) }

// Decode decodes the blocks one after another.
func (c codec) Decode(dst io.Writer, src io.Reader) error {
	d := &decoder{codec: c}
	for {
		size, err := frame.ReadInt32(src)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if size < 0 {
			// A stored block. Like the C code, read all of it before writing any.
			if size == math.MinInt32 {
				return badData("stored block of 2 GB")
			}
			if d.in, err = frame.ReadN(d.in, src, int(-size)); err != nil {
				return err
			}
			if _, err := dst.Write(d.in); err != nil {
				return err
			}
			continue
		}
		if d.in, err = frame.ReadN(d.in, src, int(size)); err != nil {
			return err
		}
		out, err := d.block(d.in)
		if err != nil {
			return err
		}
		if _, err := dst.Write(out); err != nil {
			return err
		}
	}
}

// decoder is the state of one Decode call; the buffers are reused from block to block.
type decoder struct {
	codec
	in    []byte   // compressed block
	out   []byte   // decoded block; grows up to blockSize as needed
	table []uint32 // hash table: position-5 of the latest occurrence of a context, 0 meaning position 5
}

// grow makes d.out at least need bytes long, keeping its contents; need beyond the block size is an error.
func (d *decoder) grow(need int) error {
	if uint64(need) > uint64(d.blockSize) {
		return badData("block decodes to more than %d bytes", d.blockSize)
	}
	if need <= len(d.out) {
		return nil
	}
	out := make([]byte, min(int(d.blockSize), max(need, 2*len(d.out), frame.MaxPrealloc)))
	copy(out, d.out)
	d.out = out
	return nil
}

// context returns the hash table slot for the position pos: it depends on the 4 bytes before pos and on the
// 4 bytes before those, minus the last one (the C code's lzpH with lzpC(p-1)).
func context(out []byte, pos int, mask uint32) uint32 {
	v := binary.LittleEndian.Uint64(out[pos-8:])
	last4 := uint32(v >> 32)
	previous4 := uint32(v >> 24)
	return (last4 + 5*bits.RotateLeft32(last4, -17) + 3*previous4) & mask
}

// last4 returns the 4 bytes before pos as a number.
func last4(out []byte, pos int) uint32 { return binary.LittleEndian.Uint32(out[pos-4:]) }

// block decodes one compressed block and returns its data, valid until the next call.
func (d *decoder) block(in []byte) ([]byte, error) {
	if len(in) <= prefixLen {
		return nil, badData("compressed block of %d bytes", len(in))
	}
	bitsN := d.hashBits()
	if bitsN > maxHashBits {
		return nil, fmt.Errorf("lzp: hash table of 2^%d entries is too large", bitsN)
	}
	if d.table == nil {
		d.table = make([]uint32, 1<<bitsN)
	} else {
		clear(d.table)
	}
	if err := d.grow(prefixLen); err != nil {
		return nil, err
	}
	out := d.out
	copy(out, in[:prefixLen])
	pos := prefixLen
	inPos, inEnd := prefixLen, len(in)

	table := d.table
	hashSize := uint32(len(table))
	mask := hashSize - 1
	n, n1 := uint32(1), uint32(1)
	ctx := last4(out, pos)
	slot := context(out, pos, mask)
	for {
		p := int(table[slot]) + 5 // where this context was seen last
		if n--; n == 0 {
			table[slot] = uint32(pos - 5)
			n = n1
		}
		b := in[inPos]
		inPos++
		if b == matchFlag && ctx == last4(out, p) {
			// The flag stands for a match unless its record at the end of the block says it is a literal.
			if inEnd--; inEnd < inPos {
				return nil, badData("match record overlaps the data")
			}
			if in[inEnd] != literalFlag {
				table[slot] = uint32(pos - 5)
				dist := int64(pos - p)
				if dist > int64((n1+1)*hashSize) && n1 < maxN1 {
					n1++
				}
				length := uint32(d.minMatchLen)
				if dist > int64(d.barrier) {
					length = uint32(d.smallestLen)
				}
				length-- // the record counts the bytes beyond the minimum, plus one
				for in[inEnd] == 0 {
					length += 254
					if inEnd--; inEnd < inPos {
						return nil, badData("match record overlaps the data")
					}
				}
				length += uint32(in[inEnd])

				// A length of 0 means 2^32 in the C loop, which can never fit.
				total := uint64(length)
				if length == 0 {
					total = 1 << 32
				}
				if total > uint64(d.blockSize)-uint64(pos) { // pos <= blockSize always holds
					return nil, badData("match of %d bytes at %d exceeds the block size", total, pos)
				}
				if pos+int(total) > len(out) {
					if err := d.grow(pos + int(total)); err != nil {
						return nil, err
					}
					out = d.out
				}
				pos = copyMatch(out, table, pos, p, int(total), int(2*n1+1), mask)
				ctx = last4(out, pos)
				slot = context(out, pos, mask)
				if inPos >= inEnd {
					break
				}
				continue
			}
		}
		if pos == len(out) {
			if err := d.grow(pos + 1); err != nil {
				return nil, err
			}
			out = d.out
		}
		out[pos] = b
		pos++
		ctx = last4(out, pos)
		slot = context(out, pos, mask)
		if inPos >= inEnd {
			break
		}
	}
	return out[:pos], nil
}

// copyMatch copies total bytes from out[src:] to out[pos:] (the ranges may overlap, as in an LZ77 copy) and
// returns the new position. Every step bytes of the copy, the hash table gets the position reached.
func copyMatch(out []byte, table []uint32, pos, src, total, step int, mask uint32) int {
	for total > 0 {
		chunk := min(total, step)
		if pos-src >= chunk {
			copy(out[pos:pos+chunk], out[src:src+chunk])
		} else {
			for i := range chunk {
				out[pos+i] = out[src+i]
			}
		}
		pos += chunk
		src += chunk
		total -= chunk
		if total > 0 {
			table[context(out, pos, mask)] = uint32(pos - 5)
		}
	}
	return pos
}

func badData(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrBadData, fmt.Sprintf(format, args...))
}
