package extract

import (
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/n0madic/go-unarc/internal/arc"
	"github.com/n0madic/go-unarc/internal/method"
)

func TestUnsafePath(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"":             false,
		"a.txt":        false,
		"dir/a.txt":    false,
		"a..b/c":       false,
		"..a/b..":      false,
		"./a":          false,
		"x:":           true, // drive letter
		"C:\\win":      true,
		"c:a":          true,
		"1:a":          false,
		"/etc/passwd":  true,
		"\\server\\x":  true,
		"..":           true,
		"../a":         true,
		"a/../../b":    true,
		"a\\..\\b":     true,
		"a/..":         true,
		"a//..//b":     true,
		"dir/sub/..\\": true,
	}
	for name, want := range tests {
		if got := UnsafePath(name); got != want {
			t.Errorf("UnsafePath(%q) = %v, want %v", name, got, want)
		}
	}
}

type quietUI struct{}

func (quietUI) ProgressFile(bool, string, string, uint64) {}
func (quietUI) AskOverwrite(string, uint64, uint32) byte  { return 'n' }

func TestFullTestKeepsReading(t *testing.T) {
	const content = "file contents"
	file := arc.File{Name: "file", Size: uint64(len(content)), CRC: crc32.ChecksumIEEE([]byte(content))}
	for _, tc := range []struct {
		name      string
		selection []string
		data      string
		want      error
	}{
		{"exact", nil, content, nil},
		{"extra", nil, content + "extra", method.ErrBadData},
		{"selected", []string{"file"}, content + "extra", method.ErrStop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New(Options{Cmd: 't', Filenames: tc.selection, UI: quietUI{}})
			p.dir = &arc.Dir{Files: []arc.File{file}}
			if err := p.open(false); err != nil {
				t.Fatal(err)
			}
			if n, err := p.Write([]byte(tc.data)); !errors.Is(err, tc.want) || n < len(content) {
				t.Fatalf("Write = %d, %v; want %v", n, err, tc.want)
			}
			if tc.want == nil {
				if n, err := p.Write([]byte("extra")); n != 0 || !errors.Is(err, method.ErrBadData) {
					t.Fatalf("late surplus = %d, %v", n, err)
				}
			}
		})
	}
}

func TestPublishReplacesLinkNotItsTarget(t *testing.T) {
	dir := t.TempDir()
	original, alias := filepath.Join(dir, "original"), filepath.Join(dir, "alias")
	if err := os.WriteFile(original, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original", alias); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	const content = "after"
	p := New(Options{Cmd: 'x', OutDir: dir, Yes: true, UI: quietUI{}})
	defer p.Close()
	p.dir = &arc.Dir{Files: []arc.File{{Name: "alias", Size: uint64(len(content)), CRC: crc32.ChecksumIEEE([]byte(content))}}}
	if err := p.open(false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write([]byte(content)); !errors.Is(err, method.ErrStop) {
		t.Fatal(err)
	}
	for path, want := range map[string]string{original: "before", alias: content} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s: got %q, %v, want %q", path, got, err, want)
		}
	}
}

func TestAccept(t *testing.T) {
	t.Parallel()
	f := &arc.File{Dir: "dir", Name: "a.txt"}
	tests := []struct {
		names []string
		want  bool
	}{
		{nil, true},
		{[]string{"a.txt"}, true},
		{[]string{"b", "a.txt"}, true},
		{[]string{"dir/a.txt"}, false}, // C compares the name without its directory
		{[]string{"A.TXT"}, false},
	}
	for _, tt := range tests {
		if got := Accept(f, tt.names); got != tt.want {
			t.Errorf("Accept(%q) = %v, want %v", tt.names, got, tt.want)
		}
	}
}
