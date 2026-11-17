package dict_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/n0madic/go-unarc/internal/codec/dict"
	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

func TestParse(t *testing.T) {
	t.Parallel()
	// Whatever the parameters, the C method reports 1 MB of decompression memory.
	tests := []struct {
		method string
		ok     bool
	}{
		{"dict", true},
		{"dict:p", true},
		{"dict:f", true},
		{"dict:p:f", true},
		{"dict:1mb", true},
		{"dict:b1mb", true},
		{"dict:20", true},
		{"dict:c5:l100:m20:s10:r2", true},
		{"dict:128kb:50%:c8", true},
		{"dict:b", true}, // a lone "b" is not the parameter but a memory size of "0 bytes"
		{"dict:k", true},
		{"dict:m", true},
		{"dict:g", true},
		{"dict:c", false},
		{"dict:l", false},
		{"dict:s", false},
		{"dict:r", false},
		{"dict:cx", false},
		{"dict:bx", false},
		{"dict:pp", false},
		{"dict:fx", false},
		{"dict:x", false},
		{"dict:", false},
		{"dict:%", false},
		{"dict:5%%", false},
		{"dict:1mb:", false},
		{"dic", false},
		{"dictt", false},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			c, ok := dict.Parse(method.Params(tt.method), &method.Env{})
			if ok != tt.ok {
				t.Fatalf("accepted = %v, want %v", ok, tt.ok)
			}
			if ok && c.DecompressionMem() != 1<<20 {
				t.Errorf("DecompressionMem = %d, want 1 MB", c.DecompressionMem())
			}
		})
	}
}

// TestParseMatchesC compares what the C parser accepts, and the memory it reports, on generated method strings.
func TestParseMatchesC(t *testing.T) {
	codectest.FagenPath(t)
	pieces := []string{"p", "f", "b", "b8mb", "8mb", "16", "b16", "c", "c16", "l", "l9", "m", "m9", "s", "s9", "r", "r0", "x", "cx",
		"5%", "%", "x%", "100%", "", "=3", "b=2m", "99999999999", "4gb", "^", "b^", "5q", "k", "g", "bb", "pp", "ff", "a2"}
	cases := []string{"dict"}
	for _, a := range pieces {
		cases = append(cases, "dict:"+a)
		for _, b := range pieces {
			cases = append(cases, "dict:"+a+":"+b)
		}
	}
	codectest.CheckAgainstC(t, dict.Parse, cases)
}

// spec describes a dictionary for the test encoder.
type spec struct {
	one    map[byte]string    // words coded by one byte
	two    map[[2]byte]string // words coded by two bytes
	sep    byte               // word separator, which no word contains
	escape byte               // prefix of the codes of single bytes
}

// dictionary encodes the dictionary part of a block the way phase5 of the C encoder does.
func (s spec) dictionary() []byte {
	var out []byte
	rows := map[byte]bool{}
	for k := range s.two {
		rows[k[0]] = true
	}
	for i := range 256 {
		switch {
		case rows[byte(i)]:
			out = append(out, 1)
		default:
			out = append(out, byte(len(s.one[byte(i)])))
		}
	}
	rowList := slices.Sorted(func(yield func(byte) bool) {
		for r := range rows {
			if !yield(r) {
				return
			}
		}
	})
	prefixLen := func(a, b string) int {
		n := 0
		for n < len(a) && n < len(b) && a[n] == b[n] {
			n++
		}
		return n
	}
	var prev *string
	var prefixes, texts []byte
	for _, r := range rowList {
		for j := range 256 {
			w, ok := s.two[[2]byte{r, byte(j)}]
			n := 0
			if ok && prev != nil {
				n = prefixLen(w, *prev)
			}
			prefixes = append(prefixes, byte(n))
			if ok {
				texts = append(texts, w[n:]...)
				prev = &w
			} else {
				prev = nil
			}
			texts = append(texts, s.sep)
		}
	}
	out = append(out, prefixes...)
	for i := range 256 {
		if !rows[byte(i)] {
			out = append(out, s.one[byte(i)]...)
		}
	}
	out = append(out, s.sep)
	out = append(out, texts...)
	return append(out, s.escape)
}

// block is a compressed block: the dictionary followed by the text.
func (s spec) block(text []byte) []byte {
	return compressed(append(s.dictionary(), text...))
}

func stored(payload []byte) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, uint32(-len(payload))), payload...)
}

func compressed(payload []byte) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, uint32(len(payload))), payload...)
}

func TestDecodeKnown(t *testing.T) {
	t.Parallel()
	s := spec{
		one:    map[byte]string{'A': "hello ", 'B': "world", 'C': "ab"},
		two:    map[[2]byte]string{{'X', 'A'}: "abc", {'X', 'B'}: "abd", {'X', 'C'}: "", {'Y', 'q'}: "abdomen"},
		sep:    0,
		escape: 'Z',
	}
	tests := []struct {
		name string
		text string
		want string
	}{
		{"empty", "", ""},
		{"plain", "plain text 123", "plain text 123"},
		{"one-byte words", "ABAB!", "hello worldhello world!"},
		{"two-byte words", "XAXB.XC.Yq", "abcabd..abdomen"},
		{"escape", "Z" + "A" + "ZZ" + "Zq", "AZq"},
		{"empty word", "<XC>", "<>"},
		{"mixed", "A:XA:B:Yq:ZX:Z\x00", "hello :abc:world:abdomen:X:\x00"},
		{"unknown second byte is an empty word", "XzXA", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := codectest.Decode(codectest.Parse(t, "dict"), s.block([]byte(tt.text)))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("stored and compressed blocks", func(t *testing.T) {
		t.Parallel()
		stream := slices.Concat(stored([]byte("raw")), s.block([]byte("AB")), s.block([]byte("XB")))
		got, err := codectest.Decode(codectest.Parse(t, "dict"), stream)
		if err != nil || string(got) != "rawhello worldabd" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("empty stream", func(t *testing.T) {
		t.Parallel()
		got, err := codectest.Decode(codectest.Parse(t, "dict"), nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %q, %v", got, err)
		}
	})
}

// TestDictionaryOverlap: a prefix longer than the previous word continues into the bytes just copied,
// as the C copy loop does.
func TestDictionaryOverlap(t *testing.T) {
	t.Parallel()
	dict := spec{two: map[[2]byte]string{{'X', 'A'}: "ab"}, sep: 0, escape: 'Z'}.dictionary()
	// Layout: 256 lengths, 256 prefix lengths of row X, (no one-byte words), separator, the 256 words of row X.
	const prefixes, texts = 256, 256 + 256 + 1
	// XA is "ab"+sep at texts+'A'; XB (the next word) is empty. Give it prefix 3 and the text "!".
	dict[prefixes+'B'] = 3
	dict = slices.Insert(dict, texts+'A'+len("ab")+1, '!')
	got, err := codectest.Decode(codectest.Parse(t, "dict"), compressed(append(dict, "XB"...)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "aba!" {
		t.Fatalf("got %q, want %q", got, "aba!")
	}
}

// randomSpec returns a random dictionary and a block text with the data it decodes to.
func randomSpec(rng *rand.Rand, textLen int) (s spec, text, want []byte) {
	word := func(minLen int) string {
		w := make([]byte, minLen+rng.IntN(20))
		for i := range w {
			w[i] = 'a' + byte(rng.IntN(26))
			if rng.IntN(5) == 0 {
				w[i] = byte(rng.IntN(255)) + 1 // anything but the separator 0
			}
		}
		return string(w)
	}
	s = spec{one: map[byte]string{}, two: map[[2]byte]string{}, sep: 0}
	used := map[byte]bool{}
	pick := func() byte {
		for {
			if b := byte(rng.IntN(256)); !used[b] {
				used[b] = true
				return b
			}
		}
	}
	var ones, rows []byte
	for range rng.IntN(20) {
		b := pick()
		s.one[b] = word(2) // a length of 1 would mark a row of two-byte codes
		ones = append(ones, b)
	}
	for range rng.IntN(4) {
		r := pick()
		rows = append(rows, r)
		base := word(1)
		for j := range 256 {
			if rng.IntN(3) == 0 {
				tail := word(1)
				s.two[[2]byte{r, byte(j)}] = base[:rng.IntN(len(base)+1)] + tail[:min(len(tail), rng.IntN(4))]
			}
		}
	}
	s.escape = pick()
	for len(want) < textLen {
		switch k := rng.IntN(5); {
		case k == 0 && len(ones) > 0:
			b := ones[rng.IntN(len(ones))]
			text = append(text, b)
			want = append(want, s.one[b]...)
		case k == 1 && len(rows) > 0:
			r, j := rows[rng.IntN(len(rows))], byte(rng.IntN(256))
			text = append(text, r, j)
			want = append(want, s.two[[2]byte{r, j}]...)
		case k == 2:
			b := byte(rng.IntN(256))
			text = append(text, s.escape, b)
			want = append(want, b)
		default:
			b := byte(rng.IntN(256))
			if used[b] {
				continue
			}
			text = append(text, b)
			want = append(want, b)
		}
	}
	return s, text, want
}

func TestDecodeRandomDictionaries(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	c := codectest.Parse(t, "dict:1mb")
	for i := range 500 {
		var stream, want []byte
		for range 1 + rng.IntN(3) { // dictionaries of one block must not leak into the next
			s, text, w := randomSpec(rng, rng.IntN(3000))
			stream = append(stream, s.block(text)...)
			want = append(want, w...)
		}
		got, err := codectest.Decode(c, stream)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("case %d: output differs (%d bytes, want %d)", i, len(got), len(want))
		}
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
	s := spec{
		one:    map[byte]string{'A': "hello ", 'B': "world"},
		two:    map[[2]byte]string{{'X', 'A'}: "abc", {'X', 'B'}: "abd"},
		sep:    0,
		escape: 'Z',
	}
	dictionary := s.dictionary()
	tests := []struct {
		name   string
		method string
		stream []byte
	}{
		{"partial size", "dict", []byte{1}},
		{"truncated stored", "dict", stored([]byte("hello"))[:6]},
		{"truncated block", "dict", s.block([]byte("AB"))[:len(s.block([]byte("AB")))-1]},
		{"stored of 2 GB", "dict", u32(1 << 31)},
		{"stored beyond the data", "dict", u32(0xfffffff0, 1, 2, 3)},
		{"zero size", "dict", compressed(nil)},
		{"first word copies from nothing", "dict", func() []byte {
			d := slices.Clone(dictionary)
			d[256+0] = 1 // prefix length of word X,0, the first two-byte word
			return compressed(d)
		}()},
		{"word copies from an empty word", "dict", func() []byte {
			e := spec{two: map[[2]byte]string{{'X', 'A'}: "", {'X', 'B'}: "b"}, sep: 0, escape: 'Z'}.dictionary()
			e[256+'B'] = 1 // XA is empty, so there is nothing to copy
			return compressed(e)
		}()},
		{"two-byte code cut off", "dict", s.block([]byte("A" + "X"))},
		{"output beyond block size", "dict:b3", s.block([]byte("AAAAAA"))},
		{"literals beyond block size", "dict:b3", s.block([]byte("abcdefghijklmnop"))},
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
	t.Run("every cut of the dictionary", func(t *testing.T) {
		t.Parallel()
		for n := range dictionary {
			_, err := codectest.Decode(codectest.Parse(t, "dict"), compressed(dictionary[:n]))
			if !errors.Is(err, method.ErrBadData) {
				t.Fatalf("cut at %d of %d: err = %v, want ErrBadData", n, len(dictionary), err)
			}
		}
		if _, err := codectest.Decode(codectest.Parse(t, "dict"), compressed(dictionary)); err != nil {
			t.Fatalf("whole dictionary: %v", err)
		}
	})
}

func TestDecodeStopsOnWriteError(t *testing.T) {
	t.Parallel()
	for _, stream := range [][]byte{stored([]byte("abc")), spec{sep: 0, escape: 'Z'}.block([]byte("abc"))} {
		err := codectest.Parse(t, "dict").Decode(&codectest.LimitWriter{Max: 0}, bytes.NewReader(stream))
		if !errors.Is(err, codectest.ErrLimit) {
			t.Fatalf("err = %v, want the writer's error", err)
		}
	}
}

// words returns text made of a few hundred distinct words, used a lot: what the dict method is made for.
func words(rng *rand.Rand, size int) []byte {
	vocab := make([]string, 400)
	for i := range vocab {
		w := make([]byte, 2+rng.IntN(10))
		for j := range w {
			w[j] = 'a' + byte(rng.IntN(26))
		}
		vocab[i] = string(w)
	}
	var out []byte
	for len(out) < size {
		out = append(out, vocab[int(rng.ExpFloat64()*60)%len(vocab)]...)
		out = append(out, " \n,."[rng.IntN(4)])
	}
	return out[:size]
}

// allBytes returns text that contains every byte value, some of them often.
func allBytes(rng *rand.Rand, size int) []byte {
	out := words(rng, size)
	for i := range out {
		if rng.IntN(7) == 0 {
			out[i] = byte(rng.IntN(256))
		}
	}
	return out
}

// TestRoundTripFagen decodes what the C encoder makes of various inputs.
func TestRoundTripFagen(t *testing.T) {
	codectest.FagenPath(t)
	rng := rand.New(rand.NewPCG(5, 6))
	random := make([]byte, 40<<10)
	codectest.FillBytes(rng, random)
	inputs := map[string][]byte{
		"empty":     nil,
		"one byte":  {42},
		"short":     []byte("hello world hello world"),
		"sources":   codectest.SourceText(t, 300<<10),
		"words":     words(rng, 300<<10),
		"all bytes": allBytes(rng, 200<<10),
		"random":    random,
		"zeros":     make([]byte, 100<<10),
		"odd":       words(rng, 99999),
	}
	methods := []string{"dict", "dict:64kb", "dict:p", "dict:f", "dict:50kb:c4:l50:m20:s10:r2", "dict:128kb:50%:c1:l20:m10:s5", "dict:20kb:c1:r0"}
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
				if name == "sources" && m == "dict:f" && len(stream) > len(in)*9/10 {
					t.Errorf("the stream is %d bytes for %d: no block was dictionary-coded", len(stream), len(in))
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
		{"words", "dict:c4:l50:m20:s10:r2", words(rng, 40<<10)},
		{"blocks", "dict:16kb:c4:l50:m20:s10:r2", words(rng, 60<<10)},
		{"sources", "dict:f", codectest.SourceText(t, 40<<10)},
		{"allbytes", "dict:20kb:c4:l50:m20:s10:r2", allBytes(rng, 30<<10)},
		{"stored", "dict:p", codectest.SourceText(t, 5<<10)},
		{"random", "dict", random},
		{"empty", "dict", nil},
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
	for range 10 {
		s, text, _ := randomSpec(rng, 200)
		f.Add(s.block(text))
	}
	f.Add(stored([]byte("hello")))
	f.Add([]byte{})
	if files, _ := filepath.Glob(filepath.Join("testdata", "*.bin")); len(files) > 0 {
		for _, file := range files {
			if b, err := os.ReadFile(file); err == nil && len(b) < 8<<10 {
				f.Add(b)
			}
		}
	}
	f.Fuzz(func(t *testing.T, stream []byte) {
		c, _ := method.Lookup("dict:1mb", &method.Env{})
		_ = c.Decode(&codectest.LimitWriter{Max: 4 << 20}, bytes.NewReader(stream))
	})
}

// BenchmarkDecode times the Go decoder on a stream the C encoder made of a 40 MB text.
func BenchmarkDecode(b *testing.B) {
	in := codectest.SourceText(b, 40<<20)
	stream := codectest.Fagen(b, in, "c", "dict")
	if path := os.Getenv("BENCH_STREAM"); path != "" {
		if err := os.WriteFile(path, stream, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	c := codectest.Parse(b, "dict")
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
