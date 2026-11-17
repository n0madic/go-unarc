package method

import (
	"math"
	"testing"
)

func TestAddTempfile(t *testing.T) {
	t.Parallel()
	env := &Env{}
	tests := []struct{ in, want string }{
		{"fakemem:400+fakemem:600", "fakemem:400+fakemem:600"},
		{"fakemem:600+fakemem:600", "fakemem:600+tempfile+fakemem:600"},
		{"fakemem:600+fakemem:300+fakemem:600", "fakemem:600+fakemem:300+tempfile+fakemem:600"},
		{"fakemem:0+fakemem:2000+fakemem:0", "fakemem:0+fakemem:2000+tempfile+fakemem:0"},
		{"fakemem:2000", "fakemem:2000"},
		// An unknown method needs (MemSize)-2 bytes, so a tempfile follows it.
		{"nosuchmethod+fakemem:1", "nosuchmethod+tempfile+fakemem:1"},
		// Memory is a 32-bit MemSize: 2^32+5 is 5.
		{"fakemem:4294967301+fakemem:900", "fakemem:4294967301+fakemem:900"},
	}
	for _, tt := range tests {
		if got := AddTempfile(tt.in, 1000, env); got != tt.want {
			t.Errorf("AddTempfile(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if m := MaxMem(); m == 0 || m > math.MaxUint32/4*3 {
		t.Errorf("MaxMem() = %d", m)
	}
}
