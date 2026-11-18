package plugin_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/n0madic/go-unarc/internal/codec/lzma"
	_ "github.com/n0madic/go-unarc/internal/codec/plugin"
	_ "github.com/n0madic/go-unarc/internal/codec/srep"
	"github.com/n0madic/go-unarc/internal/method"
)

// A CLS DLL named like a built-in method does not replace it: plugins are looked up last, as in C.
func TestBuiltinFirst(t *testing.T) {
	t.Parallel()
	cls := t.TempDir()
	for _, n := range []string{"CLS-lzma.dll", "CLS-other.dll"} {
		if err := os.WriteFile(filepath.Join(cls, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := &method.Env{CLSDir: cls}
	c, ok := method.Lookup("lzma:1mb", env)
	if !ok || fmt.Sprintf("%T", c) == "*plugin.codec" {
		t.Errorf("lzma resolved to %T", c)
	}
	if c, ok := method.Lookup("other", env); !ok || fmt.Sprintf("%T", c) != "*plugin.codec" {
		t.Errorf("CLS-other.dll resolved to %T", c)
	}
}

func TestSrepOverride(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", "/opt/srep"} {
		c, ok := method.Lookup("srep:m3f", &method.Env{Srep: path})
		if !ok || (fmt.Sprintf("%T", c) == "*plugin.codec") != (path != "") {
			t.Errorf("UNARC_SREP=%q: resolved to %T", path, c)
		}
	}
}
