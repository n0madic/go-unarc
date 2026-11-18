package tta

import (
	"errors"
	"fmt"
	"io"
	"math"
	"slices"

	"github.com/n0madic/go-unarc/internal/method"
)

func init() { method.Register(Parse) }

const (
	// decompressionMem is what C GetDecompressionMem() reports (1*mb).
	decompressionMem = 1 << 20

	// maxBlock is the largest block the C decoder accepts (frame sizes and compressed sizes).
	maxBlock = 1 << 30

	maxLevel = 3

	// copyBuffer is the size of the chunks of a stored (level 0) stream.
	copyBuffer = 1 << 20

	// readStep bounds how much memory a size announced by the stream may grab before the data behind it
	// has been seen: buffers grow in steps of this size while the bytes arrive.
	readStep = 4 << 20

	// writeBlock is the number of samples (per channel) converted to bytes and written at a time.
	writeBlock = 1 << 14
)

type codec struct{ env *method.Env }

func (codec) DecompressionMem() uint64 { return decompressionMem }

// Parse mirrors parse_TTA. The parameters only steer the compressor (level, header detection, channel layout,
// raw output); the decoder takes everything from the stream header, so they are validated and dropped.
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	if params[0] != "tta" {
		return nil, false
	}
	for _, param := range params[1:] {
		if !validParam(param) {
			return nil, false
		}
	}
	return codec{env: env}, true
}

// validParam reports whether parse_TTA accepts one parameter.
func validParam(p string) bool {
	if p == "" {
		return false
	}
	switch p[0] {
	case 's', 'f': // flags: anything may follow the letter
		return true
	case 'm', 'c', 'w', 'o', 'r':
		_, ok := method.ParseInt(p[1:])
		return ok
	}
	return validLayout(p)
}

// validLayout reports whether sscanf(p, "%d*%d") succeeds twice; whatever follows the second number is
// ignored (sscanf("%d*%d%s") only changes is_float, which does not matter for decoding).
func validLayout(p string) bool {
	i, ok := scanInt(p, 0)
	if !ok || i >= len(p) || p[i] != '*' {
		return false
	}
	_, ok = scanInt(p, i+1)
	return ok
}

// scanInt matches scanf's %d at s[i:]: optional white space, an optional sign and decimal digits.
// It returns the index after the number.
func scanInt(s string, i int) (int, bool) {
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i, i > start
}

func isSpace(c byte) bool {
	return c == ' ' || (c >= '\t' && c <= '\r')
}

// header is the 4-byte header of a stream.
type header struct {
	level    int
	isFloat  bool
	raw      bool
	numChan  int
	byteSize int // bytes per sample
	unit     int // bytes of one sample of every channel
	planes   int // channels after splitting a float into two
}

// Decode decodes a TTA stream.
//
// A stream that ends right after a complete frame is a success. The C decoder reports
// FREEARC_ERRCODE_READ there, which only goes unnoticed because unarc stops it earlier, once the last file
// is complete (ErrStop here); a consumer that wants more data finds it missing either way.
func (c codec) Decode(dst io.Writer, src io.Reader) error {
	d := decoder{dst: dst, src: src, env: c.env}
	return d.decode()
}

// decoder is the state of one Decode call; its buffers are reused from frame to frame.
type decoder struct {
	dst io.Writer
	src io.Reader
	hdr header
	env *method.Env

	scratch []byte    // headers, copied blocks, raw samples
	bitArr  []byte    // compressed frame, followed by bitArrayPad zero bytes
	samples []int32   // the planes of a frame one after the other
	planes  [][]int32 // views of samples
	out     []byte    // interleaved bytes of a block of samples
	filters filterSet
}

func badData(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrBadData, fmt.Sprintf(format, args...))
}

// readErr turns the error of a read that ended early into the error of Decode.
func readErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return badData("unexpected end of data")
	}
	return err
}

// readFull fills buf from the source, an incomplete read being a corrupt stream.
func (d *decoder) readFull(buf []byte) error {
	if _, err := io.ReadFull(d.src, buf); err != nil {
		return readErr(err)
	}
	return nil
}

// readUint32 reads a little-endian 4-byte number. eof is true when the stream ended before its first byte.
func (d *decoder) readUint32() (v uint32, eof bool, err error) {
	var b [4]byte
	n, err := io.ReadFull(d.src, b[:])
	switch {
	case err == io.EOF && n == 0:
		return 0, true, nil
	case err != nil:
		return 0, false, readErr(err)
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, false, nil
}

// readBlock reads exactly n bytes into buf[:n] (reusing its memory) and appends pad zero bytes.
// The buffer grows while the data arrives, so a bogus n costs no more memory than the stream really holds.
func (d *decoder) readBlock(buf []byte, n uint64, pad int) ([]byte, error) {
	if n+uint64(pad) > math.MaxInt {
		return nil, fmt.Errorf("%w: TTA buffer doesn't fit in an int", method.ErrLimit)
	}
	if err := d.env.CheckDecoderMemory(n + uint64(pad)); err != nil {
		return nil, err
	}
	buf = buf[:0]
	for uint64(len(buf)) < n {
		step := int(min(n-uint64(len(buf)), readStep))
		buf = slices.Grow(buf, step)
		have := len(buf)
		buf = buf[:have+step]
		if err := d.readFull(buf[have:]); err != nil {
			return buf[:have], err
		}
	}
	return append(buf, make([]byte, pad)...), nil
}

func (d *decoder) write(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	_, err := d.dst.Write(p)
	return err
}

func (d *decoder) readHeader() error {
	var b [4]byte
	if err := d.readFull(b[:]); err != nil {
		return err
	}
	h := &d.hdr
	h.level = int(b[0])
	h.isFloat = b[1]%2 != 0
	rawMode := b[1] / 2
	h.raw = rawMode != 0
	h.numChan = int(b[2])
	h.byteSize = (int(b[3]) + 7) / 8

	// Reject what the C decoder rejects: unknown level or raw mode, floats that are not 32 bits and
	// integers of 32 bits or more.
	if h.level > maxLevel || rawMode > 1 || (h.isFloat && h.byteSize != 4) || (!h.isFloat && h.byteSize >= 4) {
		return badData("unsupported stream header %x", b)
	}
	h.unit = h.numChan * h.byteSize
	h.planes = h.numChan
	if h.isFloat {
		h.planes *= 2
	}
	return nil
}

func (d *decoder) decode() error {
	if err := d.readHeader(); err != nil {
		return err
	}
	if d.hdr.level == 0 {
		return d.copyStored()
	}

	// The part of the original file in front of the audio data is stored as is.
	n, eof, err := d.readUint32()
	if err != nil {
		return err
	}
	if eof {
		return badData("unexpected end of data")
	}
	d.scratch, err = d.readBlock(d.scratch, uint64(n), 0)
	if err != nil {
		return err
	}
	if err := d.write(d.scratch); err != nil {
		return err
	}

	for {
		bytesRead, eof, err := d.readUint32()
		if err != nil {
			return err
		}
		if eof {
			return nil
		}
		if err := d.frame(uint64(bytesRead)); err != nil {
			return err
		}
	}
}

// copyStored copies the rest of the stream: level 0 means the data was stored.
func (d *decoder) copyStored() error {
	buf := make([]byte, copyBuffer)
	_, err := io.CopyBuffer(struct{ io.Writer }{d.dst}, struct{ io.Reader }{d.src}, buf)
	return err
}

// frame decodes one frame of bytesRead original bytes.
func (d *decoder) frame(bytesRead uint64) error {
	h := &d.hdr
	if bytesRead > maxBlock {
		return badData("frame of %d bytes", bytesRead)
	}
	if h.unit == 0 {
		// The C decoder divides by zero here.
		return badData("no channels or no bits per sample")
	}
	frameLen := int(bytesRead) / h.unit
	restLen := int(bytesRead) % h.unit

	if h.raw {
		if err := d.readRawSamples(frameLen); err != nil {
			return err
		}
	} else {
		compressed, eof, err := d.readUint32()
		if err != nil {
			return err
		}
		if eof {
			return badData("unexpected end of data")
		}
		if compressed > maxBlock {
			return badData("compressed frame of %d bytes", compressed)
		}
		if compressed == 0 {
			// A stored frame: the original bytes follow, rest included.
			return d.copyBlock(bytesRead)
		}
		if err := d.decodeSamples(frameLen, uint64(compressed)); err != nil {
			return err
		}
	}

	d.unfilter(frameLen)
	if frameLen > 0 {
		if err := d.writeSamples(frameLen); err != nil {
			return err
		}
	}

	// The bytes that do not fill a whole sample of every channel follow the frame as they are.
	return d.copyBlock(uint64(restLen))
}

// copyBlock copies n bytes from the stream to the output. They are read completely before the first is
// written, as the C decoder does.
func (d *decoder) copyBlock(n uint64) error {
	var err error
	d.scratch, err = d.readBlock(d.scratch, n, 0)
	if err != nil {
		return err
	}
	return d.write(d.scratch)
}

// readRawSamples reads the planes of an uncompressed frame (raw mode): frameLen int32 for each plane.
func (d *decoder) readRawSamples(frameLen int) error {
	total := uint64(frameLen) * uint64(d.hdr.planes)
	if err := d.env.CheckDecoderMemory(4 * total); err != nil {
		return err
	}
	d.samples = d.samples[:0]
	for uint64(len(d.samples)) < total {
		step := int(min(total-uint64(len(d.samples)), readStep/4))
		var err error
		d.scratch, err = d.readBlock(d.scratch, uint64(step)*4, 0)
		if err != nil {
			return err
		}
		d.samples = slices.Grow(d.samples, step)
		for i := 0; i < len(d.scratch); i += 4 {
			d.samples = append(d.samples, int32(uint32(d.scratch[i])|uint32(d.scratch[i+1])<<8|
				uint32(d.scratch[i+2])<<16|uint32(d.scratch[i+3])<<24))
		}
	}
	return nil
}

// decodeSamples reads the compressed size bytes of a frame and decodes the entropy coded planes.
func (d *decoder) decodeSamples(frameLen int, size uint64) error {
	var err error
	d.bitArr, err = d.readBlock(d.bitArr, size, bitArrayPad)
	if err != nil {
		return err
	}

	// Every sample takes at least one bit, which bounds the memory a corrupt frame length can ask for.
	total := uint64(frameLen) * uint64(d.hdr.planes)
	if total > size*8 {
		return badData("frame of %d samples in %d bytes", total, size)
	}
	if err := d.env.CheckDecoderMemory(size + 4*total); err != nil {
		return err
	}
	d.samples = slices.Grow(d.samples[:0], int(total))[:total]

	pos, endBits := uint64(0), size*8
	for p := range d.hdr.planes {
		var err error
		pos, err = decodeFrame(d.samples[p*frameLen:(p+1)*frameLen], d.bitArr, pos, endBits)
		if err != nil {
			return badData("%v", err)
		}
	}
	return nil
}

// unfilter undoes the predictor of every plane and then joins them: the channels of integer samples are
// decorrelated against each other, a float is split into two planes (exponent and mantissa).
func (d *decoder) unfilter(frameLen int) {
	h := &d.hdr
	d.planes = d.planes[:0]
	for p := range h.planes {
		plane := d.samples[p*frameLen : (p+1)*frameLen : (p+1)*frameLen]
		d.filters.decodeChannel(plane, h.level, h.byteSize)
		d.planes = append(d.planes, plane)
	}
	if h.isFloat {
		combineFloat(d.planes, h.numChan)
	} else {
		combineInt(d.planes)
	}
}

// writeSamples writes the frame as interleaved little-endian samples, byteSize bytes each.
func (d *decoder) writeSamples(frameLen int) error {
	h := &d.hdr
	size := min(frameLen, writeBlock) * h.unit
	if cap(d.out) < size {
		d.out = make([]byte, size)
	}
	for lo := 0; lo < frameLen; lo += writeBlock {
		hi := min(lo+writeBlock, frameLen)
		out := d.out[:(hi-lo)*h.unit]
		for c := range h.numChan {
			interleave(out, d.samples[c*frameLen+lo:c*frameLen+hi], c, h.numChan, h.byteSize)
		}
		if err := d.write(out); err != nil {
			return err
		}
	}
	return nil
}

// interleave stores the samples of channel c into out, where they are numChan samples apart.
func interleave(out []byte, samples []int32, c, numChan, byteSize int) {
	stride := numChan * byteSize
	o := c * byteSize
	switch byteSize {
	case 1:
		for _, v := range samples {
			out[o] = byte(v + 0x80)
			o += stride
		}
	case 2:
		for _, v := range samples {
			out[o], out[o+1] = byte(v), byte(v>>8)
			o += stride
		}
	case 3:
		for _, v := range samples {
			out[o], out[o+1], out[o+2] = byte(v), byte(v>>8), byte(v>>16)
			o += stride
		}
	default:
		for _, v := range samples {
			out[o], out[o+1], out[o+2], out[o+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
			o += stride
		}
	}
}
