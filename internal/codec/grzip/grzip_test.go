package grzip

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

// fagen runs "fagen <verb> <method>" with stdin as input. The C encoder crashes now and then with tiny block
// sizes (a race in its worker threads), so a failed run is repeated.
func fagen(tb testing.TB, verb, meth string, stdin []byte) []byte {
	tb.Helper()
	var r codectest.Result
	for range 5 {
		if r = codectest.FagenTry(tb, stdin, verb, meth); r.OK() {
			return r.Out
		}
	}
	tb.Fatalf("fagen %s %s: exit %d: %s", verb, meth, r.Exit, r.Stderr)
	return nil
}

func mustParse(tb testing.TB, meth string) *codec {
	tb.Helper()
	c, ok := Parse(method.Params(meth), method.NewEnv())
	if !ok {
		tb.Fatalf("Parse(%q) rejected", meth)
	}
	return c.(*codec)
}

// decode runs the codec on stream with the given number of workers.
func decode(c *codec, threads int, stream []byte) ([]byte, error) {
	cc := *c
	cc.threads = threads
	var out bytes.Buffer
	err := cc.Decode(&out, bytes.NewReader(stream))
	return out.Bytes(), err
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method  string
		ok      bool
		block   uint32 // expected block size
		comment string
	}{
		{"grzip", true, 8 << 20, "defaults"},
		{"grzip:m1", true, 8 << 20, ""},
		{"grzip:m4:l:s:a:d:p", true, 8 << 20, "all switches"},
		{"grzip:m9", true, 8 << 20, "method number is not range checked"},
		{"grzip:b1mb", true, 1 << 20, ""},
		{"grzip:b=64kb", true, 64 << 10, "= prefix"},
		{"grzip:4mb", true, 4 << 20, "bare size"},
		{"grzip:24", true, 8 << 20, "bare number is the match length"},
		{"grzip:64kb:m2:l16:h20", true, 64 << 10, ""},
		{"grzip:b24", true, 16 << 20, "power of two"},
		{"grzip:b3", true, 8, "power of two"},
		{"grzip:m", true, 0, "single letter m falls through to a size of 0 mb"},
		{"grzip:b", true, 0, "ditto"},
		{"grzip:h", false, 0, "h is not a size suffix"},
		{"grzip:k", true, 0, "0 kb"},
		{"grzip:p:d", true, 8 << 20, ""},
		{"grzip:foo", false, 0, "not a number or size"},
		{"grzip:mx", false, 0, "bad number"},
		{"grzip:l1000mb", false, 0, "l takes a plain integer"},
		{"grzip:lzp", false, 0, ""},
		{"grzip:sx", false, 0, ""},
		{"grzip:", false, 0, "empty parameter"},
		{"grzip:m1:", false, 0, "empty parameter"},
		{"grzip:b=", false, 0, ""},
		{"grzip:4gb", true, 0, "wraps to 0 in 32 bits"},
		{"grzipp", false, 0, ""},
		{"grzip2", false, 0, ""},
		{"lzma", false, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			c, ok := Parse(method.Params(tt.method), method.NewEnv())
			if ok != tt.ok {
				t.Fatalf("Parse(%q) ok = %v, want %v (%s)", tt.method, ok, tt.ok, tt.comment)
			}
			if !ok {
				return
			}
			if got := c.(*codec).blockSize; got != tt.block {
				t.Errorf("block size %d, want %d", got, tt.block)
			}
		})
	}
}

// TestDecompressionMem compares with the C library (fagen reports it for one thread).
func TestDecompressionMem(t *testing.T) {
	t.Parallel()
	for _, m := range []string{"grzip", "grzip:m3", "grzip:b1mb", "grzip:4mb:a", "grzip:p", "grzip:64", "grzip:b3", "grzip:b24", "grzip:b800mb", "grzip:b4gb"} {
		t.Run(m, func(t *testing.T) {
			c := mustParse(t, m)
			c.threads = 1
			if codectest.HaveFagen(t) {
				want, err := strconv.ParseUint(strings.TrimSpace(string(fagen(t, "mem", m, nil))), 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				if got := c.DecompressionMem(); got != want {
					t.Errorf("DecompressionMem = %d, C says %d", got, want)
				}
			}
			c.threads = 3
			if got, want := c.DecompressionMem(), uint64(c.blockSize*5*3); got != want {
				t.Errorf("with 3 threads: %d, want %d", got, want)
			}
		})
	}
}

// ---------- test inputs ----------

func sourceText(tb testing.TB) []byte {
	tb.Helper()
	return codectest.SourceText(tb, 128<<10)
}

// table builds records of 16/32 bit counters with small deltas, which trigger the delta filter.
func table(width, n int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	out := make([]byte, 0, width*n)
	var v uint32 = 1000
	for i := 0; i < n; i++ {
		v += uint32(rng.Intn(9)) - 3
		for b := 0; b < width; b++ {
			out = append(out, byte(v>>(8*b)))
		}
	}
	return out
}

// walk16 is a bounded random walk of 16-bit words: small deltas, constant high bytes (filter 3).
func walk16(n int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	out := make([]byte, 0, 2*n)
	v := 30000
	for i := 0; i < n; i++ {
		v = min(max(v+rng.Intn(9)-4, 30000), 30500)
		out = append(out, byte(v), byte(v>>8))
	}
	return out
}

// records builds n records of width bytes, of which the first is noise and the rest constant (filters 1 and 2).
func records(width, n int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	out := make([]byte, 0, width*n)
	for i := 0; i < n; i++ {
		out = append(out, byte(rng.Intn(256)))
		for b := 1; b < width; b++ {
			out = append(out, byte(b))
		}
	}
	return out
}

// records4z builds 4-byte records of one noise byte and three zeros (filter 2).
func records4z(n int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	out := make([]byte, 4*n)
	for i := 0; i < n; i++ {
		out[4*i] = byte(rng.Intn(256))
	}
	return out
}

func randomBytes(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func repeat(s string, n int) []byte { return bytes.Repeat([]byte(s), n) }

// skewed bytes: a few common values, long runs, some noise.
func skewed(n int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	b := make([]byte, 0, n)
	for len(b) < n {
		c := byte(rng.Intn(4) * 37)
		if rng.Intn(10) == 0 {
			c = byte(rng.Intn(256))
		}
		run := 1 + rng.Intn(1+rng.Intn(300))
		for i := 0; i < run && len(b) < n; i++ {
			b = append(b, c)
		}
	}
	return b
}

type input struct {
	name string
	data []byte
}

func testInputs(tb testing.TB) []input {
	text := sourceText(tb)
	ins := []input{
		{"empty", nil},
		{"1", []byte{'x'}},
		{"2", []byte("ab")},
		{"31", text[:31]},
		{"32", text[:32]},
		{"33", text[:33]},
		{"1023", text[:1023]},
		{"1024", text[:1024]},
		{"1025", text[:1025]},
		{"text", text},
		{"text7", text[:len(text)-len(text)%8-1]}, // a length of 7 mod 8, inside the text
		{"zeros", make([]byte, 100000)},
		{"abc", repeat("abc", 40000)},
		{"random", randomBytes(70000, 1)},
		{"skewed", skewed(300000, 2)},
		{"table2", table(2, 90000, 3)},
		{"walk16", walk16(40000, 8)},
		{"records2", records(2, 30000, 9)},
		{"records4", records(4, 20000, 10)},
		{"records4z", records4z(20000, 19)},
		{"records4odd", append(records(4, 20000, 10), 9, 9, 9)},
		{"table4", table(4, 60000, 4)},
		{"table4odd", append(table(4, 60000, 5), 1, 2, 3)},
		{"mixed", append(append(append([]byte{}, text...), randomBytes(20000, 6)...), skewed(50000, 7)...)},
	}
	return ins
}

// TestRoundTrip compresses with the C encoder for a matrix of methods and inputs and decodes with the Go decoder
// (single worker and several).
func TestRoundTrip(t *testing.T) {
	if !codectest.HaveFagen(t) {
		t.Skip("fagen not built")
	}
	t.Parallel()
	methods := []string{
		"grzip", "grzip:m1", "grzip:m2", "grzip:m3", "grzip:m4", "grzip:m2:s", "grzip:m4:s",
		"grzip:l", "grzip:m2:l", "grzip:m3:l", "grzip:l16:h10", "grzip:l4:h18", "grzip:l64:h22",
		"grzip:d", "grzip:m2:d", "grzip:m4:d", "grzip:p", "grzip:m1:a",
		"grzip:b64kb", "grzip:m2:b32kb:d:a", "grzip:m3:b100000b:l8:h12", "grzip:b4kb",
	}
	ins := testInputs(t)
	for _, m := range methods {
		for _, in := range ins {
			t.Run(m+"/"+in.name, func(t *testing.T) {
				t.Parallel()
				stream := fagen(t, "c", m, in.data)
				c := mustParse(t, m)
				for _, threads := range []int{1, 4} {
					got, err := decode(c, threads, stream)
					if err != nil {
						t.Fatalf("threads=%d: %v", threads, err)
					}
					if !bytes.Equal(got, in.data) {
						t.Fatalf("threads=%d: output differs (got %d bytes, want %d)", threads, len(got), len(in.data))
					}
				}
			})
		}
	}
}

// ---------- committed streams ----------

type sample struct {
	name   string // file in testdata
	method string
	sha    string // SHA-256 of the decoded data
	size   int
}

// samples lists the committed streams; testdata/samples.txt is written by TestWriteSamples.
func readSamples(tb testing.TB) []sample {
	tb.Helper()
	raw, err := os.ReadFile("testdata/samples.txt")
	if err != nil {
		tb.Fatal(err)
	}
	var out []sample
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			tb.Fatalf("bad samples.txt line %q", line)
		}
		n, _ := strconv.Atoi(f[3])
		out = append(out, sample{name: f[0], method: f[1], sha: f[2], size: n})
	}
	return out
}

func TestSamples(t *testing.T) {
	t.Parallel()
	for _, s := range readSamples(t) {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			stream, err := os.ReadFile(filepath.Join("testdata", s.name))
			if err != nil {
				t.Fatal(err)
			}
			c := mustParse(t, s.method)
			for _, threads := range []int{1, 3} {
				got, err := decode(c, threads, stream)
				if err != nil {
					t.Fatalf("threads=%d: %v", threads, err)
				}
				sum := sha256.Sum256(got)
				if len(got) != s.size || hex.EncodeToString(sum[:]) != s.sha {
					t.Fatalf("threads=%d: decoded %d bytes, sha256 %x; want %d bytes, %s", threads, len(got), sum, s.size, s.sha)
				}
			}
		})
	}
}

// TestWriteSamples regenerates testdata (GRZIP_WRITE_SAMPLES=1, needs fagen).
func TestWriteSamples(t *testing.T) {
	if os.Getenv("GRZIP_WRITE_SAMPLES") == "" {
		t.Skip("set GRZIP_WRITE_SAMPLES=1 to regenerate testdata")
	}
	if !codectest.HaveFagen(t) {
		t.Skip("fagen not built")
	}
	text := sourceText(t)
	type spec struct {
		name, method string
		data         []byte
	}
	specs := []spec{
		{"m1.grz", "grzip:m1", text[:60000]},
		{"m2.grz", "grzip:m2", text[:60000]},
		{"m3.grz", "grzip:m3", text[:60000]},
		{"m4.grz", "grzip:m4", text[:60000]},
		{"m1_strong.grz", "grzip:m1:s", text[:60000]},
		{"m4_strong.grz", "grzip:m4:s", text[:60000]},
		{"nolzp.grz", "grzip:m2:l", text[:40000]},
		{"lzp_small_hash.grz", "grzip:m1:l8:h10", text[:40000]},
		{"blocks.grz", "grzip:m1:b8kb", text[:50000]},
		{"blocks_mixed.grz", "grzip:m2:b6000b", append(append(append([]byte{}, text[:20000]...), randomBytes(9000, 11)...), skewed(20000, 12)...)},
		{"delta16.grz", "grzip:m1:d", walk16(20000, 13)},
		{"reorder1.grz", "grzip:m2:d", records(2, 20000, 17)},
		{"reorder2.grz", "grzip:m1:d", append(records4z(10000, 18), 1, 2, 3)},
		{"delta32.grz", "grzip:m2:d", table(4, 10000, 14)},
		{"delta32_tail.grz", "grzip:m1:d", append(table(4, 5000, 15), 7, 8)},
		{"stored.grz", "grzip:m1", randomBytes(5000, 16)},
		{"tiny.grz", "grzip:m1", []byte("hello, grzip")},
		{"runs.grz", "grzip:m1", append(repeat("a", 100000), repeat("xyz", 5000)...)},
		{"runs_wfc_mtf.grz", "grzip:m2", append(repeat("a", 100000), repeat("xyz", 5000)...)},
	}
	var list strings.Builder
	for _, s := range specs {
		stream := fagen(t, "c", s.method, s.data)
		if err := os.WriteFile(filepath.Join("testdata", s.name), stream, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(s.data)
		fmt.Fprintf(&list, "%s %s %x %d\n", s.name, s.method, sum, len(s.data))
	}
	if err := os.WriteFile("testdata/samples.txt", []byte(list.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------- robustness ----------

// blockKinds lists the kinds of the blocks of a stream (containers also list their sub-blocks).
func blockKinds(tb testing.TB, stream []byte) map[string]bool {
	tb.Helper()
	kinds := map[string]bool{}
	var walk func(b []byte)
	walk = func(b []byte) {
		for len(b) > 0 {
			h, err := parseHeader(b)
			if err != nil {
				tb.Fatal(err)
			}
			switch {
			case h.mode == modeStored && h.aux == 0:
				kinds["stored"] = true
			case h.mode == modeStored:
				kinds["stored-lzp"] = true
			case h.mode == modeContainer:
				kinds[fmt.Sprintf("container-%d", h.aux)] = true
				walk(b[headerSize : headerSize+h.payload])
			default:
				if h.mode&modeST4 != 0 {
					kinds["st4"] = true
				} else if h.fbp&strongFlag != 0 {
					kinds["bwt-strong"] = true
				} else {
					kinds["bwt-fast"] = true
				}
				if h.mode&modeMTF != 0 {
					kinds["mtf"] = true
				} else {
					kinds["wfc"] = true
				}
				if h.mode>>8 != 0 {
					kinds["lzp"] = true
				} else {
					kinds["no-lzp"] = true
				}
			}
			b = b[headerSize+h.payload:]
		}
	}
	walk(stream)
	return kinds
}

// TestSampleKinds makes sure the committed streams exercise every block kind.
func TestSampleKinds(t *testing.T) {
	t.Parallel()
	all := map[string]bool{}
	for _, s := range readSamples(t) {
		stream, err := os.ReadFile(filepath.Join("testdata", s.name))
		if err != nil {
			t.Fatal(err)
		}
		for k := range blockKinds(t, stream) {
			all[k] = true
		}
	}
	for _, k := range []string{"stored", "stored-lzp", "container-1", "container-2", "container-3", "container-4",
		"st4", "bwt-fast", "bwt-strong", "mtf", "wfc", "lzp", "no-lzp"} {
		if !all[k] {
			t.Errorf("no committed sample has a %s block", k)
		}
	}
}

// TestParseVsC compares Parse with the C parser on generated parameter lists: whether the method is accepted
// and the memory it reports.
func TestParseVsC(t *testing.T) {
	if !codectest.HaveFagen(t) {
		t.Skip("fagen not built")
	}
	t.Parallel()
	words := []string{"s", "a", "l", "d", "p", "m1", "m2", "m4", "m9", "m", "b", "h", "b1mb", "b64kb", "b=2mb", "b4", "b24", "bx",
		"l32", "l8", "lx", "l=5", "h15", "h20", "hz", "1", "16", "99", "4mb", "512kb", "1gb", "7b", "24", "=3", "=", "x", "mb", "1m1",
		"0", "00", "2^", "3^", "k", "g", "+", "ll", "ss", "p1", "M1", "4GB", "4g", "5g"}
	rng := rand.New(rand.NewSource(7))
	seen := map[string]bool{}
	var specs []string
	for _, w := range words {
		specs = append(specs, "grzip:"+w)
	}
	for len(specs) < 400 {
		var parts []string
		for i := 0; i <= rng.Intn(4); i++ {
			parts = append(parts, words[rng.Intn(len(words))])
		}
		specs = append(specs, "grzip:"+strings.Join(parts, ":"))
	}
	specs = append(specs, "grzip", "grzip:", "grzip::", "grzip:m1:", "grzipx", "grzi")
	for _, spec := range specs {
		if seen[spec] {
			continue
		}
		seen[spec] = true
		want, err := strconv.ParseUint(strings.TrimSpace(string(fagen(t, "mem", spec, nil))), 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		c, ok := Parse(method.Params(spec), method.NewEnv())
		wantOK := want != 0xFFFFFFFE // what the C library reports for a method it cannot parse
		if ok != wantOK {
			t.Errorf("%q: Parse ok = %v, C accepts = %v", spec, ok, wantOK)
			continue
		}
		if ok {
			cc := c.(*codec)
			cc.threads = 1
			if got := cc.DecompressionMem(); got != want {
				t.Errorf("%q: DecompressionMem = %d, C says %d", spec, got, want)
			}
		}
	}
}

// ---------- reorder filters ----------

// TestStoredBlocks: plain stored blocks yield their payload, even when Size disagrees (as in C); an empty stream
// and zero-length blocks are fine.
func TestStoredBlocks(t *testing.T) {
	t.Parallel()
	c := mustParse(t, "grzip")
	var stream []byte
	stream = append(stream, storedBlock([]byte("hello "))...)
	stream = append(stream, mkBlock(3, modeStored, 0, 0, []byte("world"))...) // Size is ignored
	stream = append(stream, storedBlock(nil)...)
	stream = append(stream, storedBlock([]byte("!"))...)
	got, err := decode(c, 3, stream)
	if err != nil || string(got) != "hello world!" {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := decode(c, 3, nil); err != nil || len(got) != 0 {
		t.Errorf("empty stream: %q, %v", got, err)
	}
}
