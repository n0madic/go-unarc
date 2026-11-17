package grzip

import (
	"bytes"
	"cmp"
	"fmt"
	"github.com/n0madic/go-unarc/internal/codectest"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// benchData is the input of BenchmarkDecode: the file named by GRZIP_BENCH_FILE (a 20-50 MB corpus is the
// intended use), or about 4 MB made of the package sources.
func benchData(tb testing.TB) []byte {
	tb.Helper()
	if name := os.Getenv("GRZIP_BENCH_FILE"); name != "" {
		data, err := os.ReadFile(name)
		if err != nil {
			tb.Fatal(err)
		}
		return data
	}
	text := sourceText(tb)
	var b bytes.Buffer
	for b.Len() < 4<<20 {
		b.Write(text)
		b.Write(skewed(len(text)/4, int64(b.Len())))
	}
	return b.Bytes()
}

func BenchmarkDecode(b *testing.B) {
	if !codectest.HaveFagen(b) {
		b.Skip("fagen not built")
	}
	data := benchData(b)
	methods := []string{"grzip:m1", "grzip:m2", "grzip:m3", "grzip:m4", "grzip:m1:b1mb"}
	if m := os.Getenv("GRZIP_BENCH_METHODS"); m != "" {
		methods = strings.Split(m, ",")
	}
	for _, m := range methods {
		stream := fagen(b, "c", m, data)
		c := mustParse(b, m)
		for _, threads := range []int{1, runtime.NumCPU()} {
			b.Run(fmt.Sprintf("%s/threads=%d", m, threads), func(b *testing.B) {
				cc := *c
				cc.threads = threads
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				for range b.N {
					if err := cc.Decode(io.Discard, bytes.NewReader(stream)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// TestPerf times the decoder on a stream file when GRZIP_PERF_STREAM is set (GRZIP_PERF_METHOD, default "grzip";
// GRZIP_PERF_THREADS, default 1; GRZIP_PERF_RUNS, default 5) and prints the best wall and CPU time. CPU time
// (user+system of the process) stays meaningful on a loaded machine, where wall time does not.
func TestPerf(t *testing.T) {
	name := os.Getenv("GRZIP_PERF_STREAM")
	if name == "" {
		t.Skip("set GRZIP_PERF_STREAM to a grzip stream to time it")
	}
	stream, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	meth := cmp.Or(os.Getenv("GRZIP_PERF_METHOD"), "grzip")
	threads, _ := strconv.Atoi(cmp.Or(os.Getenv("GRZIP_PERF_THREADS"), "1"))
	runs, _ := strconv.Atoi(cmp.Or(os.Getenv("GRZIP_PERF_RUNS"), "5"))
	c := mustParse(t, meth)
	cc := *c
	cc.threads = threads
	cpu := func() time.Duration {
		var ru syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
			t.Fatal(err)
		}
		return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	}
	bestWall, bestCPU := time.Duration(1<<62), time.Duration(1<<62)
	var size int64
	for range runs {
		cpu0, wall0 := cpu(), time.Now()
		w := &countWriter{}
		if err := cc.Decode(w, bytes.NewReader(stream)); err != nil {
			t.Fatal(err)
		}
		bestWall = min(bestWall, time.Since(wall0))
		bestCPU = min(bestCPU, cpu()-cpu0)
		size = w.n
	}
	t.Logf("PERF %s threads=%d: %d -> %d bytes, best wall %.3fs, best cpu %.3fs", meth, threads, len(stream), size,
		bestWall.Seconds(), bestCPU.Seconds())
}

type countWriter struct{ n int64 }

func (w *countWriter) Write(p []byte) (int, error) { w.n += int64(len(p)); return len(p), nil }
