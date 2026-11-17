// Package filestage supports chain stages decoded by another program file to file: the stage's input is
// spilled into a temp folder, the program writes its output there, and the output is streamed on.
package filestage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/n0madic/go-unarc/internal/cleanup"
)

// Dir is a temp folder, removed on interrupt too.
type Dir struct {
	Path   string
	forget func()
}

// NewDir creates a temp folder in parent (os.MkdirTemp pattern).
func NewDir(parent, pattern string) (*Dir, error) {
	p, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return nil, fmt.Errorf("temp folder: %w", err)
	}
	return &Dir{Path: p, forget: cleanup.Register(p)}, nil
}

// Remove deletes the folder and everything in it.
func (d *Dir) Remove() {
	_ = os.RemoveAll(d.Path) // best effort, like the C destructors
	d.forget()
}

// Keep leaves the folder on disk, also on interrupt.
func (d *Dir) Keep() { d.forget() }

// Spill writes prefix and then everything from src into a new file at path, reading chunk bytes at a time,
// and returns how many bytes it wrote. With lazy set the file is created only once there is a byte to write.
func Spill(path string, prefix []byte, src io.Reader, chunk int, lazy bool) (int64, error) {
	var f *os.File
	open := func() error {
		var err error
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		return err
	}
	if !lazy || len(prefix) > 0 {
		if err := open(); err != nil {
			return 0, err
		}
	}
	n := int64(0)
	write := func(p []byte) error {
		if f == nil {
			if err := open(); err != nil {
				return err
			}
		}
		m, err := f.Write(p)
		n += int64(m)
		return err
	}
	err := func() error {
		if len(prefix) > 0 {
			if err := write(prefix); err != nil {
				return err
			}
		}
		buf := make([]byte, chunk)
		for {
			m, err := io.ReadFull(src, buf)
			if m > 0 {
				if err := write(buf[:m]); err != nil {
					return err
				}
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}()
	if f != nil {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	return n, err
}

// Stream writes the file at path to dst, chunk bytes at a time. A dst error (such as method.ErrStop) is
// returned unchanged.
func Stream(dst io.Writer, path string, chunk int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, chunk)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// FindTool locates a helper program: the path set explicitly, else name beside unarc (in exeDir), else
// name in fallbackDir, else the bare name for a PATH search.
func FindTool(set, name, exeDir, fallbackDir string) string {
	if set != "" {
		return set
	}
	if exeDir != "" {
		if p := filepath.Join(exeDir, name); exists(p) {
			return p
		}
	}
	if fallbackDir != "" {
		return fallbackDir + "/" + name // as C prints it
	}
	return name
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
