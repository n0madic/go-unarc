package tornado

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

// BenchmarkDecode decodes 8 MiB of text (the unarc sources, mixed with generated pieces) packed by every
// coder. TOR_BENCH_FILE names another input (e.g. a large binary); TOR_BENCH_STREAM and TOR_BENCH_METHOD
// give a stream that is already packed, to be timed against "fagen d <method> < stream".
func BenchmarkDecode(b *testing.B) {
	if stream := os.Getenv("TOR_BENCH_STREAM"); stream != "" {
		packed, err := os.ReadFile(stream)
		if err != nil {
			b.Fatal(err)
		}
		m := os.Getenv("TOR_BENCH_METHOD")
		out, err := decode(b, m, packed)
		if err != nil {
			b.Fatal(err)
		}
		benchDecode(b, m, packed, len(out))
		return
	}
	codectest.FagenPath(b)
	in := append(codectest.SourceText(b, 6<<20), genMixed(2<<20, 51)...)
	if f := os.Getenv("TOR_BENCH_FILE"); f != "" {
		var err error
		if in, err = os.ReadFile(f); err != nil {
			b.Fatal(err)
		}
	}
	for _, m := range []string{"tor:1", "tor:2", "tor:3", "tor:4", "tor:5", "tor:6"} {
		packed := codectest.Fagen(b, in, "c", m)
		b.Run(m, func(b *testing.B) { benchDecode(b, m, packed, len(in)) })
	}
}

func benchDecode(b *testing.B, m string, packed []byte, size int) {
	c, ok := Parse(method.Params(m), nil)
	if !ok {
		b.Fatalf("Parse(%q) failed", m)
	}
	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := c.Decode(io.Discard, bytes.NewReader(packed)); err != nil {
			b.Fatal(err)
		}
	}
}
