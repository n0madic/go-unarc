// Package codectest holds what the codec tests share: running fagen (FreeArc's C compression library built
// as a command by scripts/fagen/build.sh) as the reference, building and running codecs, test writers and
// test data.
package codectest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/n0madic/go-unarc/internal/fixturetest"
	"github.com/n0madic/go-unarc/internal/method"
)

func fagenPath(tb testing.TB) string {
	return filepath.Join(filepath.Dir(fixturetest.Testdata(tb)), ".build", "fagen")
}

// HaveFagen reports whether .build/fagen is built.
func HaveFagen(tb testing.TB) bool {
	tb.Helper()
	_, err := os.Stat(fagenPath(tb))
	return err == nil
}

// FagenPath returns the absolute path of .build/fagen, or skips the test when it is not built.
func FagenPath(tb testing.TB) string {
	tb.Helper()
	if !HaveFagen(tb) {
		tb.Skip("fagen is not built (scripts/fagen/build.sh)")
	}
	return fagenPath(tb)
}

// Result is how a fagen run ended.
type Result struct {
	Out    []byte
	Exit   int // exit code; -1 when killed by a signal
	Stderr string
}

// OK reports whether fagen exited with code 0.
func (r Result) OK() bool { return r.Exit == 0 }

// FagenTry runs "fagen <args...>" with stdin as input, however it ends; the test fails only if fagen cannot
// be started.
func FagenTry(tb testing.TB, stdin []byte, args ...string) Result {
	tb.Helper()
	cmd := exec.Command(FagenPath(tb), args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	r := Result{}
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			tb.Fatalf("fagen %s: %v", strings.Join(args, " "), err)
		}
		r.Exit = ee.ExitCode()
	}
	r.Out, r.Stderr = out.Bytes(), errOut.String()
	return r
}

// Fagen runs "fagen <args...>" with stdin as input and returns its stdout; the test fails unless it exits 0.
func Fagen(tb testing.TB, stdin []byte, args ...string) []byte {
	tb.Helper()
	r := FagenTry(tb, stdin, args...)
	if !r.OK() {
		tb.Fatalf("fagen %s: exit %d\n%s", strings.Join(args, " "), r.Exit, r.Stderr)
	}
	return r.Out
}

// Parse builds a codec from a method string the way the chain does.
func Parse(tb testing.TB, m string) method.Codec {
	tb.Helper()
	c, ok := method.Lookup(m, &method.Env{})
	if !ok {
		tb.Fatalf("method %q is not accepted", m)
	}
	return c
}

// Decode runs the codec on a compressed stream.
func Decode(c method.Codec, stream []byte) ([]byte, error) {
	var out bytes.Buffer
	err := c.Decode(&out, bytes.NewReader(stream))
	return out.Bytes(), err
}

// ErrLimit is what LimitWriter returns past its limit.
var ErrLimit = errors.New("output limit")

// LimitWriter fails once more than Max bytes were written (keeps fuzzing and error tests bounded).
type LimitWriter struct {
	N, Max int
}

func (w *LimitWriter) Write(p []byte) (int, error) {
	w.N += len(p)
	if w.N > w.Max {
		return 0, ErrLimit
	}
	return len(p), nil
}

// MemWriter collects the output in a buffer that is reused from run to run, which is what the C timing tool does.
type MemWriter struct{ Buf []byte }

func (w *MemWriter) Write(p []byte) (int, error) {
	w.Buf = append(w.Buf, p...)
	return len(p), nil
}

// Reset empties the buffer, keeping its storage.
func (w *MemWriter) Reset() { w.Buf = w.Buf[:0] }

// Sum is the hex SHA-256 of b.
func Sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// FillBytes fills b with pseudo-random bytes (math/rand/v2 has no FillBytes).
func FillBytes(rng *rand.Rand, b []byte) {
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
}

// notAccepted is what "fagen mem" prints for a method string the C library does not accept (error code -2).
const notAccepted = 4294967294

// cMems asks the C library for GetDecompressionMem of every method string, concurrently.
func cMems(tb testing.TB, methods []string) []uint64 {
	tb.Helper()
	bin := FagenPath(tb)
	mems := make([]uint64, len(methods))
	errs := make([]error, len(methods))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, m := range methods {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out, err := exec.Command(bin, "mem", m).Output()
			if err == nil {
				mems[i], err = strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			tb.Fatalf("fagen mem %q: %v", methods[i], err)
		}
	}
	return mems
}

// CheckAgainstC compares parse with the C parser on the given method strings: same acceptance, same memory.
func CheckAgainstC(t *testing.T, parse method.Parser, methods []string) {
	t.Helper()
	for i, cMem := range cMems(t, methods) {
		m := methods[i]
		c, ok := parse(method.Params(m), &method.Env{})
		if wantOK := cMem != notAccepted; ok != wantOK {
			t.Errorf("%q: accepted = %v, C accepts = %v", m, ok, wantOK)
		} else if ok && c.DecompressionMem() != cMem {
			t.Errorf("%q: DecompressionMem = %d, C reports %d", m, c.DecompressionMem(), cMem)
		}
	}
}

var (
	corpusOnce sync.Once
	corpus     []byte
)

// SourceText returns size bytes of real text: the Go sources of this module (repeated as needed; a stock
// sentence if there are none).
func SourceText(tb testing.TB, size int) []byte {
	tb.Helper()
	corpusOnce.Do(func() {
		root := filepath.Dir(fixturetest.Testdata(tb))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if name := d.Name(); name == ".git" || name == ".build" || name == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") {
				if b, err := os.ReadFile(path); err == nil {
					corpus = append(corpus, b...)
				}
			}
			return nil
		})
		if len(corpus) == 0 {
			corpus = []byte("the quick brown fox jumps over the lazy dog\n")
		}
	})
	out := make([]byte, 0, size)
	for len(out) < size {
		out = append(out, corpus[:min(len(corpus), size-len(out))]...)
	}
	return out
}

// ReuseStreams returns streams of method m (made by fagen) for CheckReuse: text and random data of several
// sizes, the largest first so that later streams find memory written by earlier ones, each followed by a
// copy with a few bytes changed and a truncated copy.
func ReuseStreams(tb testing.TB, m string, sizes ...int) [][]byte {
	tb.Helper()
	rng := rand.New(rand.NewPCG(uint64(len(m)), 7))
	var streams [][]byte
	for i, size := range sizes {
		in := SourceText(tb, size)
		if i%2 == 1 {
			in = make([]byte, size)
			FillBytes(rng, in)
		}
		s := Fagen(tb, in, "c", m)
		bad := bytes.Clone(s)
		for range 3 {
			bad[rng.IntN(len(bad))] ^= byte(1 + rng.IntN(255))
		}
		streams = append(streams, s, bad, s[:rng.IntN(len(s))])
	}
	return streams
}

// CheckReuse decodes streams one after the other with one decoder from c's NewDecoder and checks that each
// result (output and error) is what a new decoder gives. After each stream, scribble fills the memory the
// decoder keeps with garbage: a reused decoder must never read what an earlier stream left there.
func CheckReuse(t *testing.T, c method.Codec, streams [][]byte, scribble func(method.Decoder, *rand.Rand)) {
	t.Helper()
	r, ok := c.(interface{ NewDecoder() method.Decoder })
	if !ok {
		t.Fatalf("%T has no NewDecoder", c)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	dec := r.NewDecoder()
	for i, s := range streams {
		want, wantErr := Decode(c, s)
		var got bytes.Buffer
		err := dec.Decode(&got, bytes.NewReader(s))
		if !bytes.Equal(got.Bytes(), want) || fmt.Sprint(err) != fmt.Sprint(wantErr) {
			t.Errorf("stream %d (%d bytes): the reused decoder gives %d bytes, %v; a new one %d bytes, %v",
				i, len(s), got.Len(), err, len(want), wantErr)
		}
		scribble(dec, rng)
	}
}
