package tornado

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method string
		ok     bool
		mem    uint64
	}{
		{"tor", true, 16 << 20},
		{"tor:5", true, 16 << 20},
		{"tor:0", true, 1 << 20},
		{"tor:1", true, 1 << 20},
		{"tor:3", true, 4 << 20},
		{"tor:6", true, 64 << 20},
		{"tor:8", true, 1 << 30},
		{"tor:11", true, 1 << 30},
		{"tor:12", false, 0}, // C reads past the preset table
		{"tor:24", false, 0},
		{"tor:4294967295", false, 0},
		{"tor:5:b64kb", true, 64 << 10},
		{"tor:b64kb", true, 64 << 10},
		{"tor:64kb", true, 64 << 10},
		{"tor:2mb", true, 2 << 20},
		{"tor:b20", true, 1 << 20},    // no suffix: a power of two
		{"tor:b4m:11", true, 1 << 30}, // a preset replaces what came before
		{"tor:11:b4m", true, 4 << 20},
		{"tor:7:h1mb:l8:c4:p2:u1:t0:ah64kb:al3", true, 256 << 20},
		{"tor:5:c1", true, 16 << 20},
		{"tor:5:ahx", false, 0},
		{"tor:5:al", false, 0},
		{"tor:5:a", false, 0},
		{"tor:5:ab", false, 0},
		{"tor:5:b", false, 0},
		{"tor:5:h", false, 0},
		{"tor:5:lx", false, 0},
		{"tor:5:", false, 0},
		{"tor:", false, 0},
		{"tor:x", false, 0},
		{"tor:5:q4", false, 0},
		{"tor:5:b64kb:zz", false, 0},
		{"tor:=7", true, 256 << 20},
		{"tor:b=2mb", true, 2 << 20},
		{"tor:b64q", false, 0},
		{"torx", false, 0},
		{"to", false, 0},
		{"lzma", false, 0},
	}
	for _, tt := range tests {
		c, ok := Parse(method.Params(tt.method), nil)
		if ok != tt.ok {
			t.Errorf("Parse(%q) ok = %v, want %v", tt.method, ok, tt.ok)
			continue
		}
		if ok && c.DecompressionMem() != tt.mem {
			t.Errorf("Parse(%q).DecompressionMem() = %d, want %d", tt.method, c.DecompressionMem(), tt.mem)
		}
	}
}

// TestParseFagen compares Parse with the C parser: which methods are accepted and DecompressionMem.
func TestParseFagen(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	const invalid = "4294967294" // GetDecompressionMem of a method that does not parse: INVALID_COMPRESSOR
	for _, m := range []string{
		"tor", "tor:0", "tor:7", "tor:11", "tor:5:b64kb", "tor:b9mb", "tor:4mb:3", "tor:3:b1gb", "tor:1:h1gb:p2:b300kb",
		"tor:=7", "tor:b=2mb", "tor:b20", "tor:b4m:11", "tor:11:b4m", "tor:5:ah1mb:al2", "tor:5:c1:t0:u1:l4",
		"tor:5:ahx", "tor:5:al", "tor:5:q4", "tor:5:", "tor:", "tor:x", "tor:b", "tor:5:ab", "tor:5:b64q", "tor:5:a", "torx",
		"tor:5:hx", "tor:5:lx", "tor:5:c", "tor:b64kb:zz", "tor:5:b4294967296", "tor:4294967296", "tor:b1g", "tor:b4g", "tor:5:b5g",
	} {
		want := string(bytes.TrimSpace(codectest.Fagen(t, nil, "mem", m)))
		c, ok := Parse(method.Params(m), nil)
		if ok != (want != invalid) {
			t.Errorf("Parse(%q) ok = %v, C says %s", m, ok, want)
			continue
		}
		if ok && fmt.Sprint(c.DecompressionMem()) != want {
			t.Errorf("%s: DecompressionMem = %d, C says %s", m, c.DecompressionMem(), want)
		}
	}
}

// roundTrip packs data with the C encoder and checks that both decoders give it back.
func roundTrip(t *testing.T, m string, data []byte) []byte {
	t.Helper()
	packed := codectest.Fagen(t, data, "c", m)
	got, err := decode(t, m, packed)
	if err != nil {
		t.Fatalf("%s: decoding %d bytes (packed %d): %v", m, len(data), len(packed), err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("%s: decoded %d bytes (packed %d), want %d: first difference at %d", m, len(got), len(packed), len(data), firstDiff(got, data))
	}
	return packed
}

func firstDiff(a, b []byte) int {
	for i := 0; i < min(len(a), len(b)); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// roundTripMethods are the variants the C encoder accepts: every preset, and others with a given coder.
var roundTripMethods = []string{
	"tor:1", "tor:2", "tor:3", "tor:4", "tor:5", "tor:6",
	"tor:7:b2mb", "tor:8:b2mb", "tor:9:b1mb", "tor:10:b1mb", "tor:11:b1mb",
	"tor:11:b1mb:c1", "tor:9:b1mb:c2", "tor:7:b1mb:c3",
	"tor:5:u1:t0", "tor:5:b64kb", "tor:3:b32kb", "tor:1:b64kb", "tor:2:b100kb", "tor:6:b4mb",
}

func TestRoundTripFagen(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	sizes := []int{0, 1, 2, 3, 4, 5, 6, 7, 100, 4095, 4096, 4097, 65535, 65536, 65537, 300000}
	type input struct {
		name string
		gen  func(n int) []byte
	}
	inputs := []input{
		{"text", func(n int) []byte { return genText(n, 11) }},
		{"sources", func(n int) []byte { return codectest.SourceText(t, n) }},
		{"random", func(n int) []byte { return genRandom(n, 12) }},
		{"periodic", func(n int) []byte { return genPeriodic(n, 777, 13) }},
		{"zeros", func(n int) []byte { return make([]byte, n) }},
		{"mixed", func(n int) []byte { return genMixed(n, 14) }},
	}
	for _, m := range roundTripMethods {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			for _, in := range inputs {
				for _, n := range sizes {
					if in.name != "text" && in.name != "mixed" && n > 100 && n != 4096 && n != 65537 && n != 300000 {
						continue
					}
					roundTrip(t, m, in.gen(n))
				}
			}
		})
	}
}

func TestStopFagen(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	data := genMixed(3<<20, 21)
	for _, m := range []string{"tor:1", "tor:2", "tor:3", "tor:5"} {
		packed := codectest.Fagen(t, data, "c", m)
		c, _ := Parse(method.Params(m), nil)
		if err := c.Decode(&limitWriter{n: 100000}, bytes.NewReader(packed)); !errors.Is(err, method.ErrStop) {
			t.Errorf("%s: err = %v, want ErrStop", m, err)
		}
	}
}

// stats counts the elements a stream is made of.
type stats struct{ literals, matches, tables, repChars int }

type countSource struct {
	source
	st *stats
}

func (c countSource) decode(ev []element) (int, error) {
	n, err := c.source.decode(ev)
	for _, e := range ev[:n] {
		switch {
		case e.dist == 0:
			c.st.literals++
		case e.len > impossibleLen && e.len <= impossibleLen+maxTableRow:
			c.st.tables++
		case e.len < impossibleLen:
			c.st.matches++
		}
	}
	return n, err
}

// decodeStats decodes a stream like Decode does and reports what it contained.
func decodeStats(packed []byte) ([]byte, stats, error) {
	var st stats
	s, bufSize, err := openStream(newInStream(bytes.NewReader(packed), nil))
	if err != nil {
		return nil, st, err
	}
	var out bytes.Buffer
	o, err := newOutput(&out, bufSize, nil)
	if err != nil {
		return nil, st, err
	}
	err = o.run(countSource{s, &st})
	return out.Bytes(), st, err
}

func TestLargeFagen(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	tests := []struct {
		method string
		size   int
		gen    func(n int) []byte
		tables bool // the data must be found to hold tables
	}{
		// The ring is the larger of the buffer and 8 MiB; data several times that wraps it.
		{"tor:5:b9mb", 30 << 20, func(n int) []byte { return genMixed(n, 31) }, true},
		{"tor:5:b9mb", 30 << 20, func(n int) []byte { return genPeriodic(n, 100000, 32) }, false},
		{"tor:5", 40 << 20, func(n int) []byte { return genMixed(n, 33) }, true},
		{"tor:4", 20 << 20, func(n int) []byte { return genMixed(n, 34) }, true},
		{"tor:3:b64kb", 20 << 20, func(n int) []byte { return genMixed(n, 35) }, true},
		{"tor:6:b10mb", 30 << 20, func(n int) []byte { return genMixed(n, 36) }, true},
		{"tor:7:b12mb", 30 << 20, func(n int) []byte { return genMixed(n, 37) }, true},
		{"tor:9:b20mb", 50 << 20, func(n int) []byte { return genPeriodic(n, 5<<20, 38) }, false},
		{"tor:2", 20 << 20, func(n int) []byte { return genMixed(n, 39) }, false},
		{"tor:1", 20 << 20, func(n int) []byte { return genMixed(n, 40) }, false},
		// Tables spanning the write points.
		{"tor:5", 30 << 20, func(n int) []byte { return genTable32(n, 41) }, true},
		{"tor:5:b9mb", 30 << 20, func(n int) []byte { return genTable16(n, 42) }, true},
		{"tor:4", 30 << 20, func(n int) []byte { return genTable32(n, 43) }, true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%d", tt.method, tt.size>>20), func(t *testing.T) {
			t.Parallel()
			data := tt.gen(tt.size)
			packed := codectest.Fagen(t, data, "c", tt.method)
			got, st, err := decodeStats(packed)
			if err != nil {
				t.Fatalf("decoding %d bytes (packed %d): %v", len(data), len(packed), err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("decoded %d bytes (packed %d), want %d: first difference at %d", len(got), len(packed), len(data), firstDiff(got, data))
			}
			t.Logf("packed %d -> %d: %+v", len(data), len(packed), st)
			if tt.tables && st.tables == 0 {
				t.Errorf("the stream holds no data tables")
			}
		})
	}
}
