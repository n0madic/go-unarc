package extract

import (
	"testing"

	"github.com/n0madic/go-unarc/internal/arc"
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
