package srep

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/method"
)

func fixtures(t testing.TB) map[string][]byte {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/srep/streams.txt")
	if err != nil {
		t.Fatal(err)
	}
	streams := make(map[string][]byte)
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		name, encoded, _ := strings.Cut(line, " ")
		streams[name], err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return streams
}

func checkClean(t *testing.T, dir string) {
	t.Helper()
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("work dir after decode: %v, %v", entries, err)
	}
}

func TestDecode(t *testing.T) {
	t.Parallel()
	want := []byte(strings.Repeat("FreeArc/SREP-0123456789abcdef!?--", 615) + "tail!")
	streams := fixtures(t)
	for _, name := range []string{"v1", "v2", "v3", "v3le", "v4", "sip", "sha512", "none", "empty"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expected := want
			if name == "empty" {
				expected = nil
			}
			dir := t.TempDir()
			c := codec{&method.Env{TempDir: dir}}
			for _, seekable := range []bool{true, false} {
				in := io.Reader(bytes.NewReader(streams[name]))
				if !seekable {
					in = struct{ io.Reader }{in}
				}
				var got bytes.Buffer
				if err := c.Decode(&got, in); err != nil || !bytes.Equal(got.Bytes(), expected) {
					t.Fatalf("seekable=%t: decoded %d bytes, want %d: %v", seekable, got.Len(), len(expected), err)
				}
				checkClean(t, dir)
			}
		})
	}
}

func TestBadData(t *testing.T) {
	t.Parallel()
	streams := fixtures(t)
	mutate := func(name string, offset int, value byte) []byte {
		b := bytes.Clone(streams[name])
		if offset < 0 {
			offset += len(b)
		}
		b[offset] = value
		return b
	}
	v3, v4 := streams["v3"], streams["v4"]
	firstV3 := 16 + int(v3[10])
	firstV4 := 16 + int(v4[10])
	tests := map[string][]byte{
		"header":       v3[:15],
		"magic":        mutate("v3", 0, 0),
		"version":      mutate("v3", 8, 5),
		"hash length":  mutate("v3", 11, 239),
		"key":          v3[:firstV3-1],
		"block size":   mutate("v3", firstV3+7, 255),
		"checksum":     mutate("v3", firstV3+12, v3[firstV3+12]^1),
		"stats size":   mutate("v3", firstV3+8, 31),
		"source":       mutate("v3", firstV3+28+3, 255),
		"destination":  mutate("v3", firstV3+28+11, 255),
		"last byte":    v3[:len(v3)-1],
		"pending":      v3[:firstV3+28+32+96],
		"footer":       mutate("v4", -1, 0),
		"footer size":  mutate("v4", -16, 23),
		"index size":   mutate("v4", -17, 255),
		"count table":  mutate("v4", -36, 1),
		"inline index": mutate("v4", firstV4+8, 16),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			c := codec{&method.Env{TempDir: t.TempDir()}}
			var out bytes.Buffer
			err := c.Decode(&out, struct{ io.Reader }{bytes.NewReader(data)})
			if !errors.Is(err, method.ErrBadData) {
				t.Errorf("err = %v, want ErrBadData", err)
			}
			if name == "checksum" && out.Len() != 0 {
				t.Error("emitted a block with a bad checksum")
			}
			checkClean(t, c.env.TempDir)
		})
	}
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

type noMoreInput struct{ t *testing.T }

func (r noMoreInput) Read([]byte) (int, error) {
	r.t.Fatal("read past the first block despite a destination error")
	return 0, io.EOF
}

func TestStreamingStop(t *testing.T) {
	t.Parallel()
	first := fixtures(t)["v3"][:48+28+32+96]
	dir := t.TempDir()
	c := codec{&method.Env{TempDir: dir}}
	for _, want := range []error{method.ErrStop, io.EOF} {
		src := io.MultiReader(bytes.NewReader(first), noMoreInput{t})
		if err := c.Decode(errorWriter{want}, src); err != want {
			t.Fatalf("err = %v, want %v", err, want)
		}
		checkClean(t, dir)
	}
}

func TestLongDistance(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "history")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte(" abc")); err != nil {
		t.Fatal(err)
	}
	// A backward match more than 4 GiB away, without needing a 4 GiB test file.
	d := decoder{version: 2, pos: 1<<32 + 3, out: make([]byte, 3), stats: make([]byte, 16), history: f}
	binary.LittleEndian.PutUint64(d.stats[4:], d.pos-1)
	binary.LittleEndian.PutUint32(d.stats[12:], 3)
	if err := d.backward(&io.LimitedReader{R: strings.NewReader("")}); err != nil || string(d.out) != "abc" {
		t.Fatalf("got %q, %v", d.out, err)
	}
}

// TestOracle exercises larger blocks and cross-block distances with a supplied reference encoder.
func TestOracle(t *testing.T) {
	oracle := os.Getenv("SREP_ORACLE")
	if oracle == "" {
		t.Skip("set SREP_ORACLE to the SREP 3.93a binary")
	}
	seed := make([]byte, 2*method.MB)
	_, _ = rand.New(rand.NewSource(923)).Read(seed)
	raw := append(bytes.Repeat(seed, 2), seed[len(seed)-65000:]...)
	for _, mode := range []string{"-m3o", "-m5o", "-m3f", "-m3"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "in"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{mode, "-b1mb", "in", "packed.srep"}, {"-d", "packed.srep", "out"}} {
				cmd := exec.Command(oracle, append([]string{"-v0"}, args...)...)
				cmd.Dir = dir
				if log, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v: %v\n%s", args, err, log)
				}
			}
			want, err := os.ReadFile(filepath.Join(dir, "out"))
			if err != nil || !bytes.Equal(raw, want) {
				t.Fatalf("oracle did not round trip: %v", err)
			}
			packed, err := os.ReadFile(filepath.Join(dir, "packed.srep"))
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			c := codec{&method.Env{TempDir: dir}}
			if err := c.Decode(&got, struct{ io.Reader }{bytes.NewReader(packed)}); err != nil || !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("decoded %d bytes, want %d: %v", got.Len(), len(want), err)
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	for _, b := range fixtures(f) {
		f.Add(b)
	}
	c := codec{&method.Env{TempDir: f.TempDir()}}
	f.Fuzz(func(t *testing.T, data []byte) {
		err := c.Decode(io.Discard, bytes.NewReader(data))
		if err != nil && !errors.Is(err, method.ErrBadData) {
			t.Fatal(err)
		}
	})
}
