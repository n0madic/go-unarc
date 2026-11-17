package grzip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/n0madic/go-unarc/internal/method"
)

// limitWriter fails after max bytes, like a consumer that has seen enough.
type limitWriter struct {
	n, max int
}

var errLimit = errors.New("limit reached")

// decodeLimited decodes with bounded output and a deadline and reports panics.
func decodeLimited(tb testing.TB, c *codec, threads int, stream []byte) error {
	tb.Helper()
	cc := *c
	cc.threads = threads
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("PANIC: %v", r)
			}
		}()
		done <- cc.Decode(&limitWriter{max: 64 << 20}, bytes.NewReader(stream))
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Minute):
		tb.Fatal("decoder hangs")
		return nil
	}
}

func checkDecodeError(tb testing.TB, err error) {
	tb.Helper()
	if err != nil && strings.Contains(err.Error(), "internal error") {
		tb.Errorf("a decoder panicked: %v", err)
		return
	}
	if err == nil || errors.Is(err, method.ErrBadData) || errors.Is(err, errLimit) {
		return
	}
	tb.Errorf("unexpected error: %v", err)
}

// TestCorrupt flips bytes and truncates the committed streams: the decoder must neither panic nor hang.
func TestCorrupt(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(99))
	for _, s := range readSamples(t) {
		stream, err := os.ReadFile(filepath.Join("testdata", s.name))
		if err != nil {
			t.Fatal(err)
		}
		c := mustParse(t, s.method)
		for i := 0; i < 60; i++ {
			bad := bytes.Clone(stream)
			for k := 0; k <= rng.Intn(3); k++ {
				pos := rng.Intn(len(bad))
				if i%3 == 0 {
					pos = rng.Intn(min(len(bad), 60)) // the first header
				}
				bad[pos] ^= byte(1 << rng.Intn(8))
			}
			checkDecodeError(t, decodeLimited(t, c, 2, bad))
		}
		for _, cut := range []int{1, 5, 27, 28, 29, len(stream) / 2, len(stream) - 1} {
			if cut >= len(stream) {
				continue
			}
			err := decodeLimited(t, c, 1, stream[:cut])
			if err == nil && cut != 0 {
				// A cut on a block boundary is a valid shorter stream.
				continue
			}
			checkDecodeError(t, err)
		}
	}
}

func TestTruncatedIsBadData(t *testing.T) {
	t.Parallel()
	stream, err := os.ReadFile("testdata/m1.grz")
	if err != nil {
		t.Fatal(err)
	}
	c := mustParse(t, "grzip")
	for _, n := range []int{1, 27, 28, 100, len(stream) - 1} {
		_, err := decode(c, 2, stream[:n])
		if !errors.Is(err, method.ErrBadData) {
			t.Errorf("stream cut to %d bytes: err = %v, want ErrBadData", n, err)
		}
	}
}

// TestErrStop: a writer that returns ErrStop ends decoding with that error, promptly, with no goroutine left.
func TestErrStop(t *testing.T) {
	// Not parallel: it counts the goroutines of the whole process.
	stream, err := os.ReadFile("testdata/blocks.grz")
	if err != nil {
		t.Fatal(err)
	}
	c := mustParse(t, "grzip")
	before := runtime.NumGoroutine()
	for _, threads := range []int{1, 4} {
		cc := *c
		cc.threads = threads
		w := &stopWriter{}
		err := cc.Decode(w, bytes.NewReader(stream))
		if !errors.Is(err, method.ErrStop) {
			t.Errorf("threads=%d: err = %v, want ErrStop", threads, err)
		}
		if w.calls != 1 {
			t.Errorf("threads=%d: %d writes after ErrStop, want exactly 1", threads, w.calls)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Errorf("goroutines leaked: %d before, %d after", before, after)
	}
}

type stopWriter struct{ calls int }

// failReader returns data, then an error.
type failReader struct {
	data []byte
	err  error
}

func TestSourceError(t *testing.T) {
	t.Parallel()
	stream, err := os.ReadFile("testdata/blocks.grz")
	if err != nil {
		t.Fatal(err)
	}
	c := mustParse(t, "grzip")
	boom := errors.New("boom")
	var out bytes.Buffer
	err = c.Decode(&out, &failReader{data: stream[:len(stream)/2], err: boom})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the source error", err)
	}
	if out.Len() == 0 {
		t.Error("blocks before the failure were not written")
	}
}

// TestPartialOutputBeforeError: blocks before a corrupt block are written before the error is returned.
func TestPartialOutputBeforeError(t *testing.T) {
	t.Parallel()
	good, err := os.ReadFile("testdata/blocks.grz")
	if err != nil {
		t.Fatal(err)
	}
	c := mustParse(t, "grzip")
	want, err := decode(c, 1, good)
	if err != nil {
		t.Fatal(err)
	}
	// Find the second block and damage its signature.
	_, first := firstBlock(t, good)
	bad := bytes.Clone(good)
	bad[first+24] = 1
	for _, threads := range []int{1, 4} {
		var out bytes.Buffer
		cc := *c
		cc.threads = threads
		err := cc.Decode(&out, bytes.NewReader(bad))
		if !errors.Is(err, method.ErrBadData) {
			t.Fatalf("threads=%d: err = %v", threads, err)
		}
		if out.Len() == 0 || !bytes.HasPrefix(want, out.Bytes()) {
			t.Errorf("threads=%d: partial output of %d bytes is not a prefix of the data", threads, out.Len())
		}
	}
}

// firstBlock returns the first block's header and the offset of the second block.
func firstBlock(tb testing.TB, stream []byte) (header, int) {
	tb.Helper()
	h, err := parseHeader(stream)
	if err != nil {
		tb.Fatal(err)
	}
	return h, headerSize + h.payload
}

func FuzzDecode(f *testing.F) {
	for _, name := range []string{"m1", "m2", "m3", "m4", "m1_strong", "nolzp", "lzp_small_hash", "delta16", "delta32", "delta32_tail", "reorder1", "reorder2", "stored", "tiny", "blocks"} {
		if data, err := os.ReadFile("testdata/" + name + ".grz"); err == nil {
			f.Add(data)
		}
	}
	c := &codec{blockSize: defaultBlockSize, threads: 2}
	f.Fuzz(func(t *testing.T, stream []byte) {
		err := c.Decode(&limitWriter{max: 32 << 20}, bytes.NewReader(stream))
		checkDecodeError(t, err)
	})
}

// ---------- benchmarks ----------

// recEncode is GRZip_Rec_Encode (the encoder side of the filters), to test recDecode on any data.
func recEncode(in []byte, mode int) []byte {
	size := len(in)
	out := make([]byte, size)
	le := binary.LittleEndian
	switch mode {
	case 1, 2:
		stride := 2 * mode
		k := 0
		for first := 0; first < stride; first++ {
			for i := first; i < size; i += stride {
				out[k] = in[i]
				k++
			}
		}
	case 3:
		n := size >> 1
		var pred uint16
		for i := 0; i < n; i++ {
			code := le.Uint16(in[2*i:])
			delta := code - pred
			pred = code
			if delta&0x8000 != 0 {
				delta = (^delta)<<1 | 1
			} else {
				delta <<= 1
			}
			out[i] = byte(delta >> 8)
			out[n+i] = byte(delta)
		}
		copy(out[2*n:], in[2*n:])
	case 4:
		n := size >> 2
		var pred uint32
		for i := 0; i < n; i++ {
			code := le.Uint32(in[4*i:])
			delta := code - pred
			pred = code
			if delta&0x80000000 != 0 {
				delta = (^delta)<<1 | 1
			} else {
				delta <<= 1
			}
			out[i] = byte(delta >> 24)
			out[n+i] = byte(delta)
			out[2*n+i] = byte(delta >> 8)
			out[3*n+i] = byte(delta >> 16)
		}
		copy(out[4*n:], in[4*n:])
	}
	return out
}

func TestRecDecode(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(5))
	for mode := 1; mode <= 4; mode++ {
		for size := 0; size < 70; size++ {
			for _, smooth := range []bool{false, true} {
				in := make([]byte, size)
				rng.Read(in)
				if smooth {
					for i := range in {
						in[i] = byte(i/4) + in[i]&3
					}
				}
				got := make([]byte, size)
				recDecode(got, recEncode(in, mode), mode)
				if !bytes.Equal(got, in) {
					t.Fatalf("mode %d size %d smooth=%v: round trip differs\n got %x\nwant %x", mode, size, smooth, got, in)
				}
			}
		}
	}
}

// mkBlock builds a block with the given header fields.
func mkBlock(size, mode, aux, fbp int32, payload []byte) []byte {
	b := make([]byte, headerSize, headerSize+len(payload))
	le := binary.LittleEndian
	le.PutUint32(b[0:], uint32(size))
	le.PutUint32(b[4:], uint32(mode))
	le.PutUint32(b[8:], uint32(aux))
	le.PutUint32(b[12:], uint32(fbp))
	le.PutUint32(b[16:], uint32(len(payload)))
	return append(b, payload...)
}

func storedBlock(data []byte) []byte { return mkBlock(int32(len(data)), modeStored, 0, 0, data) }

func container(filter int32, size int, parts ...[]byte) []byte {
	return mkBlock(int32(size), modeContainer, filter, 0, bytes.Join(parts, nil))
}

// TestContainers builds containers by hand, nested ones included, and checks them against recEncode.
func TestContainers(t *testing.T) {
	t.Parallel()
	c := mustParse(t, "grzip")
	data := []byte("The quick brown fox jumps over the lazy dog, 0123456789.")
	for filter := 1; filter <= 4; filter++ {
		planes := recEncode(data, filter)
		nparts := 4
		if filter&1 != 0 {
			nparts = 2
		}
		// Cut the planes into stored sub-blocks of equal size (the last one takes the rest).
		var parts [][]byte
		cut := len(planes) / nparts
		for i := 0; i < nparts; i++ {
			end := (i + 1) * cut
			if i == nparts-1 {
				end = len(planes)
			}
			parts = append(parts, storedBlock(planes[i*cut:end]))
		}
		got, err := decode(c, 1, container(int32(filter), len(data), parts...))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("filter %d: %q, %v", filter, got, err)
		}
	}

	// Nested: a filter-1 container whose two sub-blocks are filter-1 containers of stored blocks.
	want := []byte("0123456789abcdefghijklmnopqrstuv")
	planes := recEncode(want, 1)
	inner := func(p []byte) []byte { // decodes to p
		pl := recEncode(p, 1)
		return container(1, len(p), storedBlock(pl[:len(pl)/2]), storedBlock(pl[len(pl)/2:]))
	}
	nested := container(1, len(want), inner(planes[:16]), inner(planes[16:]))
	if got, err := decode(c, 2, nested); err != nil || !bytes.Equal(got, want) {
		t.Errorf("nested containers: %q, %v", got, err)
	}

	// Inconsistent containers are rejected.
	p1, p2 := storedBlock(planes[:16]), storedBlock(planes[16:])
	truncated := container(1, 32, p1, p2)
	truncated = truncated[:len(truncated)-1]
	binary.LittleEndian.PutUint32(truncated[16:], uint32(len(truncated)-headerSize))
	for name, bad := range map[string][]byte{
		"sub-blocks decode to less": container(1, 40, p1, p2),
		"sub-blocks decode to more": container(1, 20, p1, p2),
		"missing sub-block":         container(1, 32, p1),
		"filter 0":                  container(0, 32, p1, p2),
		"filter 5":                  container(5, 32, p1, p2),
		"filter -1":                 container(-1, 32, p1, p2),
		"sub-block past the end":    truncated,
	} {
		if _, err := decode(c, 1, bad); !errors.Is(err, method.ErrBadData) {
			t.Errorf("%s: err = %v, want ErrBadData", name, err)
		}
	}
}

// TestBadHeaders: invalid header fields are rejected.
func TestBadHeaders(t *testing.T) {
	t.Parallel()
	c := mustParse(t, "grzip")
	good := storedBlock([]byte("0123456789"))
	tests := map[string]func(b []byte){
		"reserved +20":  func(b []byte) { b[20] = 1 },
		"reserved +24":  func(b []byte) { b[27] = 0x80 },
		"negative size": func(b []byte) { binary.LittleEndian.PutUint32(b[0:], 0xFFFFFFFF) },
		"huge size":     func(b []byte) { binary.LittleEndian.PutUint32(b[0:], maxBlockSize+1) },
		"huge payload":  func(b []byte) { binary.LittleEndian.PutUint32(b[16:], maxBlockSize+1) },
		"neg payload":   func(b []byte) { binary.LittleEndian.PutUint32(b[16:], 0x80000000) },
		"payload > data": func(b []byte) {
			binary.LittleEndian.PutUint32(b[16:], 11)
		},
		"mode -3":      func(b []byte) { binary.LittleEndian.PutUint32(b[4:], 0xFFFFFFFD) },
		"mode min int": func(b []byte) { binary.LittleEndian.PutUint32(b[4:], 0x80000000) },
		"stored LZP params negative": func(b []byte) {
			binary.LittleEndian.PutUint32(b[8:], 0x80000100)
		},
		"stored LZP hash too large": func(b []byte) {
			binary.LittleEndian.PutUint32(b[8:], 0x00200000|31<<8)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			bad := bytes.Clone(good)
			mutate(bad)
			if _, err := decode(c, 1, bad); !errors.Is(err, method.ErrBadData) {
				t.Errorf("err = %v, want ErrBadData", err)
			}
		})
	}
}

// TestCorruptHeaders overwrites each header field of the sample blocks with edge values.
func TestCorruptHeaders(t *testing.T) {
	t.Parallel()
	edge := []uint32{0, 1, 3, 4, 7, 8, 9, 28, 255, 256, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFF, 0xFFFFFFFE, maxBlockSize, maxBlockSize + 1,
		0x40000000, 0x40000001, 0x00200f04, 0x01000000, 0x0000FF00}
	for _, s := range readSamples(t) {
		if s.size > 70000 {
			continue
		}
		stream, err := os.ReadFile(filepath.Join("testdata", s.name))
		if err != nil {
			t.Fatal(err)
		}
		c := mustParse(t, s.method)
		h, _ := firstBlock(t, stream)
		for _, field := range []int{0, 4, 8, 12} {
			for _, v := range append([]uint32{uint32(h.size), uint32(h.mode), uint32(h.aux), uint32(h.fbp)}, edge...) {
				for _, delta := range []uint32{0, 1, ^uint32(0)} {
					bad := bytes.Clone(stream)
					binary.LittleEndian.PutUint32(bad[field:], v+delta)
					checkDecodeError(t, decodeLimited(t, c, 1, bad))
				}
			}
		}
	}
}

func (w *limitWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	if w.n > w.max {
		return 0, errLimit
	}
	return len(p), nil
}

func (w *stopWriter) Write(p []byte) (int, error) {
	w.calls++
	return 0, method.ErrStop
}

func (r *failReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestResize(t *testing.T) {
	t.Parallel()
	b := make([]byte, 3, 10)
	if r := resize(b, 8); len(r) != 8 || &r[0] != &b[0] {
		t.Error("resize did not reuse the storage")
	}
	if r := resize(b, 11); len(r) != 11 {
		t.Error("resize did not grow")
	}
	if r := resize([]uint32(nil), 4); len(r) != 4 {
		t.Error("resize of a nil []uint32 did not grow")
	}
}
