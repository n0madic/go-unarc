// Package plugin runs chain stages that FreeArc setups decode through plugins rather than built-in methods
// (C_WineCLS.cpp):
//
//   - srep: the external srep binary, used by unarc when UNARC_SREP overrides its built-in decoder;
//   - any other method: <UNARC_CLS_DIR>/CLS-<name>.dll from the setup (name matched ignoring case), run by
//     clshost.exe under Wine; clshost.exe is UNARC_CLSHOST, else beside unarc, else in UNARC_CLS_DIR.
//
// A plugin stage spills its whole input into a temp folder under TMPDIR, runs the codec file to file and
// streams the result on. UNARC_CLS_KEEP keeps the spilled input for debugging. Plugin methods are looked up
// after every built-in one.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/n0madic/go-unarc/internal/filestage"
	"github.com/n0madic/go-unarc/internal/method"
)

func init() { method.RegisterFallback(Parse) }

const (
	ioChunk = 8 * method.MB
	// notStarted is the wait status of a child that could not exec its program (_exit(127)).
	notStarted = 127 << 8
)

type codec struct {
	native string // srep binary, or "" for a CLS DLL
	dll    string // absolute path of the CLS DLL
	params string // the method's parameters after its name, ':'-separated, as the codec expects them
	env    *method.Env
}

// Parse recognises srep, and any method with a CLS-<name>.dll in the CLS folder.
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	c := &codec{params: strings.Join(params[1:], ":"), env: env}
	if params[0] == "srep" {
		c.native = filestage.FindTool(env.Srep, "srep", env.ExeDir, "")
		return c, true
	}
	if env.CLSDir == "" {
		return nil, false
	}
	dll, ok := findDLL(env.CLSDir, params[0])
	if !ok {
		return nil, false
	}
	c.dll = dll
	return c, true
}

// findDLL finds dir/CLS-<name>.dll ignoring case, as Windows would.
func findDLL(dir, name string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	want := "CLS-" + name + ".dll"
	for _, e := range entries {
		if strings.EqualFold(e.Name(), want) {
			p, err := filepath.Abs(filepath.Join(dir, e.Name()))
			if err != nil {
				return "", false
			}
			return p, true
		}
	}
	return "", false
}

// DecompressionMem is 0: the work happens in another process.
func (*codec) DecompressionMem() uint64 { return 0 }

func (c *codec) stdout() io.Writer { return orDiscard(c.env.Stdout) }
func (c *codec) stderr() io.Writer { return orDiscard(c.env.Stderr) }

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// Decode spills src to <tmp>/packed, has the plugin write <tmp>/unpacked and streams that to dst.
func (c *codec) Decode(dst io.Writer, src io.Reader) error {
	dir, err := filestage.NewDir(os.TempDir(), "unarc-cls.")
	if err != nil {
		return err
	}
	in, out := filepath.Join(dir.Path, "packed"), filepath.Join(dir.Path, "unpacked")
	_, err = filestage.Spill(in, nil, src, ioChunk, false)
	if err == nil {
		err = c.run(in, out)
	}
	if c.env.CLSKeep {
		fmt.Fprintf(c.stderr(), "[cls] kept %s\n", in)
	}
	if err == nil {
		err = filestage.Stream(dst, out, ioChunk)
	}
	if c.env.CLSKeep {
		_ = os.Remove(out)
		dir.Keep()
	} else {
		dir.Remove()
	}
	return err
}

// run executes the codec from in to out and reports its failure as C does.
func (c *codec) run(in, out string) error {
	wine := c.env.Wine
	if wine == "" {
		wine = "wine"
	}
	ctx := c.env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var cmd *exec.Cmd
	prog := wine
	if c.native != "" {
		prog = c.native
		fmt.Fprintf(c.stderr(), "\n[cls] %s -d\n", c.native)
		cmd = exec.CommandContext(ctx, c.native, "-d", in, out) // srep decompression needs no parameters
	} else {
		host := filestage.FindTool(c.env.CLSHost, "clshost.exe", c.env.ExeDir, c.env.CLSDir)
		fmt.Fprintf(c.stderr(), "\n[cls] %s %s \"%s\" under %s\n", host, filepath.Base(c.dll), c.params, wine)
		cmd = exec.CommandContext(ctx, wine, host, winePath(c.dll), c.params, winePath(in), winePath(out))
		cmd.Env = os.Environ()
		if _, set := os.LookupEnv("WINEDEBUG"); !set {
			cmd.Env = append(cmd.Env, "WINEDEBUG=-all")
		}
	}
	cmd.Stdout, cmd.Stderr = c.stdout(), c.stderr()
	status := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			status = notStarted
		} else {
			status = filestage.WaitStatus(exitErr.ProcessState)
		}
	}
	if status == notStarted {
		fmt.Fprintf(c.stderr(), "[cls] could not start %s\n", prog)
	}
	if status != 0 {
		fmt.Fprintf(c.stderr(), "[cls] codec failed (status %d)\n", status)
		return fmt.Errorf("%w: plugin codec failed (status %d)", method.ErrBadData, status)
	}
	return nil
}

// winePath is how Wine sees a Unix path: through drive Z:.
func winePath(p string) string { return "Z:" + strings.ReplaceAll(p, "/", `\`) }
