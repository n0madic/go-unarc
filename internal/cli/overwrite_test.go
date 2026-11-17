package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/cli"
	_ "github.com/n0madic/go-unarc/internal/codec/storing"
	"github.com/n0madic/go-unarc/internal/fixturetest"
	"github.com/n0madic/go-unarc/internal/method"
)

// TestOverwritePrompt answers the overwrite question (the C prompt accepted no answer at all).
func TestOverwritePrompt(t *testing.T) {
	fixturetest.RequireArchives(t)
	archive := filepath.Join(fixturetest.Testdata(t), "archives", "storing.arc")
	const old = "old"
	tests := []struct {
		name, stdin  string
		args         []string
		code         int
		a, b         string // contents afterwards
		prompts      int
		stdoutSubstr []string
	}{
		{"yes then no", "y\nn\n", nil, 0, "first\n", old, 2, []string{"Overwrite a.txt (y/n/a/s/q) ? ", "Skipping b.txt"}},
		{"all", "A\n", nil, 0, "first\n", "second\n", 1, nil},
		{"skip all", "s\n", nil, 0, old, old, 1, nil},
		{"quit", "q\n", nil, 1, old, old, 1, []string{"Extraction aborted\n"}},
		{"invalid answer first", "maybe\ny\ny\n", nil, 0, "first\n", "second\n", 3,
			[]string{"Valid answers: Y - yes, N - no, A - overwrite all, S - skip all, Q - quit\n"}},
		{"end of input means no", "", nil, 0, old, old, 2, nil},
		{"-o+", "", []string{"-o+"}, 0, "first\n", "second\n", 0, nil},
		{"-o-", "", []string{"-o-"}, 0, old, old, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			for _, f := range []string{"a.txt", "b.txt"} {
				if err := os.WriteFile(f, []byte(old), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"x"}, tt.args...)
			args = append(args, archive, "a.txt", "b.txt")
			var stdout bytes.Buffer
			code := cli.Run(args, &method.Env{Stderr: &bytes.Buffer{}}, strings.NewReader(tt.stdin), &stdout)
			out := stdout.String()
			if code != tt.code {
				t.Errorf("exit %d, want %d\n%s", code, tt.code, out)
			}
			if n := strings.Count(out, "(y/n/a/s/q) ? "); n != tt.prompts {
				t.Errorf("%d prompts, want %d\n%s", n, tt.prompts, out)
			}
			for _, s := range tt.stdoutSubstr {
				if !strings.Contains(out, s) {
					t.Errorf("stdout lacks %q\n%s", s, out)
				}
			}
			for f, want := range map[string]string{"a.txt": tt.a, "b.txt": tt.b} {
				if got, err := os.ReadFile(f); err != nil || string(got) != want {
					t.Errorf("%s = %q, %v; want %q", f, got, err, want)
				}
			}
		})
	}
}
