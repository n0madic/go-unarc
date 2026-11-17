// Command genuine prepares and finishes the archives in testdata/genuine, which the genuine FreeArc 0.67
// creates (see build.sh, which runs the whole pipeline):
//
//	go run ./scripts/genuine inputs -src $FREEARC_SRC -out DIR/in
//	(the guest runs archives.sh: DIR/in -> DIR/out/archives, DIR/out/commands.tsv)
//	go run ./scripts/genuine golden -from DIR/out -unarc $C_UNARC -dest testdata/genuine
//
// "inputs" writes the files to archive: the unarc sources at git HEAD (text), synthetic binary data and
// synthetic WAV files, all with fixed modification times. "golden" copies the archives into testdata,
// records the C unarc's l, v and t runs for each in the layout internal/fixturetest parses, and writes
// the README listing every FreeArc command line.
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Password of the encrypted archives (archives.sh uses the same).
const Password = "test-password"

// baseTime is the modification time of the inputs: FreeArc 0.67's release day. File i (in path order)
// gets baseTime + i*stepTime, so that listings show differing times.
var (
	baseTime = time.Date(2014, 3, 15, 20, 0, 0, 0, time.UTC)
	stepTime = time.Hour + 7*time.Minute + 3*time.Second
)

// unarcTimeout bounds one C unarc run; it must never wait for input.
const unarcTimeout = 2 * time.Minute

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		log.Fatal("usage: genuine inputs|golden [flags]")
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "inputs":
		err = runInputs(args)
	case "golden":
		err = runGolden(args)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Fatal(err)
	}
}

// --- inputs

func runInputs(args []string) error {
	fl := flag.NewFlagSet("inputs", flag.ExitOnError)
	src := fl.String("src", os.Getenv("FREEARC_SRC"), "git checkout of the C unarc; its HEAD sources are the text inputs (default $FREEARC_SRC)")
	out := fl.String("out", "", "output folder (must not exist)")
	fl.Parse(args)
	if *out == "" {
		return errors.New("inputs: -out is required")
	}
	if *src == "" {
		return errors.New("inputs: set -src or FREEARC_SRC to a git checkout of the C unarc")
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("inputs: %s already exists", *out)
	}
	sources, err := gitSources(*src)
	if err != nil {
		return err
	}
	files := inputFiles(sources)
	paths := slices.Sorted(func(yield func(string) bool) {
		for p := range files {
			if !yield(p) {
				return
			}
		}
	})
	var total int
	for _, p := range paths {
		full := filepath.Join(*out, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, files[p], 0o644); err != nil {
			return err
		}
		total += len(files[p])
	}
	// Times last, folders after their contents.
	i := 0
	err = filepath.WalkDir(*out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		t := baseTime.Add(time.Duration(i) * stepTime)
		i++
		return os.Chtimes(p, t, t)
	})
	if err != nil {
		return err
	}
	var dirs []string
	err = filepath.WalkDir(*out, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return err
	})
	if err != nil {
		return err
	}
	for _, d := range slices.Backward(dirs) {
		if err := os.Chtimes(d, baseTime, baseTime); err != nil {
			return err
		}
	}
	log.Printf("inputs: %d files, %d bytes in %s", i, total, *out)
	return nil
}

// source is one text file of the unarc sources.
type source struct {
	path string
	data []byte
}

// gitSources returns the C/C++ sources of the git HEAD of dir, sorted by path. HEAD (not the work tree)
// keeps the inputs the same when the checkout has local changes.
func gitSources(dir string) ([]source, error) {
	cmd := exec.Command("git", "-C", dir, "archive", "--format=tar", "HEAD")
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git archive in %s: %w\n%s", dir, err, errOut.String())
	}
	var out []source
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || !slices.Contains([]string{".c", ".cpp", ".h"}, path.Ext(h.Name)) {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		out = append(out, source{h.Name, data})
	}
	slices.SortFunc(out, func(a, b source) int { return strings.Compare(a.path, b.path) })
	if len(out) == 0 {
		return nil, fmt.Errorf("no C/C++ sources at HEAD of %s", dir)
	}
	return out, nil
}

// KiB is a kibibyte.
const KiB = 1024

// inputFiles maps input paths to contents; a path ending in "/" is an empty folder.
func inputFiles(sources []source) map[string][]byte {
	var all []byte
	files := map[string][]byte{}
	for _, s := range sources {
		files["src/"+s.path] = s.data
		all = append(all, s.data...)
	}
	text := func(off, n int) []byte { return all[off : off+n] }
	random := Random(1, 32*KiB)
	far := slices.Concat(random[:16*KiB], text(0, 600*KiB), random[:16*KiB], text(0, 300*KiB), random[:16*KiB])

	maps := map[string][]byte{
		// Text: all sources in one file, and prefixes of it.
		"text/all.txt":   all,
		"text/1mb.txt":   text(0, 1024*KiB),
		"text/small.txt": text(0, 256*KiB),
		// Binary.
		"bin/table.bin":  Table(2, 512*KiB),
		"bin/random.bin": random,
		"bin/x86.bin":    X86(3, 256*KiB),
		"bin/far.bin":    far,
		// Audio: one file per sample format.
		"wav/u8_mono.wav":    Wav(8, 1, 22050, 11025),
		"wav/s16_mono.wav":   Wav(16, 1, 22050, 22050),
		"wav/s16_stereo.wav": Wav(16, 2, 44100, 22050),
		"wav/s24_stereo.wav": Wav(24, 2, 44100, 22050),
		// The mix the presets compress: every file group FreeArc tells apart, folders, an empty file.
		"mix/docs/readme.txt":      text(300*KiB, 160*KiB),
		"mix/docs/notes/notes.txt": text(500*KiB, 48*KiB),
		"mix/src/main.cpp":         text(600*KiB, 64*KiB),
		"mix/data/table.bin":       Table(4, 192*KiB),
		"mix/data/random.bin":      Random(5, 8*KiB),
		"mix/bin/prog.exe":         X86(6, 96*KiB),
		"mix/media/tone.wav":       Wav(16, 2, 44100, 8000),
		"mix/empty.txt":            {},
		"mix/emptydir/":            nil,
		// Small inputs for the encrypted archives.
		"crypt/secret.txt": text(700*KiB, 24*KiB),
		"crypt/key.bin":    Random(7, 4*KiB),
		// Odds and ends.
		"misc/hello.txt": []byte("Hello from FreeArc 0.67\r\n"),
		"misc/empty.txt": {},
		"misc/emptydir/": nil,
		"misc/sub/a.txt": text(800*KiB, 3*KiB),
		"misc/sub/b.txt": text(810*KiB, 5*KiB),
		"misc/sub/c.bin": Table(8, 6*KiB),
		"misc/sub/d.txt": text(820*KiB, 2*KiB),
		"misc/sub/e.bin": Random(9, 1*KiB),
		"misc/sub/f.txt": text(830*KiB, 7*KiB),
		"misc/sub/g.txt": text(840*KiB, 1*KiB),
		"misc/sub/h.txt": text(850*KiB, 4*KiB),
		"misc/comment.txt": []byte("Genuine FreeArc 0.67 test archive.\r\n" +
			"Second line of the comment.\r\n"),
	}
	for p, d := range maps {
		files[p] = d
	}
	return files
}

// Random returns n pseudo-random bytes.
func Random(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 1))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// Table returns n bytes of 12-byte records with slowly changing fields: the data delta finds.
func Table(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 2))
	b := make([]byte, 0, n+12)
	var a, c uint32
	for len(b) < n {
		a += uint32(r.IntN(16))
		c += 1000 + uint32(r.IntN(3))
		b = binary.LittleEndian.AppendUint32(b, a)
		b = binary.LittleEndian.AppendUint32(b, c)
		b = binary.LittleEndian.AppendUint16(b, uint16(r.IntN(4)))
		b = binary.LittleEndian.AppendUint16(b, 7)
	}
	return b[:n]
}

// X86 returns n bytes looking like x86 code: random instruction bytes with frequent CALL/JMP rel32 whose
// targets cluster (what the exe filter turns into absolute addresses).
func X86(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 3))
	targets := make([]int32, 64)
	for i := range targets {
		targets[i] = int32(r.IntN(n))
	}
	ops := []byte{0x8B, 0x45, 0x89, 0x5D, 0x83, 0xC4, 0x50, 0x53, 0x56, 0x57, 0x0F, 0x85}
	b := make([]byte, 0, n+8)
	for len(b) < n {
		if r.IntN(3) == 0 {
			op := byte(0xE8)
			if r.IntN(4) == 0 {
				op = 0xE9
			}
			b = append(b, op)
			rel := targets[r.IntN(len(targets))] - int32(len(b)+4)
			b = binary.LittleEndian.AppendUint32(b, uint32(rel))
			continue
		}
		for range 1 + r.IntN(6) {
			b = append(b, ops[r.IntN(len(ops))])
		}
	}
	return b[:n]
}

// Wav returns a PCM RIFF WAVE file of n frames: two tones and a little noise.
func Wav(bits, channels, rate, frames int) []byte {
	r := rand.New(rand.NewPCG(uint64(bits*10+channels), 4))
	bps := bits / 8
	pcm := make([]byte, 0, frames*channels*bps)
	for i := range frames {
		for c := range channels {
			v := 0.5*math.Sin(float64(i)*0.031*float64(c+1)) + 0.2*math.Sin(float64(i)*0.0017) + 0.01*(r.Float64()-0.5)
			s := int64(v * float64(int64(1)<<(bits-1)-1))
			if bits == 8 {
				s += 128 // 8-bit samples are unsigned
			}
			for k := range bps {
				pcm = append(pcm, byte(s>>(8*k)))
			}
		}
	}
	h := []byte("RIFF")
	h = binary.LittleEndian.AppendUint32(h, uint32(36+len(pcm)))
	h = append(h, "WAVEfmt "...)
	h = binary.LittleEndian.AppendUint32(h, 16)
	h = binary.LittleEndian.AppendUint16(h, 1) // PCM
	h = binary.LittleEndian.AppendUint16(h, uint16(channels))
	h = binary.LittleEndian.AppendUint32(h, uint32(rate))
	h = binary.LittleEndian.AppendUint32(h, uint32(rate*channels*bps))
	h = binary.LittleEndian.AppendUint16(h, uint16(channels*bps))
	h = binary.LittleEndian.AppendUint16(h, uint16(bits))
	h = append(h, "data"...)
	h = binary.LittleEndian.AppendUint32(h, uint32(len(pcm)))
	return append(h, pcm...)
}

// --- golden

// entry is one archive archives.sh made (a line of commands.tsv).
type entry struct {
	name     string
	password string // UNARC_PASSWORD for the C unarc, "" for none
	note     string // what the archive tests
	command  string // the FreeArc command line, run in the input folder
}

// run is one C unarc run.
type run struct {
	args           []string
	stdout, stderr string
	code           int
}

// result is what the C unarc made of one archive.
type result struct {
	entry
	size   int64
	blocks []string // data block methods (UNARC_BLOCKS)
	runs   []run    // l, v, t
}

// ok reports whether every recorded run succeeded.
func (r *result) ok() bool {
	return !slices.ContainsFunc(r.runs, func(x run) bool { return x.code != 0 })
}

func runGolden(args []string) error {
	fl := flag.NewFlagSet("golden", flag.ExitOnError)
	from := fl.String("from", "", "output folder of archives.sh (archives/, commands.tsv)")
	unarc := fl.String("unarc", os.Getenv("C_UNARC"), "C unarc used as the oracle (default $C_UNARC)")
	dest := fl.String("dest", "testdata/genuine", "destination (archives/, golden/, README.md)")
	info := fl.String("info", "", "file with the build facts for the README (build.sh writes it)")
	fl.Parse(args)
	if *from == "" {
		return errors.New("golden: -from is required")
	}
	if *unarc == "" {
		return errors.New("golden: set -unarc or C_UNARC to the C unarc binary")
	}
	unarcAbs, err := filepath.Abs(*unarc)
	if err != nil {
		return err
	}
	entries, err := readCommands(filepath.Join(*from, "commands.tsv"))
	if err != nil {
		return err
	}
	archives, golden := filepath.Join(*dest, "archives"), filepath.Join(*dest, "golden")
	for _, d := range []string{archives, golden} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	var results []result
	var total int64
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(*from, "archives", e.name+".arc"))
		if err != nil {
			return err
		}
		arc := filepath.Join(archives, e.name+".arc")
		if err := os.WriteFile(arc, data, 0o644); err != nil {
			return err
		}
		abs, err := filepath.Abs(arc)
		if err != nil {
			return err
		}
		res := result{entry: e, size: int64(len(data))}
		total += res.size
		blocks, err := unarcBlocks(unarcAbs, abs, e.password)
		if err != nil {
			return err
		}
		res.blocks = blocks
		for _, cmd := range []string{"l", "v", "t"} {
			r, err := runUnarc(unarcAbs, []string{cmd, abs}, e.password)
			if err != nil {
				return err
			}
			r.args = []string{cmd, "genuine/archives/" + e.name + ".arc"}
			res.runs = append(res.runs, r)
		}
		if res.ok() {
			for i, r := range res.runs {
				file := filepath.Join(golden, fmt.Sprintf("%s.%d", e.name, i))
				if err := os.WriteFile(file, []byte(formatGolden(r, e.password)), 0o644); err != nil {
					return err
				}
			}
		}
		status := "ok"
		if !res.ok() {
			status = "C unarc fails"
		}
		log.Printf("%-24s %8d bytes  %s", e.name, res.size, status)
		results = append(results, res)
	}
	log.Printf("total %d bytes", total)
	facts := ""
	if *info != "" {
		b, err := os.ReadFile(*info)
		if err != nil {
			return err
		}
		facts = string(b)
	}
	return os.WriteFile(filepath.Join(*dest, "README.md"), []byte(readme(results, total, facts)), 0o644)
}

// readCommands parses commands.tsv: name, password, note, command line.
func readCommands(file string) ([]entry, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) != 4 {
			return nil, fmt.Errorf("%s: bad line %q", file, line)
		}
		out = append(out, entry{name: fields[0], password: fields[1], note: fields[2], command: fields[3]})
	}
	return out, sc.Err()
}

// formatGolden renders a run in the layout internal/fixturetest parses.
func formatGolden(r run, password string) string {
	env := ""
	if password != "" {
		env = "UNARC_PASSWORD=" + password + " "
	}
	return fmt.Sprintf("$ %sunarc %s\n%s\n--- stderr\n%s\n--- exit %d\n", env, strings.Join(r.args, " "),
		r.stdout, r.stderr, r.code)
}

// runUnarc runs the C unarc in an empty scratch folder with only TZ, PATH and the password set.
func runUnarc(unarc string, args []string, password string, extraEnv ...string) (run, error) {
	dir, err := os.MkdirTemp("", "genuine-unarc-*")
	if err != nil {
		return run{}, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), unarcTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, unarc, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"TZ=UTC", "PATH=" + os.Getenv("PATH")}, extraEnv...)
	if password != "" {
		cmd.Env = append(cmd.Env, "UNARC_PASSWORD="+password)
	}
	cmd.Stdin = strings.NewReader("")
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	if ctx.Err() != nil {
		return run{}, fmt.Errorf("unarc %v timed out", args)
	}
	r := run{args: args, stdout: o.String(), stderr: e.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.code = ee.ExitCode()
	} else if err != nil {
		return run{}, err
	}
	return r, nil
}

// unarcBlocks returns the methods of the archive's data blocks, as the C unarc reads them.
func unarcBlocks(unarc, archive, password string) ([]string, error) {
	r, err := runUnarc(unarc, []string{"l", archive}, password, "UNARC_BLOCKS=1")
	if err != nil {
		return nil, err
	}
	var out []string
	for line := range strings.Lines(r.stderr) {
		if f := strings.Fields(line); len(f) == 4 && f[0] == "datablock" {
			out = append(out, f[1])
		}
	}
	return out, nil
}

// readme renders testdata/genuine/README.md.
func readme(results []result, total int64, facts string) string {
	var b strings.Builder
	b.WriteString(`# Archives made by the genuine FreeArc 0.67

The archives in ` + "`archives/`" + ` were created by FreeArc 0.67 itself: the official 32-bit Windows console
build, run under 32-bit Wine in an emulated i386 PC. Unlike ` + "`testdata/archives`" + ` (our own writer around
streams from FreeArc's encoders built for 64-bit macOS), they have FreeArc's real headers and method
strings, and their PPMd streams that fill the model memory come from the 32-bit memory layout that end
users' archives have.

` + "`golden/<name>.<N>`" + ` is the C unarc run on ` + "`archives/<name>.arc`" + `, with
N = 0 for ` + "`l`" + `, 1 for ` + "`v`" + `, 2 for ` + "`t`" + `, in an empty scratch folder, with only ` + "`TZ=UTC`" + `, ` + "`PATH`" + ` and,
for encrypted archives, ` + "`UNARC_PASSWORD=" + Password + "`" + ` set. ` + "`internal/cli`" + `'s TestGolden replays them.
Archives the C unarc fails on have no golden files; they are listed at the end.

Everything here is generated; do not edit it by hand. Rebuild with

    scripts/genuine/build.sh

which builds the Docker image ` + "`go-unarc-freearc067`" + `, writes the inputs
(` + "`go run ./scripts/genuine inputs`" + `), boots the guest to run ` + "`scripts/genuine/archives.sh`" + `, then
records the golden files and this README (` + "`go run ./scripts/genuine golden`" + `). FreeArc's output is
deterministic: a rebuild gives the same archives, except the encrypted ones (random salts and IVs).

## FreeArc and the build

`)
	b.WriteString(facts)
	fmt.Fprintf(&b, `
## Archives

%d archives, %d bytes. Each command ran in the input folder as `+"`wine Arc.exe`"+` with the arguments
shown (`+"`arc`"+` stands for it). Encrypted archives use the password `+"`%s`"+`. "Blocks" are the
methods of the archive's data blocks as the C unarc reads them (`+"`UNARC_BLOCKS=1`"+`); FreeArc gives
empty files a block of their own.

| Archive | What | Command | Size | Blocks |
|:--|:--|:--|--:|:--|
`, len(results), total, Password)
	for _, r := range results {
		blocks := make([]string, len(r.blocks))
		for i, m := range r.blocks {
			blocks[i] = "`" + m + "`"
		}
		name := r.name
		if !r.ok() {
			name += " (no golden)"
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | %d | %s |\n", name, cell(r.note), r.command, r.size,
			strings.Join(blocks, "<br>"))
	}
	b.WriteString("\n## C unarc fails on these\n\n")
	var failed bool
	for _, r := range results {
		if r.ok() {
			continue
		}
		failed = true
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", r.name, failureReason(&r))
		for _, x := range r.runs {
			if x.code != 0 {
				fmt.Fprintf(&b, "```\n%s```\n\n", formatGolden(x, r.password))
			}
		}
	}
	if !failed {
		b.WriteString("None.\n")
	}
	return b.String()
}

// cell escapes text for a Markdown table cell.
func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// failureReason explains a C unarc failure from its output.
func failureReason(r *result) string {
	var out strings.Builder
	for _, x := range r.runs {
		out.WriteString(x.stdout + x.stderr)
	}
	s := out.String()
	// The methods the Go port decodes and the C unarc does not have.
	for _, m := range []string{"4x4", "dispack070"} {
		if strings.Contains(s, `"`+m+`" is not supported`) {
			return "The C unarc (UnArc 0.60 based) has no " + m + " method; the Go port decodes it."
		}
	}
	usesPPMd := slices.ContainsFunc(r.blocks, func(m string) bool { return strings.Contains(m, "ppmd") })
	if strings.Contains(s, "failed CRC check") && usesPPMd {
		return "PPMd filled its model memory. The 64-bit C build lays the model out differently from the " +
			"32-bit encoder, so its decoding diverges once the memory is full; the Go port is expected to " +
			"decode these."
	}
	return "The C unarc's failing runs:"
}
