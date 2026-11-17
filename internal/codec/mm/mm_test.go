package mm_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/n0madic/go-unarc/internal/codec/mm"
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
		{"mm", true},
		{"mm:s", true},
		{"mm:f", true},
		{"mm:s5", true}, // the letter alone decides, whatever follows
		{"mm:d1", true},
		{"mm:d9", true},
		{"mm:c2", true},
		{"mm:w16", true},
		{"mm:o44", true},
		{"mm:r1", true},
		{"mm:2*16", true},
		{"mm:2*16f", true},
		{"mm:2*16 f", true},
		{"mm:2*16x", true}, // not "f", but "2*16" alone is fine
		{"mm:+2*-16", true},
		{"mm:2*0x10", true}, // "2*0", then junk
		{"mm:2*16:o44:d1", true},
		{"mm:c2:w16:f", true},
		{"mm:d", false},
		{"mm:dx", false},
		{"mm:c", false},
		{"mm:o", false},
		{"mm:x", false},
		{"mm:", false},
		{"mm:2*", false},
		{"mm:*16", false},
		{"mm:2", false},
		{"mm:2x16", false},
		{"mm:2 * 16", false},
		{"mm:0x2*16", false},
		{"mm:16", false},
		{"mm:s:x", false},
		{"mmm", false},
		{"m", false},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			c, ok := mm.Parse(method.Params(tt.method), &method.Env{})
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
	pieces := []string{"s", "s1", "f", "fx", "d", "d1", "d9", "dx", "d-1", "c", "c2", "w", "w16", "wx", "o", "o44", "o3k", "r", "r1",
		"2*16", "2*16f", "2*16 f", "2*16 fx", "2*16\tf", "2*16x", "2*16f2", "2*", "*16", "2", "x", "", "16", "+2*8", "-1*8", " 2*8",
		"2*-8", "2*+8", "2 *8", "2* 8", "0x2*8", "2*0x8", "99999999999*8", "2*99999999999", "1*8:", "5%", "b1mb", "2*16F"}
	cases := []string{"mm"}
	for _, a := range pieces {
		cases = append(cases, "mm:"+a)
		for _, b := range pieces {
			cases = append(cases, "mm:"+a+":"+b)
		}
	}
	codectest.CheckAgainstC(t, mm.Parse, cases)
}

// model is a test-side encoder, independent of the decoder: it writes what mm_compress writes for the given
// layout, with the filter done by plain per-sample arithmetic.
func encode(data []byte, numChan, bits, offset int) []byte {
	offset = min(offset, len(data))
	byteSize := (bits + 7) / 8
	n := numChan * byteSize
	out := []byte{1, byte(numChan), byte(bits)}
	out = binary.LittleEndian.AppendUint32(out, uint32(offset))
	out = append(out, data[:offset]...)
	out = append(out, make([]byte, (len(out)+n-1)/n*n-len(out))...) // align the data to the sample size
	body := bytes.Clone(data[offset:])
	if byteSize <= 4 {
		diff(body, numChan, byteSize)
	}
	return append(out, body...)
}

// diff replaces every whole sample of b by its difference to the previous sample of the channel.
func diff(b []byte, numChan, byteSize int) {
	prev := make([]uint32, numChan)
	for p := 0; p+numChan*byteSize <= len(b); p += numChan * byteSize {
		for c := range numChan {
			s := b[p+c*byteSize : p+(c+1)*byteSize]
			var v uint32
			for i := byteSize - 1; i >= 0; i-- {
				v = v<<8 | uint32(s[i])
			}
			d := v - prev[c]
			for i := range s {
				s[i] = byte(d >> (8 * i))
			}
			prev[c] = v
		}
	}
}

// stored is a stream of the stored kind.
func stored(payload []byte) []byte { return append([]byte{0}, payload...) }

func TestDecodeKnown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stream []byte
		want   []byte
	}{
		{"stored", stored([]byte("any data")), []byte("any data")},
		{"stored nothing", stored(nil), nil},
		{"mono 8 bit", []byte{1, 1, 8, 0, 0, 0, 0, 10, 1, 1, 255}, []byte{10, 11, 12, 11}},
		{"stereo 8 bit", []byte{1, 2, 8, 0, 0, 0, 0, 0 /*pad*/, 1, 100, 1, 200, 254, 1}, []byte{1, 100, 2, 44, 0, 45}},
		{"three channels 8 bit", []byte{1, 3, 8, 0, 0, 0, 0, 0, 0 /*pad*/, 1, 2, 3, 1, 1, 1, 1, 1, 1}, []byte{1, 2, 3, 2, 3, 4, 3, 4, 5}},
		{"mono 16 bit", []byte{1, 1, 16, 0, 0, 0, 0, 0 /*pad*/, 0xff, 0x00, 0x01, 0x00, 0x00, 0xff}, []byte{0xff, 0x00, 0x00, 0x01, 0x00, 0x00}},
		{"stereo 16 bit with a value in the padding", []byte{1, 2, 16, 0, 0, 0, 0, 5 /*pad*/, 1, 0, 3, 0, 2, 0, 4, 0}, []byte{6, 0, 3, 0, 8, 0, 7, 0}},
		{"mono 24 bit with a value in the padding", []byte{1, 1, 24, 0, 0, 0, 0, 0xff, 0xff /*pad*/, 1, 0, 0, 0xff, 0xff, 0xff}, []byte{0, 0, 1, 0xff, 0xff, 0}},
		{"mono 32 bit", []byte{1, 1, 32, 0, 0, 0, 0, 0 /*pad*/, 1, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff}, []byte{1, 0, 0, 0xff, 0, 0, 0, 0xff}},
		{"header is copied", []byte{1, 1, 8, 3, 0, 0, 0, 'a', 'b', 'c', 5, 1}, []byte("abc\x05\x06")},
		{"header and padding", []byte{1, 2, 16, 2, 0, 0, 0, 'H', 'I', 0, 0, 0 /*pad*/, 1, 0, 1, 0, 1, 0, 1, 0}, []byte{'H', 'I', 1, 0, 1, 0, 2, 0, 2, 0}},
		{"partial sample at the end is not filtered", []byte{1, 2, 8, 0, 0, 0, 0, 0, 1, 1, 1, 1, 7}, []byte{1, 1, 2, 2, 7}},
		{"partial sample only", []byte{1, 2, 16, 0, 0, 0, 0, 0 /*pad*/, 9, 9, 9}, []byte{9, 9, 9}},
		{"no data at all", []byte{1, 2, 16, 0, 0, 0, 0, 0 /*pad*/}, nil},
		{"more than 32 bits pass through", []byte{1, 1, 40, 0, 0, 0, 0, 0, 0, 0 /*pad*/, 1, 2, 3, 4, 5, 1, 2, 3, 4, 5}, []byte{1, 2, 3, 4, 5, 1, 2, 3, 4, 5}},
		{"odd bit counts use whole bytes", []byte{1, 1, 12, 0, 0, 0, 0, 0 /*pad*/, 1, 0, 1, 0}, []byte{1, 0, 2, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := codectest.Decode(codectest.Parse(t, "mm"), tt.stream)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("got % x, want % x", got, tt.want)
			}
		})
	}
}

// layouts are the (channels, bits per sample) pairs of the random tests, covering every code path of the
// filter: 1 and 2 channels have their own loops, so do the sample sizes of 1, 2, 3 and 4 bytes.
var layouts = [][2]int{
	{1, 8}, {2, 8}, {3, 8}, {4, 8}, {6, 8}, {255, 8},
	{1, 16}, {2, 16}, {3, 16}, {6, 16}, {17, 16},
	{1, 24}, {2, 24}, {3, 24}, {5, 24},
	{1, 32}, {2, 32}, {3, 32}, {8, 32},
	{1, 12}, {2, 20}, {1, 40}, {2, 64}, {3, 256 - 8},
}

// waveform returns size bytes that look like samples: smooth in every channel.
func waveform(rng *rand.Rand, size, numChan, bits int) []byte {
	byteSize := (bits + 7) / 8
	out := make([]byte, size)
	phase := make([]float64, numChan)
	step := make([]float64, numChan)
	for c := range step {
		step[c] = 0.01 + rng.Float64()/10
	}
	for p := 0; p+numChan*byteSize <= size; p += numChan * byteSize {
		for c := range numChan {
			phase[c] += step[c]
			v := uint64(int64(math.Sin(phase[c])*1e6) + int64(rng.IntN(64)))
			s := out[p+c*byteSize : p+(c+1)*byteSize]
			for i := range s {
				s[i] = byte(v >> (8 * i))
			}
		}
	}
	// A partial sample at the end stays at zero unless a random byte is put there.
	for i := size - size%(numChan*byteSize); i < size; i++ {
		out[i] = byte(rng.IntN(256))
	}
	return out
}

func TestDecodeAgainstModel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	c := codectest.Parse(t, "mm")
	sizes := []int{0, 1, 2, 3, 7, 100, 1000, 65536, 65537, 150000}
	for _, l := range layouts {
		for _, size := range sizes {
			offset := []int{0, 1, 5, 44, 100000}[rng.IntN(5)]
			data := waveform(rng, size, l[0], l[1])
			stream := encode(data, l[0], l[1], offset)
			got, err := codectest.Decode(c, stream)
			if err != nil {
				t.Fatalf("%d*%d size %d offset %d: %v", l[0], l[1], size, offset, err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("%d*%d size %d offset %d: output differs", l[0], l[1], size, offset)
			}
		}
	}
}

// TestDecodeSplitReads: the decoder must not depend on how the input arrives.
func TestDecodeSplitReads(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(3, 4))
	c := codectest.Parse(t, "mm")
	readers := map[string]func(io.Reader) io.Reader{
		"one byte":  iotest.OneByteReader,
		"half":      iotest.HalfReader,
		"data err":  iotest.DataErrReader,
		"no change": func(r io.Reader) io.Reader { return r },
	}
	for name, wrap := range readers {
		for _, l := range [][2]int{{1, 8}, {2, 16}, {3, 24}, {5, 32}} {
			data := waveform(rng, 70000, l[0], l[1])
			stream := encode(data, l[0], l[1], 10)
			var out bytes.Buffer
			if err := c.Decode(&out, wrap(bytes.NewReader(stream))); err != nil {
				t.Fatalf("%s %d*%d: %v", name, l[0], l[1], err)
			}
			if !bytes.Equal(out.Bytes(), data) {
				t.Fatalf("%s %d*%d: output differs", name, l[0], l[1])
			}
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()
	u32 := func(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
	// Offset 2 and samples of 4 bytes: 3+4 bytes, the header "HI" and 3 bytes of padding make 12 bytes.
	valid := []byte{1, 2, 16, 2, 0, 0, 0, 'H', 'I', 0, 0, 0, 5, 0, 6, 0, 7, 0, 8, 0}
	const validHeader = 12
	tests := []struct {
		name   string
		stream []byte
	}{
		{"empty", nil},
		{"flags 2: reorder bytes", append([]byte{2, 2, 16}, u32(0)...)},
		{"flags 3: diff and reorder bytes", append([]byte{3, 2, 16}, u32(0)...)},
		{"flags 5", append([]byte{5, 2, 16}, u32(0)...)},
		{"flags 0x81", append([]byte{0x81, 2, 16}, u32(0)...)},
		{"flags only", []byte{1}},
		{"no word size", []byte{1, 2}},
		{"no offset", []byte{1, 2, 16}},
		{"short offset", []byte{1, 2, 16, 0, 0, 0}},
		{"negative offset", append([]byte{1, 2, 16}, u32(0xffffffff)...)},
		{"smallest negative offset", append([]byte{1, 2, 16}, u32(0x80000000)...)},
		{"header cut", append([]byte{1, 2, 16}, append(u32(3), 'a', 'b')...)},
		{"huge header", append([]byte{1, 2, 16}, append(u32(0x7fffffff), 'a', 'b')...)},
		{"no channels", append([]byte{1, 0, 16}, u32(0)...)},
		{"no bits", append([]byte{1, 2, 0}, u32(0)...)},
		{"no channels and no bits", append([]byte{1, 0, 0}, u32(0)...)},
		{"padding cut", append([]byte{1, 3, 16}, u32(0)...)}, // 7 bytes so far, 6 per sample: 5 padding bytes
		{"padding half cut", append(append([]byte{1, 3, 16}, u32(0)...), 0, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := codectest.Decode(codectest.Parse(t, "mm"), tt.stream)
			if !errors.Is(err, method.ErrBadData) {
				t.Fatalf("err = %v, want ErrBadData", err)
			}
		})
	}
	t.Run("every cut of the header", func(t *testing.T) {
		t.Parallel()
		for n := range validHeader {
			if _, err := codectest.Decode(codectest.Parse(t, "mm"), valid[:n]); !errors.Is(err, method.ErrBadData) {
				t.Fatalf("cut at %d: err = %v, want ErrBadData", n, err)
			}
		}
		if got, err := codectest.Decode(codectest.Parse(t, "mm"), valid[:validHeader]); err != nil || string(got) != "HI" {
			t.Fatalf("header only: %q, %v", got, err)
		}
	})
	t.Run("the file header is written before a later error", func(t *testing.T) {
		t.Parallel()
		got, err := codectest.Decode(codectest.Parse(t, "mm"), valid[:validHeader-1]) // cuts the padding
		if !errors.Is(err, method.ErrBadData) || string(got) != "HI" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
}

func TestDecodeStopsOnWriteError(t *testing.T) {
	t.Parallel()
	streams := map[string][]byte{
		"stored":   stored([]byte("abc")),
		"filtered": encode([]byte("abcdefgh"), 2, 8, 0),
		"header":   encode([]byte("abcdefgh"), 2, 8, 8),
	}
	for name, stream := range streams {
		for _, max := range []int{0, 2} {
			err := codectest.Parse(t, "mm").Decode(&codectest.LimitWriter{Max: max}, bytes.NewReader(stream))
			if !errors.Is(err, codectest.ErrLimit) {
				t.Errorf("%s, limit %d: err = %v, want the writer's error", name, max, err)
			}
		}
	}
}

// TestHugeHeaderIsLazy: a stream that claims a header of 2 GB gets memory as it produces data.
func TestHugeHeaderIsLazy(t *testing.T) {
	stream := append([]byte{1, 2, 16}, binary.LittleEndian.AppendUint32(nil, math.MaxInt32)...)
	stream = append(stream, "short"...)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := codectest.Decode(codectest.Parse(t, "mm"), stream)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, method.ErrBadData) {
		t.Fatalf("err = %v, want ErrBadData", err)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 16<<20 {
		t.Errorf("a cut stream allocated %d bytes", grown)
	}
}

// TestRoundTripFagen decodes what the C encoder makes of various inputs.
func TestRoundTripFagen(t *testing.T) {
	codectest.FagenPath(t)
	rng := rand.New(rand.NewPCG(5, 6))
	random := make([]byte, 50000)
	codectest.FillBytes(rng, random)
	type input struct {
		name           string
		data           []byte
		channels, bits int
	}
	var inputs []input
	for _, l := range layouts {
		if l[0] == 255 || l[1] > 32 {
			continue // the C encoder keeps these for the model test only
		}
		for _, size := range []int{100, 70001, 1<<20 + 77, 2<<20 + 5} {
			inputs = append(inputs, input{fmt.Sprintf("%dx%d/%d", l[0], l[1], size), waveform(rng, size, l[0], l[1]), l[0], l[1]})
		}
	}
	inputs = append(inputs, input{"noise", random, 2, 16}, input{"zeros", make([]byte, 100000), 2, 16})
	for _, in := range inputs {
		for _, extra := range []string{"", ":o44", ":o3", ":o70000"} {
			if extra != "" && len(in.data) > 1<<20 {
				continue
			}
			m := fmt.Sprintf("mm:%d*%d%s", in.channels, in.bits, extra)
			t.Run(in.name+m, func(t *testing.T) {
				stream := codectest.Fagen(t, in.data, "c", m)
				got, err := codectest.Decode(codectest.Parse(t, m), stream)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, in.data) {
					t.Fatalf("round trip differs: got %d bytes (%s), want %d bytes (%s)", len(got), codectest.Sum(got), len(in.data), codectest.Sum(in.data))
				}
				if want := encode(in.data, in.channels, in.bits, min(offsetOf(extra), len(in.data))); !bytes.Equal(stream, want) {
					t.Errorf("the test encoder disagrees with the C encoder on the stream (%d and %d bytes)", len(want), len(stream))
				}
			})
		}
	}
	t.Run("float", func(t *testing.T) {
		data := waveform(rng, 100000, 2, 32)
		stream := codectest.Fagen(t, data, "c", "mm:2*32f")
		got, err := codectest.Decode(codectest.Parse(t, "mm"), stream)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("round trip: %v", err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		stream := codectest.Fagen(t, nil, "c", "mm:2*16")
		if len(stream) != 0 {
			t.Skipf("C encoder writes %d bytes for empty input", len(stream))
		}
	})
	t.Run("stored", func(t *testing.T) {
		stream := codectest.Fagen(t, random, "c", "mm:d1")
		if len(stream) == 0 || stream[0] != 0 {
			t.Skip("the C encoder did not store the data")
		}
		got, err := codectest.Decode(codectest.Parse(t, "mm"), stream)
		if err != nil || !bytes.Equal(got, random) {
			t.Fatalf("round trip: %v", err)
		}
	})
}

// offsetOf reads the offset of an "o<n>" parameter such as ":o44".
func offsetOf(extra string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(extra, ":o"))
	return n
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
		{"stereo16", "mm:2*16", waveform(rng, 200003, 2, 16)}, // more than one 64 KB chunk, and a partial sample
		{"mono8", "mm:1*8", waveform(rng, 40000, 1, 8)},
		{"bitmap24", "mm:3*24:o54", waveform(rng, 60000, 3, 24)},
		{"float32", "mm:2*32f", waveform(rng, 40000, 2, 32)},
		{"six16", "mm:6*16:o7", waveform(rng, 50000, 6, 16)},
		{"mono32", "mm:1*32", waveform(rng, 30000, 1, 32)},
		{"stored", "mm:d1", random},
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
		manifest.WriteString(strings.Join([]string{file, "mm", codectest.Sum(c.in), strconv.Itoa(len(c.in))}, "\t") + "\n")
	}
	if err := os.WriteFile(filepath.Join("testdata", "manifest.txt"), []byte(manifest.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func FuzzDecode(f *testing.F) {
	rng := rand.New(rand.NewPCG(9, 10))
	for _, l := range layouts[:12] {
		f.Add(encode(waveform(rng, 200, l[0], l[1]), l[0], l[1], rng.IntN(10)))
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
		c, _ := method.Lookup("mm", &method.Env{})
		_ = c.Decode(&codectest.LimitWriter{Max: 4 << 20}, bytes.NewReader(stream))
	})
}

// benchLayouts are the code paths worth timing: the sample sizes and the one- and two-channel special cases.
var benchLayouts = []struct {
	name           string
	channels, bits int
}{
	{"1x8", 1, 8}, {"2x8", 2, 8}, {"2x16", 2, 16}, {"6x16", 6, 16}, {"2x24", 2, 24}, {"1x32", 1, 32}, {"2x32", 2, 32},
}

// BenchmarkDecode times the Go decoder on streams the C encoder made of 32 MB of samples. With
// BENCH_STREAM_DIR set, the streams are saved there as mm_<layout>.bin, for timing the C decoder on them.
func BenchmarkDecode(b *testing.B) {
	const size = 32 << 20
	rng := rand.New(rand.NewPCG(11, 12))
	for _, l := range benchLayouts {
		b.Run(l.name, func(b *testing.B) {
			data := waveform(rng, size, l.channels, l.bits)
			stream := codectest.Fagen(b, data, "c", fmt.Sprintf("mm:%d*%d", l.channels, l.bits))
			if dir := os.Getenv("BENCH_STREAM_DIR"); dir != "" {
				if err := os.WriteFile(filepath.Join(dir, "mm_"+l.name+".bin"), stream, 0o644); err != nil {
					b.Fatal(err)
				}
			}
			c := codectest.Parse(b, "mm")
			b.SetBytes(int64(len(data)))
			sink := &codectest.MemWriter{Buf: make([]byte, 0, len(data))}
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
		})
	}
}
