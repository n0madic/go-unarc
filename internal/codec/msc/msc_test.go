package msc_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	_ "github.com/n0madic/go-unarc/internal/codec/all" // Lookup finds msc through the registry
	"github.com/n0madic/go-unarc/internal/codec/tak"
	"github.com/n0madic/go-unarc/internal/codec/tak/taktest"
	"github.com/n0madic/go-unarc/internal/method"
)

func decode(t testing.TB, stream []byte) ([]byte, error) {
	t.Helper()
	env := method.NewEnv()
	c, ok := method.Lookup("msc:tak=9:bmp=0:dxt=0", env)
	if !ok {
		t.Fatal("msc is not registered")
	}
	var out bytes.Buffer
	err := c.Decode(&out, bytes.NewReader(stream))
	return out.Bytes(), err
}

func TestParse(t *testing.T) {
	t.Parallel()
	env := method.NewEnv()
	env.CLSDir = t.TempDir() // no DLL there: a method the built-in decoder does not take is not found at all
	for _, c := range []struct {
		method string
		native bool
	}{
		{"msc:tak=9:bmp=0:dxt=0", true},
		{"msc:dxt=0:bmp=0:tak=1", true},
		{"msc:tak=0:bmp=0:dxt=0", false},
		{"msc:tak=9:bmp=1:dxt=0", false},
		{"msc:tak=9:bmp=0:dxt=2", false},
		{"msc:tak=9:bmp=0", false},
		{"msc", false},
		{"msc:tak=9:bmp=0:dxt=0:x=1", false},
		{"msc:tak=9:tak=9:bmp=0:dxt=0", false},
		{"msc:tak:bmp=0:dxt=0", false},
		{"msc:tak=x:bmp=0:dxt=0", false},
		{"msd:tak=9:bmp=0:dxt=0", false},
	} {
		if _, ok := method.Lookup(c.method, env); ok != c.native {
			t.Errorf("Lookup(%q) = %v, want %v", c.method, ok, c.native)
		}
	}
}

// item builds an item with a length field of n bytes.
func item(kind byte, n int, payload []byte) []byte {
	out := []byte{byte(n-1)<<4 | kind}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(payload)))[:1+n]
	return append(out, payload...)
}

func header() []byte { return []byte("MSC \x01\x00\x00\x00") }

// fmtChunk is the fmt chunk of a PCM file: 24 bytes from "fmt " on.
func fmtChunk(channels, rate, bits int) []byte {
	out := []byte("fmt ")
	out = binary.LittleEndian.AppendUint32(out, 16)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint16(out, uint16(channels))
	out = binary.LittleEndian.AppendUint32(out, uint32(rate))
	out = binary.LittleEndian.AppendUint32(out, uint32(rate*channels*bits/8))
	out = binary.LittleEndian.AppendUint16(out, uint16(channels*bits/8))
	return binary.LittleEndian.AppendUint16(out, uint16(bits))
}

func wavPayload(riffSize uint32, fmtChunk []byte, dataSize uint32, tak []byte) []byte {
	p := []byte{0, 44, 0, 0, 0, 0, 0}
	p = binary.LittleEndian.AppendUint32(p, riffSize)
	p = append(p, 0, 0, 0, 0)
	p = append(p, fmtChunk...)
	p = append(p, "data"...)
	p = binary.LittleEndian.AppendUint32(p, dataSize)
	return append(p, tak...)
}

// wavHeader is the 44 bytes a kind 3 item stands for.
func wavHeader(riffSize uint32, fmtChunk []byte, dataSize uint32) []byte {
	h := []byte("RIFF")
	h = binary.LittleEndian.AppendUint32(h, riffSize)
	h = append(h, "WAVE"...)
	h = append(h, fmtChunk...)
	h = append(h, "data"...)
	return binary.LittleEndian.AppendUint32(h, dataSize)
}

// takStream is a TAK stream of one frame of stored 16-bit mono samples.
func takStream(samples []int32) []byte {
	info := taktest.Info{Codec: 2, SampleRate: 22050, Channels: 1, BPS: 16, FrameType: 7, Samples: int64(len(samples))}
	frame := taktest.Frame(0, len(samples), nil, func(w *taktest.BitWriter) {
		taktest.StoredSamples(w, 16, [][]int32{samples})
	})
	return append(taktest.Metadata(info), frame...)
}

func TestRawItems(t *testing.T) {
	t.Parallel()
	stream := header()
	var want []byte
	for _, c := range []struct {
		width   int
		payload []byte
	}{
		{1, []byte("hello")},
		{2, bytes.Repeat([]byte{7}, 300)},
		{3, bytes.Repeat([]byte("abcdefghij"), 7000)}, // 70000 bytes
		{4, []byte("four")},                           // a wider field than the length needs
	} {
		stream = append(stream, item(0, c.width, c.payload)...)
		want = append(want, c.payload...)
	}
	got, err := decode(t, stream)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("err %v, %d bytes, want %d", err, len(got), len(want))
	}
	if got, err := decode(t, header()); err != nil || len(got) != 0 {
		t.Fatalf("empty stream: %d bytes, %v", len(got), err)
	}
	if got, err := decode(t, append(header(), item(0, 1, nil)...)); err != nil || len(got) != 0 {
		t.Fatalf("empty item: %d bytes, %v", len(got), err)
	}
}

func TestWAVItem(t *testing.T) {
	t.Parallel()
	samples := []int32{-3, 0, 32767, -32768, 100}
	pcm := []byte{0xfd, 0xff, 0, 0, 0xff, 0x7f, 0, 0x80, 100, 0}
	f := fmtChunk(1, 22050, 16)
	tail := []byte("cue \x04\x00\x00\x00abcd")
	payload := wavPayload(uint32(36+len(pcm)+len(tail)), f, uint32(len(pcm)), takStream(samples))

	var stream, want []byte
	stream = append(header(), item(0, 1, []byte("BIGF"))...)
	stream = append(stream, item(3, 1, payload)...) // a short length, a long one and a wide one
	stream = append(stream, item(0, 1, tail)...)
	stream = append(stream, item(3, 3, payload)...)
	want = append(want, "BIGF"...)
	want = append(want, wavHeader(uint32(36+len(pcm)+len(tail)), f, uint32(len(pcm)))...)
	want = append(want, pcm...)
	want = append(want, tail...)
	want = append(want, wavHeader(uint32(36+len(pcm)+len(tail)), f, uint32(len(pcm)))...)
	want = append(want, pcm...)

	// item(3, 1, ...) cannot hold a payload longer than 255 bytes; make sure the test data fits
	if len(payload) > 255 {
		t.Fatalf("test payload of %d bytes", len(payload))
	}
	got, err := decode(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output differs:\n got % x\nwant % x", got, want)
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	samples := []int32{1, 2, 3}
	f := fmtChunk(1, 22050, 16)
	tak := takStream(samples)
	pcmSize := uint32(6)
	good := wavPayload(42, f, pcmSize, tak)

	mutate := func(i int, v byte) []byte {
		p := bytes.Clone(good)
		p[i] = v
		return p
	}
	wavErr := map[string][]byte{
		"prefix":              mutate(0, 1),
		"header size":         mutate(1, 45),
		"zeros":               mutate(11, 1),
		"data tag":            mutate(7+4+4+24, 'x'),
		"fmt tag":             mutate(7+4+4, 'x'),
		"fmt size":            mutate(7+4+4+4, 17),
		"data size too big":   wavPayload(42, f, pcmSize+2, tak),
		"data size too small": wavPayload(42, f, pcmSize-2, tak),
		"trailing bytes":      append(bytes.Clone(good), 1),
		"not TAK":             wavPayload(42, f, pcmSize, []byte("tBaX")),
		"damaged TAK":         func() []byte { p := bytes.Clone(good); p[len(p)-4] ^= 1; return p }(),
		"too short":           good[:20],
		"no frames":           wavPayload(42, f, pcmSize, taktest.Metadata(taktest.Info{Codec: 2, SampleRate: 22050, Channels: 1, BPS: 16, FrameType: 7})),
	}
	for name, payload := range wavErr {
		_, err := decode(t, append(header(), item(3, 2, payload)...))
		if !errors.Is(err, method.ErrBadData) {
			t.Errorf("%s: err = %v, want ErrBadData", name, err)
		}
	}
	if _, err := decode(t, append(header(), item(3, 2, good)...)); err != nil {
		t.Fatalf("the unmodified item fails: %v", err)
	}

	stream := append(header(), item(0, 2, bytes.Repeat([]byte{1}, 100))...)
	stream = append(stream, item(3, 2, good)...)
	for name, s := range map[string][]byte{
		"empty":            {},
		"short magic":      []byte("MSC"),
		"wrong magic":      []byte("MSD \x01\x00\x00\x00"),
		"wrong version":    []byte("MSC \x02\x00\x00\x00"),
		"unknown kind 1":   append(header(), item(1, 1, []byte{1})...),
		"unknown kind 2":   append(header(), item(2, 1, []byte{1})...),
		"tag with 5 bytes": append(header(), 0x40, 0, 0, 0, 0, 0),
		"cut length":       append(header(), 0x20, 1),
		"cut raw item":     stream[:50],
		"cut audio item":   stream[:len(stream)-3],
		"huge audio item":  append(header(), 0x33, 0xff, 0xff, 0xff, 0xff),
	} {
		if _, err := decode(t, s); !errors.Is(err, method.ErrBadData) {
			t.Errorf("%s: err = %v, want ErrBadData", name, err)
		}
	}
}

// A failure of the TAK decoder is corrupt data to the chain, and the decoder's own error stays in the chain.
func TestTAKErrorsKeepTheirKind(t *testing.T) {
	t.Parallel()
	f := fmtChunk(1, 22050, 16)
	info := taktest.Info{Codec: 3, SampleRate: 22050, Channels: 1, BPS: 16, FrameType: 7}
	unsupported := append(taktest.Metadata(info), taktest.Frame(0, 3, nil, func(w *taktest.BitWriter) {
		taktest.StoredSamples(w, 16, [][]int32{{1, 2, 3}})
	})...)
	damaged := takStream([]int32{1, 2, 3})
	damaged[len(damaged)-4] ^= 1
	for _, c := range []struct {
		name string
		tak  []byte
		want error
	}{
		{"unsupported", unsupported, tak.ErrUnsupported},
		{"damaged", damaged, tak.ErrCorrupt},
	} {
		_, err := decode(t, append(header(), item(3, 2, wavPayload(42, f, 6, c.tak))...))
		if !errors.Is(err, method.ErrBadData) || !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want ErrBadData and %v", c.name, err, c.want)
		}
	}
}

// stopAfter fails with ErrStop once it has had n bytes, as the last consumer of a chain does.
type stopAfter struct{ n int }

func (w *stopAfter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		return 0, method.ErrStop
	}
	w.n -= len(p)
	return len(p), nil
}

func TestWriteErrorPasses(t *testing.T) {
	t.Parallel()
	f := fmtChunk(1, 22050, 16)
	payload := wavPayload(42, f, 6, takStream([]int32{1, 2, 3}))
	stream := append(header(), item(0, 1, []byte("0123456789"))...)
	stream = append(stream, item(3, 2, payload)...)
	env := method.NewEnv()
	c, _ := method.Lookup("msc:tak=9:bmp=0:dxt=0", env)
	for _, limit := range []int{0, 5, 10, 11, 54, 55} { // in the raw item, in the header, in the samples
		if err := c.Decode(&stopAfter{n: limit}, bytes.NewReader(stream)); !errors.Is(err, method.ErrStop) {
			t.Errorf("limit %d: err = %v, want ErrStop", limit, err)
		}
	}
	if err := c.Decode(&stopAfter{n: 10 + 44 + 6}, bytes.NewReader(stream)); err != nil {
		t.Errorf("all the data fits: %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	fm := fmtChunk(1, 22050, 16)
	payload := wavPayload(42, fm, 6, takStream([]int32{1, 2, 3}))
	good := append(header(), item(0, 1, []byte("abc"))...)
	good = append(good, item(3, 2, payload)...)
	f.Add(good)
	f.Add(header())
	f.Add(good[:len(good)/2])
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := decode(t, data)
		if err != nil && !errors.Is(err, method.ErrBadData) {
			t.Fatalf("unexpected error: %v", err)
		}
		_ = out
	})
}
