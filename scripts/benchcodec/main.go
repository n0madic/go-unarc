//go:build unix

// Command benchcodec compares the decoding speed of the Go codecs with FreeArc's C library (fagen d) on the
// same compressed streams. It measures CPU time (user+sys), best of -n runs, which stays meaningful on a busy
// machine where wall time does not; wall time is printed too.
//
//	go run ./scripts/benchcodec [-fagen .build/fagen] [-in file] [-size 32] [-n 3] [method...]
//	go run ./scripts/benchcodec -archives a.arc,big.bin [-unarc $C_UNARC] [-gounarc .build/go-unarc]
//
// Cipher methods (aes, blowfish-128/cfb, ...) are encrypted with an explicit key from "fagen crypt", so the
// timing leaves out PBKDF2. Multi-threaded decoders (grzip) add up the CPU time of all their threads.
// The archive mode runs "t" with both programs; UNARC_PASSWORD and the other variables pass through.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	_ "github.com/n0madic/go-unarc/internal/codec/all"
	"github.com/n0madic/go-unarc/internal/method"
)

// defaultMethods cover every built-in codec that compresses generic data. PPMd gets 1 GB so that its model
// never fills: once it does, fagen's 64-bit model layout diverges from the 32-bit one both FreeArc and the
// Go port use.
var defaultMethods = []string{
	"lzma:8mb:normal", "lzma:1mb:normal:bt4:32", "delta", "rep", "lzp", "dict", "ppmd:o6:m1gb", "ppmd:o16:m1gb",
	"tor:3", "tor:7", "grzip", "grzip:m4", "aes", "aes/cfb", "blowfish", "twofish", "serpent",
}

var cipherPrefixes = []string{"aes", "blowfish", "twofish", "serpent"}

// cost is the time one run took.
type cost struct{ cpu, wall time.Duration }

func (c cost) better(o cost) cost {
	if o.cpu < c.cpu || c.cpu == 0 {
		return o
	}
	return c
}

func main() {
	log.SetFlags(0)
	fagen := flag.String("fagen", ".build/fagen", "fagen binary (scripts/fagen/build.sh)")
	in := flag.String("in", "", "input file (default: Go's sources and the go binary)")
	size := flag.Int("size", 32, "input size in MiB when -in is not given")
	n := flag.Int("n", 3, "runs per decoder; the fastest counts")
	archives := flag.String("archives", "", "comma-separated archives: compare \"t\" of both programs instead")
	cUnarc := flag.String("unarc", os.Getenv("C_UNARC"), "C unarc for -archives (default $C_UNARC)")
	goUnarc := flag.String("gounarc", ".build/go-unarc", "Go unarc for -archives (go build -o .build/go-unarc ./cmd/unarc)")
	flag.Parse()

	if *archives != "" {
		if *cUnarc == "" {
			log.Fatal("benchcodec: set -unarc or C_UNARC to the C unarc binary")
		}
		benchArchives(strings.Split(*archives, ","), *cUnarc, *goUnarc, *n)
		return
	}
	data, err := input(*in, *size<<20)
	if err != nil {
		log.Fatal(err)
	}
	methods := flag.Args()
	if len(methods) == 0 {
		methods = defaultMethods
	}
	fmt.Printf("input %d bytes, best of %d runs\n", len(data), *n)
	fmt.Printf("%-24s %10s %10s %10s %7s %10s %10s\n", "method", "packed", "C cpu", "Go cpu", "Go/C", "C wall", "Go wall")
	for _, m := range methods {
		if err := benchMethod(*fagen, m, data, *n); err != nil {
			fmt.Printf("%-24s %v\n", m, err)
		}
	}
}

// input returns the file, or size bytes of Go's own sources and the go binary, interleaved (text and code).
func input(file string, size int) ([]byte, error) {
	if file != "" {
		return os.ReadFile(file)
	}
	root := runtime.GOROOT()
	bin, err := os.ReadFile(filepath.Join(root, "bin", "go"))
	if err != nil {
		return nil, fmt.Errorf("reading the go binary: %w", err)
	}
	var b bytes.Buffer
	errFull := errors.New("full")
	err = filepath.WalkDir(filepath.Join(root, "src"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".go" {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		b.Write(data)
		// A slice of the binary every 4 MiB of text, so that both kinds run through the whole input.
		if b.Len()/(4<<20) != (b.Len()-len(data))/(4<<20) && len(bin) > 0 {
			k := min(len(bin), 1<<20)
			b.Write(bin[:k])
			bin = bin[k:]
		}
		if b.Len() >= size {
			return errFull
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFull) {
		return nil, fmt.Errorf("reading the Go sources: %w", err)
	}
	if b.Len() < size {
		return nil, fmt.Errorf("only %d bytes of input: give -in or a smaller -size", b.Len())
	}
	return b.Bytes()[:size], nil
}

func fagenOut(bin string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command(bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("fagen %s: %w: %s", args[0], err, strings.TrimSpace(errOut.String()))
	}
	return out.Bytes(), nil
}

// countWriter counts the bytes written to it.
type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

func benchMethod(fagen, m string, data []byte, runs int) error {
	for _, p := range cipherPrefixes {
		if strings.HasPrefix(m, p) {
			lines, err := fagenOut(fagen, nil, "crypt", m, "bench-password")
			if err != nil {
				return err
			}
			m = strings.Fields(string(lines))[0]
			break
		}
	}
	packed, err := fagenOut(fagen, data, "c", m)
	if err != nil {
		return err
	}
	var cBest, goBest cost
	for range runs {
		var out countWriter
		cmd := exec.Command(fagen, "d", m)
		cmd.Stdin, cmd.Stdout = bytes.NewReader(packed), &out
		start := time.Now()
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("fagen d: %w", err)
		}
		wall := time.Since(start)
		if out.n != int64(len(data)) {
			return fmt.Errorf("fagen d wrote %d bytes, want %d", out.n, len(data))
		}
		cBest = cBest.better(cost{cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime(), wall})
	}
	env := &method.Env{Stderr: os.Stderr, TempDir: os.TempDir()}
	for range runs {
		var out countWriter
		before, start := selfCPU(), time.Now()
		if err := method.Run(m, &out, bytes.NewReader(packed), env); err != nil {
			return fmt.Errorf("go: %w", err)
		}
		c := cost{selfCPU() - before, time.Since(start)}
		if out.n != int64(len(data)) {
			return fmt.Errorf("go wrote %d bytes, want %d", out.n, len(data))
		}
		goBest = goBest.better(c)
	}
	name := m
	if len(name) > 24 {
		name = name[:21] + "..."
	}
	fmt.Printf("%-24s %10d %10s %10s %7.2f %10s %10s\n", name, len(packed), round(cBest.cpu), round(goBest.cpu),
		float64(goBest.cpu)/float64(cBest.cpu), round(cBest.wall), round(goBest.wall))
	return nil
}

func selfCPU() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		log.Fatal(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func round(d time.Duration) time.Duration { return d.Round(time.Millisecond) }

func benchArchives(archives []string, cUnarc, goUnarc string, runs int) {
	fmt.Printf("%-30s %10s %10s %7s %10s %10s\n", "archive (t)", "C cpu", "Go cpu", "Go/C", "C wall", "Go wall")
	for _, a := range archives {
		var best [2]cost
		var failed error
		for i, bin := range []string{cUnarc, goUnarc} {
			for range runs {
				cmd := exec.Command(bin, "t", a)
				cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
				start := time.Now()
				if err := cmd.Run(); err != nil {
					failed = fmt.Errorf("%s t: %w", bin, err)
					break
				}
				best[i] = best[i].better(cost{cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime(), time.Since(start)})
			}
		}
		if failed != nil {
			fmt.Printf("%-30s %v\n", filepath.Base(a), failed)
			continue
		}
		fmt.Printf("%-30s %10s %10s %7.2f %10s %10s\n", filepath.Base(a), round(best[0].cpu), round(best[1].cpu),
			float64(best[1].cpu)/float64(best[0].cpu), round(best[0].wall), round(best[1].wall))
	}
}
