package parallel

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/n0madic/go-unarc/internal/method"
)

// blocks returns a reader of n blocks: block i holds the text "<i>;" and takes its index as the header. Block
// fail is a read error, or a panic when failErr is nil.
func blocks(n int, fail int, failErr error) Read[int] {
	i := 0
	return func(j *Job[int]) (bool, error) {
		if i == fail {
			if failErr == nil {
				panic("read boom")
			}
			return false, failErr
		}
		if i == n {
			return false, nil
		}
		j.Header = i
		j.In = append(j.In[:0], fmt.Sprintf("%d;", i)...)
		i++
		return true, nil
	}
}

// echo decodes a block by copying it, slower for even blocks so that they finish out of order; block "bad"
// fails and block "boom" panics.
func echo(bad, boom int) func(*int, *Job[int]) {
	return func(calls *int, j *Job[int]) {
		*calls++
		if j.Header%2 == 0 {
			time.Sleep(time.Millisecond)
		}
		switch j.Header {
		case bad:
			j.Err = fmt.Errorf("%w: block %d", method.ErrBadData, bad)
		case boom:
			panic("boom")
		default:
			j.Out = append(j.Out[:0], j.In...)
		}
	}
}

func want(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%d;", i)
	}
	return b.String()
}

type stopAfter struct {
	n   int
	got bytes.Buffer
}

func (s *stopAfter) Write(p []byte) (int, error) {
	if s.got.Len() >= s.n {
		return 0, method.ErrStop
	}
	return s.got.Write(p)
}

// decodeRecover runs Decode and returns the value of a panic that escapes it.
func decodeRecover(dst *bytes.Buffer, workers int, read Read[int]) (r any) {
	defer func() { r = recover() }()
	_ = Decode(dst, workers, read, echo(-1, -1)) // only the panic matters
	return nil
}

func TestDecode(t *testing.T) {
	before := runtime.NumGoroutine()
	readErr := errors.New("read failed")
	for _, workers := range []int{0, 1, 4, 16} {
		var out bytes.Buffer
		if err := Decode(&out, workers, blocks(100, -1, nil), echo(-1, -1)); err != nil || out.String() != want(100) {
			t.Errorf("%d workers: %q, %v", workers, out.String(), err)
		}
		out.Reset()
		if err := Decode(&out, workers, blocks(100, 50, readErr), echo(-1, -1)); err != readErr || out.String() != want(50) {
			t.Errorf("%d workers, read error: %d bytes, %v", workers, out.Len(), err)
		}
		out.Reset()
		if err := Decode(&out, workers, blocks(100, -1, nil), echo(30, -1)); !errors.Is(err, method.ErrBadData) || out.String() != want(30) {
			t.Errorf("%d workers, bad block: %d bytes, %v", workers, out.Len(), err)
		}
		out.Reset()
		err := Decode(&out, workers, blocks(100, -1, nil), echo(-1, 20))
		if !errors.Is(err, method.ErrBadData) || !strings.Contains(err.Error(), ": internal error") || out.String() != want(20) {
			t.Errorf("%d workers, panic: %d bytes, %v", workers, out.Len(), err)
		}
		s := &stopAfter{n: 10}
		if err := Decode(s, workers, blocks(1000, -1, nil), echo(-1, -1)); !errors.Is(err, method.ErrStop) {
			t.Errorf("%d workers, stop: %v", workers, err)
		}
		// A panic in read reaches the caller; the goroutine check below makes sure nothing is left running.
		out.Reset()
		if r := decodeRecover(&out, workers, blocks(100, 40, nil)); r != "read boom" {
			t.Errorf("%d workers, read panic: recovered %v", workers, r)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines left running", n-before)
	}
}
