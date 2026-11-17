package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n0madic/go-unarc/internal/arc"
	"github.com/n0madic/go-unarc/internal/extract"
)

// console is the text UI of unarc (CUI.h).
type console struct {
	out *output
	in  *bufio.Reader

	totalFiles, totalBytes, totalPacked uint64
}

// formatTime formats a file time as FormatDateTime does (local time).
func formatTime(t uint32) string {
	return time.Unix(int64(t), 0).Format("2006-01-02 15:04:05")
}

// float0 prints an integer the way printf("%.0lf", double(x)) does.
func float0(x uint64) string { return strconv.FormatFloat(float64(x), 'f', 0, 64) }

// pad right-aligns s in a field of width w.
func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return strings.Repeat(" ", w-len(s)) + s
}

// dropDirname returns the part of name after the last ':', '/' or '\'.
func dropDirname(name string) string {
	return name[strings.LastIndexAny(name, ":/\\")+1:]
}

func (c *console) allowProcessing(cmd byte, arcname string, comment []byte) {
	op := "Extracting"
	switch cmd {
	case 'l', 'v':
		op = "Listing"
	case 't':
		op = "Testing"
	}
	fmt.Fprintf(c.out, ". %s archive: %s\n", op, dropDirname(arcname))
	if len(comment) > 0 {
		// printf("%*.*s\n"): stops at a NUL byte, right-aligned to the comment's length.
		s := comment
		if i := strings.IndexByte(string(s), 0); i >= 0 {
			s = s[:i]
		}
		fmt.Fprintf(c.out, "%s\n", pad(string(s), len(comment)))
	}
}

// ProgressFile implements extract.UI.
func (c *console) ProgressFile(isDir bool, operation, name string, size uint64) {
	if isDir {
		fmt.Fprintf(c.out, "%s %s/\n", operation, name)
	} else {
		fmt.Fprintf(c.out, "%s %s (%d bytes)\n", operation, name, size)
	}
}

// AskOverwrite implements extract.UI. Unlike C, whose fgets kept the newline and so rejected every
// answer, it accepts one letter followed by Enter. End of input counts as 'n'.
func (c *console) AskOverwrite(name string, size uint64, mtime uint32) byte {
	for {
		fmt.Fprintf(c.out, "Overwrite %s (y/n/a/s/q) ? ", name)
		c.out.Flush()
		line, err := c.in.ReadString('\n')
		answer := strings.ToLower(strings.TrimRight(line, "\r\n"))
		if err != nil && line == "" {
			fmt.Fprint(c.out, "\n")
			return 'n'
		}
		if len(answer) == 1 && strings.Contains("ynasq", answer) {
			if answer == "q" {
				fmt.Fprint(c.out, "Extraction aborted\n")
			}
			return answer[0]
		}
		fmt.Fprint(c.out, "Valid answers: Y - yes, N - no, A - overwrite all, S - skip all, Q - quit\n")
	}
}

var _ extract.UI = (*console)(nil)

func (c *console) listHeader(cmd byte) {
	if cmd == 'l' {
		fmt.Fprint(c.out, "Date/time                  Size Filename\n"+strings.Repeat("-", 40)+"\n")
	} else {
		fmt.Fprint(c.out, "Date/time              Attr            Size          Packed      CRC Filename\n"+
			strings.Repeat("-", 77)+"\n")
	}
	c.totalFiles, c.totalBytes, c.totalPacked = 0, 0, 0
}

func (c *console) listFooter(cmd byte) {
	if cmd == 'l' {
		fmt.Fprint(c.out, strings.Repeat("-", 40)+"\n")
	} else {
		fmt.Fprint(c.out, strings.Repeat("-", 77)+"\n")
	}
	fmt.Fprintf(c.out, "%s files, %s bytes, %s compressed", float0(c.totalFiles), float0(c.totalBytes), float0(c.totalPacked))
}

// encryptedMarkers mark a solid block whose compressor ends with encryption.
var encryptedMarkers = []string{"+aes-", "+serpent-", "+blowfish-", "+twofish-"}

func (c *console) listFiles(d *arc.Dir, cmd byte, names []string) {
	b := 0
	encrypted := false
	var packed uint64
	for i := range d.Files {
		if i >= d.Blocks[b].End {
			b++
		}
		if i == d.Blocks[b].Start {
			packed = d.Blocks[b].CompSize // the whole block's size goes to its first listed file
			encrypted = false
			for _, m := range encryptedMarkers {
				if strings.Contains(d.Blocks[b].Compressor, m) {
					encrypted = true
				}
			}
		}
		f := &d.Files[i]
		if !extract.Accept(f, names) {
			continue
		}
		ts := formatTime(f.Time)
		if cmd == 'l' {
			if f.IsDir {
				fmt.Fprintf(c.out, "%s       -dir-", ts)
			} else {
				fmt.Fprintf(c.out, "%s %s", ts, pad(float0(f.Size), 11))
			}
		} else {
			attr := "......."
			if f.IsDir {
				attr = ".D....."
			}
			fmt.Fprintf(c.out, "%s %s %s %s %08x", ts, attr, pad(float0(f.Size), 15), pad(float0(packed), 15), f.CRC)
		}
		mark := byte(' ')
		if encrypted {
			mark = '*'
		}
		c.out.WriteByte(mark)
		fmt.Fprintf(c.out, "%s\n", f.FullName())
		c.totalFiles++
		c.totalBytes += f.Size
		c.totalPacked += packed
		packed = 0
	}
}

// output is the buffered stdout. Codec goroutines write to stderr (through stderrWriter) while the main
// goroutine prints progress, so both take the same lock.
type output struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func newOutput(w io.Writer) *output { return &output{w: bufio.NewWriterSize(w, 64*1024)} }

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.w.Write(p)
}

func (o *output) WriteByte(b byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.w.WriteByte(b)
}

func (o *output) Flush() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.w.Flush()
}

// stderrWriter flushes stdout before writing to stderr, so that the two streams keep their order on a terminal.
type stderrWriter struct {
	out *output
	w   io.Writer
}

func (s stderrWriter) Write(p []byte) (int, error) {
	s.out.mu.Lock()
	defer s.out.mu.Unlock()
	if err := s.out.w.Flush(); err != nil {
		return 0, err
	}
	return s.w.Write(p)
}
