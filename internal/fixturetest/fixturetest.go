// Package fixturetest replays the runs of the C unarc recorded in testdata against the Go unarc.
//
// A golden file (testdata/golden/* and testdata/*/golden/*) looks like:
//
//	$ [UNARC_PASSWORD=<pw> ]unarc <args...>
//	<stdout>
//	--- stderr
//	<stderr>
//	--- exit <code>
//
// Archive paths in <args> end with ".arc" and are relative to testdata. The C unarc ran in an empty
// scratch folder with TZ=UTC and no UNARC_* variables but the password.
package fixturetest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n0madic/go-unarc/internal/arc"
	"github.com/n0madic/go-unarc/internal/cli"
	"github.com/n0madic/go-unarc/internal/method"
)

// Golden is one recorded run.
type Golden struct {
	File     string
	Args     []string
	Password string
	Stdout   string
	Stderr   string
	Code     int
}

// Archive returns the archive path of the run (relative to testdata) and its fixture name.
func (g *Golden) Archive() (rel, name string) {
	for _, a := range g.Args {
		if strings.HasSuffix(a, ".arc") {
			return a, strings.TrimSuffix(path.Base(a), ".arc")
		}
	}
	return "", ""
}

// Testdata returns the absolute path of the module's testdata folder, found from the current folder upwards.
func Testdata(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "testdata")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the current folder")
		}
		dir = parent
	}
}

// lfsPointerHeader starts the pointer file that Git LFS leaves in place of a large file it did not fetch.
const lfsPointerHeader = "version https://git-lfs.github.com/spec/v1"

var (
	lfsOnce sync.Once
	lfsErr  error
)

// RequireArchives fails the test when the .arc fixtures are Git LFS pointer files, which is what a clone made
// without git-lfs holds, instead of letting every test fail with "not an ArC archive".
func RequireArchives(t testing.TB) {
	t.Helper()
	root := Testdata(t)
	lfsOnce.Do(func() { lfsErr = checkArchives(root) })
	if lfsErr != nil {
		t.Fatal(lfsErr)
	}
}

// checkArchives reports an error when the first archive under root/archives is a Git LFS pointer. The archives
// are all tracked the same way, so one tells for all.
func checkArchives(root string) error {
	files, _ := filepath.Glob(filepath.Join(root, "archives", "*.arc"))
	if len(files) == 0 {
		return nil
	}
	f, err := os.Open(files[0])
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, len(lfsPointerHeader))
	n, _ := io.ReadFull(f, head)
	if string(head[:n]) == lfsPointerHeader {
		return fmt.Errorf("%s is a Git LFS pointer, not an archive: the repository was cloned without git-lfs; "+
			"run `git lfs install && git lfs pull`", files[0])
	}
	return nil
}

// Parse reads a golden file.
func Parse(file string) (Golden, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return Golden{}, err
	}
	g := Golden{File: file}
	head, rest, _ := strings.Cut(string(raw), "\n")
	fields := strings.Fields(strings.TrimPrefix(head, "$ "))
	if len(fields) > 0 {
		if pw, ok := strings.CutPrefix(fields[0], "UNARC_PASSWORD="); ok {
			g.Password = pw
			fields = fields[1:]
		}
	}
	if len(fields) == 0 || fields[0] != "unarc" {
		return g, fmt.Errorf("%s: no command line", file)
	}
	g.Args = fields[1:]
	i := strings.LastIndex(rest, "\n--- stderr\n")
	j := strings.LastIndex(rest, "\n--- exit ")
	if i < 0 || j < i {
		return g, fmt.Errorf("%s: bad layout", file)
	}
	g.Stdout = rest[:i]
	// The golden runs are the C unarc's output; the Go one prints its own header instead of C's.
	if body, ok := strings.CutPrefix(g.Stdout, CHeader); ok {
		g.Stdout = cli.Header + body
	}
	g.Stderr = rest[i+len("\n--- stderr\n") : j]
	if _, err := fmt.Sscanf(rest[j:], "\n--- exit %d", &g.Code); err != nil {
		return g, fmt.Errorf("%s: %w", file, err)
	}
	return g, nil
}

// All returns the golden files whose fixture name matches one of the patterns (path.Match), or all of them.
func All(t testing.TB, patterns ...string) []Golden {
	t.Helper()
	RequireArchives(t)
	root := Testdata(t)
	var files []string
	for _, glob := range []string{"golden/*", "*/golden/*"} {
		m, err := filepath.Glob(filepath.Join(root, glob))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	var out []Golden
	for _, f := range files {
		g, err := Parse(f)
		if err != nil {
			t.Fatal(err)
		}
		_, name := g.Archive()
		if matches(name, patterns) {
			out = append(out, g)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no golden runs match %v (run go run ./scripts/mkfixtures)", patterns)
	}
	return out
}

func matches(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// fakeMethods are the method names the fixtures use to test unsupported methods.
var fakeMethods = map[string]bool{"foo": true, "bar": true, "baz": true}

// Ported reports whether every method the archive uses has a registered codec (or is one of the fake
// methods). Runs that need a codec not registered in the test binary are skipped.
func Ported(t testing.TB, g *Golden) bool {
	rel, _ := g.Archive()
	if rel == "" {
		return true
	}
	env := &method.Env{Stderr: io.Discard}
	known := func(compressor string) bool {
		for _, m := range method.Split(compressor) {
			if _, ok := method.Lookup(m, env); !ok && !fakeMethods[method.Name(m)] {
				return false
			}
		}
		return true
	}
	a, err := arc.Open(filepath.Join(Testdata(t), rel), env)
	if err != nil {
		// Only the footer's compressor is unknown here; it is fine when it is a fake one.
		return !strings.Contains(err.Error(), "unsupported compression method") || strings.Contains(err.Error(), `"baz"`)
	}
	defer a.Close()
	for _, blk := range a.Blocks {
		if blk.Type != arc.DirBlock {
			continue
		}
		if !known(blk.Compressor) {
			return false
		}
		d, err := a.ReadDir(blk)
		if err != nil {
			return true
		}
		for _, db := range d.Blocks {
			if !known(db.Compressor) {
				return false
			}
		}
	}
	return true
}

// Exec runs the Go unarc with the arguments of g in a new scratch folder (the current folder changes to it
// for the rest of the test) and returns what it printed.
func Exec(t *testing.T, g *Golden) (stdout, stderr string, code int) {
	t.Helper()
	root := Testdata(t)
	args := make([]string, len(g.Args))
	for i, a := range g.Args {
		args[i] = a
		if strings.HasSuffix(a, ".arc") {
			args[i] = filepath.Join(root, filepath.FromSlash(a))
		}
	}
	t.Chdir(t.TempDir())
	var out, errOut bytes.Buffer
	env := &method.Env{Password: g.Password, Wine: "wine", Stderr: &errOut}
	code = cli.Run(args, env, strings.NewReader(""), &out)
	return out.String(), errOut.String(), code
}

// CHeader is the header the C unarc prints first (HEADER1 NAME); the Go unarc prints cli.Header in its place.
const CHeader = "UnArc 0.60RC unpacker"

// Run replays the matching golden runs as subtests and compares stdout, stderr and the exit code.
// A run needing a codec that is not registered fails, so that a missing codec cannot pass as a skipped test:
// link them all with _ "github.com/n0madic/go-unarc/internal/codec/all".
func Run(t *testing.T, patterns ...string) {
	time.Local = time.UTC
	for _, g := range All(t, patterns...) {
		t.Run(filepath.Base(g.File), func(t *testing.T) {
			if !Ported(t, &g) {
				t.Fatal("a codec of this archive is not registered")
			}
			stdout, stderr, code := Exec(t, &g)
			cmdline := strings.Join(g.Args, " ")
			if stdout != g.Stdout {
				t.Errorf("unarc %s: stdout differs\n--- got\n%s\n--- want\n%s", cmdline, stdout, g.Stdout)
			}
			if stderr != g.Stderr {
				t.Errorf("unarc %s: stderr differs\n--- got\n%q\n--- want\n%q", cmdline, stderr, g.Stderr)
			}
			if code != g.Code {
				t.Errorf("unarc %s: exit %d, want %d", cmdline, code, g.Code)
			}
		})
	}
}
