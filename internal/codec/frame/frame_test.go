package frame

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/n0madic/go-unarc/internal/method"
)

func TestReadInt32(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want int32
		err  error
	}{
		{"\x01\x00\x00\x00", 1, nil},
		{"\xff\xff\xff\xff", -1, nil},
		{"", 0, io.EOF},
		{"\x01\x02", 0, ErrTruncated},
	}
	for _, tt := range tests {
		got, err := ReadInt32(strings.NewReader(tt.in))
		if got != tt.want || !errors.Is(err, tt.err) {
			t.Errorf("ReadInt32(%q) = %d, %v; want %d, %v", tt.in, got, err, tt.want, tt.err)
		}
	}
	if !errors.Is(ErrTruncated, method.ErrBadData) {
		t.Error("ErrTruncated is not ErrBadData")
	}
	boom := errors.New("boom")
	if _, err := ReadInt32(iotest.ErrReader(boom)); err != boom {
		t.Errorf("read error: %v", err)
	}
}

// countingReader serves n bytes and tracks the largest buffer it was asked to fill.
type countingReader struct {
	r       io.Reader
	maxRead int
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.maxRead = max(c.maxRead, len(p))
	return c.r.Read(p)
}

func TestReadN(t *testing.T) {
	t.Parallel()
	data := bytes.Repeat([]byte("0123456789"), 500000) // 5 MB
	buf := make([]byte, 0, 16)
	got, err := ReadN(buf, bytes.NewReader(data), len(data))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("ReadN: %d bytes, %v", len(got), err)
	}
	// A forged size: 1 GiB claimed, 10 bytes present; nothing near the claim is allocated.
	r := &countingReader{r: strings.NewReader("0123456789")}
	got, err = ReadN(nil, r, 1<<30)
	if !errors.Is(err, ErrTruncated) || string(got) != "0123456789" || cap(got) > 2*MaxPrealloc {
		t.Errorf("forged size: %q, cap %d, %v", got, cap(got), err)
	}
	if got, err := ReadN(got, strings.NewReader(""), 0); err != nil || len(got) != 0 {
		t.Errorf("empty read: %q, %v", got, err)
	}
}
