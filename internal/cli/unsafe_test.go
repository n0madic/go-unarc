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

// TestUnsafePaths: names leading out of the output folder stop extraction (C wrote them, even to /tmp).
func TestUnsafePaths(t *testing.T) {
	fixturetest.RequireArchives(t)
	archive := filepath.Join(fixturetest.Testdata(t), "archives", "unsafe_paths.arc")
	tests := []struct {
		cmd, unsafe string
		files       []string
	}{
		{"x", "../evil.txt", []string{"ok.txt"}},
		// e drops the folders, so only the name with backslashes is refused.
		{"e", `..\..\win.txt`, []string{"abs.txt", "evil.txt", "ok.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			outer := t.TempDir()
			work := filepath.Join(outer, "work")
			if err := os.Mkdir(work, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(work)
			var stdout bytes.Buffer
			code := cli.Run([]string{tt.cmd, archive}, &method.Env{Stderr: &bytes.Buffer{}}, strings.NewReader(""), &stdout)
			if want := "\nERROR: unsafe path in archive: " + tt.unsafe; code != 2 || !strings.HasSuffix(stdout.String(), want) {
				t.Errorf("exit %d, stdout %q; want exit 2 and %q", code, stdout.String(), want)
			}
			if entries, _ := os.ReadDir(outer); len(entries) != 1 {
				t.Errorf("files written beside the output folder: %v", entries)
			}
			var got []string
			_ = filepath.WalkDir(work, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					rel, _ := filepath.Rel(work, p)
					got = append(got, rel)
				}
				return nil
			})
			if strings.Join(got, ",") != strings.Join(tt.files, ",") {
				t.Errorf("extracted %q, want %q", got, tt.files)
			}
		})
	}
}
