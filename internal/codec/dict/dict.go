package dict

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/n0madic/go-unarc/internal/codec/frame"
	"github.com/n0madic/go-unarc/internal/method"
)

const (
	// Defaults of the method parameters (the DICT_METHOD constructor).
	defaultBlockSize      = 64 * method.MB
	defaultMinCompression = 100
	defaultMinWeakChars   = 20
	defaultMinLargeCnt    = 2048
	defaultMinMediumCnt   = 100
	defaultMinSmallCnt    = 50
	defaultMinRatio       = 4

	// decompressionMem is what the C method reports as its decompression memory (1*mb).
	decompressionMem = 1 * method.MB
	// useDict2 is the word length that marks a byte starting two-byte codes (USE_DICT2).
	useDict2 = 1
)

func init() { method.Register(Parse) }

// codec is a parsed "dict" method. Of its parameters only the block size matters when decoding: it bounds
// the size of a decoded block. The others steer the encoder.
type codec struct {
	blockSize      uint32 // b
	minCompression int32  // N%
	minWeakChars   int32  // c, or a bare number
	minLargeCnt    int32  // l
	minMediumCnt   int32  // m
	minSmallCnt    int32  // s
	minRatio       int32  // r
	env            *method.Env
}

// Parse mirrors parse_DICT.
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	if params[0] != "dict" {
		return nil, false
	}
	c := codec{
		blockSize:      defaultBlockSize,
		minCompression: defaultMinCompression,
		minWeakChars:   defaultMinWeakChars,
		minLargeCnt:    defaultMinLargeCnt,
		minMediumCnt:   defaultMinMediumCnt,
		minSmallCnt:    defaultMinSmallCnt,
		minRatio:       defaultMinRatio,
		env:            env,
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
	// The C code tests the one-letter presets only for one-character parameters, and the parameters with a
	// value only for the others; a lone "b", "c", ... falls through to the generic cases below.
	switch {
	case len(param) == 1:
		switch param[0] {
		case 'p':
			c.minLargeCnt, c.minMediumCnt, c.minSmallCnt, c.minRatio = 8192, 400, 100, 4
			return true
		case 'f':
			c.minLargeCnt, c.minMediumCnt, c.minSmallCnt, c.minRatio = 2048, 100, 50, 0
			return true
		}
	case len(param) > 1:
		value := param[1:]
		switch param[0] {
		case 'b':
			return setMem(&c.blockSize, value)
		case 'c':
			return setInt(&c.minWeakChars, value)
		case 'l':
			return setInt(&c.minLargeCnt, value)
		case 'm':
			return setInt(&c.minMediumCnt, value)
		case 's':
			return setInt(&c.minSmallCnt, value)
		case 'r':
			return setInt(&c.minRatio, value)
		}
	}
	// "N%" is the minimal compression percentage, a bare number is the minimal number of weak characters,
	// anything else must be the block size.
	if percent, ok := strings.CutSuffix(param, "%"); ok && setInt(&c.minCompression, percent) {
		return true
	}
	if setInt(&c.minWeakChars, param) {
		return true
	}
	return setMem(&c.blockSize, param)
}

// setInt and setMem store a parsed number only if the text was valid, so that a failed attempt (the
// parameter may be tried as something else) leaves the field alone.
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

// DecompressionMem is 1 MB: that is what the C method reports, whatever the block size.
func (codec) DecompressionMem() uint64 { return decompressionMem }

// Dictionary mirrors DICT_METHOD::GetDictionary: the block size.
func (c codec) Dictionary() uint64 { return uint64(c.blockSize) }

// Decode decodes the blocks one after another.
func (c codec) Decode(dst io.Writer, src io.Reader) error {
	d := &decoder{limit: int(min(uint64(c.blockSize), c.env.DecoderLimit(), math.MaxInt))}
	for {
		size, err := frame.ReadInt32(src)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var out []byte
		if size < 0 {
			// A stored block. Like the C code, read all of it before writing any.
			if size == math.MinInt32 {
				return badData("stored block of 2 GB")
			}
			if err := c.env.CheckDecoderMemory(uint64(-size)); err != nil {
				return err
			}
			if d.in, err = frame.ReadN(d.in, src, int(-size)); err != nil {
				return err
			}
			out = d.in
		} else {
			if err := c.env.CheckDecoderMemory(uint64(size)); err != nil {
				return err
			}
			if d.in, err = frame.ReadN(d.in, src, int(size)); err != nil {
				return err
			}
			if out, err = d.block(d.in); err != nil {
				return err
			}
		}
		if _, err := dst.Write(out); err != nil {
			return err
		}
	}
}

// entry describes a word of the dictionary: the text is words[off:off+n]. In the first-level table n is
// also the marker of a byte that stands for itself (0) or starts two-byte codes (useDict2).
type entry struct{ off, n uint32 }

// decoder is the state of one Decode call; the buffers are reused from block to block.
type decoder struct {
	limit int // largest decoded block
	in    []byte
	out   []byte
	words []byte     // text of all words of the current block
	one   [256]entry // words coded by one byte, or the marker of the two-byte codes
	two   []entry    // [first<<8|second]: words coded by two bytes, allocated at the first block
}

// block decodes one compressed block and returns its data, valid until the next call.
func (d *decoder) block(in []byte) ([]byte, error) {
	if d.two == nil {
		d.two = make([]entry, 256*256)
	}
	r := reader{b: in}
	if err := d.readDictionary(&r); err != nil {
		return nil, err
	}
	return d.decodeText(in[r.i:])
}

// reader is a cursor over a block with checked reads.
type reader struct {
	b []byte
	i int
}

func (r *reader) byte() (byte, error) {
	if r.i >= len(r.b) {
		return 0, badData("dictionary runs past the end of the block")
	}
	c := r.b[r.i]
	r.i++
	return c, nil
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n > len(r.b)-r.i {
		return nil, badData("dictionary runs past the end of the block")
	}
	p := r.b[r.i : r.i+n]
	r.i += n
	return p, nil
}

// readDictionary reads the five parts of the dictionary (see the package comment).
func (d *decoder) readDictionary(r *reader) error {
	lens, err := r.bytes(256)
	if err != nil {
		return err
	}
	for i, n := range lens {
		d.one[i] = entry{n: uint32(n)}
	}
	for i := range d.one {
		if d.one[i].n != useDict2 {
			continue
		}
		prefixes, err := r.bytes(256)
		if err != nil {
			return err
		}
		for j, n := range prefixes {
			d.two[i<<8|j] = entry{n: uint32(n)} // for now: how many bytes to take from the previous word
		}
	}
	d.words = d.words[:0]
	for i := range d.one {
		if d.one[i].n == useDict2 {
			continue
		}
		text, err := r.bytes(int(d.one[i].n))
		if err != nil {
			return err
		}
		d.one[i].off = uint32(len(d.words))
		d.words = append(d.words, text...)
	}

	sep, err := r.byte()
	if err != nil {
		return err
	}
	prev := -1 // start of the previous two-byte word
	for i := range d.one {
		if d.one[i].n != useDict2 {
			continue
		}
		for j := range 256 {
			e := &d.two[i<<8|j]
			start := len(d.words)
			// The word starts with as many bytes of the previous word as the dictionary says. A count beyond
			// the previous word's length reads on into the bytes just copied, like the C code does.
			for k := range int(e.n) {
				if prev < 0 || prev+k >= len(d.words) {
					return badData("word %d,%d copies from a word that is not there", i, j)
				}
				d.words = append(d.words, d.words[prev+k])
			}
			rest := r.b[r.i:]
			end := bytes.IndexByte(rest, sep)
			if end < 0 {
				return badData("dictionary runs past the end of the block")
			}
			d.words = append(d.words, rest[:end]...)
			r.i += end + 1
			*e = entry{off: uint32(start), n: uint32(len(d.words) - start)}
			prev = start
		}
	}

	// The escape byte codes every byte value, for the bytes that gave their codes to words.
	escape, err := r.byte()
	if err != nil {
		return err
	}
	d.one[escape] = entry{n: useDict2}
	for j := range 256 {
		d.two[int(escape)<<8|j] = entry{off: uint32(len(d.words)), n: 1}
		d.words = append(d.words, byte(j))
	}
	return nil
}

// decodeText replaces the codes of the text by their words.
func (d *decoder) decodeText(text []byte) ([]byte, error) {
	out := d.out[:0]
	for i := 0; i < len(text); {
		c := text[i]
		i++
		e := d.one[c]
		switch e.n {
		case 0:
			if len(out) >= d.limit {
				return nil, errTooBig(d.limit)
			}
			out = append(out, c)
			continue
		case useDict2:
			if i >= len(text) {
				return nil, badData("a two-byte code is cut off at the end of the block")
			}
			e = d.two[int(c)<<8|int(text[i])]
			i++
		}
		if int(e.n) > d.limit-len(out) {
			return nil, errTooBig(d.limit)
		}
		out = append(out, d.words[e.off:e.off+e.n]...)
	}
	d.out = out
	return out, nil
}

func errTooBig(limit int) error {
	return badData("block decodes to more than %d bytes", limit)
}

func badData(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrBadData, fmt.Sprintf(format, args...))
}
