//go:build unix

package plugin

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/method"
)

func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// tmpdir points TMPDIR at a fresh folder, where the plugin stages put their files.
func tmpdir(t *testing.T) string {
	d := t.TempDir()
	t.Setenv("TMPDIR", d)
	return d
}

func decode(t *testing.T, env *method.Env, m string, in []byte) ([]byte, error) {
	t.Helper()
	c, ok := method.Lookup(m, env)
	if !ok {
		t.Fatalf("%s not recognised", m)
	}
	var out bytes.Buffer
	err := c.Decode(&out, bytes.NewReader(in))
	return out.Bytes(), err
}

func TestSrep(t *testing.T) {
	tmp := tmpdir(t)
	bin := t.TempDir()
	srep := script(t, bin, "srep", `[ "$1" = -d ] || exit 9
tr a-z A-Z < "$2" > "$3"
`)
	var stderr bytes.Buffer
	work := t.TempDir()
	env := &method.Env{Srep: srep, Stderr: &stderr, TempDir: work}
	got, err := decode(t, env, "srep:m3f:a1", []byte("hello"))
	if err != nil || string(got) != "HELLO" {
		t.Fatalf("decode = %q, %v", got, err)
	}
	if want := "\n[cls] " + srep + " -d\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temp files left: %s", entries[0].Name())
	}

	// UNARC_CLS_KEEP keeps the input.
	stderr.Reset()
	env.CLSKeep = true
	if _, err := decode(t, env, "srep", []byte("kept")); err != nil {
		t.Fatal(err)
	}
	var kept string
	ok := false
	for _, line := range strings.Split(stderr.String(), "\n") {
		if k, found := strings.CutPrefix(line, "[cls] kept "); found {
			kept, ok = k, true
		}
	}
	if data, err := os.ReadFile(kept); !ok || err != nil || string(data) != "kept" {
		t.Errorf("kept input %q: %q, %v (stderr %q)", kept, data, err, stderr.String())
	}
	if filepath.Dir(filepath.Dir(kept)) != work {
		t.Errorf("kept input %q is not under the work directory %q", kept, work)
	}
}

func TestFailures(t *testing.T) {
	tmpdir(t)
	bin := t.TempDir()
	tests := []struct {
		srep, stderr string
	}{
		{script(t, bin, "fails", "exit 1\n"), "[cls] codec failed (status 256)\n"},
		{script(t, bin, "notfound", "exit 127\n"), "[cls] could not start TOOL\n[cls] codec failed (status 32512)\n"},
		{filepath.Join(bin, "missing"), "[cls] could not start TOOL\n[cls] codec failed (status 32512)\n"},
		{script(t, bin, "killed", "kill -9 $$\n"), "[cls] codec failed (status 9)\n"},
	}
	for _, tt := range tests {
		var stderr bytes.Buffer
		env := &method.Env{Srep: tt.srep, Stderr: &stderr}
		_, err := decode(t, env, "srep", []byte("x"))
		if !errors.Is(err, method.ErrBadData) {
			t.Errorf("%s: err = %v, want ErrBadData", tt.srep, err)
		}
		if want := "\n[cls] " + tt.srep + " -d\n" + strings.ReplaceAll(tt.stderr, "TOOL", tt.srep); stderr.String() != want {
			t.Errorf("%s: stderr = %q, want %q", tt.srep, stderr.String(), want)
		}
	}
}

func TestCLS(t *testing.T) {
	tmpdir(t)
	t.Chdir(t.TempDir())
	if err := os.Mkdir("work", 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WINEDEBUG", "")
	os.Unsetenv("WINEDEBUG")
	cls, bin := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(cls, "cls-Foo.DLL"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A wine that runs "clshost dll params in out" itself: it maps Z:\ paths back and writes what it got.
	wine := script(t, bin, "wine", `unz() { printf '%s\n' "$1" | sed 's/^Z://; s|\\|/|g'; }
in=$(unz "$4"); out=$(unz "$5")
case "$in" in /*) ;; *) exit 9 ;; esac
{ echo "$1|$(basename "$(unz "$2")")|$3|$WINEDEBUG"; cat "$in"; } > "$out"
`)
	var stderr bytes.Buffer
	env := &method.Env{CLSDir: cls, Wine: wine, CLSHost: "/opt/clshost.exe", Stderr: &stderr, TempDir: "work"}
	got, err := decode(t, env, "foo:a:b", []byte("data"))
	if want := "/opt/clshost.exe|cls-Foo.DLL|a:b|-all\ndata"; err != nil || string(got) != want {
		t.Fatalf("decode = %q, %v; want %q", got, err, want)
	}
	if want := "\n[cls] /opt/clshost.exe cls-Foo.DLL \"a:b\" under " + wine + "\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	// clshost.exe from the CLS folder, as C prints the path.
	env.CLSHost, env.CLSDir = "", cls+"/"
	stderr.Reset()
	if _, err := decode(t, env, "FOO", nil); err != nil || !strings.Contains(stderr.String(), cls+"//clshost.exe cls-Foo.DLL") {
		t.Errorf("host from the CLS folder: %v, stderr %q", err, stderr.String())
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	cls := t.TempDir()
	if err := os.WriteFile(filepath.Join(cls, "CLS-bar.dll"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		m   string
		env *method.Env
		ok  bool
	}{
		{"srep", &method.Env{}, true},
		{"srep:m1", &method.Env{}, true},
		{"bar", &method.Env{}, false},
		{"bar", &method.Env{CLSDir: cls}, true},
		{"baz", &method.Env{CLSDir: cls}, false},
		{"bar", &method.Env{CLSDir: filepath.Join(cls, "missing")}, false},
	}
	for _, tt := range tests {
		if _, ok := Parse(method.Params(tt.m), tt.env); ok != tt.ok {
			t.Errorf("Parse(%q, CLSDir %q) = %v, want %v", tt.m, tt.env.CLSDir, ok, tt.ok)
		}
	}
}

type stopWriter struct{}

func (stopWriter) Write([]byte) (int, error) { return 0, method.ErrStop }

func TestStop(t *testing.T) {
	tmp := tmpdir(t)
	env := &method.Env{Srep: script(t, t.TempDir(), "srep", `cp "$2" "$3"`)}
	c, _ := Parse([]string{"srep"}, env)
	if err := c.Decode(stopWriter{}, strings.NewReader("abc")); !errors.Is(err, method.ErrStop) {
		t.Errorf("err = %v, want ErrStop", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temp files left: %s", entries[0].Name())
	}
}

// TestRealSrep decodes a stream made by the srep on PATH, if there is one.
func TestRealSrep(t *testing.T) {
	srep, err := exec.LookPath("srep")
	if err != nil {
		t.Skip("srep not on PATH")
	}
	tmpdir(t)
	dir := t.TempDir()
	data := bytes.Repeat([]byte("FreeArc srep test block. "), 200000)
	in, packed := filepath.Join(dir, "in"), filepath.Join(dir, "in.srep")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(srep, in, packed).CombinedOutput(); err != nil {
		t.Skipf("srep cannot compress here: %v\n%s", err, out)
	}
	p, err := os.ReadFile(packed)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decode(t, &method.Env{Srep: srep}, "srep", p)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("decode: %v, equal %v", err, bytes.Equal(got, data))
	}
}
