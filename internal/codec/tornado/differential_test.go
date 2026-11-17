package tornado

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/n0madic/go-unarc/internal/codectest"
	"math/rand/v2"
	"testing"
)

// The tests here build byte-coder streams by hand, with elements the C encoder never produces (data tables
// of any row size, matches at the edges of the ring buffer), and compare the Go decoder with the C one.

// elem is a stream element: a literal, a match, or (len >= impossibleLen) a table or the end marker.
type elem struct {
	len, dist uint32
	lit       byte
	isLit     bool
}

// encodeByteCoder writes the byte coder's encoding of elems, then the end marker.
func encodeByteCoder(minLen uint8, hdrSize uint32, elems []elem) []byte {
	out := []byte{coderByte, minLen}
	out = binary.LittleEndian.AppendUint32(out, hdrSize)
	elems = append(elems[:len(elems):len(elems)], elem{len: impossibleLen, dist: impossibleDist})
	flagsAt := 0
	var flags uint32
	for i, e := range elems {
		if i%16 == 0 {
			if i > 0 {
				binary.LittleEndian.PutUint32(out[flagsAt:], flags)
			}
			flagsAt, flags = len(out), 0
			out = append(out, 0, 0, 0, 0)
		}
		bit := uint32(1) << (2 * (i % 16))
		if e.isLit {
			out = append(out, e.lit)
			continue
		}
		dist, ln := e.dist, e.len-uint32(minLen)
		switch {
		case e.len < impossibleLen && ln < 16 && dist < 1<<12:
			out = binary.LittleEndian.AppendUint16(out, uint16(ln<<12+dist))
			flags += bit
		case e.len < impossibleLen && ln < 64 && dist < 1<<18:
			out = binary.LittleEndian.AppendUint32(out, ln<<18+dist)[:len(out)+3]
			flags += bit * 2
		default:
			if dist >= 1<<24 {
				out = append(out, 255, byte(dist>>24))
			}
			if ln >= 254 {
				out = append(out, 254, byte(ln>>8), byte(ln>>16), byte(ln>>24))
				ln %= 256
			}
			out = binary.LittleEndian.AppendUint32(out, ln+dist<<8)
			flags += bit * 3
		}
	}
	binary.LittleEndian.PutUint32(out[flagsAt:], flags)
	return out
}

// diffGen builds a random valid element sequence.
type diffGen struct {
	r        *rand.Rand
	hdr      uint32
	ring     uint64
	minLen   uint32
	produced uint64
	elems    []elem
}

func (g *diffGen) literal(n int) {
	for range n {
		g.elems = append(g.elems, elem{isLit: true, lit: byte(g.r.Uint32())})
	}
	g.produced += uint64(n)
}

// match adds a match of about n bytes with a random distance, in the ring.
func (g *diffGen) match(n uint32) {
	if g.produced == 0 {
		g.literal(1)
	}
	n = min(max(n, g.minLen, 1), 2*g.hdr)
	dist := uint64(1 + g.r.Uint64N(min(g.produced, g.ring, 1<<g.r.UintN(24))))
	if g.r.IntN(50) == 0 {
		dist = min(g.produced, g.ring) // the farthest distance there is
	}
	g.elems = append(g.elems, elem{len: n, dist: uint32(dist)})
	g.produced += uint64(n)
}

// nextWrite returns the first point after cur at which the decoder writes its buffer out.
func (g *diffGen) nextWrite(cur uint64) uint64 {
	return cur + min(g.ring-cur%g.ring, hugeBuffer)
}

// table adds a table of rows rows of size bytes and the data to fill it with. When the table is divided
// between two writes, it adds a match that copies what lies before the write point, to check that the
// decoder leaves it diffed.
func (g *diffGen) table(size, rows uint32) {
	start := g.produced
	g.tableData(size, rows)
	for w := g.nextWrite(start); w < g.produced; w = g.nextWrite(w) {
		if g.produced-w < 1<<20 {
			g.elems = append(g.elems, elem{len: 1100, dist: uint32(g.produced - (w - 1100))})
			g.produced += 1100
		}
	}
}

func (g *diffGen) tableData(size, rows uint32) {
	g.elems = append(g.elems, elem{len: impossibleLen + size, dist: rows})
	total := size * rows
	for total > 0 {
		n := min(total, uint32(1+g.r.IntN(300)))
		if g.r.IntN(3) == 0 && g.produced > 0 {
			g.match(max(n, g.minLen))
		} else {
			g.literal(int(n))
		}
		total -= min(total, n)
	}
}

func (g *diffGen) tableSizes() uint32 {
	sizes := []uint32{2, 4, 2, 4, 1, 3, 5, 8, 16, 63, 64, 100, 255, 256}
	return sizes[g.r.IntN(len(sizes))]
}

func (g *diffGen) fill(total uint64) {
	for g.produced < total {
		switch k := g.r.IntN(10); {
		case k < 3:
			g.literal(1 + g.r.IntN(100))
		case k < 6:
			g.match(uint32(1 + g.r.IntN(40)))
		case k < 7:
			g.match(uint32(1 + g.r.IntN(1<<g.r.UintN(21))))
		default:
			size := g.tableSizes()
			rows := uint32(1 + g.r.IntN(max(1, min(int(2*g.hdr/size), 3000))))
			g.table(size, rows)
		}
	}
}

func TestDifferentialByteCoder(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	tests := []struct {
		name   string
		hdr    uint32
		minLen uint8
		total  uint64
	}{
		{"small", 32 << 10, 2, 20 << 20},
		{"ring8", 8 << 20, 0, 20 << 20},
		{"ring12", 12 << 20, 4, 40 << 20},
		{"ring9", 9<<20 + 4096, 2, 30 << 20},
		{"short", 1 << 20, 3, 1 << 20},
	}
	for _, tt := range tests {
		for seed := range uint64(4) {
			t.Run(fmt.Sprintf("%s/%d", tt.name, seed), func(t *testing.T) {
				t.Parallel()
				g := &diffGen{r: newRand(100 + seed), hdr: tt.hdr, ring: uint64(max(tt.hdr, hugeBuffer)), minLen: uint32(tt.minLen)}
				g.fill(tt.total)
				packed := encodeByteCoder(tt.minLen, tt.hdr, g.elems)
				compareWithC(t, packed)
			})
		}
	}
}

// compareWithC checks that the Go decoder gives the output of the C decoder for the stream.
func compareWithC(t *testing.T, packed []byte) {
	t.Helper()
	c := codectest.FagenTry(t, packed, "d", "tor")
	want := c.Out
	got, err := decode(t, "tor", packed)
	if !c.OK() {
		t.Fatalf("the C decoder fails on the stream: exit %d", c.Exit)
	}
	if err != nil {
		t.Fatalf("Go: %v (C decoded %d bytes)", err, len(want))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded %d bytes, C decoded %d: first difference at %d", len(got), len(want), firstDiff(got, want))
	}
}

// TestDifferentialFullTableList fills the list of tables, which forces a write in the middle of the data.
func TestDifferentialFullTableList(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	for _, n := range []int{maxTables - 1, maxTables, maxTables + 1, 3*maxTables + 7} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			g := &diffGen{r: newRand(7), hdr: 1 << 20, ring: hugeBuffer, minLen: 2}
			g.literal(5000)
			for i := range n {
				g.elems = append(g.elems, elem{len: impossibleLen + 2 + 2*uint32(i%2), dist: uint32(1 + i%3)})
			}
			g.literal(300)
			g.table(4, 100)
			g.literal(100)
			compareWithC(t, encodeByteCoder(2, g.hdr, g.elems))
		})
	}
}
