package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/cli"
	"github.com/n0madic/go-unarc/internal/fixturetest"
	"github.com/n0madic/go-unarc/internal/method"
)

func TestOverwriteRetainsFileOnCRCError(t *testing.T) {
	fixturetest.RequireArchives(t)
	archive := filepath.Join(fixturetest.Testdata(t), "archives", "bad_crc.arc")
	out := t.TempDir()
	file := filepath.Join(out, "b.txt")
	const original = "existing data"
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	code := cli.Run([]string{"x", "-o+", "-dp" + out, archive, "b.txt"}, &method.Env{Stderr: &bytes.Buffer{}},
		strings.NewReader(""), &stdout)
	if code != cli.ExitError || !strings.Contains(stdout.String(), "failed CRC check") {
		t.Fatalf("exit %d: %s", code, stdout.String())
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != original {
		t.Fatalf("original file: %q, %v", got, err)
	}
	if entries, err := os.ReadDir(out); err != nil || len(entries) != 1 {
		t.Fatalf("destination holds leftover files: %v, %v", entries, err)
	}
}

func TestDirectoryConflictReturnsError(t *testing.T) {
	fixturetest.RequireArchives(t)
	archive := filepath.Join(fixturetest.Testdata(t), "genuine", "archives", "genuine_dirs.arc")
	out := t.TempDir()
	file := filepath.Join(out, "bin")
	if err := os.WriteFile(file, []byte("existing data"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	code := cli.Run([]string{"x", "-dp" + out, archive, "bin"}, &method.Env{Stderr: &bytes.Buffer{}},
		strings.NewReader(""), &stdout)
	if code != cli.ExitError || !strings.Contains(stdout.String(), "can't create directory") ||
		strings.Contains(stdout.String(), "Extracting bin/") {
		t.Fatalf("exit %d: %s", code, stdout.String())
	}
}
