package tornado

import (
	"bytes"
	"encoding/binary"
	"io"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/method"
)

// decode runs the Go decoder for method m on a stream.
func decode(t testing.TB, m string, packed []byte) ([]byte, error) {
	t.Helper()
	c, ok := Parse(method.Params(m), nil)
	if !ok {
		t.Fatalf("Parse(%q) failed", m)
	}
	var out bytes.Buffer
	err := c.Decode(&out, bytes.NewReader(packed))
	return out.Bytes(), err
}

// --- test data ---------------------------------------------------------------------------------------------

func newRand(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) }

var vocabulary = strings.Fields(`the of and to in is that for it as was with be by on not he this are or his from at which
	but have an had they you were their one all we can her has there been if more when will would who so no
	compression window buffer literal match distance length huffman arithmetic table diff block stream`)

// genText returns n bytes of pseudo-English text.
func genText(n int, seed uint64) []byte {
	r := newRand(seed)
	var b bytes.Buffer
	for b.Len() < n {
		b.WriteString(vocabulary[r.IntN(len(vocabulary))])
		switch r.IntN(12) {
		case 0:
			b.WriteString(".\n")
		case 1:
			b.WriteString(", ")
		default:
			b.WriteByte(' ')
		}
	}
	return b.Bytes()[:n]
}

func genRandom(n int, seed uint64) []byte {
	r := newRand(seed)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// genPeriodic returns a random block of the given period repeated, with every so often a changed byte.
func genPeriodic(n, period int, seed uint64) []byte {
	r := newRand(seed)
	block := genRandom(period, seed+1)
	b := make([]byte, n)
	for i := range b {
		b[i] = block[i%period]
		if r.IntN(5000) == 0 {
			b[i] ^= byte(1 + r.IntN(255))
		}
	}
	return b
}

// genTable32 returns an array of 32-bit integers that rise by a few 100000 per element, the kind of data
// the encoder detects as a table of 4-byte rows.
func genTable32(n int, seed uint64) []byte {
	r := newRand(seed)
	b := make([]byte, n&^3)
	v := uint32(r.Uint32() & 0xffff)
	for i := 0; i+4 <= len(b); i += 4 {
		binary.LittleEndian.PutUint32(b[i:], v)
		v += 70000 + uint32(r.IntN(200000))
	}
	return append(b, make([]byte, n-len(b))...)
}

// genTable16 returns an array of 16-bit integers that rise slowly.
func genTable16(n int, seed uint64) []byte {
	r := newRand(seed)
	b := make([]byte, n&^1)
	v := uint16(100)
	for i := 0; i+2 <= len(b); i += 2 {
		binary.LittleEndian.PutUint16(b[i:], v)
		v += uint16(1 + r.IntN(40))
		if v > 0x7000 {
			v = uint16(100 + r.IntN(100))
		}
	}
	return append(b, make([]byte, n-len(b))...)
}

// genMixed concatenates pieces of every kind, so that coders meet literals, matches, tables and long runs.
func genMixed(n int, seed uint64) []byte {
	r := newRand(seed)
	var b []byte
	for len(b) < n {
		size := 1 + r.IntN(max(n/5, 1))
		switch r.IntN(7) {
		case 0:
			b = append(b, genText(size, r.Uint64())...)
		case 1:
			b = append(b, genRandom(size, r.Uint64())...)
		case 2:
			b = append(b, genPeriodic(size, 1+r.IntN(5000), r.Uint64())...)
		case 3:
			b = append(b, genTable32(size, r.Uint64())...)
		case 4:
			b = append(b, genTable16(size, r.Uint64())...)
		case 5:
			b = append(b, bytes.Repeat([]byte{byte(r.IntN(256))}, size)...)
		default:
			b = append(b, sourcesPiece(size, r)...)
		}
	}
	return b[:n]
}

func sourcesPiece(n int, r *rand.Rand) []byte {
	text := genText(1<<16, r.Uint64())
	start := r.IntN(len(text) / 2)
	return bytes.Repeat(text[start:start+len(text)/4], n/(len(text)/4)+1)[:n]
}

// limitWriter stops the decoder with method.ErrStop after n bytes.
type limitWriter struct{ n int }

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n -= len(p); l.n < 0 {
		return 0, method.ErrStop
	}
	return len(p), nil
}

var _ io.Writer = (*limitWriter)(nil)
