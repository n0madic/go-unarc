package ppmd

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

func mustParse(t testing.TB, spec string) *codec {
	t.Helper()
	c, ok := Parse(method.Params(spec), nil)
	if !ok {
		t.Fatalf("Parse(%q) failed", spec)
	}
	return c.(*codec)
}

// decode runs the whole decoder and returns the model it ended with.
func decode(t testing.TB, c *codec, comp []byte) ([]byte, *decoder, error) {
	t.Helper()
	var out bytes.Buffer
	d, err := newDecoder(c.order, c.mem, c.mr, &out, bytes.NewReader(comp))
	if err != nil {
		t.Fatal(err)
	}
	d.run()
	d.flush()
	return out.Bytes(), d, d.err
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestParse(t *testing.T) {
	t.Parallel()
	const defMem = 48 << 20
	tests := []struct {
		spec  string
		ok    bool
		order int
		mem   uint32
		mr    uint32
	}{
		{"ppmd", true, 10, defMem, 0},
		{"ppmd:o4", true, 4, defMem, 0},
		{"ppmd:o=7", true, 7, defMem, 0},
		{"ppmd:16", true, 16, defMem, 0},
		{"ppmd:=5", true, 5, defMem, 0},
		{"ppmd:20", true, 20, defMem, 0},
		{"ppmd:16:32mb", true, 16, 32 << 20, 0},
		{"ppmd:32mb:16", true, 16, 32 << 20, 0},
		{"ppmd:mem=100mb", true, 10, 100 << 20, 0},
		{"ppmd:mem64m", true, 10, 64 << 20, 0},
		{"ppmd:m3k", true, 10, 3 << 10, 0},
		{"ppmd:m=3k", true, 10, 3 << 10, 0},
		{"ppmd:m4096b", true, 10, 4096, 0},
		{"ppmd:24", true, 24, defMem, 0}, // a bare number is the order, not a power of two
		{"ppmd:^", false, 0, 0, 0},       // 1 byte of memory
		{"ppmd:1gb", true, 10, 1 << 30, 0},
		{"ppmd:m64mb:o6:r", true, 6, 64 << 20, 1},
		{"ppmd:r", true, 10, defMem, 1},
		{"ppmd:r0", true, 10, defMem, 0},
		{"ppmd:r2", true, 10, defMem, 2},
		{"ppmd:r5:8:4mb", true, 8, 4 << 20, 5},
		{"ppmd:r4294967295", true, 10, defMem, 4294967295},
		{"ppmd:5mb:6mb", true, 10, 6 << 20, 0},
		{"ppmd:2:2kb", true, 2, 2 << 10, 0},
		{"ppmd:128", true, 128, defMem, 0},
		{"ppmd:o4294967295", false, 0, 0, 0}, // order -1
		{"ppmd:o1", false, 0, 0, 0},          // C crashes
		{"ppmd:o0", false, 0, 0, 0},
		{"ppmd:o129", false, 0, 0, 0},
		{"ppmd:m", false, 0, 0, 0}, // 0 bytes
		{"ppmd:mem", false, 0, 0, 0},
		{"ppmd:4gb", false, 0, 0, 0}, // wraps to 0
		{"ppmd:m1000b", false, 0, 0, 0},
		{"ppmd:foo", false, 0, 0, 0},
		{"ppmd:", false, 0, 0, 0},
		{"ppmd:o", false, 0, 0, 0},
		{"ppmd:o4x", false, 0, 0, 0},
		{"ppmd:r1x", false, 0, 0, 0},
		{"ppmd:4:", false, 0, 0, 0},
		{"ppmdx", false, 0, 0, 0},
		{"lzp", false, 0, 0, 0},
		{"", false, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			t.Parallel()
			c, ok := Parse(method.Params(tt.spec), nil)
			if ok != tt.ok {
				t.Fatalf("Parse(%q) = %v, want %v", tt.spec, ok, tt.ok)
			}
			if !ok {
				return
			}
			got := c.(*codec)
			if got.order != tt.order || got.mem != tt.mem || got.mr != tt.mr {
				t.Errorf("Parse(%q) = order %d mem %d r %d, want order %d mem %d r %d",
					tt.spec, got.order, got.mem, got.mr, tt.order, tt.mem, tt.mr)
			}
			if c.DecompressionMem() != uint64(tt.mem) {
				t.Errorf("DecompressionMem = %d, want %d", c.DecompressionMem(), tt.mem)
			}
		})
	}
}

// TestParseAgainstC compares the accepted strings and the decompression memory with the C parser.
func TestParseAgainstC(t *testing.T) {
	const cError = 4294967294 // what "fagen mem" prints when the method does not parse
	specs := []string{
		"ppmd", "ppmd:o4", "ppmd:16:32mb", "ppmd:mem=100mb", "ppmd:m64mb:o6:r", "ppmd:foo", "ppmd:", "ppmd:16:r2",
		"ppmd:r3:8:4mb", "ppmd:20", "ppmd:24", "ppmd:1gb", "ppmd:o4294967295", "ppmd:r4294967295", "ppmd:o",
		"ppmd:m=3k", "ppmd:=5", "ppmd:o=7", "ppmd:5mb:6mb", "ppmd:32mb:16", "ppmd:m4096b", "ppmd:128", "ppmd:2:2kb",
		"ppmd:o4x", "ppmd:r1x", "ppmd:4:", "ppmd:mem64m", "ppmd:m3k", "ppmd:r",
	}
	for _, spec := range specs {
		out := codectest.Fagen(t, nil, "mem", spec)
		want, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			t.Fatalf("fagen mem %s: %q", spec, out)
		}
		// parseParams mirrors the C parser exactly...
		pc, pok := parseParams(method.Params(spec))
		if pok != (want != cError) {
			t.Errorf("%s: C accepts it: %v, parseParams: %v", spec, want != cError, pok)
			continue
		}
		if !pok {
			continue
		}
		if uint64(pc.mem) != want {
			t.Errorf("%s: memory %d, C says %d", spec, pc.mem, want)
		}
		// ...and Parse only adds the rejection of parameters the C decoder crashes on.
		c, ok := Parse(method.Params(spec), nil)
		sane := pc.order >= minOrder && pc.order <= maxOrder && pc.mem >= minMem && pc.mem <= maxMem
		if ok != sane {
			t.Errorf("%s: Parse accepts it: %v, parameters sane: %v", spec, ok, sane)
		}
		if ok && c.DecompressionMem() != want {
			t.Errorf("%s: DecompressionMem = %d, C says %d", spec, c.DecompressionMem(), want)
		}
	}
}

// streamCase is a line of testdata/streams.txt.
type streamCase struct {
	file                            string
	order                           int
	mem, mr                         uint32
	size                            int
	sha                             string
	minRestarts, minCuts, minFrozen int
	source                          string
}

func readManifest(t testing.TB) []streamCase {
	t.Helper()
	f, err := os.Open("testdata/streams.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cases []streamCase
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		fl := strings.Fields(line)
		if len(fl) != 10 {
			t.Fatalf("streams.txt: bad line %q", line)
		}
		n := make([]int, 9)
		for i, idx := range []int{1, 2, 3, 4, 6, 7, 8} {
			if n[i], err = strconv.Atoi(fl[idx]); err != nil {
				t.Fatalf("streams.txt: bad line %q: %v", line, err)
			}
		}
		cases = append(cases, streamCase{
			file: fl[0], order: n[0], mem: uint32(n[1]), mr: uint32(n[2]), size: n[3], sha: fl[5],
			minRestarts: n[4], minCuts: n[5], minFrozen: n[6], source: fl[9],
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return cases
}

func readStream(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestStreams decodes the committed streams, made by encoders that were not FreeArc's 64-bit build
// (see testdata/streams.txt), and checks that the memory-filling ones really exercise the restart,
// cut-off and freeze paths.
func TestStreams(t *testing.T) {
	t.Parallel()
	for _, tc := range readManifest(t) {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			comp := readStream(t, tc.file)
			spec := fmt.Sprintf("ppmd:o%d:m%db:r%d", tc.order, tc.mem, tc.mr)
			c := mustParse(t, spec)
			out, d, err := decode(t, c, comp)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(out) != tc.size || sum(out) != tc.sha {
				t.Fatalf("decoded %d bytes, sha256 %s; want %d bytes, %s", len(out), sum(out), tc.size, tc.sha)
			}
			if d.counts.restarts < tc.minRestarts || d.counts.cutOffs < tc.minCuts || d.counts.frozen < tc.minFrozen {
				t.Errorf("model events %+v, want at least %d restarts, %d cut-offs, %d frozen steps",
					d.counts, tc.minRestarts, tc.minCuts, tc.minFrozen)
			}
			if d.eofReads != 0 {
				t.Errorf("the decoder read %d bytes past the end of the stream", d.eofReads)
			}
			// The public entry point gives the same result.
			var got bytes.Buffer
			if err := c.Decode(&got, bytes.NewReader(comp)); err != nil || !bytes.Equal(got.Bytes(), out) {
				t.Errorf("Decode: %v, output equal %v", err, bytes.Equal(got.Bytes(), out))
			}
		})
	}
}

// testText returns n bytes of deterministic pseudo-English text.
func testText(n int, seed int64) []byte {
	words := strings.Fields(`the of and to in a is that for it as was with be by on not he this are or his from at which
		but have an had they you were their one all we can her has there been if more when will would who so no
		compression archive model context symbol frequency escape order memory range decoder stream block data
		function return int void struct static const unsigned char buffer length offset pointer index table`)
	rng := rand.New(rand.NewSource(seed))
	var b bytes.Buffer
	for b.Len() < n {
		b.WriteString(words[rng.Intn(len(words))])
		switch rng.Intn(12) {
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

func testInputs() map[string][]byte {
	rng := rand.New(rand.NewSource(7))
	random := make([]byte, 100_000)
	rng.Read(random)
	allBytes := make([]byte, 0, 256*40)
	for range 40 {
		for i := range 256 {
			allBytes = append(allBytes, byte(i))
		}
	}
	periodic := bytes.Repeat([]byte("abcdefg"), 30_000)
	mixed := append(append(testText(60_000, 3), random[:30_000]...), bytes.Repeat([]byte{0}, 20_000)...)
	return map[string][]byte{
		"empty":    {},
		"one":      {'x'},
		"two":      {0, 255},
		"odd":      testText(4097, 1),
		"text":     testText(150_000, 2),
		"random":   random,
		"allbytes": allBytes,
		"periodic": periodic,
		"zeros":    make([]byte, 100_000),
		"mixed":    mixed,
	}
}

// TestRoundTripAgainstC decodes what the C encoder made. The streams are valid oracles because the
// model memory is large enough never to fill up (the test checks it), where the 64-bit C build and
// the 32-bit layout agree.
func TestRoundTripAgainstC(t *testing.T) {
	codectest.FagenPath(t)
	inputs := testInputs()
	specs := []string{"ppmd:2:32mb", "ppmd:3:32mb", "ppmd:4:32mb", "ppmd:8:32mb", "ppmd:16:32mb", "ppmd:64:32mb", "ppmd:128:32mb"}
	for name, in := range inputs {
		for _, spec := range specs {
			t.Run(name+"/"+spec, func(t *testing.T) {
				t.Parallel()
				comp := codectest.FagenTry(t, in, "c", spec).Out
				if len(comp) == 0 {
					t.Fatal("fagen produced nothing")
				}
				out, d, err := decode(t, mustParse(t, spec), comp)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !bytes.Equal(out, in) {
					t.Fatalf("decoded %d bytes, want %d (equal=false)", len(out), len(in))
				}
				if d.counts != (counters{}) || d.glues != 0 {
					t.Fatalf("the model ran out of memory (%+v, %d glues), fagen is no oracle for this input", d.counts, d.glues)
				}
			})
		}
	}
}

// TestDamagedAgainstC feeds truncated, bit-flipped and random streams to both decoders: the C one decodes
// them to garbage without complaint, and so must this one, with identical output.
func TestDamagedAgainstC(t *testing.T) {
	codectest.FagenPath(t)
	const spec = "ppmd:8:64mb"
	in := testText(30_000, 11)
	comp := codectest.FagenTry(t, in, "c", spec).Out
	c := mustParse(t, spec)
	rng := rand.New(rand.NewSource(1))
	check := func(t *testing.T, data []byte) {
		t.Helper()
		want := codectest.FagenTry(t, data, "d", spec).Out
		got, _, err := decode(t, c, data)
		if err != nil {
			t.Errorf("decode: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("decoded %d bytes, C decoded %d bytes", len(got), len(want))
		}
	}
	t.Run("truncated", func(t *testing.T) {
		for _, cut := range []int{1, 2, 3, 4, 5, 6, 8, 12, 100, 1000, len(comp) / 2, len(comp) - 4, len(comp)} {
			check(t, comp[:len(comp)-cut])
		}
	})
	t.Run("flipped", func(t *testing.T) {
		for range 30 {
			d := bytes.Clone(comp[:200+rng.Intn(len(comp)-200)])
			for range 1 + rng.Intn(3) {
				d[rng.Intn(len(d))] ^= byte(1 << rng.Intn(8))
			}
			check(t, d)
		}
	})
	t.Run("random", func(t *testing.T) {
		for range 20 {
			d := make([]byte, rng.Intn(300))
			rng.Read(d)
			check(t, d)
		}
	})
}

// TestShortStreams: like the C decoder, missing trailing bytes read as 0xFF, which often still
// decodes the whole data, and an empty stream is an empty result.
func TestShortStreams(t *testing.T) {
	t.Parallel()
	c := mustParse(t, "ppmd:8:16mb")
	comp := readStream(t, "fagen_o8.ppmd")
	want := readManifestSHA(t, "fagen_o8.ppmd")
	for _, cut := range []int{1, 2} {
		out, d, err := decode(t, c, comp[:len(comp)-cut])
		if err != nil || sum(out) != want {
			t.Errorf("cut %d: %d bytes, err %v, sha %s", cut, len(out), err, sum(out))
		}
		if d.eofReads < 1 {
			t.Errorf("cut %d: no read past the end", cut)
		}
	}
	out, _, err := decode(t, c, nil)
	if err != nil || len(out) != 0 {
		t.Errorf("empty stream: %d bytes, err %v", len(out), err)
	}
}

func readManifestSHA(t testing.TB, file string) string {
	t.Helper()
	for _, tc := range readManifest(t) {
		if tc.file == file {
			return tc.sha
		}
	}
	t.Fatalf("%s is not in streams.txt", file)
	return ""
}

type failWriter struct {
	err   error
	after int // bytes accepted before failing
	n     int
}

func (w *failWriter) Write(p []byte) (int, error) {
	if w.n+len(p) > w.after {
		return 0, w.err
	}
	w.n += len(p)
	return len(p), nil
}

type failReader struct {
	r   io.Reader
	err error
}

func (r *failReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err == io.EOF {
		return n, r.err
	}
	return n, err
}

func TestIOErrors(t *testing.T) {
	t.Parallel()
	comp := readStream(t, "mix_r0_o6.ppmd")
	c := mustParse(t, "ppmd:o6:m1048576b:r0")

	t.Run("write", func(t *testing.T) {
		for _, werr := range []error{method.ErrStop, errors.New("disk full")} {
			w := &failWriter{err: werr, after: bufSize}
			err := c.Decode(w, bytes.NewReader(comp))
			if err != werr {
				t.Errorf("Decode = %v, want %v unchanged", err, werr)
			}
			if w.n > bufSize {
				t.Errorf("decoded on after the failed write: %d bytes accepted", w.n)
			}
		}
	})
	t.Run("read", func(t *testing.T) {
		rerr := errors.New("bad sector")
		err := c.Decode(io.Discard, &failReader{r: bytes.NewReader(comp[:5000]), err: rerr})
		if !errors.Is(err, rerr) {
			t.Errorf("Decode = %v, want %v", err, rerr)
		}
	})
}

// TestMemoryTooSmallForCutOff: with r1 and a heap that cannot hold even the order-0 model within 3/4 of its
// size the C decoder never leaves the cut-off loop; this one reports corrupt data.
func TestMemoryTooSmallForCutOff(t *testing.T) {
	t.Parallel()
	c := mustParse(t, "ppmd:o4:m2048b:r1")
	rng := rand.New(rand.NewSource(5))
	for range 20 {
		comp := make([]byte, 20_000)
		rng.Read(comp)
		_, _, err := decode(t, c, comp)
		if err != nil && !errors.Is(err, method.ErrBadData) {
			t.Fatalf("unexpected error %v", err)
		}
	}
}

// TestParseMemoryLimits: the smallest accepted heap really holds the root context.
func TestParseMemoryLimits(t *testing.T) {
	t.Parallel()
	for _, mem := range []uint32{minMem, minMem + 1, 2048} {
		if _, err := newDecoder(2, mem, 0, io.Discard, bytes.NewReader(nil)); err != nil {
			t.Errorf("mem %d: %v", mem, err)
		}
	}
	if _, err := newDecoder(2, minMem-1, 0, io.Discard, bytes.NewReader(nil)); err == nil {
		t.Errorf("mem %d: the model should not fit", minMem-1)
	}
}

// FuzzDecode: arbitrary input must neither panic nor hang. The first byte picks the parameters.
func FuzzDecode(f *testing.F) {
	for _, name := range []string{"tiny_r0_o4.ppmd", "tiny_r1_o4.ppmd", "tiny_r2_o4.ppmd", "fagen_o8.ppmd", "fagen_o2.ppmd"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(byte(0), b[:min(len(b), 4000)])
	}
	f.Add(byte(1), []byte{})
	f.Add(byte(7), []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	orders := [...]int{2, 3, 4, 6, 8, 16, 64, 128}
	mems := [...]uint32{minMem, 2048, 5000, 20_000, 100_000, 1 << 20}
	f.Fuzz(func(t *testing.T, params byte, data []byte) {
		c := &codec{
			order: orders[int(params)%len(orders)],
			mem:   mems[int(params>>3)%len(mems)],
			mr:    uint32(params >> 6),
		}
		err := c.Decode(&failWriter{err: errors.New("enough"), after: 2 << 20}, bytes.NewReader(data))
		if err != nil && !errors.Is(err, method.ErrBadData) && err.Error() != "enough" {
			t.Fatalf("unexpected error %v", err)
		}
	})
}

func BenchmarkDecode(b *testing.B) {
	for _, name := range []string{"mix_r0_o6.ppmd", "mix_r1_o8.ppmd", "mix_r2_o5.ppmd"} {
		for _, tc := range readManifest(b) {
			if tc.file != name {
				continue
			}
			comp := readStream(b, name)
			c := mustParse(b, fmt.Sprintf("ppmd:o%d:m%db:r%d", tc.order, tc.mem, tc.mr))
			b.Run(name, func(b *testing.B) {
				b.SetBytes(int64(tc.size))
				for range b.N {
					if err := c.Decode(io.Discard, bytes.NewReader(comp)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
	// A larger stream the model does not fill, made by the C encoder when it is available.
	if codectest.HaveFagen(b) {
		in := testText(20<<20, 9)
		const spec = "ppmd:8:256mb"
		comp := codectest.FagenTry(b, in, "c", spec).Out
		c := mustParse(b, spec)
		b.Run("text20MB_o8", func(b *testing.B) {
			b.SetBytes(int64(len(in)))
			for range b.N {
				if err := c.Decode(io.Discard, bytes.NewReader(comp)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
