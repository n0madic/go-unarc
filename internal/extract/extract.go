// Package extract tests and extracts the files of solid blocks, cutting the decompressed stream into files
// the way PROCESS::ExtractFiles of the C unarc does.
package extract

import (
	"errors"
	"hash"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/n0madic/go-unarc/internal/arc"
	"github.com/n0madic/go-unarc/internal/cleanup"
	"github.com/n0madic/go-unarc/internal/method"
)

// UI receives progress and answers the overwrite question.
type UI interface {
	// ProgressFile reports a file (or, during 'x', a directory) being extracted, tested or skipped.
	ProgressFile(isDir bool, operation, name string, size uint64)
	// AskOverwrite asks whether to overwrite name; it answers one of 'y', 'n', 'a', 's', 'q'.
	AskOverwrite(name string, size uint64, mtime uint32) byte
}

// ErrAborted is returned when the user answers 'q' to the overwrite question.
var ErrAborted = errors.New("extraction aborted")

// Options of an extract or test command.
type Options struct {
	Cmd       byte // 'e', 'x' or 't'
	OutDir    string
	Filenames []string // names to process (without directories); empty means all
	Yes, No   bool     // -o+ / -o-
	UI        UI
	Env       *method.Env
}

// Accept reports whether f is selected by the names given on the command line (COMMAND::accept_file).
func Accept(f *arc.File, names []string) bool {
	if len(names) == 0 {
		return true
	}
	for _, n := range names {
		if f.Name == n {
			return true
		}
	}
	return false
}

// Process extracts or tests one archive, keeping the state (-o+/-o- answers) across solid blocks.
type Process struct {
	opts Options
	base string // OutDir with a trailing path delimiter, as MYFILE::SetBaseDir builds it

	// state of the solid block being extracted
	dir          *arc.Dir
	cur          int
	extractUntil int
	included     bool
	bytesToWrite uint64
	crc          hash.Hash32
	xname        string // name shown in progress lines
	outName      string // name of the output file (base + xname)
	out          *os.File
	forget       func()
}

// New prepares extraction or testing.
func New(opts Options) *Process {
	p := &Process{opts: opts, crc: crc32.NewIEEE()}
	if opts.Cmd != 't' {
		p.base = opts.OutDir
		if p.base != "" && !strings.ContainsRune(":/\\", rune(p.base[len(p.base)-1])) {
			p.base += "/"
		}
	}
	return p
}

// Abort closes and removes the output file being written, as PROCESS::quit does.
func (p *Process) Abort() {
	if p.out != nil {
		p.out.Close()
		os.Remove(p.outName)
		p.out = nil
		p.forget()
	}
}

// ExtractBlock processes the files of solid block b of dir d.
func (p *Process) ExtractBlock(a *arc.Archive, d *arc.Dir, b int) error {
	p.dir = d
	blk := d.Blocks[b]
	p.extractUntil = -1
	// First pass: directories and empty files right away; remember the last file that needs data.
	for p.cur = blk.Start; p.cur < blk.End; p.cur++ {
		if !Accept(&d.Files[p.cur], p.opts.Filenames) {
			continue
		}
		if d.Files[p.cur].Size == 0 {
			if err := p.open(true); err != nil {
				return err
			}
			if err := p.close(); err != nil {
				return err
			}
		} else {
			p.extractUntil = p.cur
		}
	}
	if p.extractUntil < 0 {
		return nil
	}

	// Second pass: decompress the block and cut the stream into files.
	src, err := a.SectionReader(blk.Pos, blk.CompSize)
	if err != nil {
		return err
	}
	p.cur = blk.Start
	if err := p.open(false); err != nil {
		return err
	}
	compressor := method.AddTempfile(blk.Compressor, method.MaxMem(), p.opts.Env)
	err = method.Run(compressor, p, src, p.opts.Env)
	var fatal *arc.Error
	switch {
	case errors.As(err, &fatal), errors.Is(err, ErrAborted):
		return err
	case errors.Is(err, method.ErrUnsupportedMethod):
		return arc.Errorf("ERROR: unsupported compression method %s", blk.Compressor)
	case err != nil:
		return arc.Errorf("ERROR: archive data corrupted (decompression fails)")
	}
	stoppedAt := p.cur
	if err := p.close(); err != nil {
		return err
	}
	// The stream ended before the last file that had to be extracted. When that happens inside an extracted
	// file, its CRC check above fails as in C; when inside a skipped one, C silently missed the rest.
	if stoppedAt <= p.extractUntil {
		return arc.Errorf("ERROR: archive data corrupted (decompression fails)")
	}
	return nil
}

// Write receives the decompressed stream of the solid block (DecompressCallback "write").
func (p *Process) Write(buf []byte) (int, error) {
	if p.cur > p.extractUntil {
		return 0, method.ErrStop
	}
	n0 := len(buf)
	for len(buf) > 0 {
		n := int(min(p.bytesToWrite, uint64(len(buf))))
		if err := p.write(buf[:n]); err != nil {
			return 0, err
		}
		p.bytesToWrite -= uint64(n)
		if p.bytesToWrite == 0 {
			if err := p.close(); err != nil {
				return 0, err
			}
			p.cur++
			if p.cur > p.extractUntil {
				return n0, method.ErrStop
			}
			if err := p.open(false); err != nil {
				return 0, err
			}
		}
		buf = buf[n:]
	}
	return n0, nil
}

// UnsafePath reports whether a name from the archive could lead outside the output folder:
// an absolute path, a drive letter, or a ".." component (with either kind of slash).
func UnsafePath(name string) bool {
	if name == "" {
		return false
	}
	if name[0] == '/' || name[0] == '\\' {
		return true
	}
	if len(name) >= 2 && name[1] == ':' && ('a' <= name[0]|0x20 && name[0]|0x20 <= 'z') {
		return true
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

// open starts file p.cur (PROCESS::outfile_open).
func (p *Process) open(firstPass bool) error {
	f := &p.dir.Files[p.cur]
	p.crc.Reset()
	p.bytesToWrite = f.Size
	if !firstPass && f.Size == 0 {
		return nil // directories and empty files were done in the first pass
	}
	cmd := p.opts.Cmd
	p.included = Accept(f, p.opts.Filenames)
	p.xname = f.FullName()
	if cmd == 'e' {
		p.xname = f.Name
	}
	p.outName = p.base + p.xname
	if p.included && cmd != 't' && !(f.IsDir && cmd == 'e') {
		if UnsafePath(p.xname) {
			return arc.Errorf("ERROR: unsafe path in archive: %s", p.xname)
		}
		if f.IsDir {
			buildPathTo(p.outName)
			_ = os.Mkdir(p.outName, 0o777) // an existing folder is fine, as in C
		} else {
			if err := p.askOverwrite(f); err != nil {
				return err
			}
			if p.included {
				if err := p.create(); err != nil {
					return err
				}
			}
		}
	}
	if (firstPass || f.Size > 0) && !(f.IsDir && cmd != 'x') {
		op := "Skipping"
		if p.included {
			op = "Extracting"
			if cmd == 't' {
				op = "Testing"
			}
		}
		p.opts.UI.ProgressFile(f.IsDir, op, p.xname, p.bytesToWrite)
	}
	return nil
}

func (p *Process) askOverwrite(f *arc.File) error {
	if _, err := os.Lstat(p.outName); err != nil {
		return nil
	}
	switch {
	case p.opts.No:
		p.included = false
	case !p.opts.Yes:
		switch p.opts.UI.AskOverwrite(p.outName, f.Size, f.Time) {
		case 'n':
			p.included = false
		case 'a':
			p.opts.Yes = true
		case 's':
			p.opts.No = true
			p.included = false
		case 'q':
			return ErrAborted
		}
	}
	return nil
}

// create opens the output file the way MYFILE::open(WRITE_MODE) does: parent folders first, mode 0600.
func (p *Process) create() error {
	buildPathTo(p.outName)
	out, err := os.OpenFile(p.outName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return arc.Errorf("ERROR: can't open file %s", p.outName)
	}
	p.out = out
	p.forget = cleanup.Register(p.outName)
	return nil
}

func (p *Process) write(buf []byte) error {
	p.crc.Write(buf)
	if p.out != nil && len(buf) > 0 {
		if _, err := p.out.Write(buf); err != nil {
			return arc.Errorf("ERROR: file write operation failed")
		}
	}
	return nil
}

// close finishes file p.cur: checks its CRC, closes it and sets its time (PROCESS::outfile_close).
func (p *Process) close() error {
	if p.included {
		f := &p.dir.Files[p.cur]
		if p.crc.Sum32() != f.CRC {
			return arc.Errorf("ERROR: file %s failed CRC check", p.outName)
		}
		if p.out != nil {
			err := p.out.Close()
			p.out = nil
			p.forget()
			if err != nil {
				return arc.Errorf("ERROR: can't close file %s", p.outName)
			}
			// Only files written here get the archived time (C also touched same-named files in 't' and 'e').
			_ = os.Chtimes(p.outName, time.Time{}, time.Unix(int64(f.Time), 0))
		}
	}
	p.included = false
	return nil
}

// buildPathTo creates the folders leading to name (BuildPathTo), ignoring failures as C does:
// opening the file reports them.
func buildPathTo(name string) {
	i := strings.LastIndexAny(name, "/\\")
	if i <= 0 {
		return
	}
	_ = os.MkdirAll(filepath.FromSlash(name[:i]), 0o777)
}
