package delta_test

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

	"github.com/n0madic/go-unarc/internal/codec/delta"
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
		{"delta", true, 8 << 20},
		{"delta:x", true, 8 << 20},
		{"delta:16mb", true, 16 << 20},
		{"delta:b16mb", true, 16 << 20},
		{"delta:b4kb:x", true, 4 << 10},
		{"delta:x:b1gb", true, 1 << 30},
		{"delta:24", true, 1 << 24}, // a bare number is a power of two
		{"delta:100b", true, 100},
		{"delta:b", false, 0}, // "b" without a size
		{"delta:y", false, 0},
		{"delta:xx", false, 0},
		{"delta:", false, 0},
		{"delta:5q", false, 0},
		{"delta:x:", false, 0},
		{"delt", false, 0},
		{"deltax", false, 0},
		{"lzp", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			c, ok := delta.Parse(method.Params(tt.method), &method.Env{})
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
	pieces := []string{"x", "b", "b8mb", "8mb", "16", "1gb", "4k", "5q", "xx", "", "=3", "b=2m", "99999999999", "2gb", "4gb", "^"}
	var cases []string
	cases = append(cases, "delta")
	for _, a := range pieces {
		cases = append(cases, "delta:"+a)
		for _, b := range pieces {
			cases = append(cases, "delta:"+a+":"+b)
		}
	}
	codectest.CheckAgainstC(t, delta.Parse, cases)
}

// table describes one filtered table of a test block.
type table struct {
	skip      int    // bytes between the previous table (or block start) and this one
	n         int    // record size
	rows      int    // number of records
	immutable uint32 // columns that are not subtracted
}

// filter applies the encoder's transform (diff_table, then reorder_table of the C code) to the tables of
// data in place and returns the stream of a block holding the result.
func filter(data []byte, tables []table) []byte {
	var skip, typ, rows []byte
	pos := 0
	for _, tb := range tables {
		pos += tb.skip
		t := data[pos : pos+tb.n*tb.rows]
		pos += tb.n * tb.rows
		diff(t, tb.n, tb.rows, tb.immutable)
		reorder(t, tb.n, tb.rows, tb.immutable)
		skip = binary.LittleEndian.AppendUint32(skip, uint32(tb.skip))
		typ = binary.LittleEndian.AppendUint32(typ, 1<<tb.n|tb.immutable)
		rows = binary.LittleEndian.AppendUint32(rows, uint32(tb.rows))
	}
	return block(data, skip, typ, rows)
}

func block(data, skip, typ, rows []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(data)))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(skip)))
	b = append(b, skip...)
	b = append(b, typ...)
	b = append(b, rows...)
	return append(b, data...)
}

// diff is diff_table: subtract the previous record, last record first.
func diff(t []byte, n, rows int, immutable uint32) {
	for r := n * (rows - 1); r > 0; r -= n {
		carry := 0
		for i := range n {
			if immutable>>i&1 != 0 {
				carry = 0
				continue
			}
			sub := int(t[r+i-n]) + carry
			newCarry := 0
			if int(t[r+i]) < sub {
				newCarry = 1
			}
			t[r+i] = byte(int(t[r+i]) - sub)
			carry = newCarry
		}
	}
}

// reorder is reorder_table: immutable columns first, then the others.
func reorder(t []byte, n, rows int, immutable uint32) {
	tmp := bytes.Clone(t)
	p := 0
	for _, wantImmutable := range []bool{true, false} {
		for r := range rows {
			for k := range n {
				if (immutable>>k&1 != 0) == wantImmutable {
					t[p] = tmp[r*n+k]
					p++
				}
			}
		}
	}
}

// randomBlock returns a block with random tables in random data, and the data it must decode to.
func randomBlock(rng *rand.Rand) (stream, want []byte) {
	var tables []table
	var want0 []byte
	for range 1 + rng.IntN(4) {
		n := 1 + rng.IntN(30)
		rows := rng.IntN(40)
		tb := table{skip: rng.IntN(20), n: n, rows: rows}
		if rng.IntN(3) > 0 {
			tb.immutable = rng.Uint32() & (1<<n - 1)
		}
		gap := make([]byte, tb.skip)
		codectest.FillBytes(rng, gap)
		want0 = append(want0, gap...)
		body := make([]byte, n*rows)
		for i := range body {
			// Small steps in the columns keep carries interesting; occasional jumps wrap around.
			switch rng.IntN(3) {
			case 0:
				body[i] = byte(rng.Uint32())
			case 1:
				body[i] = 0xff
			default:
				if i >= n {
					body[i] = body[i-n] + byte(rng.IntN(3))
				}
			}
		}
		want0 = append(want0, body...)
		tables = append(tables, tb)
	}
	tail := make([]byte, rng.IntN(10))
	codectest.FillBytes(rng, tail)
	want0 = append(want0, tail...)
	return filter(bytes.Clone(want0), tables), want0
}

func TestDecodeTables(t *testing.T) {
	t.Parallel()
	c := codectest.Parse(t, "delta")
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 2000 {
		stream, want := randomBlock(rng)
		// Two blocks in a row must not share state.
		two := append(bytes.Clone(stream), stream...)
		got, err := codectest.Decode(c, two)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !bytes.Equal(got, append(bytes.Clone(want), want...)) {
			t.Fatalf("case %d: output differs\n got %x\nwant %x", i, got, want)
		}
	}
}

func TestDecodeEmpty(t *testing.T) {
	t.Parallel()
	got, err := codectest.Decode(codectest.Parse(t, "delta"), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %x, %v; want an empty output without error", got, err)
	}
	// An empty block is fine too.
	got, err = codectest.Decode(codectest.Parse(t, "delta"), block(nil, nil, nil, nil))
	if err != nil || len(got) != 0 {
		t.Fatalf("empty block: got %x, %v", got, err)
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
	data8 := make([]byte, 8)
	const neg = 1 << 31
	tests := []struct {
		name   string
		stream []byte
	}{
		{"partial size", []byte{1, 2}},
		{"missing table size", u32(4)},
		{"negative data size", u32(neg, 0)},
		{"negative table size", u32(4, neg, 1, 2, 3, 4)},
		{"truncated tables", append(u32(4, 8), 1, 2, 3)},
		{"truncated data", block(data8, nil, nil, nil)[:len(block(data8, nil, nil, nil))-1]},
		{"table beyond data", block(data8, u32(0), u32(1<<2), u32(5))},
		{"table size overflow", block(data8, u32(0), u32(1<<2), u32(1<<30))},
		{"negative rows", block(data8, u32(0), u32(1<<2), u32(neg|2))},
		{"negative skip", block(data8, u32(neg|1), u32(1<<2), u32(1))},
		{"skip beyond data", block(data8, u32(9), u32(1<<2), u32(0))},
		{"record size 31", block(data8, u32(0), u32(1<<31), u32(0))},
		{"record size 30 beyond data", block(data8, u32(0), u32(1<<30), u32(1))},
		{"second table beyond data", block(data8, u32(0, 1), u32(1<<2, 1<<2), u32(1, 8))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := codectest.Decode(codectest.Parse(t, "delta"), tt.stream)
			if !errors.Is(err, method.ErrBadData) {
				t.Fatalf("err = %v, want ErrBadData", err)
			}
		})
	}
}

// TestDecodeNoOpTables: type words below 2 describe a record size of zero, which changes nothing.
func TestDecodeNoOpTables(t *testing.T) {
	t.Parallel()
	data := []byte("0123456789")
	for _, typ := range []uint32{0, 1} {
		stream := block(bytes.Clone(data), binary.LittleEndian.AppendUint32(nil, 3), binary.LittleEndian.AppendUint32(nil, typ), binary.LittleEndian.AppendUint32(nil, 7))
		got, err := codectest.Decode(codectest.Parse(t, "delta"), stream)
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("type %d: got %q, %v", typ, got, err)
		}
	}
}

func TestDecodeStopsOnWriteError(t *testing.T) {
	t.Parallel()
	stream, _ := randomBlock(rand.New(rand.NewPCG(3, 4)))
	err := codectest.Parse(t, "delta").Decode(&codectest.LimitWriter{Max: 0}, bytes.NewReader(stream))
	if !errors.Is(err, codectest.ErrLimit) {
		t.Fatalf("err = %v, want the writer's error", err)
	}
}

// tableFile builds a file with tables of several shapes among plain data, the kind of input delta is made for.
func tableFile(rng *rand.Rand, size int) []byte {
	var out []byte
	for len(out) < size {
		gap := make([]byte, rng.IntN(300))
		codectest.FillBytes(rng, gap)
		out = append(out, gap...)
		n := []int{1, 2, 3, 4, 6, 8, 12, 16, 20}[rng.IntN(9)]
		rows := 300 + rng.IntN(3000)
		cols := make([]int, n) // per column: 0 constant, 1 counter with step, 2 noise
		steps := make([]int, n)
		for i := range cols {
			cols[i] = rng.IntN(3)
			steps[i] = 1 + rng.IntN(5)
		}
		state := make([]int, n)
		for range rows {
			for i := range n {
				switch cols[i] {
				case 1:
					state[i] += steps[i]
					out = append(out, byte(state[i]))
				case 2:
					out = append(out, byte(rng.IntN(256)))
				default:
					out = append(out, byte(i*17))
				}
			}
		}
	}
	return out[:size]
}

// TestRoundTripFagen decodes what the C encoder makes of various inputs.
func TestRoundTripFagen(t *testing.T) {
	codectest.FagenPath(t)
	rng := rand.New(rand.NewPCG(5, 6))
	text := codectest.SourceText(t, 150<<10)
	random := make([]byte, 40<<10)
	codectest.FillBytes(rng, random)
	inputs := map[string][]byte{
		"empty":       nil,
		"one byte":    {42},
		"odd":         tableFile(rng, 12345),
		"tables":      tableFile(rng, 400<<10),
		"text":        text,
		"random":      random,
		"zeros":       make([]byte, 100<<10),
		"short":       text[:31],
		"tables 64k":  tableFile(rng, 256<<10),
		"tables 64k+": tableFile(rng, 64<<10+1),
	}
	for name, in := range inputs {
		for _, m := range []string{"delta", "delta:64kb"} {
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
		{"tables", "delta", tableFile(rng, 60<<10)},
		{"tables_blocks", "delta:16kb", tableFile(rng, 50<<10)},
		{"text", "delta", codectest.SourceText(t, 20<<10)},
		{"random", "delta", random},
		{"empty", "delta", nil},
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
		stream, _ := randomBlock(rng)
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
		c, _ := method.Lookup("delta", &method.Env{})
		_ = c.Decode(&codectest.LimitWriter{Max: 4 << 20}, bytes.NewReader(stream))
	})
}

// BenchmarkDecode times the Go decoder on a stream the C encoder made of a 32 MB file full of tables.
func BenchmarkDecode(b *testing.B) {
	in := tableFile(rand.New(rand.NewPCG(11, 12)), 32<<20)
	stream := codectest.Fagen(b, in, "c", "delta")
	if path := os.Getenv("DELTA_BENCH_STREAM"); path != "" {
		if err := os.WriteFile(path, stream, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	c := codectest.Parse(b, "delta")
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
