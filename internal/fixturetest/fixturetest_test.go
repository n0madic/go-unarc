package fixturetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckArchives(t *testing.T) {
	t.Parallel()
	const pointer = lfsPointerHeader + "\noid sha256:0123\nsize 45477\n"
	tests := []struct {
		name    string
		file    string // content of archives/a.arc; "" for no archive at all
		wantErr bool
	}{
		{"no archives", "", false},
		{"real archive", "ArC\x01 data", false},
		{"shorter than the pointer header", "ArC", false},
		{"LFS pointer", pointer, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.file != "" {
				if err := os.MkdirAll(filepath.Join(root, "archives"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "archives", "a.arc"), []byte(tc.file), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := checkArchives(root)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkArchives = %v, want error: %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "git lfs pull") {
				t.Errorf("error %q does not say how to fix it", err)
			}
		})
	}
}
