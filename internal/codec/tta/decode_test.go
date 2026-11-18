package tta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"runtime"
	"testing"
	"testing/iotest"

	"github.com/n0madic/go-unarc/internal/method"
)

func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestDecodeMalformed(t *testing.T) {
	t.Parallel()
	hdr := func(level, flags, channels, wordSize byte) []byte { return []byte{level, flags, channels, wordSize} }
	stereo16 := hdr(3, 0, 2, 16)
	noOffset := le32(0)
	random := bytes.Repeat([]byte{0x5a, 0xc3, 0x17}, 100)

	tests := []struct {
		name   string
		stream []byte
	}{
		{"empty", nil},
		{"short header", []byte{3, 0, 2}},
		{"level 4", hdr(4, 0, 2, 16)},
		{"level 255", hdr(255, 0, 2, 16)},
		{"raw mode 2", hdr(3, 4, 2, 16)},
		{"float 16 bit", cat(hdr(3, 1, 2, 16), noOffset)},
		{"float 24 bit", cat(hdr(3, 1, 1, 24), noOffset)},
		{"float 8 bit", cat(hdr(3, 1, 1, 8), noOffset)},
		{"int 32 bit", cat(hdr(3, 0, 2, 32), noOffset)},
		{"int 25 bit", cat(hdr(3, 0, 2, 25), noOffset)},
		{"int 255 bit", cat(hdr(3, 0, 2, 255), noOffset)},
		{"stored with a bad header", hdr(0, 0, 0, 32)},
		{"stored float of 8 bits", hdr(0, 1, 0, 8)},
		{"no offset", stereo16},
		{"short offset", cat(stereo16, []byte{1, 2})},
		{"short offset data", cat(stereo16, le32(5), []byte("ab"))},
		{"frame header cut", cat(stereo16, noOffset, []byte{1, 2})},
		{"frame without compressed size", cat(stereo16, noOffset, le32(100))},
		{"frame of 2^30+1 bytes", cat(stereo16, noOffset, le32(1<<30+1), le32(10), random)},
		{"compressed of 2^30+1 bytes", cat(stereo16, noOffset, le32(100), le32(1<<30+1), random)},
		{"compressed data cut", cat(stereo16, noOffset, le32(100), le32(300), random[:299])},
		{"more samples than bits", cat(stereo16, noOffset, le32(4000), le32(10), random[:10])},
		{"no channels", cat(hdr(3, 0, 0, 16), noOffset, le32(100), le32(10), random[:10])},
		{"no bits", cat(hdr(3, 0, 2, 0), noOffset, le32(100), le32(10), random[:10])},
		{"no channels, raw", cat(hdr(3, 2, 0, 16), noOffset, le32(100))},
		{"stored frame cut", cat(stereo16, noOffset, le32(100), le32(0), random[:99])},
		{"rest cut", cat(stereo16, noOffset, le32(5), le32(0), random[:5], le32(7), le32(0), random[:6])},
		{"raw data cut", cat(hdr(3, 2, 2, 16), noOffset, le32(40), random[:79])},
		{"all ones", cat(stereo16, noOffset, le32(400), le32(100), bytes.Repeat([]byte{0xff}, 100))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeStream(tt.stream)
			if !errors.Is(err, method.ErrBadData) {
				t.Errorf("Decode error = %v, want ErrBadData", err)
			}
		})
	}
}

func TestDecodeValid(t *testing.T) {
	t.Parallel()
	hdr := []byte{3, 0, 2, 16}
	tests := []struct {
		name   string
		stream []byte
		want   []byte
	}{
		{"offset only", cat(hdr, le32(3), []byte("abc")), []byte("abc")},
		{"empty offset", cat(hdr, le32(0)), nil},
		{"stored stream", []byte{0, 0, 0, 0, 'x', 'y', 'z'}, []byte("xyz")},
		{"stored empty", []byte{0, 0, 0, 0}, nil},
		{"stored stream ignores the rest of the header", []byte{0, 0, 7, 8, 'x'}, []byte("x")},
		{"stored frame", cat(hdr, le32(0), le32(5), le32(0), []byte("hello")), []byte("hello")},
		{"stored frames", cat(hdr, le32(1), []byte("H"), le32(2), le32(0), []byte("ab"), le32(1), le32(0), []byte("c")), []byte("Habc")},
		{"empty frames", cat(hdr, le32(0), le32(0), le32(0), le32(0), le32(0)), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeStream(tt.stream)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Decode = %q, want %q", got, tt.want)
			}
		})
	}
}

// A frame of fewer bytes than one sample of every channel carries only rest bytes; the C encoder writes the
// compressed size 0 for it, which makes it a stored frame, but a decoder must also take a size of 1 and more.
func TestDecodeFrameWithOnlyRestBytes(t *testing.T) {
	t.Parallel()
	// 2 channels of 16 bits: unit 4; 3 bytes make no sample.
	stream := cat([]byte{3, 0, 2, 16}, le32(0), le32(3), le32(1), []byte{0x00}, []byte("xyz"))
	got, err := decodeStream(stream)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if string(got) != "xyz" {
		t.Errorf("Decode = %q, want xyz", got)
	}
}

type failWriter struct {
	err   error
	after int // writes that succeed
	calls int
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls > w.after {
		return 0, w.err
	}
	return len(p), nil
}

// A write error ends the decoding at once and reaches the caller as it is: ErrStop is how the consumer says it
// needs no more data.
func TestDecodeWriteError(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(1, 2))
	raw := rawStream(r, 3, false, 2, 16, 2000, 3, 5)
	// rawStream wrote an empty offset: give the stream a real one
	withOffset := cat(raw[:4], le32(4), []byte("HEAD"), raw[8:])
	stored := cat([]byte{3, 0, 2, 16}, le32(4), []byte("HEAD"), le32(10), le32(0), []byte("0123456789"), le32(5), le32(0), []byte("abcde"))
	levelZero := []byte{0, 0, 0, 0, 'a', 'b', 'c'}

	errCustom := errors.New("disk full")
	for _, tt := range []struct {
		name   string
		stream []byte
	}{{"raw frames", withOffset}, {"stored frames", stored}, {"level 0", levelZero}} {
		var total int
		{
			w := &failWriter{err: errCustom, after: 1 << 30}
			if err := (codec{}).Decode(w, bytes.NewReader(tt.stream)); err != nil {
				t.Fatalf("%s: Decode: %v", tt.name, err)
			}
			total = w.calls
		}
		for _, werr := range []error{method.ErrStop, errCustom} {
			for after := range total {
				w := &failWriter{err: werr, after: after}
				err := (codec{}).Decode(w, bytes.NewReader(tt.stream))
				if err != werr {
					t.Errorf("%s, write %d fails with %v: Decode = %v", tt.name, after+1, werr, err)
				}
				if w.calls != after+1 {
					t.Errorf("%s, write %d fails: %d writes, decoding went on", tt.name, after+1, w.calls)
				}
			}
		}
	}
}

// Errors of the source other than the end of data are the caller's, not corrupt data.
func TestDecodeReadError(t *testing.T) {
	t.Parallel()
	errCustom := errors.New("pipe closed")
	stream := cat([]byte{3, 0, 2, 16}, le32(4), []byte("HEAD"), le32(10), le32(0), []byte("0123456789"))
	for cut := range len(stream) {
		src := io.MultiReader(bytes.NewReader(stream[:cut]), iotest.ErrReader(errCustom))
		err := (codec{}).Decode(io.Discard, src)
		if !errors.Is(err, errCustom) {
			t.Errorf("cut at %d: Decode = %v, want the source error", cut, err)
		}
	}
	// and the stream may arrive in pieces of any size
	got, err := decodeStreamFrom(iotest.OneByteReader(bytes.NewReader(stream)))
	if err != nil || string(got) != "HEAD0123456789" {
		t.Errorf("one byte reads: %q, %v", got, err)
	}
	got, err = decodeStreamFrom(iotest.DataErrReader(bytes.NewReader(stream)))
	if err != nil || string(got) != "HEAD0123456789" {
		t.Errorf("data with the end: %q, %v", got, err)
	}
}

func decodeStreamFrom(src io.Reader) ([]byte, error) {
	var out bytes.Buffer
	err := (codec{}).Decode(&out, src)
	return out.Bytes(), err
}

// Oversized fields are either rejected by the limit or read lazily, without a large allocation.
func TestDecodeBogusSizesStayCheap(t *testing.T) {
	// not parallel: it measures the allocations of the process
	hdr := []byte{3, 0, 1, 8}
	rawHdr := []byte{3, 2, 1, 8}
	few := []byte("just a few bytes")
	for _, tt := range []struct {
		name   string
		stream []byte
		want   error
	}{
		{"offset of 4 GB", cat(hdr, le32(0xFFFFFFFF), few), method.ErrLimit},
		{"stored frame of 1 GB", cat(hdr, le32(0), le32(1<<30), le32(0), few), method.ErrBadData},
		{"compressed frame of 1 GB", cat(hdr, le32(0), le32(1<<30), le32(1<<30), few), method.ErrLimit},
		{"1 GB of samples in a few bytes", cat(hdr, le32(0), le32(1<<30), le32(len32(few)), few), method.ErrBadData},
		{"raw frame of 1 GB", cat(rawHdr, le32(0), le32(1<<30), few), method.ErrLimit},
		{"float raw frame of 1 GB", cat([]byte{3, 3, 2, 32}, le32(0), le32(1<<30), few), method.ErrLimit},
		{"rest of 4 GB", cat(hdr, le32(0), le32(0xFFFFFFF0), le32(0), few), method.ErrBadData},
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := decodeStream(tt.stream)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Decode error = %v, want %v", tt.name, err, tt.want)
		}
		if grown := after.TotalAlloc - before.TotalAlloc; grown > 24<<20 {
			t.Errorf("%s: %d MB allocated", tt.name, grown>>20)
		}
	}
}

func len32(b []byte) uint32 { return uint32(len(b)) }
