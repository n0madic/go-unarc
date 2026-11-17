package tta

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"github.com/n0madic/go-unarc/internal/codectest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "regenerate testdata/*.tta with fagen")

// goldenStreams are the streams kept in testdata, made by the C encoder; testdata/NAME.sha256 holds the SHA-256 of what
// the C decoder makes of NAME.tta.
var goldenStreams = []struct {
	name     string
	method   string
	channels int
	bits     int
	frames   int
	kind     string
	tail     int
}{
	{"stereo16", "tta", 2, 16, 6000, sigTone, 0},
	{"mono8", "tta:m1:1*8:o44", 1, 8, 8000, sigWalk, 3},
	{"stereo24", "tta:m2:2*24:o44", 2, 24, 4000, sigTone, 1},
	{"mono24 level 3", "tta:m3:1*24:o44", 1, 24, 4000, sigWalk, 2},
	{"float stereo", "tta:m3:2*32f:o44", 2, 32, 3000, sigTone, 0},
	{"three channels", "tta:3*16:o44", 3, 16, 3000, sigTone, 5},
	{"stored frames", "tta:2*16:o44", 2, 16, 3000, sigNoise, 1},
	{"raw", "tta:r1:2*16:o44", 2, 16, 1500, sigTone, 3},
	{"silence two frames", "tta:1*8:o44", 1, 8, 1<<18 + 10, sigSilence, 0},
	{"clipping", "tta:2*16:o44", 2, 16, 4000, sigExtreme, 0},
	{"header skipped", "tta:s", 2, 16, 5000, sigTone, 0},
	{"level 0", "tta:m0", 2, 16, 500, sigTone, 0},
}

func goldenPath(name, ext string) string {
	return filepath.Join("testdata", strings.ReplaceAll(name, " ", "_")+ext)
}

// TestUpdateTestdata rewrites the golden streams: go test -run TestUpdateTestdata -update
func TestUpdateTestdata(t *testing.T) {
	if !*update {
		t.Skip("run with -update to regenerate testdata")
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, g := range goldenStreams {
		data := makeWAV(uint64(len(g.name)), g.kind, g.channels, g.bits, g.frames, g.tail)
		stream := codectest.Fagen(t, data, "c", g.method)
		res := codectest.FagenTry(t, stream, "d", g.method)
		want := res.Out
		if !finished(res) {
			t.Fatalf("%s: fagen d: exit %d: %s", g.name, res.Exit, res.Stderr)
		}
		if err := os.WriteFile(goldenPath(g.name, ".tta"), stream, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(want)
		if err := os.WriteFile(goldenPath(g.name, ".sha256"), []byte(hex.EncodeToString(sum[:])+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The streams of the C encoder decode to what the C decoder made of them, without fagen.
func TestTestdata(t *testing.T) {
	t.Parallel()
	for _, g := range goldenStreams {
		t.Run(g.name, func(t *testing.T) {
			t.Parallel()
			stream, err := os.ReadFile(goldenPath(g.name, ".tta"))
			if err != nil {
				t.Fatal(err)
			}
			wantHex, err := os.ReadFile(goldenPath(g.name, ".sha256"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeStream(stream)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			sum := sha256.Sum256(got)
			if hex.EncodeToString(sum[:]) != strings.TrimSpace(string(wantHex)) {
				t.Errorf("SHA-256 of the output differs (%d bytes)", len(got))
			}
			// the streams are lossless: what went in is regenerated from the same signal
			data := makeWAV(uint64(len(g.name)), g.kind, g.channels, g.bits, g.frames, g.tail)
			if !bytes.Equal(got, data) {
				t.Errorf("output differs from the signal that was compressed")
			}
		})
	}
}
