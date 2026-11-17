package external

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/method"
)

func TestPrepareCmd(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cmd     string
		options []string
		def     string
		want    string
	}{
		{"pack {options} x", []string{"m48", "r1"}, "", "pack -m48 -r1  x"},
		{"pack {options} x", nil, "-m10", "pack -m10 x"},
		{"pack {options}", []string{""}, "", "pack - "},
		{"pack {--option=} x", []string{"a", "b"}, "", "pack --a=--b= x"},
		{"pack {no} {-option } x", []string{"a"}, "", "pack {no} -a  x"},
		{"pack {option}", nil, "dflt", "pack dflt"},
		{"pack {-option x", []string{"a"}, "", "pack {-option x"},
		{"pack x", []string{"a"}, "", "pack x"},
	}
	for _, tt := range tests {
		if got := prepareCmd(tt.cmd, tt.options, tt.def); got != tt.want {
			t.Errorf("prepareCmd(%q, %q, %q) = %q, want %q", tt.cmd, tt.options, tt.def, got, tt.want)
		}
	}
}

func TestShow3(t *testing.T) {
	t.Parallel()
	for n, want := range map[uint64]string{0: "0", 999: "999", 1000: "1.000", 1234567: "1.234.567", 100000: "100.000"} {
		if got := show3(n); got != want {
			t.Errorf("show3(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	t.Parallel()
	tests := map[string][]string{
		"a b  c":                {"a", "b", "c"},
		` "x y" z "" `:          {"x y", "z", ""},
		"p $$arcdatafile$$.tmp": {"p", "$$arcdatafile$$.tmp"},
		"  ":                    nil,
	}
	for in, want := range tests {
		if got := splitCommand(in); !slices.Equal(got, want) {
			t.Errorf("splitCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

const iniText = `; external compressors
[Other section]
foo=bar
[External compressor: ccm123, ccmx123 ]
mem = 276
packcmd   = {compressor} c $$arcdatafile$$.tmp $$arcpackedfile$$.tmp
unpackcmd = {compressor} d $$arcpackedfile$$.tmp $$arcdatafile$$.tmp
datafile   = $$arcdatafile$$.tmp
; a comment
solid=0

[External compressor:badnumber]
mem = 1x
[External compressor:blankline]
   
[External compressor:noequals]
unpackcmd x
[External compressor:unknown]
color = red
[External compressor:good2]
dmem=3
unpackcmd = x {compressor}
default=-q`

func TestLoad(t *testing.T) {
	t.Parallel()
	for _, eol := range []string{"\n", "\r\n"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, iniName), []byte(strings.ReplaceAll(iniText, "\n", eol)), 0o600); err != nil {
			t.Fatal(err)
		}
		env := &method.Env{ExeDir: dir}
		Load(env)
		if len(env.External) != 3 {
			t.Fatalf("eol %q: %d compressors registered, want 3", eol, len(env.External))
		}
		for _, name := range []string{"badnumber", "blankline", "noequals", "unknown", "Other"} {
			if _, ok := method.Lookup(name, env); ok {
				t.Errorf("eol %q: %s registered", eol, name)
			}
		}
		c, ok := method.Lookup("ccmx123:a:b", env)
		if !ok {
			t.Fatalf("eol %q: ccmx123 not registered", eol)
		}
		got := c.(*codec)
		if got.dmem != 276*method.MB || got.unpackcmd != "ccmx123 d $$arcpackedfile$$.tmp $$arcdatafile$$.tmp" ||
			got.packedfile != "$$arcpackedfile$$.tmp" || !slices.Equal(got.options, []string{"a", "b"}) {
			t.Errorf("eol %q: ccmx123 = %+v %+v", eol, *got.compressor, got.options)
		}
		c, _ = method.Lookup("good2", env)
		if got := c.(*codec); got.dmem != 3*method.MB || got.unpackcmd != "x good2" || got.defaultopt != "-q" {
			t.Errorf("eol %q: good2 = %+v", eol, *got.compressor)
		}
	}
	// No arc.ini, or no folder: nothing.
	env := &method.Env{ExeDir: t.TempDir(), External: []method.Parser{nil}}
	Load(env)
	if env.External != nil {
		t.Error("compressors left from before")
	}
}

func TestParsePMM(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method string
		ok     bool
		mem    uint32
	}{
		{"pmm", true, 192 * method.MB},
		{"pmm:16:256mb", true, 256 * method.MB},
		{"pmm:o8:m64mb:r2:r", true, 64 * method.MB},
		{"pmm:mem32mb", true, 32 * method.MB},
		{"pmm:x", false, 0},
		{"pmm:", false, 0},
		{"pmm:o", false, 0},
		{"pmmx", false, 0},
	}
	for _, tt := range tests {
		c, ok := ParsePMM(method.Params(tt.method), nil)
		if ok != tt.ok {
			t.Errorf("ParsePMM(%q) ok = %v", tt.method, ok)
			continue
		}
		if ok && c.DecompressionMem() != uint64(tt.mem) {
			t.Errorf("ParsePMM(%q) mem = %d, want %d", tt.method, c.DecompressionMem(), tt.mem)
		}
	}
}

// fakeEnv returns an env whose unarc folder holds an arc.ini declaring "fake" and the program fakeunpack.
func fakeEnv(t *testing.T, script string) (*method.Env, *bytes.Buffer) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	exe := t.TempDir()
	ini := "[External compressor:fake]\nunpackcmd = fakeunpack {options} $$arcpackedfile$$.tmp $$arcdatafile$$.tmp\ndefault = -dflt\n"
	if err := os.WriteFile(filepath.Join(exe, iniName), []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exe, "fakeunpack"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	env := &method.Env{ExeDir: exe, TempDir: t.TempDir(), Stdout: &stdout, Stderr: &bytes.Buffer{}}
	Load(env)
	return env, &stdout
}

// argsThenInput writes its arguments and then its input file (the next to last argument) to the last one.
const argsThenInput = `for a in "$@"; do in="$out"; out="$a"; done
{ echo "$*"; cat "$in"; } > "$out"
`

func decode(t *testing.T, env *method.Env, m string, in []byte) ([]byte, error) {
	t.Helper()
	c, ok := method.Lookup(m, env)
	if !ok {
		t.Fatalf("%s not registered", m)
	}
	var out bytes.Buffer
	err := c.Decode(&out, bytes.NewReader(in))
	if entries, _ := os.ReadDir(env.TempDir); len(entries) != 0 {
		t.Errorf("temp folder left: %v", entries[0].Name())
	}
	return out.Bytes(), err
}

func TestDecode(t *testing.T) {
	t.Parallel()
	env, stdout := fakeEnv(t, argsThenInput)
	got, err := decode(t, env, "fake:x:y", []byte("\x01hello"))
	want := "-x -y $$arcpackedfile$$.tmp $$arcdatafile$$.tmp\nhello"
	if err != nil || string(got) != want {
		t.Fatalf("decode = %q, %v; want %q", got, err, want)
	}
	wantOut := "\nUnpacking 5 bytes with fakeunpack -x -y  $$arcpackedfile$$.tmp $$arcdatafile$$.tmp\n\nErrorlevel=0\n"
	if stdout.String() != wantOut {
		t.Errorf("stdout = %q, want %q", stdout, wantOut)
	}

	// The default options when the method has none.
	if got, err := decode(t, env, "fake", []byte("\x01!")); err != nil || !strings.HasPrefix(string(got), "-dflt $$") {
		t.Errorf("default options: %q, %v", got, err)
	}
	// Stored data: no program run.
	stdout.Reset()
	if got, err := decode(t, env, "fake", []byte("\x00stored")); err != nil || string(got) != "stored" || stdout.Len() != 0 {
		t.Errorf("stored: %q, %v, stdout %q", got, err, stdout)
	}
	// Before the header byte existed, the first byte was data and the output went to data7777.
	env2, _ := fakeEnv(t, `cat "$2" > data7777`)
	if got, err := decode(t, env2, "fake", []byte("Xyz")); err != nil || string(got) != "Xyz" {
		t.Errorf("legacy: %q, %v", got, err)
	}
	if _, err := decode(t, env, "fake", nil); !errors.Is(err, method.ErrBadData) {
		t.Errorf("empty stream: err = %v, want ErrBadData", err)
	}
}

func TestDecodeFailures(t *testing.T) {
	t.Parallel()
	env, stdout := fakeEnv(t, "exit 3\n")
	if _, err := decode(t, env, "fake", []byte("\x01data")); !errors.Is(err, method.ErrUnsupportedMethod) {
		t.Errorf("failing program: err = %v", err)
	}
	if !strings.HasSuffix(stdout.String(), "\nErrorlevel=3\n") {
		t.Errorf("stdout = %q", stdout)
	}
	env, _ = fakeEnv(t, "exit 0\n")
	if _, err := decode(t, env, "fake", []byte("\x01data")); !errors.Is(err, method.ErrUnsupportedMethod) {
		t.Errorf("no output: err = %v", err)
	}
	env, stdout = fakeEnv(t, "")
	if err := os.Remove(filepath.Join(env.ExeDir, "fakeunpack")); err != nil {
		t.Fatal(err)
	}
	if _, err := decode(t, env, "fake", []byte("\x01data")); !errors.Is(err, method.ErrUnsupportedMethod) ||
		!strings.HasSuffix(stdout.String(), "\nErrorlevel=127\n") {
		t.Errorf("missing program: err = %v, stdout %q", err, stdout)
	}
}

func FuzzParseSection(f *testing.F) {
	f.Add(iniText)
	f.Fuzz(func(t *testing.T, s string) {
		for _, c := range parseSection(s) {
			_ = prepareCmd(c.unpackcmd, []string{"a"}, c.defaultopt)
		}
	})
}
