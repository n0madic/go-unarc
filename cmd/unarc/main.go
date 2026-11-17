// Command unarc lists, tests and extracts FreeArc archives.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/n0madic/go-unarc/internal/cleanup"
	"github.com/n0madic/go-unarc/internal/cli"
	"github.com/n0madic/go-unarc/internal/method"

	_ "github.com/n0madic/go-unarc/internal/codec/all"
)

func main() {
	// On Ctrl-C remove the half-written output file and the temp files of chain stages.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cleanup.RemoveAll()
		os.Exit(cli.ExitError)
	}()
	os.Exit(cli.Run(os.Args[1:], method.NewEnv(), os.Stdin, os.Stdout))
}
