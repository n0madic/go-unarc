package cli_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/arctest"
	"github.com/n0madic/go-unarc/internal/cli"
	_ "github.com/n0madic/go-unarc/internal/codec/all"
	"github.com/n0madic/go-unarc/internal/method"
)

func TestSrepArchive(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no external decoder
	encoded, err := os.ReadFile("../../testdata/srep/indexed.lzma.b64")
	if err != nil {
		t.Fatal(err)
	}
	packed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(strings.Repeat("FreeArc/SREP-0123456789abcdef!?--", 615) + "tail!")
	files := []arctest.File{
		{Path: "first.bin", Data: original[:8250]},
		{Path: "dir/second.bin", Data: original[8250:]},
	}
	archive := arctest.Build(arctest.Archive{Solids: []arctest.Solid{
		{Compressor: "srep:m3+lzma:1mb", Packed: packed, Files: files},
	}})
	dir, work, out := t.TempDir(), t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "srep.arc")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"t", "x"} {
		var stdout, stderr bytes.Buffer
		env := &method.Env{ExeDir: dir, Stderr: &stderr, Debug: true}
		args := []string{cmd, "-w" + work, "-dp" + out, path}
		if status := cli.Run(args, env, strings.NewReader(""), &stdout); status != cli.ExitOK {
			t.Fatalf("%s exited %d:\n%s\n%s", cmd, status, &stdout, &stderr)
		}
		if entries, err := os.ReadDir(work); err != nil || len(entries) != 0 {
			t.Fatalf("work dir after %s: %v, %v", cmd, entries, err)
		}
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(f.Path)))
		if err != nil || !bytes.Equal(data, f.Data) {
			t.Errorf("%s: got %d bytes, want %d: %v", f.Path, len(data), len(f.Data), err)
		}
	}
}
