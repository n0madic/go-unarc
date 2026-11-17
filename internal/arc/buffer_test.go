package arc

import (
	"errors"
	"testing"

	"github.com/n0madic/go-unarc/internal/arctest"
)

func TestReadInteger(t *testing.T) {
	t.Parallel()
	var values []uint64
	for n := 1; n <= 9; n++ {
		if n < 9 {
			values = append(values, 1<<(7*n)-1, 1<<(7*n))
		}
	}
	values = append(values, 0, 1, 1<<63, 1<<64-1)
	for _, v := range values {
		enc := arctest.Integer(v)
		b := &buffer{data: append(enc, 0xAA)} // a following byte must not be consumed
		got, err := b.readInteger()
		if err != nil || got != v || b.p != len(enc) {
			t.Errorf("readInteger(%x) = %d, %v after %d bytes; want %d after %d", enc, got, err, b.p, v, len(enc))
		}
		// Truncated: an error, not a panic or a wrong value.
		if len(enc) > 1 {
			b := &buffer{data: enc[:len(enc)-1]}
			if _, err := b.readInteger(); !errors.Is(err, errBadData) {
				t.Errorf("truncated %x: err = %v", enc, err)
			}
		}
	}
}

func TestReadCount(t *testing.T) {
	t.Parallel()
	// A count larger than the bytes left fails before anything is allocated.
	b := &buffer{data: append(arctest.Integer(1<<40), 0, 0)}
	if _, err := b.readCount(1, errBadString); err != errBadString {
		t.Errorf("err = %v, want errBadString", err)
	}
	b = &buffer{data: append(arctest.Integer(2), 0, 0)}
	if n, err := b.readCount(1, errBadString); n != 2 || err != nil {
		t.Errorf("readCount = %d, %v", n, err)
	}
}
