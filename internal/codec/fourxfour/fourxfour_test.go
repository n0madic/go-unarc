package fourxfour

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"runtime"
	"strings"
	"testing"

	_ "github.com/n0madic/go-unarc/internal/codec/lzma"
	_ "github.com/n0madic/go-unarc/internal/codec/ppmd"
	_ "github.com/n0madic/go-unarc/internal/codec/storing"
	_ "github.com/n0madic/go-unarc/internal/codec/tornado"
	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

func TestParse(t *testing.T) {
	t.Parallel()
	cpus := runtime.NumCPU()
	tests := []struct {
		m         string
		ok        bool
		blockSize uint32
		extra     uint32
		workers   int
	}{
		{m: "4x4", ok: true, extra: 2, workers: cpus}, // inner tor:3:1mb
		{m: "4x4:b256kb:storing", ok: true, blockSize: 256 << 10, extra: 2, workers: cpus},
		{m: "4x4:t1:storing", ok: true, extra: 2, workers: 1},
		{m: "4x4:t0:storing", ok: true, extra: 2, workers: cpus},
		{m: "4x4:1:storing", ok: true, extra: 2, workers: 1}, // a bare number is the thread count
		{m: "4x4:64kb:storing", ok: true, blockSize: 64 << 10, extra: 2, workers: cpus},
		{m: "4x4:20:storing", ok: true, extra: 2, workers: min(20, cpus)},
		{m: "4x4:i5:r99.5:storing", ok: true, extra: 5, workers: cpus},
		{m: "4x4:i4294967295:storing", ok: true, extra: 2, workers: cpus},      // a negative C int: the default
		{m: "4x4:b1mb", ok: true, blockSize: 1 << 20, extra: 2, workers: cpus}, // inner tor:3:1mb
		{m: "4x4:lzma:1mb:normal:32", ok: true, extra: 2, workers: cpus},
		{m: "4x4:tor:3:9kb:h64kb", ok: true, extra: 2, workers: cpus},
		{m: "4x4:b1x:storing"},  // a bad block size
		{m: "4x4:t1x:storing"},  // a bad thread count
		{m: "4x4:bxx:storing"},  // "bxx" starts the inner method, which does not exist
		{m: "4x4:nosuch"},       // an unknown inner method
		{m: "4x4:storing:junk"}, // the inner method rejects its parameters
		{m: "4x4:1x:storing"},   // neither a number nor a size
		{m: "4x4:4x4:storing"},  // "4x4" starts with a digit: an own parameter, so 4x4 never nests
		{m: "lzma"},             // not 4x4
	}
	for _, tt := range tests {
		c, ok := Parse(method.Params(tt.m), &method.Env{})
		if ok != tt.ok {
			t.Errorf("%s: accepted %v, want %v", tt.m, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		got := c.(*codec)
		if got.blockSize != tt.blockSize || got.extra != tt.extra || got.workers != tt.workers {
			t.Errorf("%s: block %d, extra %d, workers %d; want %d, %d, %d", tt.m,
				got.blockSize, got.extra, got.workers, tt.blockSize, tt.extra, tt.workers)
		}
	}
}

func TestDecompressionMem(t *testing.T) {
	t.Parallel()
	const buffers = 2 * (1<<20 + 1<<18 + 16<<10 + headerSize) // two buffers for 1 MiB blocks
	mem := func(m string) uint64 { return codectest.Parse(t, m).DecompressionMem() }
	storing := mem("storing")
	tests := []struct {
		m    string
		want uint64
	}{
		{"4x4:t1:i0:b1mb:storing", storing + buffers},
		{"4x4:t1:b1mb:storing", storing + 3*buffers},      // two pairs of buffers read ahead
		{"4x4:t1:i0:lzma:1mb", mem("lzma:1mb") + buffers}, // the block size is the dictionary
		{"4x4:t1:i0:tor:3:1mb", mem("tor:3:1mb") + buffers},
		{"4x4:t1:i0:b2gb:storing", 1<<32 - 1}, // capped as MemSize
	}
	for _, tt := range tests {
		if got := codectest.Parse(t, tt.m).DecompressionMem(); got != tt.want {
			t.Errorf("%s: %d, want %d", tt.m, got, tt.want)
		}
	}
}

// TestLimit follows the branches of the SetDecompressionMem part of _4x4_METHOD::GetSetDeCompressionMem. The
// PPMd model makes a worker's memory several times a pair of block buffers.
func TestLimit(t *testing.T) {
	t.Parallel()
	c := codectest.Parse(t, "4x4:b1mb:ppmd:4:16mb").(*codec)
	worker, buffers := c.memory()
	if worker < 7*buffers {
		t.Fatalf("worker %d, buffers %d: the cases below need a bigger worker", worker, buffers)
	}
	tests := []struct {
		mem            uint64
		workers, extra int
	}{
		{1 << 40, 4, 2},                  // enough for everything
		{4*worker + 2*buffers, 4, 2},     // exactly enough
		{4*worker + 2*buffers - 1, 4, 1}, // one pair of read-ahead buffers less
		{4 * worker, 4, 0},               // no read-ahead
		{4*worker - 1, 3, 2},             // a worker less, and the read-ahead fits in what remains
		{3*worker + buffers + 5, 3, 1},   // three workers and the read-ahead that fits
		{3*worker + 7*buffers, 3, 2},     // the read-ahead never grows
		{worker + buffers, 1, 1},
		{worker, 1, 0},     // one worker
		{worker - 1, 1, 0}, // too little even for one: still one
		{0, 1, 0},
	}
	for _, tt := range tests {
		c.workers, c.extra = 4, 2
		c.limit(tt.mem)
		if c.workers != tt.workers || int(c.extra) != tt.extra {
			t.Errorf("limit(%d): %d workers, %d buffers; want %d, %d", tt.mem, c.workers, c.extra, tt.workers, tt.extra)
		}
	}
}

// TestParseLimit checks that Parse fits the workers into method.MaxMem: a PPMd model of 1 GB leaves room for
// at most MaxMem/1 GB of them.
func TestParseLimit(t *testing.T) {
	t.Parallel()
	c := codectest.Parse(t, "4x4:ppmd:8:1gb").(*codec)
	worker, _ := c.memory()
	if want := max(1, min(runtime.NumCPU(), int(method.MaxMem()/worker))); c.workers != want {
		t.Errorf("%d workers with %d bytes each under a limit of %d; want %d", c.workers, worker, method.MaxMem(), want)
	}
	if c.workers > 1 && c.DecompressionMem() > method.MaxMem() {
		t.Errorf("DecompressionMem %d exceeds the limit %d", c.DecompressionMem(), method.MaxMem())
	}
}

// stream builds a 4x4 stream: the version, then a block per element of blocks. A block's OutSize is the
// length of its payload (the inner method is storing) unless out overrides it.
type block struct {
	payload []byte
	out     int32 // 0: len(payload)
	stored  bool
}

func stream(blocks ...block) []byte {
	b := binary.LittleEndian.AppendUint32(nil, formatVersion)
	for _, bl := range blocks {
		out := bl.out
		switch {
		case bl.stored:
			out = storedBlock
		case out == 0:
			out = int32(len(bl.payload))
		}
		b = binary.LittleEndian.AppendUint32(b, uint32(out))
		b = binary.LittleEndian.AppendUint32(b, uint32(len(bl.payload)))
		b = append(b, bl.payload...)
	}
	return b
}

func TestDecode(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	var blocks []block
	var want []byte
	for i := range 300 {
		p := make([]byte, rng.IntN(5000))
		codectest.FillBytes(rng, p)
		blocks = append(blocks, block{payload: p, stored: i%3 == 0})
		want = append(want, p...)
	}
	full := stream(blocks...)
	for _, m := range []string{"4x4:storing", "4x4:t1:storing", "4x4:t3:storing"} {
		got, err := codectest.Decode(codectest.Parse(t, m), full)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: %d bytes, %v; want %d bytes", m, len(got), err, len(want))
		}
	}
	empty := []block{{payload: nil}, {payload: []byte{}, stored: true}}
	for name, s := range map[string][]byte{"empty": nil, "version only": stream(), "empty blocks": stream(empty...)} {
		if got, err := codectest.Decode(codectest.Parse(t, "4x4:storing"), s); err != nil || len(got) != 0 {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
}

func TestDecodeBad(t *testing.T) {
	t.Parallel()
	abc := []byte("abc")
	big := bytes.Repeat([]byte("x"), 20<<10)
	tests := []struct {
		name   string
		stream []byte
		want   string // the output before the error
	}{
		{"short version", []byte{0, 0}, ""},
		{"version 1", binary.LittleEndian.AppendUint32(nil, 1), ""},
		{"short OutSize", append(stream(block{payload: abc}), 3, 0), "abc"},
		{"no InSize", append(stream(block{payload: abc}), 3, 0, 0, 0), "abc"},
		{"short payload", stream(block{payload: abc})[:4+8+2], ""},
		{"negative InSize", append(stream(), 0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff), ""},
		{"OutSize -2", append(stream(), 0xfe, 0xff, 0xff, 0xff, 0, 0, 0, 0), ""},
		{"too short", stream(block{payload: abc, out: 4}), ""},
		{"too long", stream(block{payload: abc, out: 2}), ""},
		// The first block sets the limit: 3 + 0 + 16 KiB.
		{"larger than the first", stream(block{payload: abc}, block{payload: big}), "abc"},
		{"larger OutSize", stream(block{payload: abc}, block{payload: abc, out: 20 << 10}), "abc"},
		{"bad after good", stream(block{payload: abc, stored: true}, block{payload: abc, out: 9}), "abc"},
	}
	for _, tt := range tests {
		got, err := codectest.Decode(codectest.Parse(t, "4x4:t2:storing"), tt.stream)
		if !errors.Is(err, method.ErrBadData) || string(got) != tt.want {
			t.Errorf("%s: %q, %v; want %q and corrupt data", tt.name, got, err, tt.want)
		}
	}
	// A big first block allows big blocks after it.
	got, err := codectest.Decode(codectest.Parse(t, "4x4:storing"), stream(block{payload: big}, block{payload: big}))
	if err != nil || len(got) != 2*len(big) {
		t.Errorf("two big blocks: %d bytes, %v", len(got), err)
	}
}

// TestDecodeInnerError: an error of the inner method names it, as the chain names its stages ("4x4: lzma: ...").
func TestDecodeInnerError(t *testing.T) {
	t.Parallel()
	bad := stream(block{payload: bytes.Repeat([]byte{0xff}, 16), out: 10}) // LZMA's first byte must be 0
	_, err := codectest.Decode(codectest.Parse(t, "4x4:lzma:1mb"), bad)
	if !errors.Is(err, method.ErrBadData) || !strings.HasPrefix(err.Error(), "lzma: compressed data corrupted") {
		t.Errorf("err = %v, want corrupt data named lzma", err)
	}
}

func TestDecodeStop(t *testing.T) {
	t.Parallel()
	var blocks []block
	for range 100 {
		blocks = append(blocks, block{payload: bytes.Repeat([]byte("y"), 1000)})
	}
	w := &codectest.LimitWriter{Max: 10000}
	err := codectest.Parse(t, "4x4:storing").Decode(w, bytes.NewReader(stream(blocks...)))
	if !errors.Is(err, codectest.ErrLimit) {
		t.Errorf("got %v, want the writer's error", err)
	}
}

// TestDecodeInner decodes blocks of a real inner method: each block is a stream of its own.
func TestDecodeInner(t *testing.T) {
	t.Parallel()
	if !codectest.HaveFagen(t) {
		t.Skip("needs .build/fagen")
	}
	text := codectest.SourceText(t, 300<<10)
	var blocks []block
	for p := text; len(p) > 0; {
		n := min(len(p), 64<<10)
		packed := codectest.Fagen(t, p[:n], "c", "lzma:64kb:normal")
		blocks = append(blocks, block{payload: packed, out: int32(n)})
		p = p[n:]
	}
	got, err := codectest.Decode(codectest.Parse(t, "4x4:lzma:64kb:normal"), stream(blocks...))
	if err != nil || !bytes.Equal(got, text) {
		t.Errorf("%d bytes, %v; want %d bytes", len(got), err, len(text))
	}
}

func FuzzDecode(f *testing.F) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 10 {
		var blocks []block
		for range 1 + rng.IntN(5) {
			p := make([]byte, rng.IntN(100))
			codectest.FillBytes(rng, p)
			blocks = append(blocks, block{payload: p, stored: rng.IntN(2) == 0})
		}
		f.Add(stream(blocks...))
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, s []byte) {
		for _, m := range []string{"4x4:t2:storing", "4x4:t2:tor:3:64kb"} {
			c := codectest.Parse(t, m)
			_ = c.Decode(&codectest.LimitWriter{Max: 4 << 20}, bytes.NewReader(s))
		}
	})
}

// BenchmarkDecode decodes text in 1 MB blocks of an inner method (made by the C encoder) on one worker and on
// one per processor. PPMd gets the model of the -m5 preset (96 MB), which is far larger than a block.
func BenchmarkDecode(b *testing.B) {
	if !codectest.HaveFagen(b) {
		b.Skip("needs .build/fagen")
	}
	const blockSize = 1 << 20
	for _, bench := range []struct {
		inner string
		size  int
	}{
		{"lzma:1mb:normal", 32 << 20},
		{"ppmd:8:96mb", 8 << 20},
		{"tor:3:1mb", 32 << 20},
	} {
		text := codectest.SourceText(b, bench.size)
		var blocks []block
		for p := text; len(p) > 0; p = p[blockSize:] {
			blocks = append(blocks, block{payload: codectest.Fagen(b, p[:blockSize], "c", bench.inner), out: blockSize})
		}
		s := stream(blocks...)
		for _, m := range []string{"4x4:t1:" + bench.inner, "4x4:" + bench.inner} {
			b.Run(m, func(b *testing.B) {
				c := codectest.Parse(b, m)
				b.SetBytes(int64(len(text)))
				b.ReportAllocs()
				for b.Loop() {
					if err := c.Decode(io.Discard, bytes.NewReader(s)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
