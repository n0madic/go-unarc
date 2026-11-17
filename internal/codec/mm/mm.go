package mm

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/n0madic/go-unarc/internal/codec/frame"
	"github.com/n0madic/go-unarc/internal/method"
)

const (
	// defaultMode is the detection speed mode, an encoder parameter ("d").
	defaultMode = 9
	// decompressionMem is what the C method reports as its decompression memory (1*mb).
	decompressionMem = 1 * method.MB
	// chunkSize is the size of the pieces the C decoder processes (BUFFER_SIZE).
	chunkSize = 64 * method.KB
	// fixedHeaderSize is the size of the flags, channel count, word size and offset fields.
	fixedHeaderSize = 3 + 4
	// floatSuffix marks floating point samples in "<channels>*<bits>f".
	floatSuffix = "f"
)

func init() { method.Register(Parse) }

// codec is a parsed "mm" method. The decoder needs none of the parameters, they steer the encoder, but
// Parse checks them the way the C parser does.
type codec struct {
	mode       int32 // d: detection speed
	skipHeader bool  // s: no WAV header detection
	isFloat    bool  // f, or "c*wf"
	numChan    int32 // c, or "c*w": number of channels
	wordSize   int32 // w, or "c*w": bits per sample
	offset     int32 // o: size of the file header
	reorder    int32 // r
}

// Parse mirrors parse_MM: the parameters s, f, d<n>, c<n>, w<n>, o<n>, r<n> and "<channels>*<bits>[f]".
func Parse(params []string, _ *method.Env) (method.Codec, bool) {
	if params[0] != "mm" {
		return nil, false
	}
	c := codec{mode: defaultMode}
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
		case 's': // like the C code, ignores whatever follows the letter
			c.skipHeader = true
			return true
		case 'f':
			c.isFloat = true
			return true
		case 'd':
			return setInt(&c.mode, value)
		case 'c':
			return setInt(&c.numChan, value)
		case 'w':
			return setInt(&c.wordSize, value)
		case 'o':
			return setInt(&c.offset, value)
		case 'r':
			return setInt(&c.reorder, value)
		}
	}
	channels, bitsPerWord, float, ok := scanChannels(param)
	if ok {
		c.numChan, c.wordSize, c.isFloat = channels, bitsPerWord, float
	}
	return ok
}

func setInt(dst *int32, s string) bool {
	n, ok := method.ParseInt(s)
	if ok {
		*dst = int32(n)
	}
	return ok
}

// scanChannels does what the C parser does with sscanf: "%d*%d%s" with the string being "f" (float data),
// or, failing that, "%d*%d" (anything may follow the second number).
func scanChannels(s string) (channels, bitsPerWord int32, float, ok bool) {
	rest, channels, ok := scanInt(s)
	if !ok || !strings.HasPrefix(rest, "*") {
		return 0, 0, false, false
	}
	rest, bitsPerWord, ok = scanInt(rest[1:])
	if !ok {
		return 0, 0, false, false
	}
	return channels, bitsPerWord, firstToken(rest) == floatSuffix, true
}

// firstToken returns what scanf's %s reads: the first run of non-white-space characters.
func firstToken(s string) string {
	s = strings.TrimLeft(s, cSpace)
	if end := strings.IndexAny(s, cSpace); end >= 0 {
		return s[:end]
	}
	return s
}

// cSpace is what isspace accepts in the C locale.
const cSpace = " \t\n\v\f\r"

// scanInt reads what scanf's %d accepts: leading white space, an optional sign and decimal digits.
// The value is the one of strtol, cut to 32 bits.
func scanInt(s string) (rest string, n int32, ok bool) {
	s = strings.TrimLeft(s, cSpace)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	i := 0
	var v int64
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		if v <= (math.MaxInt64-9)/10 {
			v = v*10 + int64(s[i]-'0')
		} else {
			v = math.MaxInt64 // strtol saturates
		}
	}
	if i == 0 {
		return s, 0, false
	}
	if neg {
		v = -v
	}
	return s[i:], int32(v), true
}

// DecompressionMem is 1 MB: that is what the C method reports.
func (codec) DecompressionMem() uint64 { return decompressionMem }

// Decode reverses the difference filter.
func (codec) Decode(dst io.Writer, src io.Reader) error {
	var hdr [3]byte
	if _, err := io.ReadFull(src, hdr[:1]); err != nil {
		return frame.Truncated(err)
	}
	if hdr[0] == 0 {
		return copyStored(dst, src)
	}
	// Bits 1 and 2 would ask for the bytes or words to be put back in order, which the C decoder cannot do.
	if hdr[0]&^1 != 0 {
		return badData("unsupported flags %#x", hdr[0])
	}
	if _, err := io.ReadFull(src, hdr[1:]); err != nil {
		return frame.Truncated(err)
	}
	var b [4]byte
	if _, err := io.ReadFull(src, b[:]); err != nil {
		return frame.Truncated(err)
	}
	offset := int32(binary.LittleEndian.Uint32(b[:]))
	if offset < 0 {
		return badData("header of negative size")
	}
	header, err := frame.ReadN(nil, src, int(offset))
	if err != nil {
		return err
	}
	if len(header) > 0 {
		if _, err := dst.Write(header); err != nil {
			return err
		}
	}

	numChan, byteSize := int(hdr[1]), (int(hdr[2])+7)/8
	n := numChan * byteSize // bytes per sample, all channels
	if n == 0 {
		// The C decoder divides by this number; the encoder writes such data as stored.
		return badData("%d channels of %d bits", numChan, hdr[2])
	}
	f := filter{numChan: numChan, byteSize: byteSize}

	// The encoder pads the header to a multiple of the sample size with zero bytes. The C decoder reads these
	// into the table of previous samples, so they are the initial values.
	stride := byteSize
	if byteSize == 3 {
		stride = 4
	}
	state := make([]byte, numChan*stride)
	total := fixedHeaderSize + int64(offset)
	padding := (total+int64(n)-1)/int64(n)*int64(n) - total
	if _, err := io.ReadFull(src, state[:padding]); err != nil {
		return frame.Truncated(err)
	}
	f.init(state)

	chunk := chunkSize / n * n
	buf := make([]byte, chunk)
	for {
		got, err := io.ReadFull(src, buf)
		if got == 0 && err == io.EOF {
			return nil
		}
		if err != nil && err != io.ErrUnexpectedEOF {
			return err
		}
		f.undiff(buf[:got])
		if _, err := dst.Write(buf[:got]); err != nil {
			return err
		}
		if err != nil { // the last, short chunk
			return nil
		}
	}
}

// copyStored copies the rest of the stream.
func copyStored(dst io.Writer, src io.Reader) error {
	buf := make([]byte, chunkSize)
	// Hide WriterTo and ReaderFrom so that buf is used.
	_, err := io.CopyBuffer(struct{ io.Writer }{dst}, struct{ io.Reader }{src}, buf)
	return err
}

// filter holds the previous sample of every channel.
type filter struct {
	numChan, byteSize int
	prev8             []byte
	prev16            []uint16
	prev32            []uint32 // 24-bit and 32-bit samples
}

// init takes the initial values of the previous samples from the bytes the stream put there.
func (f *filter) init(state []byte) {
	switch f.byteSize {
	case 1:
		f.prev8 = state
	case 2:
		f.prev16 = make([]uint16, f.numChan)
		for i := range f.prev16 {
			f.prev16[i] = binary.LittleEndian.Uint16(state[2*i:])
		}
	case 3, 4:
		f.prev32 = make([]uint32, f.numChan)
		for i := range f.prev32 {
			f.prev32[i] = binary.LittleEndian.Uint32(state[4*i:])
		}
	}
}

// undiff adds the previous sample of the channel to every whole sample of buf. Sizes that the encoder never
// filters (more than 4 bytes) pass through.
func (f *filter) undiff(buf []byte) {
	switch f.byteSize {
	case 1:
		undiff1(buf, f.numChan, f.prev8)
	case 2:
		undiff2(buf, f.numChan, f.prev16)
	case 3:
		undiff3(buf, f.numChan, f.prev32)
	case 4:
		undiff4(buf, f.numChan, f.prev32)
	}
}

// The undiff functions go through the data one channel at a time: the running sum of a channel stays in a
// register instead of the table of previous samples, and every pass touches data that is hot in cache,
// because the chunks are small. Only whole samples are processed; the previous value of each channel is left
// in prev for the next chunk.

func undiff1(buf []byte, nch int, prev []byte) {
	end := len(buf) / nch * nch
	for c := range nch {
		acc := prev[c]
		for p := c; p < end; p += nch {
			acc += buf[p]
			buf[p] = acc
		}
		prev[c] = acc
	}
}

func undiff2(buf []byte, nch int, prev []uint16) {
	stride := 2 * nch
	end := len(buf) / stride * stride
	for c := range nch {
		acc := prev[c]
		for p := 2 * c; p < end; p += stride {
			acc += binary.LittleEndian.Uint16(buf[p:])
			binary.LittleEndian.PutUint16(buf[p:], acc)
		}
		prev[c] = acc
	}
}

// undiff3 works on 24-bit samples; prev keeps 32 bits per channel like the C table, only 24 bits are stored.
func undiff3(buf []byte, nch int, prev []uint32) {
	stride := 3 * nch
	end := len(buf) / stride * stride
	for c := range nch {
		acc := prev[c]
		for p := 3 * c; p < end; p += stride {
			s := buf[p : p+3 : p+3]
			acc += uint32(s[0]) | uint32(s[1])<<8 | uint32(s[2])<<16
			s[0], s[1], s[2] = byte(acc), byte(acc>>8), byte(acc>>16)
		}
		prev[c] = acc
	}
}

func undiff4(buf []byte, nch int, prev []uint32) {
	stride := 4 * nch
	end := len(buf) / stride * stride
	for c := range nch {
		acc := prev[c]
		for p := 4 * c; p < end; p += stride {
			acc += binary.LittleEndian.Uint32(buf[p:])
			binary.LittleEndian.PutUint32(buf[p:], acc)
		}
		prev[c] = acc
	}
}

func badData(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrBadData, fmt.Sprintf(format, args...))
}
