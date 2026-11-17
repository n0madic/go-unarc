package tornado

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"
	"time"

	"github.com/n0madic/go-unarc/internal/method"
)

// decodeLimited decodes with the Go decoder, giving up on the output after limit bytes.
func decodeLimited(m string, packed []byte, limit int) error {
	c, ok := Parse(method.Params(m), nil)
	if !ok {
		return fmt.Errorf("Parse(%q) failed", m)
	}
	return c.Decode(&limitWriter{n: limit}, bytes.NewReader(packed))
}

func readStreams(t testing.TB, maxSize int) map[string][]byte {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "*.tor"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no streams in testdata: %v", err)
	}
	streams := map[string][]byte{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) <= maxSize {
			streams[filepath.Base(f)] = data
		}
	}
	return streams
}

// TestCorrupt feeds damaged streams to the decoder. Whatever they hold, it must neither panic nor hang
// nor fail with anything but ErrBadData (or ErrStop, when the output limit is reached).
func TestCorrupt(t *testing.T) {
	t.Parallel()
	for name, packed := range readStreams(t, 100000) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newRand(uint64(len(packed)))
			check := func(what string, stream []byte) {
				done := make(chan error, 1)
				go func() { done <- decodeLimited("tor", stream, 4<<20) }()
				select {
				case err := <-done:
					if err != nil && !errors.Is(err, method.ErrBadData) && !errors.Is(err, method.ErrStop) {
						t.Errorf("%s: unexpected error %v", what, err)
					}
				case <-time.After(30 * time.Second):
					t.Fatalf("%s: the decoder does not finish", what)
				}
			}
			for i := range 40 {
				// Truncated.
				cut := r.IntN(len(packed) + 1)
				check(fmt.Sprintf("truncated to %d", cut), packed[:cut])
				// Bytes changed.
				bad := bytes.Clone(packed)
				for range 1 + r.IntN(8) {
					bad[r.IntN(len(bad))] = byte(r.Uint32())
				}
				if i%2 == 0 && len(bad) > headerSize { // keep the header, to get to the data
					copy(bad, packed[:headerSize])
				}
				check("changed bytes", bad)
				// Garbage after the header.
				if len(packed) > headerSize {
					junk := append(bytes.Clone(packed[:headerSize]), genRandom(1+r.IntN(5000), r.Uint64())...)
					check("garbage", junk)
				}
			}
		})
	}
}

func TestBadHeader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"one byte", []byte{coderArith}},
		{"five bytes", []byte{coderArith, 4, 0, 0, 0}},
		{"storing", []byte{0, 4, 0, 0, 1, 0}},
		{"unknown coder", []byte{5, 4, 0, 0, 1, 0, 1, 2, 3}},
		{"coder 255", []byte{255, 4, 0, 0, 1, 0, 1, 2, 3}},
		{"header only", []byte{coderByte, 4, 0, 0, 1, 0}},
		{"header only, arith", []byte{coderArith, 4, 0, 0, 1, 0}},
		{"header only, huffman", []byte{coderHuffman, 4, 0, 0, 1, 0}},
		{"header only, bit", []byte{coderBit, 4, 0, 0, 1, 0}},
		{"4 GiB buffer", []byte{coderHuffman, 4, 255, 255, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0}},
	}
	for _, tt := range tests {
		err := decodeLimited("tor", tt.in, 1<<20)
		if !errors.Is(err, method.ErrBadData) {
			t.Errorf("%s: err = %v, want ErrBadData", tt.name, err)
		}
	}
}

// slowReader returns what its source holds in chunks of at most n bytes.
type chunkReader struct {
	r io.Reader
	n int
}

func (c chunkReader) Read(p []byte) (int, error) { return c.r.Read(p[:min(len(p), c.n)]) }

// TestReaders checks that the way the source delivers its data makes no difference, and that read errors
// and write errors reach the caller.
func TestReaders(t *testing.T) {
	t.Parallel()
	streams := readStreams(t, 200000)
	manifest := map[string]int{}
	for name, packed := range streams {
		want, err := decode(t, "tor", packed)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		manifest[name] = len(want)
		c, _ := Parse(method.Params("tor"), nil)
		for label, r := range map[string]io.Reader{
			"one byte":  iotest.OneByteReader(bytes.NewReader(packed)),
			"half":      iotest.HalfReader(bytes.NewReader(packed)),
			"chunks":    chunkReader{bytes.NewReader(packed), 1000},
			"data+err":  iotest.DataErrReader(bytes.NewReader(packed)),
			"odd":       chunkReader{bytes.NewReader(packed), 7},
			"timeout":   iotest.DataErrReader(iotest.OneByteReader(bytes.NewReader(packed))),
			"plain":     bytes.NewReader(packed),
			"bufio-ish": io.MultiReader(bytes.NewReader(packed[:len(packed)/2]), bytes.NewReader(packed[len(packed)/2:])),
		} {
			var out bytes.Buffer
			if err := c.Decode(&out, r); err != nil {
				t.Errorf("%s/%s: %v", name, label, err)
			} else if !bytes.Equal(out.Bytes(), want) {
				t.Errorf("%s/%s: output differs", name, label)
			}
		}
	}

	packed := streams["huffman.tor"]
	c, _ := Parse(method.Params("tor"), nil)
	// A read error comes back as it is (here with the data before it still to be decoded).
	boom := errors.New("boom")
	r := io.MultiReader(bytes.NewReader(packed[:len(packed)/2]), iotest.ErrReader(boom))
	if err := c.Decode(io.Discard, r); !errors.Is(err, boom) {
		t.Errorf("read error: err = %v, want %v", err, boom)
	}
	// So does a write error, wrapped or not.
	werr := errors.New("disk full")
	for _, name := range []string{"byte.tor", "bit.tor", "huffman.tor", "arith.tor", "wrap_huffman.tor"} {
		packed := readStreams(t, 1<<30)[name]
		if err := c.Decode(failWriter{werr}, bytes.NewReader(packed)); err != werr {
			t.Errorf("%s: write error: err = %v, want %v", name, err, werr)
		}
		if err := c.Decode(&limitWriter{n: 1000}, bytes.NewReader(packed)); err != method.ErrStop {
			t.Errorf("%s: err = %v, want ErrStop", name, err)
		}
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func FuzzDecode(f *testing.F) {
	for _, packed := range readStreams(f, 100000) {
		f.Add(packed)
		if len(packed) > 3000 {
			f.Add(packed[:3000]) // a shorter one fuzzes faster
		}
	}
	for coder := byte(1); coder <= coderArith; coder++ {
		f.Add([]byte{coder, 2, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		err := decodeLimited("tor", data, 4<<20)
		if err != nil && !errors.Is(err, method.ErrBadData) && !errors.Is(err, method.ErrStop) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
