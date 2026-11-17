package method

import (
	"slices"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	t.Parallel()
	if got := Split("exe+lzma:1mb+aes"); !slices.Equal(got, []string{"exe", "lzma:1mb", "aes"}) {
		t.Errorf("Split = %q", got)
	}
	// C keeps at most MAX_METHODS_IN_COMPRESSOR-1 methods; the last one holds the rest.
	many := make([]string, 120)
	for i := range many {
		many[i] = "m"
	}
	got := Split(strings.Join(many, "+"))
	if len(got) != maxMethodsInCompressor-1 || got[len(got)-1] != strings.Join(many[:22], "+") {
		t.Errorf("Split of 120 methods: %d pieces, last %q", len(got), got[len(got)-1])
	}
	if got := Params("lzma:64mb::normal"); !slices.Equal(got, []string{"lzma", "64mb", "", "normal"}) {
		t.Errorf("Params = %q", got)
	}
	long := "x:" + strings.Repeat("y", 3000)
	if got := Params(long); len(got[1]) != maxMethodStrlen-1-2 {
		t.Errorf("Params of a long method keeps %d bytes", len(got[1]))
	}
	if got := Name("aes-256/ctr:k12:i34"); got != "aes-256/ctr" {
		t.Errorf("Name = %q", got)
	}
}

func TestParseInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want uint32
		ok   bool
	}{
		{"", 0, false},
		{"=", 0, false},
		{"=5", 5, true},
		{"12", 12, true},
		{"12a", 12, false},
		{"a", 0, false},
		{"4294967296", 0, true}, // wraps like the C MemSize
		{"==1", 0, false},
	}
	for _, tt := range tests {
		if got, ok := ParseInt(tt.in); got != tt.want || ok != tt.ok {
			t.Errorf("ParseInt(%q) = %d, %v; want %d, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseMem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want uint32
		ok   bool
	}{
		{"64mb", 64 * MB, true},
		{"64m", 64 * MB, true},
		{"1k", KB, true},
		{"8b", 8, true},
		{"2g", 2 * GB, true},
		{"5gb", 1 * GB, true}, // 5 GiB wraps to 1 GiB in 32 bits
		{"24", 16 * MB, true}, // a bare number is a power of two
		{"24^", 16 * MB, true},
		{"32", 1, true}, // shift count masked as on x86
		{"=1m", MB, true},
		{"m", 0, true},
		{"", 1, false},
		{"x", 0, false},
		{"12x", 0, false},
	}
	for _, tt := range tests {
		got, ok := ParseMem(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("ParseMem(%q) = %d, %v; want %d, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
