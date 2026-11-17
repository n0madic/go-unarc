package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()
	pw, empty := "secret", ""
	tests := []struct {
		args []string
		ok   bool
		want Command
	}{
		{[]string{"l", "a.arc"}, true, Command{Cmd: 'l', Arcname: "a.arc"}},
		{[]string{"x", "-o+", "-dpout", "a", "f1", "-o-"}, true,
			Command{Cmd: 'x', Arcname: "a", OutDir: "out", Yes: true, Filenames: []string{"f1", "-o-"}}},
		{[]string{"-p" + pw, "e", "-w/tmp", "--noarcext", "a", "-o-", "f"}, true,
			Command{Cmd: 'e', Arcname: "a", WorkDir: "/tmp", NoArcExt: true, No: true, Password: &pw, Filenames: []string{"f"}}},
		{[]string{"t", "-cls", "-p", "a"}, true, Command{Cmd: 't', Arcname: "a", CLSDir: &empty, Password: &empty}},
		// "--" ends the options: what follows is the command, the archive and file names.
		{[]string{"--", "x", "-dp", "-o+"}, true, Command{Cmd: 'x', Arcname: "-dp", Filenames: []string{"-o+"}}},
		{[]string{"x", "a", "--", "-f"}, true, Command{Cmd: 'x', Arcname: "a", Filenames: []string{"-f"}}},
		{[]string{}, false, Command{}},
		{[]string{"l"}, false, Command{}},
		{[]string{"q", "a"}, false, Command{}},
		{[]string{"lx", "a"}, false, Command{}},
		{[]string{"", "a"}, false, Command{}},
		{[]string{"l", "-z", "a"}, false, Command{}},
		{[]string{"-", "l", "a"}, false, Command{}},
	}
	for _, tt := range tests {
		got, ok := ParseArgs(tt.args)
		if ok != tt.ok {
			t.Errorf("ParseArgs(%q) ok = %v, want %v", tt.args, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Cmd != tt.want.Cmd || got.Arcname != tt.want.Arcname || got.OutDir != tt.want.OutDir ||
			got.WorkDir != tt.want.WorkDir || got.Yes != tt.want.Yes || got.No != tt.want.No ||
			got.NoArcExt != tt.want.NoArcExt || !slices.Equal(got.Filenames, tt.want.Filenames) ||
			!samePtr(got.Password, tt.want.Password) || !samePtr(got.CLSDir, tt.want.CLSDir) {
			t.Errorf("ParseArgs(%q) = %+v, want %+v", tt.args, got, tt.want)
		}
	}
}

func samePtr(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func TestArchiveName(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"data.arc", "plain", "x.bin"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := func(n string) string { return filepath.Join(dir, n) }
	tests := []struct {
		name     string
		noArcExt bool
		want     string
	}{
		{p("data"), false, p("data.arc")},
		{p("data"), true, p("data")},
		{p("plain"), false, p("plain")}, // exists as is
		{p("x"), false, p("x")},         // no x.arc either
		{p("missing.bin"), false, p("missing.bin")},
		{p("data.arc"), false, p("data.arc")},
	}
	for _, tt := range tests {
		if got := archiveName(&Command{Arcname: tt.name, NoArcExt: tt.noArcExt}); got != tt.want {
			t.Errorf("archiveName(%q, noarcext=%v) = %q, want %q", tt.name, tt.noArcExt, got, tt.want)
		}
	}
}
