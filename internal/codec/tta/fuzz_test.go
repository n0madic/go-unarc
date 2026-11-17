package tta

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/n0madic/go-unarc/internal/method"
)

// limitWriter accepts up to limit bytes and then answers ErrStop, as a consumer that has what it needs does.
type limitWriter struct{ left int }

func (w *limitWriter) Write(p []byte) (int, error) {
	if len(p) > w.left {
		w.left = 0
		return 0, method.ErrStop
	}
	w.left -= len(p)
	return len(p), nil
}

// FuzzDecode feeds arbitrary bytes to the decoder: whatever the input, it must neither panic nor hang, and
// all it may answer is success, ErrBadData or the error of the writer.
func FuzzDecode(f *testing.F) {
	files, _ := filepath.Glob("testdata/*.tta")
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
		if len(data) > 64 {
			f.Add(data[:len(data)/2])
		}
	}
	hdr := []byte{3, 0, 2, 16}
	f.Add([]byte{})
	f.Add(hdr)
	f.Add(cat(hdr, le32(0), le32(8), le32(5), []byte{0xff, 0xff, 0xff, 0xff, 0xff}))
	f.Add(cat([]byte{3, 2, 1, 8}, le32(0), le32(4), []byte{1, 2, 3, 4, 5, 6, 7, 8}))
	f.Add(cat([]byte{3, 1, 2, 32}, le32(0), le32(16), le32(40), bytes.Repeat([]byte{0xa5}, 40)))
	f.Fuzz(func(t *testing.T, data []byte) {
		err := (codec{}).Decode(&limitWriter{left: 8 << 20}, bytes.NewReader(data))
		if err != nil && !errors.Is(err, method.ErrBadData) && !errors.Is(err, method.ErrStop) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
