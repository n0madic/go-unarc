package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/cli"
	_ "github.com/n0madic/go-unarc/internal/codec/all"
	"github.com/n0madic/go-unarc/internal/fixturetest"
	"github.com/n0madic/go-unarc/internal/method"
)

// TestGenuineNoGolden tests the archives made by FreeArc 0.67 that have no golden runs because the C unarc
// fails them: the genuine_ppmd_fill_* ones, whose PPMd model filled its memory (the 64-bit C layout fails
// their CRC check, the Go port keeps the 32-bit one), the 4x4 ones (genuine_*_mt4, genuine_4x4_*) and the
// dispack070 ones (genuine_dispack_*), methods the C unarc does not have. The Go port must pass the CRC
// check of every file.
func TestGenuineNoGolden(t *testing.T) {
	t.Parallel()
	fixturetest.RequireArchives(t)
	dir := filepath.Join(fixturetest.Testdata(t), "genuine")
	archives, err := filepath.Glob(filepath.Join(dir, "archives", "*.arc"))
	if err != nil || len(archives) == 0 {
		t.Fatalf("no genuine archives (%v)", err)
	}
	var tested []string
	for _, f := range archives {
		name := strings.TrimSuffix(filepath.Base(f), ".arc")
		golden, err := filepath.Glob(filepath.Join(dir, "golden", name+".*"))
		if err != nil {
			t.Fatalf("goldens of %s: %v", name, err)
		}
		if len(golden) > 0 {
			continue
		}
		tested = append(tested, name)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			code := cli.Run([]string{"t", f}, &method.Env{Stderr: &bytes.Buffer{}}, strings.NewReader(""), &stdout)
			out := stdout.String()
			if code != cli.ExitOK || strings.Contains(out, "ERROR") || !strings.Contains(out, "\nTesting ") {
				t.Errorf("exit %d\n%s", code, out)
			}
		})
	}
	// The six PPMd archives, the eight 4x4 and the two dispack070 ones; a new archive without goldens must be
	// looked at.
	if want := 16; len(tested) != want {
		t.Errorf("%d genuine archives without golden runs, want %d: %v", len(tested), want, tested)
	}
}
