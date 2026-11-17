// Command mkfixtures writes the test archives in testdata/archives and the reference output of the C unarc
// for them in testdata/golden.
//
// The compressed streams come from fagen (scripts/fagen: FreeArc's own encoders), the archive container
// from internal/arctest. Every archive marked as valid is checked with the C unarc ("t" must succeed).
//
//	go run ./scripts/mkfixtures [-fagen .build/fagen] [-unarc $C_UNARC] [-src $FREEARC_SRC] [-only name,...]
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/n0madic/go-unarc/internal/arctest"
)

// Password of the encrypted fixtures.
const Password = "test-password"

// fixedTime is the modification time of the files in the fixtures.
const fixedTime = 1300000000

var (
	fagenPath = flag.String("fagen", ".build/fagen", "fagen binary (scripts/fagen/build.sh)")
	unarcPath = flag.String("unarc", os.Getenv("C_UNARC"), "C unarc used as the oracle (default $C_UNARC)")
	srcDir    = flag.String("src", os.Getenv("FREEARC_SRC"), "folder of C sources used as text input (default $FREEARC_SRC)")
	outDir    = flag.String("out", "testdata", "output folder")
	only      = flag.String("only", "", "comma-separated fixture names to (re)build")
)

// fixture is one test archive.
type fixture struct {
	name  string
	build func() arctest.Archive
	// err marks archives the C unarc must reject (its "t" exits with 2).
	err bool
	// noOracle marks archives the C unarc must not be run on (it would write outside the folder, or it is
	// known to be wrong on them); their expected output is written by hand in the Go tests.
	noOracle bool
	password string // UNARC_PASSWORD for the C unarc, "" for none
	// extra holds additional command lines to record, e.g. {"l", "name"}: command, then file names.
	extra [][]string
}

func main() {
	log.SetFlags(0)
	flag.Parse()
	if *unarcPath == "" || *srcDir == "" {
		log.Fatal("mkfixtures: set -unarc and -src (or C_UNARC and FREEARC_SRC): the C unarc and its sources")
	}
	in := newInputs()
	fixtures := fixtureList(in)
	want := map[string]bool{}
	for _, n := range strings.Split(*only, ",") {
		if n != "" {
			want[n] = true
		}
	}
	archives := filepath.Join(*outDir, "archives")
	golden := filepath.Join(*outDir, "golden")
	for _, d := range []string{archives, golden} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatal(err)
		}
	}
	var total int
	for _, f := range fixtures {
		if len(want) > 0 && !want[f.name] {
			continue
		}
		data := arctest.Build(f.build())
		path := filepath.Join(archives, f.name+".arc")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			log.Fatal(err)
		}
		total += len(data)
		if f.noOracle {
			log.Printf("%-28s %8d bytes (no oracle)", f.name, len(data))
			continue
		}
		cmds := append([][]string{{"l"}, {"v"}, {"t"}}, f.extra...)
		for i, c := range cmds {
			args := append([]string{c[0], filepath.ToSlash(filepath.Join("archives", f.name+".arc"))}, c[1:]...)
			out, errOut, code := runC(args, f.password)
			if i == 2 && (code != 0) != f.err {
				log.Fatalf("%s: C unarc t exited with %d:\n%s\n%s", f.name, code, out, errOut)
			}
			if err := os.WriteFile(filepath.Join(golden, fmt.Sprintf("%s.%d", f.name, i)),
				[]byte(FormatGolden(args, f.password, out, errOut, code)), 0o644); err != nil {
				log.Fatal(err)
			}
		}
		log.Printf("%-28s %8d bytes", f.name, len(data))
	}
	log.Printf("total %d bytes", total)
}

// FormatGolden renders a reference run: the command line, then stdout, stderr and the exit code.
// The tests (internal/cli) parse the same layout.
func FormatGolden(args []string, password, stdout, stderr string, code int) string {
	env := ""
	if password != "" {
		env = "UNARC_PASSWORD=" + password + " "
	}
	return fmt.Sprintf("$ %sunarc %s\n%s\n--- stderr\n%s\n--- exit %d\n", env, strings.Join(args, " "), stdout, stderr, code)
}

// runC runs the C unarc in a scratch folder (so that x/e cannot touch the source tree).
func runC(args []string, password string) (stdout, stderr string, code int) {
	unarc, err := filepath.Abs(*unarcPath)
	if err != nil {
		log.Fatal(err)
	}
	args = slices.Clone(args)
	for i, a := range args {
		if strings.HasSuffix(a, ".arc") {
			if args[i], err = filepath.Abs(filepath.Join(*outDir, a)); err != nil {
				log.Fatal(err)
			}
		}
	}
	dir, err := os.MkdirTemp("", "mkfixtures-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	// The C unarc loops forever on an overwrite question it cannot read; never let it ask one.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, unarc, args...)
	cmd.Dir = dir
	cmd.Env = []string{"TZ=UTC", "PATH=" + os.Getenv("PATH")}
	if password != "" {
		cmd.Env = append(cmd.Env, "UNARC_PASSWORD="+password)
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	if ctx.Err() != nil {
		log.Fatalf("C unarc %v timed out:\n%s", args, o.String())
	}
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		log.Fatal(err)
	}
	// The archive path is absolute here but relative in the tests; only its base name is printed.
	return o.String(), e.String(), code
}

// fagen runs "fagen c <method>" on data.
func fagen(method string, data []byte) []byte {
	cmd := exec.Command(*fagenPath, "c", method)
	cmd.Stdin = bytes.NewReader(data)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		log.Fatalf("fagen c %s: %v\n%s", method, err, e.String())
	}
	return o.Bytes()
}

// cipher returns, for an encryption algorithm, the method that encrypts with a key derived from Password
// and the method string FreeArc 0.67 stores for it.
func cipher(alg string) (compress, stored string) {
	out, err := exec.Command(*fagenPath, "crypt", alg, Password).Output()
	if err != nil {
		log.Fatalf("fagen crypt %s: %v", alg, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		log.Fatalf("fagen crypt %s: unexpected output %q", alg, out)
	}
	return lines[0], lines[1]
}

// packed builds a solid block compressed with method.
func packed(method string, files ...arctest.File) arctest.Solid {
	return arctest.Solid{Compressor: method, Packed: fagen(method, arctest.Concat(files)), Files: files}
}

// encrypted builds a solid block compressed with inner and encrypted with alg, stored as 0.67 stores it.
func encrypted(inner, alg string, files ...arctest.File) arctest.Solid {
	compress, stored := cipher(alg)
	return arctest.Solid{Compressor: inner + "+" + stored,
		Packed: fagen(inner+"+"+compress, arctest.Concat(files)), Files: files}
}

func file(path string, data []byte) arctest.File {
	return arctest.File{Path: path, Data: data, Time: fixedTime}
}

func dir(path string) arctest.File {
	return arctest.File{Path: path, IsDir: true, Time: fixedTime}
}

// inputs are the deterministic data sets compressed into the fixtures.
type inputs struct {
	text []byte // C++ sources of unarc
}

func newInputs() *inputs {
	var names []string
	err := filepath.WalkDir(*srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && (strings.HasSuffix(p, ".cpp") || strings.HasSuffix(p, ".h")) {
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	slices.Sort(names)
	var b bytes.Buffer
	for _, n := range names {
		data, err := os.ReadFile(n)
		if err != nil {
			log.Fatal(err)
		}
		b.Write(data)
		if b.Len() > 4<<20 {
			break
		}
	}
	return &inputs{text: b.Bytes()}
}

// Text returns n bytes of source text starting at off.
func (in *inputs) Text(off, n int) []byte { return in.text[off : off+n] }

// Random returns n pseudo-random bytes.
func Random(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 1))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// Repetitive returns n bytes of random runs and copies of earlier data: binary data an LZ coder can compress,
// with matches near and far (for rep and small dictionaries).
func Repetitive(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 2))
	b := make([]byte, 0, n)
	for len(b) < n {
		if len(b) < 1024 || r.IntN(4) == 0 {
			for range 1 + r.IntN(64) {
				b = append(b, byte(r.IntN(32)))
			}
			continue
		}
		dist := 1 + r.IntN(len(b))
		if r.IntN(2) == 0 {
			dist = 1 + r.IntN(min(len(b), 256))
		}
		start := len(b) - dist
		for i := range 8 + r.IntN(400) {
			b = append(b, b[start+i])
		}
	}
	return b[:n]
}

// X86 returns n bytes looking like x86 code: random instruction bytes with frequent CALL/JMP rel32
// whose targets cluster (what the exe filter turns into absolute addresses).
func X86(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 3))
	targets := make([]int32, 64)
	for i := range targets {
		targets[i] = int32(r.IntN(n))
	}
	b := make([]byte, 0, n+8)
	for len(b) < n {
		switch r.IntN(6) {
		case 0, 1:
			op := byte(0xE8)
			if r.IntN(4) == 0 {
				op = 0xE9
			}
			b = append(b, op)
			rel := targets[r.IntN(len(targets))] - int32(len(b)+4)
			b = binary.LittleEndian.AppendUint32(b, uint32(rel))
		default:
			for range 1 + r.IntN(6) {
				b = append(b, []byte{0x8B, 0x45, 0x89, 0x5D, 0x83, 0xC4, 0x50, 0x53, 0x56, 0x57, 0x0F, 0x85}[r.IntN(12)])
			}
		}
	}
	return b[:n]
}

// Wav returns a RIFF WAVE file of n frames with the given sample size and channel count.
func Wav(bits, channels, frames int) []byte {
	r := rand.New(rand.NewPCG(uint64(bits*10+channels), 4))
	bps := bits / 8
	var pcm bytes.Buffer
	for i := range frames {
		for c := range channels {
			v := 0.5*math.Sin(float64(i)*0.031*float64(c+1)) + 0.2*math.Sin(float64(i)*0.0017) + 0.01*(r.Float64()-0.5)
			s := int64(v * float64(int64(1)<<(bits-1)-1))
			if bits == 8 {
				s += 128 // 8-bit WAV samples are unsigned
			}
			for k := range bps {
				pcm.WriteByte(byte(s >> (8 * k)))
			}
		}
	}
	var h bytes.Buffer
	h.WriteString("RIFF")
	binary.Write(&h, binary.LittleEndian, uint32(36+pcm.Len()))
	h.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(channels), uint32(44100),
		uint32(44100 * channels * bps), uint16(channels * bps), uint16(bits)} {
		binary.Write(&h, binary.LittleEndian, v)
	}
	h.WriteString("data")
	binary.Write(&h, binary.LittleEndian, uint32(pcm.Len()))
	h.Write(pcm.Bytes())
	return h.Bytes()
}
