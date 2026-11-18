// Package srep decodes the SREP 3.93a stream formats (I/O LZ, Future-LZ and Index-LZ).
package srep

import (
	"bufio"
	"container/heap"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/n0madic/go-unarc/internal/codec/frame"
	"github.com/n0madic/go-unarc/internal/filestage"
	"github.com/n0madic/go-unarc/internal/method"
)

func init() { method.Register(Parse) }

const (
	magic        = "\x17\x18\x35\x26SREP"
	footerMagic  = "\xac\xad\xba\xaf\xe8\xe7\xca\xd9"
	headerSize   = 16
	footerSize   = 24
	maxBlockSize = 8 * method.MB // default maximum of srep -d
	maxStatSize  = 2 * maxBlockSize
	maxPending   = 1 << 20 // bound memory used by live Future/Index-LZ references
	ioBufferSize = 64 * method.KB
)

type codec struct{ env *method.Env }

// Parse leaves an explicit UNARC_SREP override to the external plugin. The options in a method
// such as srep:m3f are encoder options; the on-disk header identifies the format and checksum.
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	if params[0] != "srep" || env.Srep != "" {
		return nil, false
	}
	return codec{env}, true
}

func (codec) DecompressionMem() uint64 { return 64 * method.MB }

func badData(what string) error { return fmt.Errorf("%w: %s", method.ErrBadData, what) }

var errEnd = errors.New("end of SREP stream")

type decoder struct {
	env     *method.Env
	version byte
	base    uint64 // base match length, also the unit of distance/length in v1
	check   blockHash
	pos     uint64
	stats   []byte
	out     []byte
	pending matchHeap
	history *os.File
	temps   *filestage.Dir
}

func (c codec) Decode(dst io.Writer, src io.Reader) error {
	var header [headerSize]byte
	if _, err := io.ReadFull(src, header[:]); err != nil {
		return frame.Truncated(err)
	}
	if string(header[:8]) != magic {
		return badData("not an SREP stream")
	}
	d := decoder{env: c.env, version: header[8], base: uint64(binary.LittleEndian.Uint32(header[12:]))}
	if d.version < 1 || d.version > 4 || (d.version == 1 && d.base == 0) {
		return badData("invalid or unsupported SREP format")
	}
	// Tag length is encoded modulo 256, biased by 16 (SipHash encodes 8 as 248).
	var err error
	d.check, err = readHash(src, header[9], int(header[10]), int(header[11]+byte(16)))
	if err != nil {
		return err
	}
	defer func() {
		if d.history != nil {
			d.history.Close()
		}
		if d.temps != nil {
			d.temps.Remove()
		}
	}()
	if d.version == 4 {
		return d.decodeIndexed(dst, src)
	}
	in := bufio.NewReaderSize(src, ioBufferSize)
	for {
		err := d.block(dst, in, in, -1)
		if err == errEnd {
			return d.finish()
		}
		if err != nil {
			return err
		}
	}
}

func (d *decoder) finish() error {
	if len(d.pending) != 0 {
		return badData("match destination past end of stream")
	}
	return nil
}

func (d *decoder) temp(name string) (*os.File, error) {
	if d.temps == nil {
		dir, err := filestage.NewDir(d.env.Temp(), "unarc-srep.*")
		if err != nil {
			return nil, err
		}
		d.temps = dir
	}
	return os.OpenFile(filepath.Join(d.temps.Path, name), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

// block reads one block. With an inline match list (indexSize == -1) the two readers are the
// same and a clean end of stream is allowed. An indexed block must consume exactly its table entry.
func (d *decoder) block(dst io.Writer, data, index io.Reader, indexSize int64) error {
	if ctx := d.env.Ctx; ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	var header [12 + maxTagSize]byte
	if _, err := io.ReadFull(data, header[:8]); err != nil {
		if err == io.EOF && indexSize < 0 {
			return errEnd
		}
		return frame.Truncated(err)
	}
	literals := binary.LittleEndian.Uint32(header[:])
	size := binary.LittleEndian.Uint32(header[4:])
	if size == 0 && literals == 0 && indexSize < 0 { // optional end marker
		return errEnd
	}
	if size == 0 || size > maxBlockSize || literals > size || d.pos > math.MaxInt64-uint64(size) {
		return badData("invalid block size")
	}
	if _, err := io.ReadFull(data, header[8:12+d.check.tagLen]); err != nil {
		return frame.Truncated(err)
	}
	statSize := int64(binary.LittleEndian.Uint32(header[8:]))
	if indexSize >= 0 {
		if statSize != 0 {
			return badData("indexed block has an inline match list")
		}
		statSize = indexSize
	}
	recordSize := int64(16)
	if d.version == 1 {
		recordSize = 12
	}
	if statSize > maxStatSize || statSize%recordSize != 0 || (d.version <= 2 && statSize/recordSize > int64(size-literals)) {
		return badData("invalid match list size")
	}
	var err error
	d.stats, err = frame.ReadN(d.stats, index, int(statSize))
	if err != nil {
		return err
	}
	if cap(d.out) < int(size) {
		d.out = make([]byte, size)
	}
	d.out = d.out[:size]
	lit := &io.LimitedReader{R: data, N: int64(literals)}
	save := d.version <= 2
	if save {
		err = d.backward(lit)
	} else {
		save, err = d.forward(lit)
	}
	if err != nil {
		return err
	}
	if !d.check.matches(d.out, header[12:12+d.check.tagLen]) {
		return badData("block checksum mismatch")
	}
	if n, err := dst.Write(d.out); err != nil {
		return err
	} else if n != len(d.out) {
		return io.ErrShortWrite
	}
	if save {
		if d.history == nil {
			d.history, err = d.temp("history")
			if err != nil {
				return err
			}
		}
		if _, err := d.history.WriteAt(d.out, int64(d.pos)); err != nil {
			return err
		}
	}
	d.pos += uint64(size)
	return nil
}

// The v4 index sits between the data blocks and the footer, followed by one match-list size per
// block. Three section readers let us walk all of these in order without loading the index in RAM.
func (d *decoder) decodeIndexed(dst io.Writer, src io.Reader) error {
	var body *io.SectionReader
	if seek, ok := src.(interface {
		io.ReaderAt
		io.Seeker
	}); ok {
		if start, err := seek.Seek(0, io.SeekCurrent); err == nil {
			end, err := seek.Seek(0, io.SeekEnd)
			if err != nil {
				return err
			}
			if start < 0 || end < start {
				return badData("invalid input size")
			}
			body = io.NewSectionReader(seek, start, end-start)
		}
	}
	if body == nil {
		f, err := d.temp("packed")
		if err != nil {
			return err
		}
		defer f.Close()
		n, err := io.CopyBuffer(f, src, make([]byte, ioBufferSize))
		if err != nil {
			return err
		}
		body = io.NewSectionReader(f, 0, n)
	}
	var end [footerSize]byte
	if body.Size() < footerSize {
		return frame.ErrTruncated
	}
	if _, err := body.ReadAt(end[:], body.Size()-footerSize); err != nil {
		return frame.Truncated(err)
	}
	if string(end[16:]) != footerMagic || end[12] != 1 {
		return badData("invalid index footer")
	}
	footer := int64(binary.LittleEndian.Uint32(end[8:]))
	statSize := binary.LittleEndian.Uint64(end[:])
	if footer < footerSize || (footer-footerSize)%4 != 0 || footer > body.Size() ||
		statSize > uint64(body.Size()-footer) || statSize%16 != 0 {
		return badData("invalid index size")
	}
	countsAt := body.Size() - footer
	dataEnd := countsAt - int64(statSize)
	data := bufio.NewReaderSize(io.NewSectionReader(body, 0, dataEnd), ioBufferSize)
	index := io.NewSectionReader(body, dataEnd, int64(statSize))
	counts := bufio.NewReaderSize(io.NewSectionReader(body, countsAt, footer-footerSize), ioBufferSize)
	var count [4]byte
	for i := (footer - footerSize) / 4; i > 0; i-- {
		if _, err := io.ReadFull(counts, count[:]); err != nil {
			return frame.Truncated(err)
		}
		n := uint64(binary.LittleEndian.Uint32(count[:]))
		if n > statSize {
			return badData("block match list exceeds index")
		}
		if err := d.block(dst, data, index, int64(n)); err != nil {
			return err
		}
		statSize -= n
	}
	if _, err := data.ReadByte(); err == nil || statSize != 0 {
		return badData("unused bytes in indexed stream")
	} else if err != io.EOF {
		return err
	}
	return d.finish()
}

type match struct {
	source, dest uint64
	length       int
}

type matchHeap []match

func (q matchHeap) Len() int           { return len(q) }
func (q matchHeap) Less(i, j int) bool { return q[i].dest < q[j].dest }
func (q matchHeap) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *matchHeap) Push(x any)        { *q = append(*q, x.(match)) }
func (q *matchHeap) Pop() any {
	x := (*q)[len(*q)-1]
	*q = (*q)[:len(*q)-1]
	return x
}

func (d *decoder) backward(lit *io.LimitedReader) error {
	recordSize := 16
	if d.version == 1 {
		recordSize = 12
	}
	at := 0
	for stat := d.stats; len(stat) != 0; stat = stat[recordSize:] {
		gap := uint64(binary.LittleEndian.Uint32(stat))
		if gap > uint64(len(d.out)-at) {
			return badData("literal run exceeds block")
		}
		dest := d.pos + uint64(at) + gap
		length := uint64(binary.LittleEndian.Uint32(stat[recordSize-4:]))
		var distance uint64
		if recordSize == 12 {
			distance = uint64(binary.LittleEndian.Uint32(stat[4:]))*d.base + dest%d.base
			length *= d.base
		} else {
			distance = binary.LittleEndian.Uint64(stat[4:])
		}
		length += d.base
		if distance == 0 || distance > dest || length == 0 || length > maxBlockSize {
			return badData("invalid backward match")
		}
		var err error
		at, err = d.emitMatch(lit, at, match{dest - distance, dest, int(length)})
		if err != nil {
			return err
		}
	}
	return d.finishBlock(lit, at)
}

func (d *decoder) forward(lit *io.LimitedReader) (save bool, err error) {
	end := d.pos + uint64(len(d.out))
	source := d.pos
	for stat := d.stats; len(stat) != 0; stat = stat[16:] {
		gap := uint64(binary.LittleEndian.Uint32(stat))
		distance := binary.LittleEndian.Uint64(stat[4:])
		length := uint64(binary.LittleEndian.Uint32(stat[12:])) + d.base
		if gap >= end-source {
			return false, badData("source is outside its block")
		}
		source += gap
		if length == 0 || length > end-source || distance == 0 || distance > math.MaxInt64-source-length {
			return false, badData("invalid forward match")
		}
		if len(d.pending) >= maxPending {
			return false, badData("too many pending matches (try UNARC_SREP)")
		}
		dest := source + distance
		heap.Push(&d.pending, match{source, dest, int(length)})
		save = save || dest >= end
	}
	at := 0
	for len(d.pending) > 0 && d.pending[0].dest < end {
		at, err = d.emitMatch(lit, at, heap.Pop(&d.pending).(match))
		if err != nil {
			return false, err
		}
	}
	return save, d.finishBlock(lit, at)
}

// emitMatch fills the literal gap and expands one LZ reference. An overlap with the output being
// written repeats the bytes already produced, doubling the available run on each copy.
func (d *decoder) emitMatch(lit *io.LimitedReader, at int, m match) (int, error) {
	end := d.pos + uint64(len(d.out))
	if m.dest < d.pos+uint64(at) || m.dest >= end || uint64(m.length) > end-m.dest {
		return 0, badData("match destination is outside its block or overlaps another match")
	}
	dest := int(m.dest - d.pos)
	if _, err := io.ReadFull(lit, d.out[at:dest]); err != nil {
		return 0, frame.Truncated(err)
	}
	next := dest + m.length
	if m.source < d.pos {
		n := int(min(uint64(m.length), d.pos-m.source))
		if d.history == nil {
			return 0, badData("match refers to missing history")
		}
		if _, err := d.history.ReadAt(d.out[dest:dest+n], int64(m.source)); err != nil {
			return 0, fmt.Errorf("srep history: %w", err)
		}
		m.source += uint64(n)
		dest += n
	}
	if dest < next {
		src := int(m.source - d.pos)
		for dest < next {
			dest += copy(d.out[dest:next], d.out[src:dest])
		}
	}
	return next, nil
}

func (d *decoder) finishBlock(lit *io.LimitedReader, at int) error {
	if lit.N != int64(len(d.out)-at) {
		return badData("literal count does not fill block")
	}
	_, err := io.ReadFull(lit, d.out[at:])
	return frame.Truncated(err)
}
