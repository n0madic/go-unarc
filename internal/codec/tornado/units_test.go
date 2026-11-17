package tornado

import (
	"bytes"
	"fmt"
	"testing"
)

// refUndiff is the bytewise definition of undiffing with carries from the lowest address (the C
// undiff_table), against which the optimised row sizes are checked.
func refUndiff(buf []byte, n, start, rows int) {
	for r := start + n; r < start+n*rows; r += n {
		carry := 0
		for i := range n {
			t := int(buf[r+i]) + int(buf[r+i-n]) + carry
			buf[r+i] = byte(t)
			carry = t >> 8
		}
	}
}

func TestDiffTables(t *testing.T) {
	t.Parallel()
	rows := []int{1, 2, 3, 17, 100}
	for n := 1; n <= maxTableRow; n++ {
		if n > 70 && n != 100 && n != 255 && n != maxTableRow {
			continue
		}
		for _, r := range rows {
			t.Run(fmt.Sprintf("n=%d/rows=%d", n, r), func(t *testing.T) {
				t.Parallel()
				rnd := newRand(uint64(n*1000 + r))
				const start = 5
				orig := genRandom(start+n*r+7, rnd.Uint64())
				if r%2 == 1 { // runs of 0x00 and 0xff exercise the carries
					for i := range orig {
						orig[i] = []byte{0, 255, byte(rnd.Uint32())}[rnd.IntN(3)]
					}
				}
				buf := bytes.Clone(orig)
				diffTable(buf, n, start, r)
				if r > 1 && bytes.Equal(buf, orig) {
					t.Fatal("diffTable changed nothing")
				}
				if !bytes.Equal(buf[:start+n], orig[:start+n]) || !bytes.Equal(buf[start+n*r:], orig[start+n*r:]) {
					t.Fatal("diffTable wrote outside the rows it should change, or changed the first row")
				}
				ref := bytes.Clone(buf)
				refUndiff(ref, n, start, r)
				undiffTable(buf, n, start, r)
				if !bytes.Equal(buf, ref) {
					t.Fatal("undiffTable differs from the bytewise definition")
				}
				if !bytes.Equal(buf, orig) {
					t.Fatal("undiffTable does not undo diffTable")
				}
			})
		}
	}
}

// TestHuffmanTree builds trees from many distributions of counters and checks that the codes are a complete
// prefix code, that the lookup tables decode every code, and that the longest code is the one of the first
// node (which the C decoder takes it from).
func TestHuffmanTree(t *testing.T) {
	t.Parallel()
	rnd := newRand(1)
	fib := func(c []uint32) {
		a, b := uint32(1), uint32(1)
		for i := range c {
			c[i] = a
			a, b = b, a+b
			if b > 1<<20 {
				a, b = 1, 1
			}
		}
	}
	dists := map[string]func(c []uint32){
		"equal":   func(c []uint32) { fill(c, func(int) uint32 { return 1 }) },
		"random":  func(c []uint32) { fill(c, func(int) uint32 { return 1 + rnd.Uint32N(40) }) },
		"large":   func(c []uint32) { fill(c, func(int) uint32 { return 1 + rnd.Uint32N(5000) }) },
		"mixed":   func(c []uint32) { fill(c, func(int) uint32 { return []uint32{1, 249, 250, 251, 7000}[rnd.IntN(5)] }) },
		"skewed":  func(c []uint32) { fill(c, func(i int) uint32 { return 1 + uint32(1000000/(i+1)) }) },
		"fibo":    fib,
		"literal": func(c []uint32) { fill(c, func(i int) uint32 { return 1 + uint32(i%256/255*4000) }) },
	}
	for name, dist := range dists {
		for _, n := range []int{2, 3, 20, 256, codes} {
			t.Run(fmt.Sprintf("%s/%d", name, n), func(t *testing.T) {
				tr := &huffTree{n: n}
				dist(tr.counter[:n])
				if err := tr.build(3); err != nil {
					if name == "fibo" {
						return // too deep for a valid stream
					}
					t.Fatal(err)
				}
				maxbits, kraft := uint8(0), uint64(0)
				for i := range n {
					maxbits = max(maxbits, tr.nbits[i])
				}
				if maxbits != tr.nbits[0] {
					t.Errorf("longest code has %d bits, the first node %d", maxbits, tr.nbits[0])
				}
				for i := range n {
					kraft += 1 << (maxbits - tr.nbits[i])
				}
				if n > 1 && kraft != 1<<maxbits {
					t.Errorf("the codes do not make a complete prefix code: Kraft sum %d/%d", kraft, uint64(1)<<maxbits)
				}
				// Every code, followed by arbitrary bits, must decode to its symbol.
				for i := range n {
					s, nb, code := uint32(tr.left[i]), uint32(tr.nbits[i]), tr.ncode[i]
					for _, tail := range []uint32{0, tr.mask} {
						in := code | tail<<nb&tr.mask
						var got uint32
						if e := tr.fast[in&(fastSize-1)]; e != 0 {
							got = e & 0xffff
							if e>>16 != nb {
								t.Fatalf("symbol %d: fast table has length %d, want %d", s, e>>16, nb)
							}
						} else {
							got = uint32(tr.index[in&tr.mask])
						}
						if got != s {
							t.Fatalf("code %b of symbol %d decodes to %d", code, s, got)
						}
					}
				}
			})
		}
	}
}

func fill(c []uint32, f func(i int) uint32) {
	for i := range c {
		c[i] = f(i)
	}
}
