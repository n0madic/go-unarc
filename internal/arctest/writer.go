// Package arctest writes FreeArc archives for tests: the container layout of FreeArc 0.67
// (header block, solid data blocks, directory block, footer block, each control block followed by its
// local descriptor), with the compressed streams supplied by the caller.
package arctest

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
)

const signature = 0x01437241 // "ArC\x01"

// Block types.
const (
	headerBlock = 1
	dirBlock    = 3
	footerBlock = 4
)

// archiveVersion is FreeArc 0.67's aARCHIVE_VERSION.
var archiveVersion = []byte{7, 6, 0, 0}

// File is one entry of a solid block.
type File struct {
	Path  string // '/'-separated path inside the archive
	Data  []byte
	Time  uint32
	IsDir bool
	// CRC overrides the CRC32 of Data when non-nil (to build archives that fail the check).
	CRC *uint32
}

// Solid is a solid block. Packed is its compressed stream; when nil, Compressor must be "storing"
// and the files' data is stored as is.
type Solid struct {
	Compressor string
	Packed     []byte
	Files      []File
}

// Packer compresses a control block with the given compressor.
type Packer func(compressor string, data []byte) []byte

// Archive describes an archive to write.
type Archive struct {
	Prefix  []byte // bytes before the archive (an SFX module)
	Solids  []Solid
	Comment []byte
	Locked  bool
	// OldComment is stored as the UCS-4 comment of early versions (each byte widened to 4 bytes).
	OldComment []byte
	// Control blocks (directory and footer) use ControlCompressor, packed by Pack; "storing" by default.
	ControlCompressor string
	Pack              Packer
	// Dirs splits Solids into several directory blocks: Dirs[i] is the number of solids in block i.
	// Empty means one directory block for everything.
	Dirs []int
}

// Concat joins the data of files in order: the uncompressed stream of a solid block.
func Concat(files []File) []byte {
	var b bytes.Buffer
	for _, f := range files {
		b.Write(f.Data)
	}
	return b.Bytes()
}

// writer accumulates the archive.
type writer struct {
	bytes.Buffer
}

// Integer encodes v in FreeArc's variable-length format.
func Integer(v uint64) []byte {
	var raw [9]byte
	for n := 1; n <= 8; n++ {
		if v < 1<<(7*n) {
			x := v<<n | (1<<(n-1) - 1)
			binary.LittleEndian.PutUint64(raw[:8], x)
			return append([]byte(nil), raw[:n]...)
		}
	}
	raw[0] = 0xFF
	binary.LittleEndian.PutUint64(raw[1:], v)
	return raw[:]
}

func (w *writer) integer(v uint64) { w.Write(Integer(v)) }
func (w *writer) str(s string)     { w.WriteString(s); w.WriteByte(0) }
func (w *writer) u32(v uint32)     { _ = binary.Write(w, binary.LittleEndian, v) }

type blockInfo struct {
	typ        uint64
	compressor string
	pos        uint64
	orig, comp uint64
	crc        uint32
}

// controlBlock writes a compressed control block and its local descriptor.
func (w *writer) controlBlock(typ uint64, compressor string, pack Packer, data []byte) blockInfo {
	packed := data
	if compressor != "storing" {
		packed = pack(compressor, data)
	}
	b := blockInfo{typ: typ, compressor: compressor, pos: uint64(w.Len()),
		orig: uint64(len(data)), comp: uint64(len(packed)), crc: crc32.ChecksumIEEE(data)}
	w.Write(packed)
	var d writer
	d.u32(signature)
	d.integer(typ)
	d.str(compressor)
	d.integer(b.orig)
	d.integer(b.comp)
	d.u32(b.crc)
	d.u32(crc32.ChecksumIEEE(d.Bytes()))
	w.Write(d.Bytes())
	return b
}

func splitPath(p string) (dir, name string) {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}

// Build returns the archive bytes.
func Build(a Archive) []byte {
	compressor := a.ControlCompressor
	if compressor == "" {
		compressor = "storing"
	}
	var w writer
	w.Write(a.Prefix)
	var control []blockInfo
	control = append(control, w.controlBlock(headerBlock, "storing", nil,
		binary.LittleEndian.AppendUint32(append([]byte(nil), 0x41, 0x72, 0x43, 0x01), binary.LittleEndian.Uint32(archiveVersion))))

	groups := a.Dirs
	if len(groups) == 0 {
		groups = []int{len(a.Solids)}
	}
	solids := a.Solids
	for _, n := range groups {
		group := solids[:n]
		solids = solids[n:]
		positions := make([]uint64, len(group))
		sizes := make([]uint64, len(group))
		for i, s := range group {
			packed := s.Packed
			if packed == nil {
				packed = Concat(s.Files)
			}
			positions[i] = uint64(w.Len())
			sizes[i] = uint64(len(packed))
			w.Write(packed)
		}
		dirPos := uint64(w.Len())
		control = append(control, w.controlBlock(dirBlock, compressor, a.Pack, dirData(group, positions, sizes, dirPos)))
	}

	footerPos := uint64(w.Len())
	var f writer
	f.integer(uint64(len(control)))
	for _, b := range control {
		f.integer(b.typ)
		f.str(b.compressor)
		f.integer(footerPos - b.pos)
		f.integer(b.orig)
		f.integer(b.comp)
		f.u32(b.crc)
	}
	if a.Locked {
		f.WriteByte(1)
	} else {
		f.WriteByte(0)
	}
	f.integer(uint64(len(a.OldComment)))
	for _, c := range a.OldComment {
		f.u32(uint32(c))
	}
	f.str("") // recovery record settings
	f.integer(uint64(len(a.Comment)))
	f.Write(a.Comment)
	w.controlBlock(footerBlock, compressor, a.Pack, f.Bytes())
	return w.Bytes()
}

// dirData encodes a directory block (ArhiveDirectory.hs archiveWriteDir).
func dirData(solids []Solid, positions, sizes []uint64, dirPos uint64) []byte {
	var d writer
	d.integer(uint64(len(solids)))
	for _, s := range solids {
		d.integer(uint64(len(s.Files)))
	}
	for _, s := range solids {
		d.str(s.Compressor)
	}
	for _, p := range positions {
		d.integer(dirPos - p)
	}
	for _, sz := range sizes {
		d.integer(sz)
	}
	var files []File
	for _, s := range solids {
		files = append(files, s.Files...)
	}
	dirIndex := map[string]int{}
	var dirs []string
	numbers := make([]int, len(files))
	for i, f := range files {
		dir, _ := splitPath(f.Path)
		n, ok := dirIndex[dir]
		if !ok {
			n = len(dirs)
			dirIndex[dir] = n
			dirs = append(dirs, dir)
		}
		numbers[i] = n
	}
	d.integer(uint64(len(dirs)))
	for _, dir := range dirs {
		d.str(dir)
	}
	for _, f := range files {
		_, name := splitPath(f.Path)
		d.str(name)
	}
	for _, n := range numbers {
		d.integer(uint64(n))
	}
	for _, f := range files {
		d.integer(uint64(len(f.Data)))
	}
	for _, f := range files {
		d.u32(f.Time)
	}
	for _, f := range files {
		if f.IsDir {
			d.WriteByte(1)
		} else {
			d.WriteByte(0)
		}
	}
	for _, f := range files {
		crc := crc32.ChecksumIEEE(f.Data)
		if f.CRC != nil {
			crc = *f.CRC
		}
		d.u32(crc)
	}
	d.WriteByte(0) // aTAG_END: no optional fields
	return d.Bytes()
}
