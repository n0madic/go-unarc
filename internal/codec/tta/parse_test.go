package tta

import (
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

// The accepted and refused method strings are those of parse_TTA. The expectations were taken from the C
// parser with "fagen mem" (TestParseMatchesC repeats the check when fagen is built).
var parseTests = []struct {
	method string
	ok     bool
}{
	{"tta", true},
	{"tta:m1", true},
	{"tta:m3", true},
	{"tta:m5", true}, // the level is not range-checked by the parser
	{"tta:m0", true},
	{"tta:m=3", true},
	{"tta:s", true},
	{"tta:sxyz", true}, // only the first letter of a flag counts
	{"tta:f", true},
	{"tta:foo", true},
	{"tta:c2", true},
	{"tta:w16", true},
	{"tta:o44", true},
	{"tta:r1", true},
	{"tta:c2:w16:o44", true},
	{"tta:m3:s", true},
	{"tta:f:c2:w32:o44", true},
	{"tta:2*16", true},
	{"tta:2*16f", true},
	{"tta:2*16 f", true},
	{"tta:2*16fx", true}, // C: not a float, still accepted
	{"tta:2*16x", true},
	{"tta:2*16f g", true},
	{"tta: 2*16", true},
	{"tta:2* 16", true},
	{"tta:+2*16", true},
	{"tta:-2*-16", true},
	{"tta:2*+16", true},
	{"tta:99999999999*1", true},
	{"tta:2*99999999999999999999999f", true},
	{"tta:m3:2*16:o44", true},
	{"tta:2*16:r1", true},
	{"tta:m=", false},
	{"tta:m", false},
	{"tta:mx", false},
	{"tta:r", false},
	{"tta:w", false},
	{"tta:", false},
	{"tta: ", false},
	{"tta:x", false},
	{"tta:2 *16", false},
	{"tta:2*", false},
	{"tta:*16", false},
	{"tta:2*-", false},
	{"tta:0x2*16", false},
	{"tta:c2*16", false},
	{"tta:=2*16", false},
	{"tta:m3:x", false},
	{"ttax", false},
	{"tt", false},
	{"TTA", false},
}

func TestParse(t *testing.T) {
	t.Parallel()
	for _, tt := range parseTests {
		c, ok := Parse(method.Params(tt.method), nil)
		if ok != tt.ok {
			t.Errorf("Parse(%q) ok = %v, want %v", tt.method, ok, tt.ok)
			continue
		}
		if ok && c.DecompressionMem() != 1<<20 {
			t.Errorf("Parse(%q): DecompressionMem = %d, want 1 MB", tt.method, c.DecompressionMem())
		}
	}
}

func TestParseMatchesC(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	for _, tt := range parseTests {
		res := codectest.FagenTry(t, nil, "mem", tt.method)
		out := res.Out
		if res.Exit != 0 {
			t.Fatalf("fagen mem %q: exit %d: %s", tt.method, res.Exit, res.Stderr)
		}
		// GetDecompressionMem answers FREEARC_ERRCODE_INVALID_COMPRESSOR (-2, as an unsigned number) for a refused method.
		want := string(out) != "4294967294\n"
		if want != tt.ok {
			t.Errorf("%q: the C parser accepts: %v, the table says %v", tt.method, want, tt.ok)
		}
		if tt.ok && string(out) != "1048576\n" {
			t.Errorf("%q: C decompression memory %q, want 1048576", tt.method, out)
		}
	}
}

// Parse is a method.Parser registered for the method, and finds nothing else.
func TestRegistered(t *testing.T) {
	t.Parallel()
	env := &method.Env{}
	if c, ok := method.Lookup("tta:m3:2*16:o44", env); !ok || c.DecompressionMem() != 1<<20 {
		t.Errorf("Lookup(tta:m3:2*16:o44) = %v, %v", c, ok)
	}
	if _, ok := method.Lookup("tta:bogus", env); ok {
		t.Error("Lookup(tta:bogus) succeeded")
	}
}
