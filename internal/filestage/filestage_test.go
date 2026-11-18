package filestage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/cleanup"
)

func TestSpillStream(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	data := strings.Repeat("0123456789", 1000)
	tests := []struct {
		name   string
		prefix string
		in     string
		lazy   bool
		exists bool
	}{
		{"data", "", data, false, true},
		{"prefix", "\x01", data, true, true},
		{"empty", "", "", false, true},
		{"empty lazy", "", "", true, false}, // no file: the program sees no input at all
		{"prefix only lazy", "\x00", "", true, true},
	}
	for i, tt := range tests {
		p := filepath.Join(dir, string(rune('a'+i)))
		n, err := Spill(p, []byte(tt.prefix), strings.NewReader(tt.in), 333, tt.lazy, nil)
		if err != nil || n != int64(len(tt.prefix)+len(tt.in)) {
			t.Errorf("%s: Spill = %d, %v", tt.name, n, err)
			continue
		}
		if _, err := os.Stat(p); (err == nil) != tt.exists {
			t.Errorf("%s: file exists = %v, want %v", tt.name, err == nil, tt.exists)
			continue
		}
		if !tt.exists {
			continue
		}
		var out bytes.Buffer
		if err := Stream(&out, p, 77, nil); err != nil || out.String() != tt.prefix+tt.in {
			t.Errorf("%s: Stream = %d bytes, %v", tt.name, out.Len(), err)
		}
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func TestStreamErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	if err := Stream(failWriter{stop}, p, 2, nil); err != stop {
		t.Errorf("writer error: %v", err)
	}
	if err := Stream(&bytes.Buffer{}, filepath.Join(dir, "missing"), 2, nil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := Spill(filepath.Join(dir, "no", "such", "dir"), nil, strings.NewReader("x"), 2, false, nil); err == nil {
		t.Error("Spill into a missing folder succeeded")
	}
}

func TestDir(t *testing.T) {
	parent := t.TempDir()
	removed, err := NewDir(parent, "x.")
	if err != nil {
		t.Fatal(err)
	}
	kept, err := NewDir(parent, "y.")
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err := NewDir(parent, "z.")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(removed.Path, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	removed.Remove()
	kept.Keep()
	cleanup.RemoveAll() // as on Ctrl-C: only the folder still registered goes
	for d, want := range map[string]bool{removed.Path: false, kept.Path: true, interrupted.Path: false} {
		if _, err := os.Stat(d); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(d), err == nil, want)
		}
	}
	if _, err := NewDir(filepath.Join(parent, "missing"), "x."); err == nil {
		t.Error("NewDir in a missing folder succeeded")
	}
}

func TestFindTool(t *testing.T) {
	t.Parallel()
	exeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(exeDir, "srep"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ set, name, exeDir, fallback, want string }{
		{"/opt/srep", "srep", exeDir, "/fb", "/opt/srep"},
		{"", "srep", exeDir, "/fb", filepath.Join(exeDir, "srep")},
		{"", "clshost.exe", exeDir, "/fb", "/fb/clshost.exe"},
		{"", "clshost.exe", exeDir, "", "clshost.exe"},
		{"", "srep", "", "", "srep"},
	}
	for _, tt := range tests {
		if got := FindTool(tt.set, tt.name, tt.exeDir, tt.fallback); got != tt.want {
			t.Errorf("FindTool(%q, %q, %q) = %q, want %q", tt.set, tt.name, tt.fallback, got, tt.want)
		}
	}
}
