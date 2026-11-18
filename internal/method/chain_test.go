package method

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runChain(t *testing.T, compressor string, in string, env *Env) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := Run(compressor, &out, strings.NewReader(in), env)
	return out.String(), err
}

func TestRunOrder(t *testing.T) {
	t.Parallel()
	env := &Env{Stderr: io.Discard, TempDir: t.TempDir()}
	// The last method decodes first: "prefix:B" reads the input, "prefix:A" writes the output.
	for _, c := range []string{"prefix:A+prefix:B", "prefix:A+tempfile+prefix:B", "prefix:A+prefix:B+tempfile"} {
		got, err := runChain(t, c, "data", env)
		if err != nil || got != "ABdata" {
			t.Errorf("%s: %q, %v", c, got, err)
		}
	}
	big := strings.Repeat("abcdefgh", 100000)
	if got, err := runChain(t, "upper+prefix:x+upper", big, env); err != nil || got != "X"+strings.ToUpper(big) {
		t.Errorf("upper chain: %d bytes, %v", len(got), err)
	}
	if entries, _ := os.ReadDir(env.TempDir); len(entries) != 0 {
		t.Errorf("temp files left: %s", entries[0].Name())
	}
}

// stopAfter accepts n bytes and then asks for no more.
type stopAfter struct {
	n   int
	got bytes.Buffer
}

func (s *stopAfter) Write(p []byte) (int, error) {
	if s.got.Len()+len(p) > s.n {
		s.got.Write(p[:s.n-s.got.Len()])
		return 0, ErrStop
	}
	return s.got.Write(p)
}

func TestRunStop(t *testing.T) {
	t.Parallel()
	env := &Env{Stderr: io.Discard}
	for _, c := range []string{"upper", "upper+upper+upper"} {
		w := &stopAfter{n: 20}
		err := Run(c, w, strings.NewReader(strings.Repeat("x", 100000)), env)
		if err != nil || w.got.String() != strings.Repeat("X", 20) {
			t.Errorf("%s: %q, %v", c, w.got.String(), err)
		}
	}
}

func TestRunFull(t *testing.T) {
	t.Parallel()
	env := &Env{Stderr: io.Discard}
	if err := RunFull("take-one+upper", io.Discard, strings.NewReader("x"), env); err != nil {
		t.Fatalf("complete chain: %v", err)
	}
	if err := RunFull("take-one+upper", io.Discard, strings.NewReader("two"), env); !errors.Is(err, ErrBadData) {
		t.Fatalf("chain left unread bytes: %v", err)
	}
	if err := RunFull("take-one+fail-at-end", io.Discard, strings.NewReader("x"), env); !errors.Is(err, ErrBadData) ||
		!strings.Contains(err.Error(), "final check failed") {
		t.Fatalf("upstream final check: %v", err)
	}
}

// TestRunErrors is not parallel: it counts goroutines.
func TestRunErrors(t *testing.T) {
	before := runtime.NumGoroutine()
	// The error names the stage that failed, once: the stages after it pass it on without being recorded.
	for _, tt := range []struct{ chain, want string }{
		{"fail", "fail: compressed data corrupted"},
		{"upper+fail", "fail: compressed data corrupted"},
		{"fail+upper+upper", "fail: compressed data corrupted"},
		{"upper+panic+upper", "panic: compressed data corrupted: decoder panic: fake panic"},
		{"upper+fail+tempfile+upper", "fail: compressed data corrupted"},
	} {
		var stderr bytes.Buffer
		_, err := runChain(t, tt.chain, strings.Repeat("y", 300000), &Env{Stderr: &stderr, TempDir: t.TempDir()})
		if !errors.Is(err, ErrBadData) || !strings.HasPrefix(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want ErrBadData starting with %q", tt.chain, err, tt.want)
		}
	}
	// Every stage goroutine has exited.
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines left running", n-before)
	}
}

// TestRunErrorDebug: with UNARC_DEBUG, a failed chain prints why on stderr; the C unarc shows nothing there.
func TestRunErrorDebug(t *testing.T) {
	var stderr bytes.Buffer
	_, err := runChain(t, "upper+fail", "yyy", &Env{Stderr: &stderr, Debug: true})
	want := "MultiDecompress upper+fail\nDecompress error: fail: compressed data corrupted\nfake failure\n"
	if !errors.Is(err, ErrBadData) || stderr.String() != want {
		t.Errorf("err = %v, stderr %q, want %q", err, stderr.String(), want)
	}
}

func TestNamed(t *testing.T) {
	t.Parallel()
	bad := fmt.Errorf("%w: truncated", ErrBadData)
	for _, tt := range []struct {
		err  error
		want string
	}{
		{bad, "lzma: compressed data corrupted: truncated"},
		{ErrStop, ErrStop.Error()},
		{ErrWrongPassword, ErrWrongPassword.Error()},
	} {
		got := Named("lzma", tt.err)
		if got.Error() != tt.want || !errors.Is(got, tt.err) {
			t.Errorf("Named(%v) = %v, want %q", tt.err, got, tt.want)
		}
	}
	if Named("lzma", nil) != nil {
		t.Error("Named(nil) is not nil")
	}
}

func TestRunUnsupported(t *testing.T) {
	t.Parallel()
	tests := []struct {
		env    *Env
		stderr string
	}{
		{&Env{}, "\nERROR: compression method \"nosuch\" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)\n"},
		{&Env{CLSDirSet: true}, "\nERROR: compression method \"nosuch\" is not supported\n"},
	}
	for _, tt := range tests {
		var stderr bytes.Buffer
		tt.env.Stderr = &stderr
		_, err := runChain(t, "upper+nosuch:1:2+upper", "x", tt.env)
		if !errors.Is(err, ErrUnsupportedMethod) || stderr.String() != tt.stderr {
			t.Errorf("err = %v, stderr = %q", err, stderr.String())
		}
	}
}

func TestDecompressMem(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	env := &Env{Stderr: &stderr}
	out, err := DecompressMem("prefix:ab", []byte("cd"), 4, env)
	if err != nil || string(out) != "abcd" {
		t.Errorf("DecompressMem = %q, %v", out, err)
	}
	if _, err := DecompressMem("prefix:ab", []byte("cd"), 3, env); !errors.Is(err, ErrOutputTooLarge) {
		t.Errorf("small buffer: err = %v", err)
	}
	// A single unknown method is reported by the caller; a chain reports it here.
	if _, err := DecompressMem("nosuch", nil, 3, env); !errors.Is(err, ErrUnsupportedMethod) || stderr.Len() != 0 {
		t.Errorf("unknown method: err = %v, stderr %q", err, stderr.String())
	}
	if _, err := DecompressMem("nosuch+upper", nil, 3, env); !errors.Is(err, ErrUnsupportedMethod) || stderr.Len() == 0 {
		t.Errorf("unknown method in a chain: err = %v, stderr %q", err, stderr.String())
	}
	for err, want := range map[error]int{nil: 0, ErrUnsupportedMethod: -2, ErrOutputTooLarge: -4, ErrBadData: -7,
		ErrStop: -9, io.ErrUnexpectedEOF: -1} {
		if got := CCode(err); got != want {
			t.Errorf("CCode(%v) = %d, want %d", err, got, want)
		}
	}
}
