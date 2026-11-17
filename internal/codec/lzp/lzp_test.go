package lzp_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/n0madic/go-unarc/internal/codec/lzp"
	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

func TestParse(t *testing.T) {
	t.Parallel()
	const defaultMem = 2*8<<20 + 8<<18 // two 8 MB buffers and 2^18 eight-byte table entries
	tests := []struct {
		method string
		ok     bool
		mem    uint64
	}{
		{"lzp", true, defaultMem},
		{"lzp:1mb", true, 2<<20 + 8<<18},
		{"lzp:b1mb:h10", true, 2<<20 + 8<<10},
		{"lzp:64:h20", true, 2*8<<20 + 8<<20},
		{"lzp:l32:d1mb:s16", true, defaultMem},
		{"lzp:99%", true, defaultMem},
		{"lzp:1mb:90%:32:h12:d4k:s8", true, 2<<20 + 8<<12},
		{"lzp:h31", true, 2 * 8 << 20},    // 1<<31 overflows an int in C: the table term wraps to 0
		{"lzp:h32", true, 2*8<<20 + 8},    // the shift count is taken modulo 32
		{"lzp:h40", true, 2*8<<20 + 8<<8}, // ditto
		{"lzp:2gb", true, 0 + 8<<18},      // 2*2 GB wraps to 0 in 32-bit arithmetic
		{"lzp:4gb", true, 8 << 18},        // 4 GB wraps to 0
		{"lzp:x", false, 0},
		{"lzp:", false, 0},
		{"lzp:h", false, 0},
		{"lzp:hx", false, 0},
		{"lzp:d", false, 0},
		{"lzp:s", false, 0},
		{"lzp:l", false, 0},
		{"lzp:%", false, 0},
		{"lzp:1mb:", false, 0},
		{"lzp:a1", false, 0}, // "a" belongs to rep
		{"lz", false, 0},
		{"lzpp", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			c, ok := lzp.Parse(method.Params(tt.method), &method.Env{})
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
	pieces := []string{"b", "b8mb", "8mb", "16", "b16", "l", "l16", "d", "d1k", "dx", "s", "s5", "sx", "h", "h20", "h31", "h32", "h99",
		"5%", "%", "x%", "100%", "", "=3", "b=2m", "99999999999", "4gb", "2gb", "^", "b^", "5q", "k", "m", "g", "bb", "x", "a2"}
	cases := []string{"lzp"}
	for _, a := range pieces {
		cases = append(cases, "lzp:"+a)
		for _, b := range pieces {
			cases = append(cases, "lzp:"+a+":"+b)
		}
	}
	codectest.CheckAgainstC(t, lzp.Parse, cases)
}

// stored and compressed wrap a payload into a block of the stream.
func stored(payload []byte) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, uint32(-len(payload))), payload...)
}

func compressed(payload []byte) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, uint32(len(payload))), payload...)
}

// A hand-made block: a prefix whose bytes 1..4 equal bytes 8..11 makes the table (which starts out pointing
// at position 5) predict a match at position 12, which the flag byte then confirms.
var prefix = []byte("aBCDEfgHBCDE")

func TestDecodeKnown(t *testing.T) {
	t.Parallel()
	periodic := func(n int) string { // what a match of length n from position 5 adds: "fgHBCDE" repeated
		return strings.Repeat("fgHBCDE", n/7+1)[:n]
	}
	tests := []struct {
		name   string
		method string
		stream []byte
		want   string
	}{
		{"empty", "lzp", nil, ""},
		{"stored", "lzp", stored([]byte("hello")), "hello"},
		{"stored twice", "lzp", append(stored([]byte("ab")), stored([]byte("cd"))...), "abcd"},
		{"literals", "lzp", compressed(append(bytes.Clone(prefix), "xyz"...)), string(prefix) + "xyz"},
		// A flag whose context was never seen is a plain literal.
		{"flag literal", "lzp", compressed(append(bytes.Clone(prefix), 'x', 0xB5, 'q')), string(prefix) + "x\xb5q"},
		// Flag, predicted context, record 255: the flag byte is a literal after all.
		{"escaped flag", "lzp", compressed(append(bytes.Clone(prefix), 0xB5, 255)), string(prefix) + "\xb5"},
		// Record 5: match length (64-1)+5 = 68 with the default minimal length of 64.
		{"match", "lzp", compressed(append(bytes.Clone(prefix), 0xB5, 5)), string(prefix) + periodic(68)},
		{"match min length", "lzp:l10", compressed(append(bytes.Clone(prefix), 0xB5, 1)), string(prefix) + periodic(10)},
		// A zero record byte adds 254 and moves on to the next record byte.
		{"long match", "lzp", compressed(append(bytes.Clone(prefix), 0xB5, 3, 0, 0)), string(prefix) + periodic(63+3+254*2)},
		{"match then literals", "lzp:l10", compressed(append(bytes.Clone(prefix), 0xB5, 'x', 'y', 2)), string(prefix) + periodic(11) + "xy"},
		{"exact fit", "lzp:80b", compressed(append(bytes.Clone(prefix), 0xB5, 5)), string(prefix) + periodic(68)},
		// Beyond the barrier distance the smallest length applies: position 5 is 7 bytes away, barrier 3.
		{"smallest length", "lzp:l50:d3b:s7", compressed(append(bytes.Clone(prefix), 0xB5, 1)), string(prefix) + periodic(7)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := codectest.Decode(codectest.Parse(t, tt.method), tt.stream)
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
	flagged := func(rec ...byte) []byte { return compressed(append(append(bytes.Clone(prefix), 0xB5), rec...)) }
	tests := []struct {
		name   string
		method string
		stream []byte
	}{
		{"partial size", "lzp", []byte{1, 2}},
		{"truncated stored", "lzp", stored([]byte("hello"))[:7]},
		{"truncated compressed", "lzp", compressed(append(bytes.Clone(prefix), "xyz"...))[:10]},
		{"stored of 2 GB", "lzp", u32(1 << 31)},
		{"stored beyond the data", "lzp", u32(0xfffffff0, 1, 2, 3)},
		{"zero size", "lzp", compressed(nil)},
		{"too short", "lzp", compressed(prefix[:5])},
		{"only the prefix", "lzp", compressed(prefix)},
		{"flag without record", "lzp", compressed(append(bytes.Clone(prefix), 0xB5))},
		{"record runs into the data", "lzp", flagged(0)},
		{"zero records only", "lzp", flagged(0, 0, 0)},
		{"match beyond block size", "lzp:79b", flagged(5)},
		{"literals beyond block size", "lzp:13b", compressed(append(bytes.Clone(prefix), "xy"...))},
		{"prefix beyond block size", "lzp:11b", compressed(append(bytes.Clone(prefix), "xy"...))},
		{"huge length", "lzp", flagged(append([]byte{5}, make([]byte, 40000)...)...)},
		{"length wraps to zero", "lzp:l0", flagged(1)},
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

func TestHashTableTooLarge(t *testing.T) {
	t.Parallel()
	_, err := codectest.Decode(codectest.Parse(t, "lzp:h31"), compressed(append(bytes.Clone(prefix), "xyz"...)))
	if err == nil || errors.Is(err, method.ErrBadData) {
		t.Fatalf("err = %v, want a failure that is not ErrBadData", err)
	}
	// Stored blocks need no table.
	if got, err := codectest.Decode(codectest.Parse(t, "lzp:h31"), stored([]byte("ok"))); err != nil || string(got) != "ok" {
		t.Fatalf("stored block: %q, %v", got, err)
	}
}

func TestDecodeStopsOnWriteError(t *testing.T) {
	t.Parallel()
	err := codectest.Parse(t, "lzp").Decode(&codectest.LimitWriter{Max: 0}, bytes.NewReader(stored([]byte("abc"))))
	if !errors.Is(err, codectest.ErrLimit) {
		t.Fatalf("err = %v, want the writer's error", err)
	}
	err = codectest.Parse(t, "lzp").Decode(&codectest.LimitWriter{Max: 0}, bytes.NewReader(compressed(append(bytes.Clone(prefix), 'x'))))
	if !errors.Is(err, codectest.ErrLimit) {
		t.Fatalf("err = %v, want the writer's error", err)
	}
}

// flagHeavy makes data in which the match flag byte appears at about every context the table will predict.
func flagHeavy(rng *rand.Rand, size int) []byte {
	var out []byte
	pattern := []byte("abcdefgh\xb5ijklmnop\xb5\xb5qrstuvwxyz\xb5")
	for len(out) < size {
		switch rng.IntN(4) {
		case 0:
			out = append(out, pattern...)
		case 1:
			out = append(out, pattern[:1+rng.IntN(len(pattern))]...)
		case 2:
			out = append(out, 0xb5, byte(rng.IntN(256)))
		default:
			piece := make([]byte, rng.IntN(40))
			codectest.FillBytes(rng, piece)
			out = append(out, piece...)
		}
	}
	return out[:size]
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
	text := codectest.SourceText(t, 300<<10)
	random := make([]byte, 40<<10)
	codectest.FillBytes(rng, random)
	inputs := map[string][]byte{
		"empty":      nil,
		"one byte":   {42},
		"short":      text[:31],
		"32 bytes":   text[:32],
		"text":       text,
		"flags":      flagHeavy(rng, 100<<10),
		"random":     random,
		"zeros":      make([]byte, 200<<10),
		"repetitive": repetitive(rng, 400<<10),
		"odd":        repetitive(rng, 99999),
	}
	// Small hash tables make the table update interval n1 grow, small blocks give several blocks per input,
	// the barrier and smallest length switch the match length with the distance.
	methods := []string{"lzp", "lzp:64kb:l16:h10", "lzp:h4", "lzp:100kb:32:h6", "lzp:8kb:l8:h12:d1000:s4", "lzp:1mb:l24:h16:d100kb:s12", "lzp:16kb:99%:l32:h8"}
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
	random := make([]byte, 3000)
	codectest.FillBytes(rng, random)
	cases := []struct {
		name, method string
		in           []byte
	}{
		{"text", "lzp", codectest.SourceText(t, 30<<10)},
		{"blocks", "lzp:8kb:l16:h10", codectest.SourceText(t, 40<<10)},
		{"flags", "lzp:h6:l8", flagHeavy(rng, 20<<10)},
		{"barrier", "lzp:16kb:l32:d500:s8:h8", repetitive(rng, 50<<10)},
		{"random", "lzp", random},
		{"empty", "lzp", nil},
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
	for _, tt := range [][]byte{
		compressed(append(bytes.Clone(prefix), 0xB5, 5)),
		compressed(append(bytes.Clone(prefix), 0xB5, 3, 0, 0)),
		compressed(append(bytes.Clone(prefix), 0xB5, 255)),
		stored([]byte("hello")),
		{},
	} {
		f.Add(tt)
	}
	if files, _ := filepath.Glob(filepath.Join("testdata", "*.bin")); len(files) > 0 {
		for _, file := range files {
			if b, err := os.ReadFile(file); err == nil && len(b) < 8<<10 {
				f.Add(b)
			}
		}
	}
	f.Fuzz(func(t *testing.T, stream []byte) {
		// Small tables keep every run cheap; the output is capped.
		c, _ := method.Lookup("lzp:64kb:h12:l10", &method.Env{})
		_ = c.Decode(&codectest.LimitWriter{Max: 4 << 20}, bytes.NewReader(stream))
	})
}

// BenchmarkDecode times the Go decoder on a stream the C encoder made of a 40 MB text.
func BenchmarkDecode(b *testing.B) {
	in := codectest.SourceText(b, 40<<20)
	stream := codectest.Fagen(b, in, "c", "lzp")
	if path := os.Getenv("BENCH_STREAM"); path != "" {
		if err := os.WriteFile(path, stream, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	c := codectest.Parse(b, "lzp")
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
