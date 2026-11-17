package msc

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
)

// compareWriter checks what it is given against a reference file as it goes.
type compareWriter struct {
	ref  *bufio.Reader
	pos  int64
	want []byte
}

func (w *compareWriter) Write(p []byte) (int, error) {
	if cap(w.want) < len(p) {
		w.want = make([]byte, len(p))
	}
	want := w.want[:len(p)]
	if _, err := io.ReadFull(w.ref, want); err != nil {
		return 0, fmt.Errorf("reference ends at %d, output goes on: %w", w.pos, err)
	}
	if !bytes.Equal(p, want) {
		for i := range p {
			if p[i] != want[i] {
				return 0, fmt.Errorf("output differs from the reference at byte %d", w.pos+int64(i))
			}
		}
	}
	w.pos += int64(len(p))
	return len(p), nil
}

// TestOracle decodes a real MSC stream and compares it with what the DLL made of it (CLS-MSC.dll under Wine).
// The stream is the input of the msc stage of an archive, too big and not free to keep in the repository, so it
// is given by MSC_PACKED and MSC_UNPACKED. UNARC_CLS_KEEP keeps that input, but only for a build that still
// sends the stage to the DLL (this decoder takes it first, and the plugin never keeps its output); the
// reference output is what `wine clshost.exe CLS-MSC.dll <params> <packed> <unpacked>` writes.
func TestOracle(t *testing.T) {
	packedPath, unpackedPath := os.Getenv("MSC_PACKED"), os.Getenv("MSC_UNPACKED")
	if packedPath == "" || unpackedPath == "" {
		t.Skip("MSC_PACKED and MSC_UNPACKED not set")
	}
	packed, err := os.Open(packedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Close()
	unpacked, err := os.Open(unpackedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer unpacked.Close()

	w := &compareWriter{ref: bufio.NewReaderSize(unpacked, 1<<20)}
	start := time.Now()
	if err := (codec{}).Decode(w, packed); err != nil {
		t.Fatalf("after %d bytes: %v", w.pos, err)
	}
	if _, err := w.ref.ReadByte(); err != io.EOF {
		t.Fatalf("the reference holds more than the %d bytes decoded", w.pos)
	}
	t.Logf("%d bytes identical to the DLL's output in %v", w.pos, time.Since(start).Round(time.Millisecond))
}
