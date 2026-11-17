//go:build !unix

package filestage

import "os"

// WaitStatus returns the exit status in the layout of a Unix wait status (exit code in the second byte).
func WaitStatus(ps *os.ProcessState) int { return ps.ExitCode() << 8 }
