package lzma

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method     string
		ok         bool
		dict       uint32
		lc, lp, pb uint32
	}{
		{"lzma", true, 64 << 20, 3, 0, 2},
		{"lzma:1mb:normal:bt4:32", true, 1 << 20, 3, 0, 2},
		{"lzma:d16m:lc8:lp1:pb0", true, 16 << 20, 8, 1, 0},
		{"lzma:24", true, 64 << 20, 3, 0, 2}, // a bare number is numFastBytes
		{"lzma:64kb:fast:hc4", true, 64 << 10, 3, 0, 2},
		{"lzma:ht4:max:ultra:fastest", true, 64 << 20, 3, 0, 2},
		{"lzma:mf=BT2:mc8:a0:fb273", true, 64 << 20, 3, 0, 2},
		{"lzma:h20", true, 64 << 20, 3, 0, 2},
		{"lzma:d20", true, 1 << 20, 3, 0, 2}, // parseMem without a suffix: a power of two
		{"lzma:xyz", false, 0, 0, 0, 0},
		{"lzma:mf=zz", false, 0, 0, 0, 0},
		{"lzma:", false, 0, 0, 0, 0},
		{"lzma:lcx", false, 0, 0, 0, 0},
		{"lzma:*lcx", true, 64 << 20, 0, 0, 2},     // optional: the error is ignored...
		{"lzma:*lcx:lc1", false, 0, 0, 0, 0},       // ...but sticks to the next named parameter, as in C
		{"lzma:*lcx:bt4", true, 64 << 20, 0, 0, 2}, // an unnamed one resets it
		{"lzma:*mf=zz:normal", false, 0, 0, 0, 0},
		{"lzmax", false, 0, 0, 0, 0},
	}
	for _, tt := range tests {
		c, ok := Parse(method.Params(tt.method), nil)
		if ok != tt.ok {
			t.Errorf("Parse(%q) ok = %v, want %v", tt.method, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		got := c.(*codec)
		if got.dictSize != tt.dict || got.lc != tt.lc || got.lp != tt.lp || got.pb != tt.pb {
			t.Errorf("Parse(%q) = %+v, want dict=%d lc=%d lp=%d pb=%d", tt.method, *got, tt.dict, tt.lc, tt.lp, tt.pb)
		}
	}
}

func decode(t testing.TB, m string, packed []byte) ([]byte, error) {
	t.Helper()
	c, ok := Parse(method.Params(m), nil)
	if !ok {
		t.Fatalf("Parse(%q) failed", m)
	}
	var out bytes.Buffer
	err := c.Decode(&out, bytes.NewReader(packed))
	return out.Bytes(), err
}

func TestRoundTripFagen(t *testing.T) {
	codectest.FagenPath(t)
	text := codectest.SourceText(t, 3<<20)
	rnd := make([]byte, 300000)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range rnd {
		rnd[i] = byte(r.Uint32())
	}
	inputs := map[string][]byte{
		"empty":  nil,
		"one":    {42},
		"text":   text,
		"random": rnd,
		"zeros":  make([]byte, 1<<20+7),
		"short":  text[:19],
	}
	methods := []string{"lzma", "lzma:1mb:normal:bt4:32", "lzma:64kb:fast", "lzma:16kb:max:lc0:lp2:pb4",
		"lzma:8mb:lc8:lp4:pb0", "lzma:4kb:ultra", "lzma:256kb:hc4"}
	for name, in := range inputs {
		for _, m := range methods {
			t.Run(name+"/"+m, func(t *testing.T) {
				t.Parallel()
				packed := codectest.Fagen(t, in, "c", m)
				// The C decoder is the reference: same output, same success. (With tiny dictionaries the C
				// encoder emits distances its own decoder rejects; Go must reject them the same way.)
				ref := codectest.FagenTry(t, packed, "d", m)
				want := ref.Out
				got, err := decode(t, m, packed)
				if (err == nil) != ref.OK() {
					t.Fatalf("Go err = %v, C exit %d", err, ref.Exit)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("Go decoded %d bytes, C %d bytes; they differ", len(got), len(want))
				}
				if ref.OK() && !bytes.Equal(got, in) {
					t.Fatal("decoded data differs from the input")
				}
				// Same result when the input arrives a byte at a time.
				c, _ := Parse(method.Params(m), nil)
				var out bytes.Buffer
				err2 := c.Decode(&out, &oneByteReader{bytes.NewReader(packed)})
				if (err2 == nil) != (err == nil) || !bytes.Equal(out.Bytes(), got) {
					t.Fatalf("byte-by-byte decoding differs (err %v)", err2)
				}
			})
		}
	}
}

// oneByteReader returns one byte per Read (io.ReadFull still fills buffers; this exercises the pipe-like path).
type oneByteReader struct{ r io.Reader }

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return o.r.Read(p)
}

func TestErrors(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	in := codectest.SourceText(t, 200000)
	packed := codectest.Fagen(t, in, "c", "lzma:1mb")
	tests := []struct {
		name   string
		method string
		data   []byte
	}{
		{"empty stream", "lzma", nil},
		{"first byte not zero", "lzma", append([]byte{1}, packed[1:]...)},
		{"truncated", "lzma:1mb", packed[:len(packed)-10]},
		{"no end marker", "lzma:1mb", packed[:5]},
		{"lc too large", "lzma:lc9", packed},
		{"pb too large", "lzma:pb5", packed},
		{"zero dictionary", "lzma:d0b", packed},
	}
	for _, tt := range tests {
		_, err := decode(t, tt.method, tt.data)
		if !errors.Is(err, method.ErrBadData) {
			t.Errorf("%s: err = %v, want ErrBadData", tt.name, err)
		}
	}
}

// stopWriter accepts n bytes, then asks for no more data.
type stopWriter struct{ n int }

func (s *stopWriter) Write(p []byte) (int, error) {
	if s.n -= len(p); s.n <= 0 {
		return len(p), method.ErrStop
	}
	return len(p), nil
}

func TestStop(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	in := codectest.SourceText(t, 2<<20)
	packed := codectest.Fagen(t, in, "c", "lzma:1mb")
	c, _ := Parse(method.Params("lzma:1mb"), nil)
	if err := c.Decode(&stopWriter{n: 300000}, bytes.NewReader(packed)); !errors.Is(err, method.ErrStop) {
		t.Fatalf("err = %v, want ErrStop", err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0}, uint8(3), uint8(0), uint8(2))
	f.Add([]byte{0, 0x41, 0x10, 0x88, 0x34, 0xfe, 0x01, 0x02, 0x80}, uint8(0), uint8(4), uint8(4))
	f.Fuzz(func(t *testing.T, data []byte, lc, lp, pb uint8) {
		c := &codec{dictSize: 4096, lc: uint32(lc % 10), lp: uint32(lp % 6), pb: uint32(pb % 6)}
		w := &limitWriter{n: 1 << 20}
		_ = c.Decode(w, bytes.NewReader(data))
	})
}

type limitWriter struct{ n int }

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n -= len(p); l.n < 0 {
		return 0, method.ErrStop
	}
	return len(p), nil
}

func BenchmarkDecode(b *testing.B) {
	codectest.FagenPath(b)
	in := codectest.SourceText(b, 8<<20)
	if f := os.Getenv("LZMA_BENCH_FILE"); f != "" { // e.g. a large binary, less repetitive than the sources
		var err error
		if in, err = os.ReadFile(f); err != nil {
			b.Fatal(err)
		}
	}
	packed := codectest.Fagen(b, in, "c", "lzma:8mb:normal")
	c, _ := Parse(method.Params("lzma:8mb:normal"), nil)
	b.SetBytes(int64(len(in)))
	b.ResetTimer()
	for b.Loop() {
		if err := c.Decode(io.Discard, bytes.NewReader(packed)); err != nil {
			b.Fatal(err)
		}
	}
}
