// Package cli implements the unarc command line: argument parsing, console output and exit codes,
// byte for byte as the C unarc prints them.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/n0madic/go-unarc/internal/arc"
	"github.com/n0madic/go-unarc/internal/cleanup"
	"github.com/n0madic/go-unarc/internal/extract"
	"github.com/n0madic/go-unarc/internal/method"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitAborted = 1 // 'q' on the overwrite question
	ExitError   = 2 // FREEARC_EXIT_ERROR
)

// LoadExternal registers the compressors of arc.ini; set by the external codec package.
var LoadExternal func(env *method.Env)

// Run executes unarc with args (without the program name) and returns the exit code.
// env carries the settings taken from the environment; the command line overrides them.
func Run(args []string, env *method.Env, stdin io.Reader, stdout io.Writer) int {
	out := newOutput(stdout)
	defer out.Flush()
	env.Stdout = out
	env.Stderr = stderrWriter{out: out, w: env.Stderr}
	ui := &console{out: out, in: bufio.NewReader(stdin)}

	fmt.Fprint(out, Header)
	cmd, ok := ParseArgs(args)
	if !ok {
		fmt.Fprint(out, helpText+"\n")
		return ExitError
	}
	if cmd.Password != nil {
		env.Password = *cmd.Password
	}
	if cmd.CLSDir != nil {
		env.CLSDir, env.CLSDirSet = *cmd.CLSDir, true
	}
	env.TempDir = cmd.WorkDir
	if LoadExternal != nil {
		LoadExternal(env)
	}

	err := process(&cmd, env, ui)
	var fatal *arc.Error
	switch {
	case err == nil:
		fmt.Fprint(out, "\n")
		return ExitOK
	case errors.Is(err, extract.ErrAborted):
		cleanup.RemoveAll()
		return ExitAborted
	case errors.As(err, &fatal):
		cleanup.RemoveAll()
		fmt.Fprintf(out, "\n%s", fatal.Msg)
		return ExitError
	default:
		cleanup.RemoveAll()
		fmt.Fprintf(out, "\nERROR: %v", err)
		return ExitError
	}
}

// archiveName resolves the archive name: without --noarcext, a missing name without an extension
// gets ".arc" added when that file exists.
func archiveName(cmd *Command) string {
	name := cmd.Arcname
	if cmd.NoArcExt {
		return name
	}
	if _, err := os.Stat(name); err == nil {
		return name
	}
	if strings.Contains(dropDirname(name), ".") {
		return name
	}
	if _, err := os.Stat(name + ".arc"); err == nil {
		return name + ".arc"
	}
	return name
}

// process runs the command over the archive (PROCESS::PROCESS).
func process(cmd *Command, env *method.Env, ui *console) error {
	a, err := arc.Open(archiveName(cmd), env)
	if err != nil {
		return err
	}
	defer a.Close()
	ui.allowProcessing(cmd.Cmd, a.Name, a.Comment)

	var p *extract.Process
	if cmd.List() {
		ui.listHeader(cmd.Cmd)
	} else {
		p = extract.New(extract.Options{Cmd: cmd.Cmd, OutDir: cmd.OutDir, Filenames: cmd.Filenames,
			Yes: cmd.Yes, No: cmd.No, UI: ui, Env: env})
	}
	for _, blk := range a.Blocks {
		if blk.Type != arc.DirBlock {
			continue
		}
		d, err := a.ReadDir(blk)
		if err != nil {
			return err
		}
		if cmd.List() {
			ui.listFiles(d, cmd.Cmd, cmd.Filenames)
			continue
		}
		for b := range d.Blocks {
			if err := p.ExtractBlock(a, d, b); err != nil {
				p.Abort()
				return err
			}
		}
	}
	if cmd.List() {
		ui.listFooter(cmd.Cmd)
	}
	return nil
}
