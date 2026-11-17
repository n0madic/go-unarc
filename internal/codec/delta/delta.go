package delta

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
	"strings"

	"github.com/n0madic/go-unarc/internal/codec/frame"
	"github.com/n0madic/go-unarc/internal/method"
)

const (
	// maxElementSize is the largest record size the format can describe (MAX_ELEMENT_SIZE in the C code).
	maxElementSize = 30
	// defaultBlockSize is the default of the "b" parameter, which only matters to the encoder (8*mb).
	defaultBlockSize = 8 * method.MB
)

func init() { method.Register(Parse) }

// codec is a parsed "delta" method. Decoding needs no parameters, the stream describes itself.
type codec struct {
	blockSize      uint32 // encoder's block size; also what GetDecompressionMem reports
	extendedTables bool   // "x": unused by both the C encoder and decoder
}

// Parse mirrors parse_DELTA: "delta" with the optional parameters "x" and a block size ("b<mem>" or just "<mem>").
func Parse(params []string, _ *method.Env) (method.Codec, bool) {
	if params[0] != "delta" {
		return nil, false
	}
	c := codec{blockSize: defaultBlockSize}
	for _, param := range params[1:] {
		if param == "x" {
			c.extendedTables = true
			continue
		}
		mem, ok := method.ParseMem(strings.TrimPrefix(param, "b"))
		if !ok {
			return nil, false
		}
		c.blockSize = mem
	}
	return c, true
}

// DecompressionMem is the block size, as in the C method.
func (c codec) DecompressionMem() uint64 { return uint64(c.blockSize) }

// Decode undoes the table filter block by block.
func (codec) Decode(dst io.Writer, src io.Reader) error {
	var d decoder
	for {
		dataSize, err := frame.ReadInt32(src)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		tableSize, err := frame.ReadInt32(src)
		if err != nil {
			return frame.Truncated(err)
		}
		if dataSize < 0 || tableSize < 0 {
			return badData("negative block size")
		}
		if err := d.readBlock(src, int(dataSize), int(tableSize)); err != nil {
			return err
		}
		if err := d.restore(); err != nil {
			return err
		}
		if _, err := dst.Write(d.data); err != nil {
			return err
		}
	}
}

// decoder holds the buffers of one Decode call, reused from block to block.
type decoder struct {
	data            []byte // the block's data, restored in place
	skip, typ, rows []byte // descriptor arrays, uint32 per table
	tableCount      int    // tables described in the arrays
	tmp             []byte // scratch space for unreorder
	runs            []run  // columns subtracted in the current table
}

// readBlock reads the three descriptor arrays and the data of one block.
func (d *decoder) readBlock(src io.Reader, dataSize, tableSize int) (err error) {
	d.tableCount = tableSize / 4 // trailing bytes of a descriptor array are ignored
	if d.skip, err = frame.ReadN(d.skip, src, tableSize); err != nil {
		return err
	}
	if d.typ, err = frame.ReadN(d.typ, src, tableSize); err != nil {
		return err
	}
	if d.rows, err = frame.ReadN(d.rows, src, tableSize); err != nil {
		return err
	}
	d.data, err = frame.ReadN(d.data, src, dataSize)
	return err
}

// restore walks the table descriptors and reverses the filter of every table inside d.data.
func (d *decoder) restore() error {
	pos := int64(0)
	for i := range d.tableCount {
		skip := int32(binary.LittleEndian.Uint32(d.skip[4*i:]))
		typ := binary.LittleEndian.Uint32(d.typ[4*i:])
		rows := int32(binary.LittleEndian.Uint32(d.rows[4*i:]))

		// The type word is 1<<N plus one bit per column that is not subtracted ("immutable").
		n := max(bits.Len32(typ)-1, 0)
		if n > maxElementSize {
			return badData("table %d: record size %d", i, n)
		}
		immutable := typ &^ (1 << n)

		pos += int64(skip)
		size := int64(n) * int64(rows)
		if pos < 0 || rows < 0 || pos+size > int64(len(d.data)) {
			return badData("table %d: %d*%d bytes at %d do not fit into the block", i, n, rows, pos)
		}
		table := d.data[pos : pos+size]
		pos += size
		if size == 0 {
			continue
		}
		d.unreorder(table, n, immutable)
		d.undiff(table, n, ^immutable&(1<<n-1))
	}
	return nil
}

// unreorder moves the columns back to their places: the encoder put all immutable columns first (row by row),
// followed by all mutable ones.
func (d *decoder) unreorder(table []byte, n int, immutable uint32) {
	imm := bits.OnesCount32(immutable)
	if imm == 0 || imm == n {
		return
	}
	rows := len(table) / n
	d.tmp = append(d.tmp[:0], table...)
	// Column positions of the immutable and the mutable bytes of a row, in storage order.
	var immCols, mutCols [maxElementSize]uint8
	ni, nm := 0, 0
	for k := range n {
		if immutable>>k&1 != 0 {
			immCols[ni] = uint8(k)
			ni++
		} else {
			mutCols[nm] = uint8(k)
			nm++
		}
	}
	immPart, mutPart := d.tmp[:imm*rows], d.tmp[imm*rows:]
	for r := range rows {
		row := table[r*n : r*n+n]
		for j, k := range immCols[:ni] {
			row[k] = immPart[r*imm+j]
		}
		for j, k := range mutCols[:nm] {
			row[k] = mutPart[r*nm+j]
		}
	}
}

// run is a group of adjacent subtracted columns: the carry of the byte-wise addition stays inside it, so
// the group behaves as one little-endian number of size bytes.
type run struct{ off, size int }

// undiff adds every record to the next one in the columns of mask. Each run of adjacent columns is
// an independent running sum over the rows.
func (d *decoder) undiff(table []byte, n int, mask uint32) {
	d.runs = d.runs[:0]
	for k := 0; k < n; {
		if mask>>k&1 == 0 {
			k++
			continue
		}
		start := k
		for k < n && mask>>k&1 != 0 {
			k++
		}
		d.runs = append(d.runs, run{start, k - start})
	}
	for _, r := range d.runs {
		prefixSum(table[r.off:], n, r.size)
	}
}

// prefixSum turns the little-endian numbers table[0:size], table[n:n+size], ... into their running sums.
func prefixSum(table []byte, n, size int) {
	switch size {
	case 1:
		var acc byte
		for i := 0; i < len(table); i += n {
			acc += table[i]
			table[i] = acc
		}
	case 2:
		var acc uint16
		for i := 0; i+2 <= len(table); i += n {
			acc += binary.LittleEndian.Uint16(table[i:])
			binary.LittleEndian.PutUint16(table[i:], acc)
		}
	case 4:
		var acc uint32
		for i := 0; i+4 <= len(table); i += n {
			acc += binary.LittleEndian.Uint32(table[i:])
			binary.LittleEndian.PutUint32(table[i:], acc)
		}
	case 8:
		var acc uint64
		for i := 0; i+8 <= len(table); i += n {
			acc += binary.LittleEndian.Uint64(table[i:])
			binary.LittleEndian.PutUint64(table[i:], acc)
		}
	default:
		acc := make([]byte, size)
		for i := 0; i+size <= len(table); i += n {
			carry := 0
			for j := range size {
				sum := int(acc[j]) + int(table[i+j]) + carry
				acc[j] = byte(sum)
				carry = sum >> 8
			}
			copy(table[i:], acc)
		}
	}
}

func badData(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrBadData, fmt.Sprintf(format, args...))
}
