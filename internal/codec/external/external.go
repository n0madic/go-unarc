// Package external implements FreeArc's external compressors (C_External.cpp): the ones declared in arc.ini
// beside unarc ([External compressor:name,...] sections) and pmm (PPMonstr).
//
// A stream starts with a byte: 1 means the data was compressed by the program, 0 that it was stored. The
// stage writes the rest into a temp folder (as packedfile), runs unpackcmd there and streams datafile on.
//
// Unlike C, which ran system("./"+cmd) in the temp folder (so the program was never found and the shell
// expanded $$ to its pid), the command runs without a shell: words split at spaces (double quotes group),
// $$arcdatafile$$-style names passed literally, the program taken from beside unarc or from PATH.
package external

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/n0madic/go-unarc/internal/cli"
	"github.com/n0madic/go-unarc/internal/filestage"
	"github.com/n0madic/go-unarc/internal/method"
)

func init() {
	method.Register(ParsePMM)
	cli.LoadExternal = Load
}

const (
	bufferSize = 256 * method.KB // LARGE_BUFFER_SIZE
	// legacyOutput is where FreeArc versions that wrote no header byte expected the program's output.
	legacyOutput = "data7777"
)

// compressor is one external compressor: a version from an arc.ini section, or pmm.
type compressor struct {
	name                 string
	dmem                 uint32
	datafile, packedfile string
	unpackcmd            string
	defaultopt           string
}

// codec is a compressor with the parameters of one method string.
type codec struct {
	*compressor
	options []string
	env     *method.Env
}

// parser recognises the methods named like the compressor; any parameters become its options.
func (t *compressor) parser() method.Parser {
	return func(params []string, env *method.Env) (method.Codec, bool) {
		if params[0] != t.name {
			return nil, false
		}
		return &codec{compressor: t, options: params[1:], env: env}, true
	}
}

// DecompressionMem is dmem from arc.ini (or pmm's memory).
func (c *codec) DecompressionMem() uint64 { return uint64(c.dmem) }

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrUnsupportedMethod, fmt.Sprintf(format, args...))
}

// Decode mirrors external_program for decompression. A program that fails or leaves no output makes the
// method unsupported, as in C.
func (c *codec) Decode(dst io.Writer, src io.Reader) error {
	cmd := prepareCmd(c.unpackcmd, c.options, c.defaultopt)
	var header [1]byte
	if _, err := io.ReadFull(src, header[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: no header byte", method.ErrBadData)
		}
		return err
	}
	dir, err := filestage.NewDir(c.env.Temp(), "freearc")
	if err != nil {
		return err
	}
	defer dir.Remove()
	in, out := filepath.Join(dir.Path, c.packedfile), filepath.Join(dir.Path, c.datafile)
	run := header[0] != 0
	var prefix []byte
	if header[0] > 1 {
		// No header byte: the data starts right away (FreeArc before 0.40).
		prefix, out = header[:], filepath.Join(dir.Path, legacyOutput)
	}
	n, err := filestage.Spill(in, prefix, src, bufferSize, true)
	if err != nil {
		return err
	}
	if cmd != "" && run {
		stdout := orDiscard(c.env.Stdout)
		fmt.Fprintf(stdout, "\nUnpacking %s bytes with %s\n", show3(uint64(n)), cmd)
		code := c.runCommand(cmd, dir.Path)
		fmt.Fprintf(stdout, "\nErrorlevel=%d\n", code)
		if code != 0 {
			return unsupported("%s exited with %d", cmd, code)
		}
	} else {
		_ = os.Rename(in, out) // a missing input shows up below as a missing output, as in C
	}
	if _, err := os.Stat(out); err != nil {
		return unsupported("external %s produced no %s", c.name, filepath.Base(out))
	}
	return filestage.Stream(dst, out, bufferSize)
}

// runCommand runs cmdline in dir and returns its exit code; 127 if it could not start.
func (c *codec) runCommand(cmdline, dir string) int {
	args := splitCommand(cmdline)
	if len(args) == 0 {
		return 127
	}
	ctx := c.env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, c.program(args[0]), args[1:]...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = orDiscard(c.env.Stdout), orDiscard(c.env.Stderr)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return 127
	}
	return 0
}

// program resolves the command's program: absolute paths as they are, other paths with a slash and
// programs present there relative to the unarc folder, the rest through PATH.
func (c *codec) program(name string) string {
	if filepath.IsAbs(name) || c.env.ExeDir == "" {
		return name
	}
	p := filepath.Join(c.env.ExeDir, name)
	if strings.ContainsRune(name, '/') {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return name
}

// splitCommand splits a command line at spaces; double quotes group words and are removed.
func splitCommand(s string) []string {
	var args []string
	var word strings.Builder
	inWord, quoted := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"':
			quoted = !quoted
			inWord = true
		case isSpace(ch) && !quoted:
			if inWord {
				args = append(args, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteByte(ch)
			inWord = true
		}
	}
	if inWord {
		args = append(args, word.String())
	}
	return args
}

// prepareCmd mirrors prepare_cmd: it replaces "{options}" with "-opt1 -opt2 ", or the first "{...option...}"
// template with the text around "option" repeated for each option; without options, with the default ones.
func prepareCmd(cmd string, options []string, defaultopt string) string {
	const optionsStr, optionStr = "{options}", "option"
	before, after := "-", " "
	start, n := strings.Index(cmd, optionsStr), len(optionsStr)
	if start < 0 {
		for p1 := 0; p1 < len(cmd); p1++ {
			if cmd[p1] != '{' {
				continue
			}
			p2, p12 := p1, -1
			for ; p2 < len(cmd) && cmd[p2] != '}'; p2++ {
				if strings.HasPrefix(cmd[p2:], optionStr) {
					p12 = p2
				}
			}
			if p2 < len(cmd) && p12 >= 0 {
				before, after = cmd[p1+1:p12], cmd[p12+len(optionStr):p2]
				start, n = p1, p2-p1+1
				break
			}
		}
	}
	if start < 0 {
		return cmd
	}
	repl := defaultopt
	if len(options) > 0 {
		var b strings.Builder
		for _, o := range options {
			b.WriteString(before + o + after)
		}
		repl = b.String()
	}
	return cmd[:start] + repl + cmd[start+n:]
}

// show3 formats n with dots between groups of three digits: "1.234.567".
func show3(n uint64) string {
	s := strconv.FormatUint(n, 10)
	var b strings.Builder
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// isSpace is C isspace in the C locale.
func isSpace(ch byte) bool { return ch == ' ' || ch >= '\t' && ch <= '\r' }

// ParsePMM mirrors parse_PPMONSTR: pmm[:<order>][:<mem>][:o<order>][:m<mem>][:r<method>]. Decompression runs
// "ppmonstr d $$arcdatafile$$.pmm"; the parameters set only the memory needed.
func ParsePMM(params []string, env *method.Env) (method.Codec, bool) {
	if params[0] != "pmm" {
		return nil, false
	}
	mem := uint32(192 * method.MB)
	for _, p := range params[1:] {
		if strings.HasPrefix(p, "mem") {
			p = p[2:] // "mem..." is "m..."
		}
		ok := true
		switch {
		case p == "r":
			continue
		case len(p) > 1 && p[0] == 'm':
			mem, ok = method.ParseMem(p[1:])
		case len(p) > 1 && (p[0] == 'o' || p[0] == 'r'):
			_, ok = method.ParseInt(p[1:])
		default:
			if _, isInt := method.ParseInt(p); !isInt {
				mem, ok = method.ParseMem(p)
			}
		}
		if !ok {
			return nil, false
		}
	}
	t := &compressor{
		name:       "pmm",
		dmem:       mem,
		datafile:   "$$arcdatafile$$.tmp",
		packedfile: "$$arcdatafile$$.pmm",
		unpackcmd:  "ppmonstr d $$arcdatafile$$.pmm",
	}
	return &codec{compressor: t, options: params[1:], env: env}, true
}
