package rep_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/n0madic/go-unarc/internal/codec/rep"
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
		{"rep", true, 64 << 20},
		{"rep:1gb", true, 1 << 30},
		{"rep:b256mb", true, 256 << 20},
		{"rep:24", true, 64 << 20}, // a bare number is the minimal match length, not the size
		{"rep:b24", true, 1 << 24},
		{"rep:64kb:l32", true, 64 << 10},
		{"rep:64kb:32", true, 64 << 10},
		{"rep:5%", true, 64 << 20},
		{"rep:1mb:99%:128:d4mb:s64:h20:a3", true, 1 << 20},
		{"rep:d1gb", true, 64 << 20},
		{"rep:x", false, 0},
		{"rep:", false, 0},
		{"rep:b", false, 0},
		{"rep:l", false, 0},
		{"rep:lx", false, 0},
		{"rep:s1k", false, 0},
		{"rep:%", false, 0},
		{"rep:a%", false, 0},
		{"rep:5%%", false, 0},
		{"rep:1mb:", false, 0},
		{"rep:b1mb:y", false, 0},
		{"re", false, 0},
		{"repp", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			c, ok := rep.Parse(method.Params(tt.method), &method.Env{})
			if ok != tt.ok {
				t.Fatalf("accepted = %v, want %v", ok, tt.ok)
			}
			if ok && c.DecompressionMem() != tt.mem {
				t.Errorf("DecompressionMem = %d, want %d", c.DecompressionMem(), tt.mem)
			}
		})
	}
}

// TestParseMatchesC compares what the C parser accepts, and the memory it reports, on generated method strings.
func TestParseMatchesC(t *testing.T) {
	codectest.FagenPath(t)
	pieces := []string{"b", "b8mb", "8mb", "16", "b16", "l", "l16", "d", "d1k", "dx", "s", "s5", "sx", "h", "h20", "a", "a2",
		"5%", "%", "x%", "100%", "", "=3", "b=2m", "99999999999", "4gb", "2gb", "^", "b^", "5q", "k", "m", "g", "bb", "x"}
	cases := []string{"rep"}
	for _, a := range pieces {
		cases = append(cases, "rep:"+a)
		for _, b := range pieces {
			cases = append(cases, "rep:"+a+":"+b)
		}
	}
	codectest.CheckAgainstC(t, rep.Parse, cases)
}

// step is one literal run followed by one match.
type step struct {
	lit        []byte
	off, count int
}

// buildBlock encodes steps plus a final literal run as one block of the stream.
func buildBlock(steps []step, tail []byte) []byte {
	var lens, offsets, datalens, literals []byte
	for _, s := range steps {
		lens = binary.LittleEndian.AppendUint32(lens, uint32(s.count))
		offsets = binary.LittleEndian.AppendUint32(offsets, uint32(s.off))
		datalens = binary.LittleEndian.AppendUint32(datalens, uint32(len(s.lit)))
		literals = append(literals, s.lit...)
	}
	datalens = binary.LittleEndian.AppendUint32(datalens, uint32(len(tail)))
	literals = append(literals, tail...)
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(steps)))
	b = append(b, lens...)
	b = append(b, offsets...)
	b = append(b, datalens...)
	return append(b, literals...)
}

// streamOf wraps blocks into a stream with the given window size.
func streamOf(window uint32, blocks ...[]byte) []byte {
	s := binary.LittleEndian.AppendUint32(nil, window)
	for _, b := range blocks {
		s = binary.LittleEndian.AppendUint32(s, uint32(len(b)))
		s = append(s, b...)
	}
	return binary.LittleEndian.AppendUint32(s, 0)
}

// ringModel is a byte-by-byte model of the decoder: the window is a ring, every match copies forward.
type ringModel struct {
	ring  []byte
	pos   int
	total int
	out   []byte
}

func (m *ringModel) put(b byte) {
	m.ring[m.pos] = b
	m.pos = (m.pos + 1) % len(m.ring)
	m.total++
	m.out = append(m.out, b)
}

func (m *ringModel) match(off, n int) {
	for range n {
		m.put(m.ring[(m.pos-off%len(m.ring)+len(m.ring))%len(m.ring)])
	}
}

// randomStream makes a random valid stream for a window of the given size, and the output it must decode to.
func randomStream(rng *rand.Rand, window int) (stream, want []byte) {
	m := &ringModel{ring: make([]byte, window)}
	var blocks [][]byte
	for range 1 + rng.IntN(4) {
		var steps []step
		for range rng.IntN(12) {
			s := step{lit: make([]byte, rng.IntN(window/2+2))}
			if rng.IntN(4) == 0 {
				s.lit = s.lit[:rng.IntN(min(3, len(s.lit)+1))]
			}
			codectest.FillBytes(rng, s.lit)
			for _, b := range s.lit {
				m.put(b)
			}
			if m.total > 0 {
				if m.total < window {
					s.off = 1 + rng.IntN(m.total) // the window has not wrapped: only decoded data can be referenced
				} else {
					s.off = rng.IntN(window + 1) // any distance including 0 and the window size
				}
				switch rng.IntN(4) {
				case 0:
					s.count = rng.IntN(5)
				case 1:
					s.count = rng.IntN(3 * window)
				default:
					s.count = rng.IntN(window/2 + 1)
				}
			}
			m.match(s.off, s.count)
			steps = append(steps, s)
		}
		tail := make([]byte, rng.IntN(window/2+1))
		codectest.FillBytes(rng, tail)
		for _, b := range tail {
			m.put(b)
		}
		blocks = append(blocks, buildBlock(steps, tail))
	}
	return streamOf(uint32(window), blocks...), m.out
}

func TestDecodeAgainstModel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 3000 {
		window := 1 + rng.IntN(64)
		if i%10 == 0 {
			window = 1 + rng.IntN(5000)
		}
		stream, want := randomStream(rng, window)
		got, err := codectest.Decode(codectest.Parse(t, "rep:64kb"), stream)
		if err != nil {
			t.Fatalf("case %d (window %d): %v", i, window, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("case %d (window %d): output differs\n got %q\nwant %q", i, window, got, want)
		}
	}
}

func TestDecodeKnown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stream []byte
		want   string
	}{
		{"literals", streamOf(16, buildBlock(nil, []byte("hello"))), "hello"},
		{"empty", streamOf(16), ""},
		{"overlap", streamOf(16, buildBlock([]step{{[]byte("ab"), 2, 7}}, []byte("!"))), "ababababa!"},
		{"run", streamOf(16, buildBlock([]step{{[]byte("z"), 1, 5}}, nil)), "zzzzzz"},
		// Window of 8: after "01234567" the ring is full, "89" overwrites "01", then a match 4 back sees "4567".
		{"wrap", streamOf(8, buildBlock(nil, []byte("0123456789")), buildBlock([]step{{nil, 4, 4}}, nil)), "01234567896789"},
		// A match from before the wrap point: offset 7 from position 2 reaches ring[3].
		{"wrap offset", streamOf(8, buildBlock([]step{{[]byte("abcdefghij"), 7, 3}}, nil)), "abcdefghijdef"},
		{"two blocks", streamOf(16, buildBlock(nil, []byte("abc")), buildBlock([]step{{nil, 3, 3}}, []byte("."))), "abcabc."},
		{"zero length match", streamOf(16, buildBlock([]step{{[]byte("ab"), 99, 0}}, []byte("c"))), "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := codectest.Decode(codectest.Parse(t, "rep"), tt.stream)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()
	u32 := func(vs ...uint32) []byte {
		var b []byte
		for _, v := range vs {
			b = binary.LittleEndian.AppendUint32(b, v)
		}
		return b
	}
	const neg = 1 << 31
	good := streamOf(16, buildBlock([]step{{[]byte("ab"), 2, 4}}, []byte("c")))
	tests := []struct {
		name   string
		method string
		stream []byte
	}{
		{"empty stream", "rep", nil},
		{"partial window size", "rep", []byte{1, 2}},
		{"no terminator", "rep", good[:len(good)-4]},
		{"partial terminator", "rep", good[:len(good)-2]},
		{"truncated block", "rep", good[:len(good)-6]},
		{"window beyond the method's", "rep:1kb", u32(2000, 0)},
		{"block of 2 GB", "rep", u32(16, neg, 0)},
		{"block of 4 GB", "rep", u32(16, 0xffffffff)},
		{"tiny block", "rep", u32(16, 3, 0)},
		{"negative match count", "rep", streamOf(16, u32(neg, 0, 0, 0))},
		{"matches do not fit", "rep", streamOf(16, u32(5, 0, 0, 0))},
		{"literal overrun", "rep", streamOf(16, u32(0, 9))},
		{"negative literal run", "rep", streamOf(16, u32(0, neg))},
		{"negative match length", "rep", streamOf(16, append(u32(1, neg|1, 1, 1, 0), 'a'))},
		{"offset before the data", "rep", streamOf(16, buildBlock([]step{{[]byte("ab"), 3, 1}}, nil))},
		{"offset zero", "rep", streamOf(16, buildBlock([]step{{[]byte("ab"), 0, 1}}, nil))},
		{"offset beyond the window", "rep", streamOf(16, buildBlock([]step{{[]byte("ab"), 17, 1}}, nil))},
		{"offset 2 GB", "rep", streamOf(16, buildBlock([]step{{[]byte("ab"), neg, 1}}, nil))},
		{"data for a zero window", "rep", streamOf(0, buildBlock(nil, []byte("a")))},
		{"match in a zero window", "rep", streamOf(0, buildBlock([]step{{nil, 1, 1}}, nil))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := codectest.Decode(codectest.Parse(t, tt.method), tt.stream)
			if !errors.Is(err, method.ErrBadData) {
				t.Fatalf("err = %v, want ErrBadData", err)
			}
		})
	}
}

func TestDecodeStopsOnWriteError(t *testing.T) {
	t.Parallel()
	stream, _ := randomStream(rand.New(rand.NewPCG(3, 4)), 100)
	err := codectest.Parse(t, "rep").Decode(&codectest.LimitWriter{Max: 0}, bytes.NewReader(stream))
	if !errors.Is(err, codectest.ErrLimit) {
		t.Fatalf("err = %v, want the writer's error", err)
	}
}

// TestLargeWindowIsLazy: a stream that claims the biggest window allowed gets memory as it produces output.
func TestLargeWindowIsLazy(t *testing.T) {
	stream := streamOf(1<<30, buildBlock([]step{{[]byte("a"), 1, 100}}, nil))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := codectest.Decode(codectest.Parse(t, "rep:1gb"), stream)
	runtime.ReadMemStats(&after)
	if err != nil || len(got) != 101 {
		t.Fatalf("got %d bytes, %v", len(got), err)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 16<<20 {
		t.Errorf("decoding 101 bytes allocated %d bytes", grown)
	}
}

// repetitive makes data in which pieces of earlier data come back at various distances.
func repetitive(rng *rand.Rand, size int) []byte {
	var out []byte
	for len(out) < size {
		switch {
		case len(out) < 100 || rng.IntN(3) == 0:
			piece := make([]byte, 1+rng.IntN(3000))
			codectest.FillBytes(rng, piece)
			out = append(out, piece...)
		default:
			start := rng.IntN(len(out) - 50)
			n := 50 + rng.IntN(min(len(out)-start, 6000))
			out = append(out, out[start:start+n]...)
		}
	}
	return out[:size]
}

// TestRoundTripFagen decodes what the C encoder makes of various inputs.
func TestRoundTripFagen(t *testing.T) {
	codectest.FagenPath(t)
	rng := rand.New(rand.NewPCG(5, 6))
	text := codectest.SourceText(t, 200<<10)
	random := make([]byte, 40<<10)
	codectest.FillBytes(rng, random)
	inputs := map[string][]byte{
		"empty":      nil,
		"one byte":   {42},
		"short":      text[:31],
		"text":       text,
		"text twice": append(bytes.Clone(text), text...),
		"random":     random,
		"zeros":      make([]byte, 200<<10),
		"repetitive": repetitive(rng, 600<<10),
		"odd":        repetitive(rng, 99999),
	}
	methods := []string{"rep", "rep:64kb:l32:s32", "rep:100kb:64", "rep:16kb:l16:s16:a4", "rep:1mb:l48:d100kb:s24:h12"}
	for name, in := range inputs {
		for _, m := range methods {
			t.Run(name+"/"+m, func(t *testing.T) {
				stream := codectest.Fagen(t, in, "c", m)
				got, err := codectest.Decode(codectest.Parse(t, m), stream)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, in) {
					t.Fatalf("round trip differs: got %d bytes (%s), want %d bytes (%s)", len(got), codectest.Sum(got), len(in), codectest.Sum(in))
				}
			})
		}
	}
}

// TestCommittedStreams decodes the streams under testdata, which fagen made once, and checks their SHA-256.
func TestCommittedStreams(t *testing.T) {
	t.Parallel()
	manifest, err := os.ReadFile(filepath.Join("testdata", "manifest.txt"))
	if err != nil {
		t.Skip("no testdata/manifest.txt")
	}
	for line := range strings.Lines(string(manifest)) {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		file, m, hash, size := f[0], f[1], f[2], f[3]
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			stream, err := os.ReadFile(filepath.Join("testdata", file))
			if err != nil {
				t.Fatal(err)
			}
			got, err := codectest.Decode(codectest.Parse(t, m), stream)
			if err != nil {
				t.Fatal(err)
			}
			if strconv.Itoa(len(got)) != size || codectest.Sum(got) != hash {
				t.Fatalf("got %d bytes %s, want %s bytes %s", len(got), codectest.Sum(got), size, hash)
			}
		})
	}
}

// TestWriteTestdata regenerates testdata with fagen: UPDATE_TESTDATA=1 go test -run WriteTestdata
func TestWriteTestdata(t *testing.T) {
	if os.Getenv("UPDATE_TESTDATA") == "" {
		t.Skip("set UPDATE_TESTDATA=1 to regenerate testdata")
	}
	rng := rand.New(rand.NewPCG(7, 8))
	text := codectest.SourceText(t, 30<<10)
	cases := []struct {
		name, method string
		in           []byte
	}{
		{"repeat", "rep:l32:s32", append(bytes.Clone(text), text...)},
		{"wrap", "rep:16kb:l32:s32", repetitive(rng, 120<<10)},
		{"zeros", "rep", make([]byte, 50<<10)},
		{"text", "rep", text[:10<<10]},
		{"empty", "rep", nil},
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	var manifest strings.Builder
	for _, c := range cases {
		stream := codectest.Fagen(t, c.in, "c", c.method)
		file := c.name + ".bin"
		if err := os.WriteFile(filepath.Join("testdata", file), stream, 0o644); err != nil {
			t.Fatal(err)
		}
		manifest.WriteString(strings.Join([]string{file, c.method, codectest.Sum(c.in), strconv.Itoa(len(c.in))}, "\t") + "\n")
	}
	if err := os.WriteFile(filepath.Join("testdata", "manifest.txt"), []byte(manifest.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func FuzzDecode(f *testing.F) {
	rng := rand.New(rand.NewPCG(9, 10))
	for range 20 {
		stream, _ := randomStream(rng, 1+rng.IntN(200))
		f.Add(stream)
	}
	if files, _ := filepath.Glob(filepath.Join("testdata", "*.bin")); len(files) > 0 {
		for _, file := range files {
			if b, err := os.ReadFile(file); err == nil && len(b) < 8<<10 {
				f.Add(b)
			}
		}
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, stream []byte) {
		// A small window keeps every run cheap; the output is capped because matches can be very long.
		c, _ := method.Lookup("rep:1mb", &method.Env{})
		_ = c.Decode(&codectest.LimitWriter{Max: 4 << 20}, bytes.NewReader(stream))
	})
}

// BenchmarkDecode times the Go decoder on a stream the C encoder made of a 32 MB file.
func BenchmarkDecode(b *testing.B) {
	in := repetitive(rand.New(rand.NewPCG(11, 12)), 32<<20)
	stream := codectest.Fagen(b, in, "c", "rep:64mb:l64:s64")
	if path := os.Getenv("BENCH_STREAM"); path != "" {
		if err := os.WriteFile(path, stream, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	c := codectest.Parse(b, "rep:64mb:l64:s64")
	b.SetBytes(int64(len(in)))
	sink := &codectest.MemWriter{Buf: make([]byte, 0, len(in))}
	b.ResetTimer()
	best := time.Duration(math.MaxInt64) // the C timing tool reports the best run, so do the same
	for range b.N {
		sink.Reset()
		start := time.Now()
		if err := c.Decode(sink, bytes.NewReader(stream)); err != nil {
			b.Fatal(err)
		}
		best = min(best, time.Since(start))
	}
	b.ReportMetric(float64(best)/1e6, "best-ms")
}
