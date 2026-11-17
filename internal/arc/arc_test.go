package arc_test

import (
	"bytes"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/n0madic/go-unarc/internal/arc"
	"github.com/n0madic/go-unarc/internal/arctest"
	_ "github.com/n0madic/go-unarc/internal/codec/storing"
	"github.com/n0madic/go-unarc/internal/method"
)

func sample() arctest.Archive {
	return arctest.Archive{
		Prefix:  []byte("MZ sfx module"),
		Comment: []byte("a comment"),
		Locked:  true,
		Dirs:    []int{1, 2},
		Solids: []arctest.Solid{
			{Compressor: "storing", Files: []arctest.File{
				{Path: "dir", IsDir: true, Time: 1600000000},
				{Path: "dir/a.txt", Data: []byte("hello world\n"), Time: 1600000001},
				{Path: "empty", Time: 1600000002},
			}},
			{Compressor: "storing", Files: []arctest.File{{Path: `x\y:z/b.bin`, Data: bytes.Repeat([]byte{7}, 70000)}}},
			{Compressor: "storing", Files: []arctest.File{{Path: "c", Data: []byte("c")}}},
		},
	}
}

func write(t testing.TB, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.arc")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOpen(t *testing.T) {
	t.Parallel()
	s := sample()
	a, err := arc.Open(write(t, arctest.Build(s)), &method.Env{Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if string(a.Comment) != "a comment" || !a.Locked || a.SFXSize != uint64(len(s.Prefix)) {
		t.Errorf("comment %q, locked %v, sfx %d", a.Comment, a.Locked, a.SFXSize)
	}
	var files []arc.File
	var blocks []arc.DataBlock
	for _, blk := range a.Blocks {
		if blk.Type != arc.DirBlock {
			continue
		}
		d, err := a.ReadDir(blk)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range d.Blocks {
			var data bytes.Buffer
			r, err := a.SectionReader(b.Pos, b.CompSize)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(&data, r); err != nil {
				t.Fatal(err)
			}
			for _, f := range d.Files[b.Start:b.End] {
				got := data.Next(int(f.Size))
				if crc32.ChecksumIEEE(got) != f.CRC {
					t.Errorf("%s: data does not match its CRC", f.FullName())
				}
			}
		}
		files = append(files, d.Files...)
		blocks = append(blocks, d.Blocks...)
	}
	if len(files) != 5 || len(blocks) != 3 {
		t.Fatalf("%d files, %d blocks", len(files), len(blocks))
	}
	want := []struct {
		name  string
		size  uint64
		isDir bool
		time  uint32
	}{
		{"dir", 0, true, 1600000000}, {"dir/a.txt", 12, false, 1600000001}, {"empty", 0, false, 1600000002},
		{"x/y/z/b.bin", 70000, false, 0}, {"c", 1, false, 0},
	}
	for i, w := range want {
		f := files[i]
		if f.FullName() != w.name || f.Size != w.size || f.IsDir != w.isDir || f.Time != w.time {
			t.Errorf("file %d = %+v, want %+v", i, f, w)
		}
	}
}

func TestOpenErrors(t *testing.T) {
	t.Parallel()
	good := arctest.Build(sample())
	corrupt := bytes.Clone(good)
	corrupt[len(corrupt)-1] ^= 1 // the footer descriptor's CRC
	tests := []struct {
		name, msg string
		data      []byte
	}{
		{"empty", "ERROR: this is not FreeArc archive or this archive is corrupt", nil},
		{"text", "ERROR: this is not FreeArc archive or this archive is corrupt", []byte("hello, world")},
		{"descriptor CRC", "ERROR: archive structure corrupted (descriptor failed CRC check)", corrupt},
		{"signature only", "ERROR: archive structure corrupted (descriptor failed CRC check)", []byte("ArC\x01")},
	}
	for _, tt := range tests {
		_, err := arc.Open(write(t, tt.data), &method.Env{Stderr: io.Discard})
		if err == nil || err.Error() != tt.msg {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.msg)
		}
	}
	missing := filepath.Join(t.TempDir(), "missing.arc")
	if _, err := arc.Open(missing, &method.Env{}); err == nil || err.Error() != "ERROR: can't open file "+missing {
		t.Errorf("missing file: err = %v", err)
	}
}

// fuzzTimeout is how long one fuzz input may take: a slower one is reported as a hang.
const fuzzTimeout = 10 * time.Second

// FuzzOpen feeds damaged archives to the structure parser: errors are fine, panics and hangs are not.
func FuzzOpen(f *testing.F) {
	// Small seeds: the fuzzer minimizes every interesting input, which takes long for big ones.
	small := sample()
	small.Solids[1].Files[0].Data = []byte("bin")
	f.Add(arctest.Build(small))
	f.Add(arctest.Build(arctest.Archive{OldComment: []byte("old"), Solids: []arctest.Solid{{Compressor: "storing",
		Files: []arctest.File{{Path: "a", Data: []byte("a")}}}}}))
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		p := filepath.Join(dir, "fuzz.arc")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			a, err := arc.Open(p, &method.Env{Stderr: io.Discard})
			if err != nil {
				return
			}
			defer a.Close()
			for _, blk := range a.Blocks {
				if blk.Type == arc.DirBlock {
					_, _ = a.ReadDir(blk)
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(fuzzTimeout):
			t.Fatalf("no result after %v", fuzzTimeout)
		}
	})
}
