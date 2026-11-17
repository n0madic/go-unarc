package tornado

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/n0madic/go-unarc/internal/codectest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/streams.txt lists the streams in testdata, made by the C encoder (fagen): the file, the method
// used, the size and SHA-256 of the data. `go test -run TestUpdateStreams -update` makes them again from the
// generators below; TestStreams needs neither fagen nor the generators.
var update = flag.Bool("update", false, "make the streams in testdata again with fagen")

type streamSpec struct {
	file, method string
	tables       bool // the data must hold data tables
	gen          func() []byte
}

var streamSpecs = []streamSpec{
	{"empty.tor", "tor:5", false, func() []byte { return nil }},
	{"onebyte.tor", "tor:3", false, func() []byte { return []byte{42} }},
	{"byte.tor", "tor:1", false, func() []byte { return genMixed(120000, 61) }},
	{"bit.tor", "tor:2", false, func() []byte { return genMixed(120000, 62) }},
	{"huffman.tor", "tor:3", true, func() []byte { return genMixed(150000, 63) }},
	{"huffman4.tor", "tor:4", true, func() []byte { return genMixed(150000, 64) }},
	{"arith.tor", "tor:5", true, func() []byte { return genMixed(150000, 65) }},
	{"arith6.tor", "tor:6", false, func() []byte { return genMixed(150000, 66) }},
	{"arith9.tor", "tor:9:b1mb", false, func() []byte { return genText(100000, 67) }},
	{"smallbuf.tor", "tor:5:b64kb", false, func() []byte { return genMixed(100000, 68) }},
	{"table32.tor", "tor:5", true, func() []byte { return genTable32(100000, 69) }},
	{"table16.tor", "tor:4", true, func() []byte { return genTable16(100000, 70) }},
	// More output than the ring buffer holds (the larger of the buffer and 8 MiB), so it wraps around.
	{"wrap_byte.tor", "tor:1", false, func() []byte { return genPeriodic(20<<20, 1000, 71) }},
	{"wrap_huffman.tor", "tor:4", false, func() []byte { return genPeriodic(20<<20, 100000, 72) }},
	{"wrap_arith.tor", "tor:5:b9mb", false, func() []byte { return genPeriodic(20<<20, 100000, 73) }},
	{"wrap_tables.tor", "tor:5:b9mb", true, func() []byte { return bytes.Repeat(genTable32(40000, 74), 20<<20/40000+1)[:20<<20] }},
}

func TestUpdateStreams(t *testing.T) {
	if !*update {
		t.Skip("run with -update to make the streams again")
	}
	codectest.FagenPath(t)
	var manifest strings.Builder
	for _, s := range streamSpecs {
		data := s.gen()
		packed := codectest.Fagen(t, data, "c", s.method)
		got, st, err := decodeStats(packed)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s: round trip fails: %v", s.file, err)
		}
		if s.tables && st.tables == 0 {
			t.Fatalf("%s: no data tables in the stream", s.file)
		}
		if err := os.WriteFile(filepath.Join("testdata", s.file), packed, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&manifest, "%s %s %d %s\n", s.file, s.method, len(data), hex.EncodeToString(sum[:]))
		t.Logf("%s: %d -> %d bytes, %+v", s.file, len(data), len(packed), st)
	}
	if err := os.WriteFile(filepath.Join("testdata", "streams.txt"), []byte(manifest.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStreams(t *testing.T) {
	t.Parallel()
	manifest, err := os.ReadFile(filepath.Join("testdata", "streams.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(manifest)) {
		var file, m, sum string
		var size int
		if _, err := fmt.Sscan(line, &file, &m, &size, &sum); err != nil {
			t.Fatalf("streams.txt: %q: %v", line, err)
		}
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			packed, err := os.ReadFile(filepath.Join("testdata", file))
			if err != nil {
				t.Fatal(err)
			}
			out, err := decode(t, m, packed)
			if err != nil {
				t.Fatal(err)
			}
			if got := sha256.Sum256(out); len(out) != size || hex.EncodeToString(got[:]) != sum {
				t.Fatalf("decoded %d bytes with SHA-256 %x, want %d bytes with %s", len(out), got, size, sum)
			}
		})
	}
}
