//go:build unix

package filestage

import (
	"os"
	"syscall"
)

// WaitStatus returns the raw wait status of a finished process, as C waitpid reports it.
func WaitStatus(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok {
		return int(ws)
	}
	return ps.ExitCode() << 8
}
