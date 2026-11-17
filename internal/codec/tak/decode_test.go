package tak

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/n0madic/go-unarc/internal/codec/tak/taktest"
)

// realMetadata is the start of a real stream (an audio file of a game): a stream info block, a last frame
// block, an encoder block and the end block.
var realMetadata = []byte{
	0x74, 0x42, 0x61, 0x4b, 0x01, 0x0d, 0x00, 0x00, 0x02, 0x0d, 0x00, 0x3a, 0x00, 0x00, 0x20, 0xeb,
	0x03, 0x02, 0xf9, 0x20, 0x6b, 0x07, 0x0b, 0x00, 0x00, 0xd9, 0x25, 0x01, 0x00, 0x00, 0x07, 0x18,
	0x00, 0xa8, 0xef, 0xd8, 0x04, 0x07, 0x00, 0x00, 0x00, 0x02, 0x02, 0x24, 0x3e, 0x2f, 0x24, 0x00,
	0x00, 0x00, 0x00,
}

func TestParseMetadata(t *testing.T) {
	t.Parallel()
	info, off, err := parseMetadata(realMetadata)
	if err != nil {
		t.Fatal(err)
	}
	want := StreamInfo{Codec: CodecMonoStereo, SampleRate: 22050, Channels: 1, BitsPerSample: 16, FrameSamples: 5512, Samples: 59392}
	if info != want || off != len(realMetadata) {
		t.Fatalf("got %+v at %d, want %+v at %d", info, off, want, len(realMetadata))
	}
}

func TestParseMetadataErrors(t *testing.T) {
	t.Parallel()
	flip := func(i int) []byte {
		b := bytes.Clone(realMetadata)
		b[i] ^= 0x10
		return b
	}
	cases := map[string][]byte{
		"stream info CRC":    flip(10),
		"last frame CRC":     flip(27),
		"cut inside a block": realMetadata[:15],
		"no end block":       realMetadata[:len(realMetadata)-4],
		"cut inside header":  realMetadata[:6],
	}
	for name, data := range cases {
		if _, _, err := parseMetadata(data); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: err = %v, want ErrCorrupt", name, err)
		}
	}
	// A block of an unknown type is skipped, and so is a padding block.
	data := append(bytes.Clone(realMetadata[:len(realMetadata)-4]), 5, 3, 0, 0, 1, 2, 3, 0, 0, 0, 0)
	if _, off, err := parseMetadata(data); err != nil || off != len(data) {
		t.Errorf("padding block: off %d, err %v", off, err)
	}
	// No magic: bare frames.
	if _, off, err := parseMetadata([]byte{0xff, 0xa0}); err != nil || off != 0 {
		t.Errorf("no magic: off %d, err %v", off, err)
	}
}

func TestFrameSamples(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ rate, typ, want int }{
		{44100, 0, 4134}, {44100, 3, 11025}, {22050, 3, 5512}, {48000, 3, 12000},
		{44100, 4, 4096}, {44100, 7, 512}, {6000, 9, -1}, {44100, 10, -1}, {6000, 4, -1},
	} {
		if got := frameSamples(c.rate, c.typ); got != c.want {
			t.Errorf("frameSamples(%d, %d) = %d, want %d", c.rate, c.typ, got, c.want)
		}
	}
}

// pcm16 interleaves channels as 16-bit little-endian samples, as the decoder returns them.
func pcm16(chans ...[]int32) []byte {
	var out []byte
	for i := range chans[0] {
		for _, ch := range chans {
			out = append(out, byte(ch[i]), byte(ch[i]>>8))
		}
	}
	return out
}

// info22 is the stream info of the coded-frame tests: 22050 Hz, where a unit of a subframe table is 88
// samples and frames have 512.
func info22(channels, bps int) taktest.Info {
	return taktest.Info{Codec: CodecMonoStereo, SampleRate: 22050, Channels: channels, BPS: bps, FrameType: 7}
}

const scale22 = 88

func decodeStream(t *testing.T, stream []byte) (pcm []byte, d *Decoder) {
	t.Helper()
	d, err := NewDecoder(stream)
	if err != nil {
		t.Fatal(err)
	}
	for {
		pcm, err = d.Next(pcm)
		if errors.Is(err, io.EOF) {
			return pcm, d
		}
		if err != nil {
			t.Fatalf("frame: %v", err)
		}
	}
}

func TestStoredFrames(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		bps  int
		in   [][]int32
		want []byte
	}{
		{"16 bits mono", 16, [][]int32{{-3, 0, 32767, -32768, 100}}, []byte{0xfd, 0xff, 0, 0, 0xff, 0x7f, 0, 0x80, 100, 0}},
		{"8 bits stereo", 8, [][]int32{{-128, -1, 0, 127}, {5, 6, 7, 8}}, []byte{0, 133, 127, 134, 128, 135, 255, 136}},
		{"24 bits stereo", 24, [][]int32{{-1, 0x123456}, {-8388608, 8388607}}, []byte{0xff, 0xff, 0xff, 0, 0, 0x80, 0x56, 0x34, 0x12, 0xff, 0xff, 0x7f}},
	} {
		n := len(c.in[0])
		info := info22(len(c.in), c.bps)
		frame := taktest.Frame(0, n, nil, func(w *taktest.BitWriter) { taktest.StoredSamples(w, c.bps, c.in) })
		stream := append(taktest.Metadata(info), frame...)
		got, d := decodeStream(t, stream)
		if !bytes.Equal(got, c.want) {
			t.Errorf("%s: got % x, want % x", c.name, got, c.want)
		}
		if d.Consumed() != len(stream) {
			t.Errorf("%s: consumed %d of %d", c.name, d.Consumed(), len(stream))
		}
		if _, err := d.Next(nil); err != io.EOF {
			t.Errorf("%s: Next after the last frame = %v, want EOF", c.name, err)
		}
	}
}

// lcg gives reproducible residues in the range of taktest.Mode9.
func residues(n int, seed uint32) []int32 {
	out := make([]int32, n)
	for i := range out {
		seed = seed*1664525 + 1013904223
		out[i] = int32(seed>>26) - 32
	}
	return out
}

// weight is the impulse response of the first stage predictors: how much sample k >= 1 adds to output i.
func weight(mode, i, k int) int64 {
	switch {
	case mode == 1:
		return 1
	case mode == 2:
		return int64(i - k + 1)
	case k == 1: // mode 3
		return int64(i)
	default: // mode 3: triangular numbers
		return int64(i-k+1) * int64(i-k+2) / 2
	}
}

// integrate is the first stage predictor by its definition: sample 0 is added to every output, the others
// with their weights.
func integrate(x []int32, mode int) []int32 {
	y := make([]int32, len(x))
	for i := range x {
		sum := int64(x[0])
		for k := 1; k <= i; k++ {
			sum += weight(mode, i, k) * int64(x[k])
		}
		y[i] = int32(sum)
	}
	return y
}

func TestDecodeLPC(t *testing.T) {
	t.Parallel()
	x := make([]int32, 40)
	for i := range x {
		x[i] = int32(i*37%101) - 50
	}
	for mode := 1; mode <= 3; mode++ {
		for _, n := range []int{2, 3, 4, 5, 16, 17, 39, 40} {
			c := append([]int32(nil), x[:n]...)
			decodeLPC(c, mode)
			want := integrate(x[:n], mode)
			for i := range c {
				if c[i] != want[i] {
					t.Fatalf("mode %d, %d samples: sample %d = %d, want %d", mode, n, i, c[i], want[i])
				}
			}
		}
	}
	// Short blocks and mode 0 are left alone.
	for _, c := range [][]int32{{}, {5}} {
		decodeLPC(c, 1)
	}
}

func TestCodedMono(t *testing.T) {
	t.Parallel()
	const n = 300
	for _, lpc := range []int{0, 1, 2, 3} {
		for _, shift := range []int{0, 2} {
			for _, subframes := range [][]int{nil, {1}, {1, 2}} {
				res := residues(n-1, uint32(lpc*10+shift))
				ch := taktest.Channel{Shift: shift, First: -7, LPC: lpc, Residues: res, Subframes: subframes}
				frame := taktest.Frame(0, n, nil, func(w *taktest.BitWriter) { taktest.WriteChannel(w, 16, scale22, ch) })
				got, _ := decodeStream(t, append(taktest.Metadata(info22(1, 16)), frame...))

				samples := append([]int32{-7}, res...)
				if lpc != 0 {
					samples = integrate(samples, lpc)
				}
				for i := range samples {
					samples[i] <<= shift
				}
				if want := pcm16(samples); !bytes.Equal(got, want) {
					t.Errorf("lpc %d, shift %d, subframes %v: output differs", lpc, shift, subframes)
				}
			}
		}
	}
}

func TestDecorrelation(t *testing.T) {
	t.Parallel()
	const n = 300
	a0, b0 := int32(-11), int32(23)
	ra, rb := residues(n-1, 1), residues(n-1, 2)
	a, b := append([]int32{a0}, ra...), append([]int32{b0}, rb...)

	const dshift, dfactor = 2, 100
	scaled := func(v int32) int32 { return int32(uint32((dfactor*(v>>dshift)+128)>>8) << dshift) }

	// the first sample of each channel is never decorrelated
	expect := func(f func(i int, a, b int32) (int32, int32)) (x, y []int32) {
		x, y = make([]int32, n), make([]int32, n)
		x[0], y[0] = a[0], b[0]
		for i := 1; i < n; i++ {
			x[i], y[i] = f(i, a[i], b[i])
		}
		return x, y
	}

	for _, c := range []struct {
		mode  int
		extra func(w *taktest.BitWriter)
		want  func(i int, a, b int32) (int32, int32)
	}{
		{0, nil, func(i int, a, b int32) (int32, int32) { return a, b }},
		{1, nil, func(i int, a, b int32) (int32, int32) { return a, a + b }},
		{2, nil, func(i int, a, b int32) (int32, int32) { return b - a, b }},
		{3, nil, func(i int, a, b int32) (int32, int32) { m := a - b>>1; return m, m + b }},
		{4, func(w *taktest.BitWriter) { w.Put(1, 1); w.Put(dshift-1, 4); w.PutSigned(dfactor, 10) },
			func(i int, a, b int32) (int32, int32) { return a, scaled(a) - b }},
		{5, func(w *taktest.BitWriter) { w.Put(1, 1); w.Put(dshift-1, 4); w.PutSigned(dfactor, 10) },
			func(i int, a, b int32) (int32, int32) { return scaled(b) - a, b }},
	} {
		frame := taktest.Frame(0, n, nil, func(w *taktest.BitWriter) {
			taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: a0, Residues: ra})
			taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: b0, Residues: rb})
			w.Put(0, 1) // one subframe
			w.Put(uint64(c.mode), 3)
			if c.extra != nil {
				c.extra(w)
			}
		})
		got, _ := decodeStream(t, append(taktest.Metadata(info22(2, 16)), frame...))
		x, y := expect(c.want)
		if want := pcm16(x, y); !bytes.Equal(got, want) {
			t.Errorf("decorrelation mode %d: output differs", c.mode)
		}
	}
}

// TestFilteredDecorrelation runs modes 6 and 7 with a filter of zero coefficients, which leaves nothing but
// the negation of the first channel in the filtered range and the optional sums at both ends.
func TestFilteredDecorrelation(t *testing.T) {
	t.Parallel()
	const n = 300
	ra, rb := residues(n-1, 3), residues(n-1, 4)
	a, b := append([]int32{5}, ra...), append([]int32{-6}, rb...)
	for _, c := range []struct {
		mode, order  int
		dval1, dval2 bool
	}{
		{7, 8, false, false}, {7, 8, true, true}, {7, 16, true, false}, {6, 16, false, true}, {6, 8, true, true},
	} {
		frame := taktest.Frame(0, n, nil, func(w *taktest.BitWriter) {
			taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: a[0], Residues: ra})
			taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: b[0], Residues: rb})
			w.Put(0, 1)
			w.Put(uint64(c.mode), 3)
			w.Put(0, 1) // dshift 0
			w.Flag(c.order == 16)
			w.Flag(c.dval1)
			w.Flag(c.dval2)
			for i := range c.order {
				if i&3 == 0 {
					w.Put(0, 3) // 14-bit coefficients
				}
				w.PutSigned(0, 14)
			}
		})
		got, _ := decodeStream(t, append(taktest.Metadata(info22(2, 16)), frame...))

		// mode 7 filters the second channel into the first, mode 6 the other way round
		x, y := append([]int32(nil), a...), append([]int32(nil), b...)
		if c.mode == 6 {
			x, y = y, x
		}
		length := n - 1
		half := c.order / 2
		length2 := length - (c.order - 1)
		P, Q := x[1:], y[1:]
		if c.dval1 {
			for i := range half {
				P[i] += Q[i]
			}
		}
		if c.dval2 {
			for i := length2 + half; i < length; i++ {
				P[i] += Q[i]
			}
		}
		for i := half; i < half+length2; i++ {
			P[i] = -P[i]
		}
		if c.mode == 6 {
			x, y = y, x
		}
		if want := pcm16(x, y); !bytes.Equal(got, want) {
			t.Errorf("mode %d, order %d, dval %v/%v: output differs", c.mode, c.order, c.dval1, c.dval2)
		}
	}
}

func TestDecodeSegmentEscapes(t *testing.T) {
	t.Parallel()
	const mode = 9 // 6-bit codes, escape 44, scale 16, aescape 112, bias 100
	for _, c := range []struct {
		name string
		bits func(w *taktest.BitWriter)
		want int32
		err  bool
	}{
		{"plain", func(w *taktest.BitWriter) { w.Put(10, 6) }, 5, false},
		{"zigzag negative", func(w *taktest.BitWriter) { w.Put(11, 6) }, -6, false},
		{"escape flag clear", func(w *taktest.BitWriter) { w.Put(45, 6); w.Put(0, 1) }, -23, false},
		{"escape below aescape", func(w *taktest.BitWriter) { w.Put(45, 6); w.Put(1, 1) }, -33, false},
		{"unary 0", func(w *taktest.BitWriter) { w.Put(63, 6); w.Put(1, 1); w.Put(1, 1) }, -42, false},
		{"unary 2", func(w *taktest.BitWriter) { w.Put(63, 6); w.Put(1, 1); w.Put(0b100, 3) }, -58, false},
		{"unary 9, no scale bits", func(w *taktest.BitWriter) { w.Put(63, 6); w.Put(1, 1); w.Put(0, 9); w.Put(0, 3) }, -114, false},
		{"unary 9, 2 scale bits", func(w *taktest.BitWriter) { w.Put(63, 6); w.Put(1, 1); w.Put(0, 9); w.Put(2, 3); w.Put(1, 2) }, -130, false},
		{"scale of 30 bits", func(w *taktest.BitWriter) { w.Put(63, 6); w.Put(1, 1); w.Put(0, 9); w.Put(7, 3); w.Put(23, 5) }, 0, true},
	} {
		var w taktest.BitWriter
		c.bits(&w)
		d := &Decoder{gb: bitReader{buf: w.Bytes()}}
		out := make([]int32, 1)
		err := d.decodeSegment(mode, out)
		if (err != nil) != c.err || err != nil && !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if err == nil && out[0] != c.want {
			t.Errorf("%s: got %d, want %d", c.name, out[0], c.want)
		}
	}
	d := &Decoder{}
	out := []int32{1, 2, 3}
	if err := d.decodeSegment(0, out); err != nil || out[0]|out[1]|out[2] != 0 {
		t.Errorf("mode 0: %v, %v", out, err)
	}
	for _, mode := range []int{-1, 51} {
		if err := d.decodeSegment(mode, out); !errors.Is(err, ErrCorrupt) {
			t.Errorf("mode %d: err = %v", mode, err)
		}
	}
}

func TestMultipleFrames(t *testing.T) {
	t.Parallel()
	info := info22(1, 16)
	full := make([]int32, 512)
	for i := range full {
		full[i] = int32(i%7) - 3
	}
	frame0 := taktest.Frame(0, 0, &info, func(w *taktest.BitWriter) {
		taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: full[0], Residues: full[1:]})
	})
	frame1 := taktest.Frame(1, 5, nil, func(w *taktest.BitWriter) {
		taktest.StoredSamples(w, 16, [][]int32{{1, 2, 3, 4, 5}})
	})
	stream := append(taktest.Metadata(info), frame0...)
	stream = append(stream, frame1...)
	trailing := append(bytes.Clone(stream), 0xde, 0xad)

	want := append(pcm16(full), pcm16([]int32{1, 2, 3, 4, 5})...)
	for name, s := range map[string][]byte{"exact": stream, "trailing bytes": trailing} {
		got, d := decodeStream(t, s)
		if !bytes.Equal(got, want) {
			t.Errorf("%s: output differs", name)
		}
		if d.Consumed() != len(stream) {
			t.Errorf("%s: consumed %d, want %d", name, d.Consumed(), len(stream))
		}
		if d.Info().SampleRate != 22050 || d.Info().Channels != 1 {
			t.Errorf("%s: info %v", name, d.Info())
		}
	}

	// A stream with the first frame's info in its header only, and no metadata, decodes as well.
	got, _ := decodeStream(t, append(bytes.Clone(frame0), frame1...))
	if !bytes.Equal(got, want) {
		t.Error("bare frames: output differs")
	}
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()
	info := info22(2, 16)
	good := append(taktest.Metadata(info), taktest.Frame(0, 4, nil, func(w *taktest.BitWriter) {
		taktest.StoredSamples(w, 16, [][]int32{{1, 2, 3, 4}, {5, 6, 7, 8}})
	})...)
	meta := len(taktest.Metadata(info))

	flip := func(i int) []byte {
		b := bytes.Clone(good)
		b[i] ^= 0x01
		return b
	}
	cases := map[string][]byte{
		"sync":              flip(meta),
		"header CRC":        flip(meta + 5),
		"body CRC":          flip(len(good) - 1),
		"sample":            flip(meta + 9),
		"no frame":          good[:meta],
		"cut in the header": good[:meta+4],
		"cut in the body":   good[:len(good)-5],
	}
	for name, data := range cases {
		d, err := NewDecoder(data)
		if err != nil {
			t.Errorf("%s: NewDecoder: %v", name, err)
			continue
		}
		if _, err := d.Next(nil); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: err = %v, want ErrCorrupt", name, err)
		}
	}

	// Streams that are valid but not decoded.
	unsup := map[string]taktest.Info{
		"codec 3":        {Codec: 3, SampleRate: 22050, Channels: 2, BPS: 16, FrameType: 7},
		"12 bits":        {Codec: 2, SampleRate: 22050, Channels: 2, BPS: 12, FrameType: 7},
		"seven channels": {Codec: 4, SampleRate: 22050, Channels: 7, BPS: 16, FrameType: 7},
	}
	for name, in := range unsup {
		stream := append(taktest.Metadata(in), taktest.Frame(0, 4, nil, func(w *taktest.BitWriter) {
			taktest.StoredSamples(w, 16, [][]int32{{1, 2, 3, 4}, {5, 6, 7, 8}})
		})...)
		d, err := NewDecoder(stream)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := d.Next(nil); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
	d, _ := NewDecoder(append(taktest.Metadata(info22(3, 16)), good[meta:]...))
	if _, err := d.Next(nil); !errors.Is(err, ErrCorrupt) {
		t.Errorf("3 channels of codec 2: err = %v, want ErrCorrupt", err)
	}
}

// Every prefix of a valid stream fails cleanly: an error, never a panic, and never a different result than
// the whole stream gives for the frames that are whole.
func TestTruncations(t *testing.T) {
	t.Parallel()
	info := info22(2, 16)
	a, b := residues(299, 5), residues(299, 6)
	frame := taktest.Frame(0, 300, nil, func(w *taktest.BitWriter) {
		taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: 1, LPC: 1, Residues: a, Subframes: []int{1}})
		taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: 2, Residues: b})
		w.Put(0, 1)
		w.Put(3, 3)
	})
	stream := append(taktest.Metadata(info), frame...)
	for n := range stream {
		d, err := NewDecoder(stream[:n])
		if err != nil {
			continue
		}
		if _, err := d.Next(nil); err == nil {
			t.Fatalf("a stream cut to %d of %d bytes decodes", n, len(stream))
		}
	}
	if _, d := decodeStream(t, stream); d.Consumed() != len(stream) {
		t.Fatalf("consumed %d of %d", d.Consumed(), len(stream))
	}
}

// putResidues writes residues in one segment of a coding mode whose codes are width bits wide, each of them
// below the escape threshold of the mode (or, as in Mode9, with a clear escape flag after a code above it).
func putResidues(w *taktest.BitWriter, mode, width int, escape uint64, rs []int32) {
	w.Put(0, 1) // one segment
	w.Put(uint64(mode), 6)
	for _, r := range rs {
		zigzag := uint64(uint32(r<<1) ^ uint32(r>>31))
		w.Put(zigzag, width)
		if zigzag >= escape {
			w.Put(0, 1) // no escape
		}
	}
}

// putMode9 writes residues from -32 to 31, and putMode27 residues up to 11263 in magnitude (15-bit codes).
func putMode9(w *taktest.BitWriter, rs []int32)  { putResidues(w, taktest.Mode9, 6, 0x2C, rs) }
func putMode27(w *taktest.BitWriter, rs []int32) { putResidues(w, 27, 15, 0x5800, rs) }

// putChannelStart writes what a channel with one subframe, or with two when split is set (the first 88
// samples long at 22050 Hz), starts with: no shift, the first sample, no first stage predictor.
func putChannelStart(w *taktest.BitWriter, first int32, split bool) {
	w.Put(0, 1)
	w.PutSigned(int64(first), 16)
	w.Put(0, 2)
	if split {
		w.Put(1, 3)
		w.Put(1, 6) // the first subframe ends after one unit of 88 samples
	} else {
		w.Put(0, 3)
	}
}

// clip14 is a clip to the range of a 14-bit signed number.
func clip14(v int32) int32 { return max(-8192, min(v, 8191)) }

// filterTaps turns the predictors of a filter into its taps with the lattice recursion of the definition and
// the quantisation of the taps to quant bits.
func filterTaps(pred []int32, quant int) []int32 {
	order := len(pred)
	t := make([]int32, order)
	t[0] = pred[0] * 64
	for i := 1; i < order; i++ {
		for j := 0; j < (i+1)/2; j++ {
			p1, p2 := j, i-1-j
			x := t[p1] + (pred[i]*t[p2]+256)>>9
			t[p2] += (pred[i]*t[p1] + 256) >> 9
			t[p1] = x
		}
		t[i] = pred[i] * 64
	}
	q := uint(15 - quant)
	taps := make([]int32, order)
	for i := range order / 2 {
		taps[order-1-i] = int32(int16(1<<(32-q) - (t[i]+1<<(q-1))>>q))
		taps[i] = int32(int16(1<<(32-q) - (t[order-1-i]+1<<(q-1))>>q))
	}
	return taps
}

// filterOrders are the codes of the filter orders the test uses and the orders themselves.
var filterOrders = map[int]int{4: 0, 8: 1, 16: 3, 24: 4}

// TestFilteredSubframe decodes a subframe with a filter: the first samples of the subframe, as many as the
// filter has taps, are given as they are, and each of the others is
//
//	clip14((1 << (quant-1)) + the taps applied to the samples before it >> dshift) >> quant) << dshift
//
// minus its residue. The expectation is that recurrence, with the taps worked out by filterTaps. Samples that
// large make the prediction hit the clip. With continued set the history of the filter comes from the end of
// the previous, unfiltered subframe. The long frames run the filter through more than one window of history.
func TestFilteredSubframe(t *testing.T) {
	t.Parallel()
	big := []int32{9000, -11000, 10000, -11263}
	for _, c := range []struct {
		order            int
		pred             [4]int32 // two predictors of 10 bits and two of 6 bits (a multiple of 16 when applied)
		dshift, quant    int
		continued, large bool
		n                int // samples in the frame
	}{
		{4, [4]int32{300, 0, 0, 0}, 0, 10, false, false, 300},
		{4, [4]int32{-411, 0, 0, 0}, 0, 10, false, false, 300},
		{4, [4]int32{300, 0, 0, 0}, 2, 10, false, false, 300},
		{4, [4]int32{511, 0, 0, 0}, 0, 7, false, false, 300},
		{4, [4]int32{-512, 0, 0, 0}, 3, 4, false, false, 300},
		{4, [4]int32{300, -120, 5, -9}, 0, 10, false, false, 300},
		{4, [4]int32{-80, 200, -17, 22}, 1, 8, false, false, 300},
		{4, [4]int32{300, -120, 5, -9}, 0, 10, false, true, 300},
		{4, [4]int32{511, 0, 0, 0}, 0, 10, false, true, 300},
		{4, [4]int32{300, 0, 0, 0}, 0, 10, true, false, 300},
		{4, [4]int32{-411, 150, 0, 7}, 2, 7, true, false, 300},
		{4, [4]int32{-411, 150, 0, 7}, 0, 10, true, true, 300},
		{8, [4]int32{120, -60, 11, -3}, 0, 10, false, false, 300},
		{16, [4]int32{-200, 90, 5, 17}, 1, 9, false, false, 300},
		{24, [4]int32{150, -70, -9, 12}, 0, 10, false, false, 300},
		{24, [4]int32{150, -70, -9, 12}, 2, 8, true, false, 300},
		{4, [4]int32{300, -120, 5, -9}, 0, 10, false, false, 1500},
		{24, [4]int32{-100, 40, 3, -6}, 1, 10, true, false, 1500},
	} {
		first := int32(-5)
		warmLen := c.order
		if c.continued {
			warmLen = 88
		}
		residue := residues(c.n-1, uint32(c.pred[0]+int32(c.dshift)+int32(c.order)))
		// the samples the filter starts from: residues of the subframe, or of the one before it
		warm := residue[:warmLen]
		if c.large {
			warm = make([]int32, warmLen)
			for i := range warm {
				warm[i] = big[i%len(big)]
			}
		}
		putWarm := func(w *taktest.BitWriter) {
			if c.large {
				putMode27(w, warm)
			} else {
				putMode9(w, warm)
			}
		}
		// the predictors: the four coded first, then the others from a few small values
		pred := []int32{c.pred[0], c.pred[1], c.pred[2] * 16, c.pred[3] * 16}
		var more []int32
		for i := 4; i < c.order; i++ {
			more = append(more, int32(i*7%13)-6)
			pred = append(pred, more[len(more)-1]*16)
		}
		frame := taktest.Frame(0, c.n, nil, func(w *taktest.BitWriter) {
			putChannelStart(w, first, c.continued)
			if c.continued {
				w.Flag(false) // the first subframe has no filter
				putWarm(w)
			}
			w.Flag(true) // a filter
			w.Put(uint64(filterOrders[c.order]), 4)
			if c.continued {
				w.Flag(true) // that goes on from the previous subframe
			} else {
				w.Put(0, 2) // no first stage predictor
				putWarm(w)
			}
			if c.dshift > 0 {
				w.Put(1, 1)
				w.Put(uint64(c.dshift-1), 4)
			} else {
				w.Put(0, 1)
			}
			w.Put(0, 1) // predictors of 6 bits
			if c.quant != 10 {
				w.Put(1, 1)
				w.Put(uint64(10-c.quant-1), 3)
			} else {
				w.Put(0, 1)
			}
			w.PutSigned(int64(c.pred[0]), 10)
			w.PutSigned(int64(c.pred[1]), 10)
			w.PutSigned(int64(c.pred[2]), 6)
			w.PutSigned(int64(c.pred[3]), 6)
			if c.order > 4 {
				w.Put(0, 1) // as wide as the others
				for i, p := range more {
					if i%4 == 0 {
						w.Put(0, 2)
					}
					w.PutSigned(int64(p), 6)
				}
			}
			putMode9(w, residue[warmLen:])
		})
		got, _ := decodeStream(t, append(taktest.Metadata(info22(1, 16)), frame...))

		taps := filterTaps(pred, c.quant)
		samples := append([]int32{first}, warm...)
		hit := false
		for _, e := range residue[warmLen:] {
			from := len(samples) - c.order
			v := int32(1) << (c.quant - 1)
			for i, tap := range taps {
				v += samples[from+i] >> c.dshift * tap
			}
			p := v >> c.quant
			hit = hit || p != clip14(p)
			samples = append(samples, clip14(p)<<c.dshift-e)
		}
		if c.large && !hit {
			t.Errorf("%+v: the prediction never reaches the clip", c)
		}
		if want := pcm16(samples); !bytes.Equal(got, want) {
			t.Errorf("%+v: output differs", c)
		}
	}
}

// TestResidueSegments codes the residues of a subframe in segments with their own coding modes, reached by
// every kind of step from one mode to the next, and expects the residues back.
func TestResidueSegments(t *testing.T) {
	t.Parallel()
	const n = 300 // 299 residues: at 22050 Hz segments of 88, 88 and 123
	// the width of the codes of the modes used, each used with residues that need no escape code
	widths := map[int]int{8: 5, 9: 6, 10: 6, 11: 7, 12: 7, 13: 8, 20: 11}
	for _, c := range []struct {
		name  string
		modes [3]int
		steps func(w *taktest.BitWriter)
	}{
		{"same mode", [3]int{9, 9, 9}, func(w *taktest.BitWriter) { w.Put(1, 1); w.Put(1, 1) }},
		{"up and down by one", [3]int{9, 10, 9}, func(w *taktest.BitWriter) { w.Put(0b100, 3); w.Put(0b10, 2) }},
		{"by two and three", [3]int{9, 11, 8}, func(w *taktest.BitWriter) {
			w.Put(0b1000, 4)
			w.Put(0, 1) // up: a step of c-1 = 2
			w.Put(0b10000, 5)
			w.Put(1, 1) // down: a step of c-1 = 3, written with c = 4
		}},
		{"by four", [3]int{8, 12, 8}, func(w *taktest.BitWriter) {
			w.Put(0b100000, 6)
			w.Put(0, 1)
			w.Put(0b100000, 6)
			w.Put(1, 1)
		}},
		{"absolute", [3]int{13, 20, 9}, func(w *taktest.BitWriter) {
			w.Put(0, 6)
			w.Put(20, 6)
			w.Put(0, 6)
			w.Put(9, 6)
		}},
	} {
		res := make([]int32, n-1)
		for i := range res {
			res[i] = int32(i%11) - 5 // zigzag 0..10: below every escape threshold
		}
		frame := taktest.Frame(0, n, nil, func(w *taktest.BitWriter) {
			putChannelStart(w, 7, false)
			w.Flag(false) // no filter
			w.Flag(true)  // segments
			w.Put(uint64(c.modes[0]), 6)
			c.steps(w)
			pos := 0
			for i, size := range []int{88, 88, 123} {
				for _, r := range res[pos : pos+size] {
					w.Put(uint64(uint32(r<<1)^uint32(r>>31)), widths[c.modes[i]])
				}
				pos += size
			}
		})
		got, _ := decodeStream(t, append(taktest.Metadata(info22(1, 16)), frame...))
		if want := pcm16(append([]int32{7}, res...)); !bytes.Equal(got, want) {
			t.Errorf("%s: output differs", c.name)
		}
	}
}

// TestMultichannel decodes three channels of a multichannel frame: each by itself, and then with a channel
// list that joins channel 2 with channel 0 as side and mid (the pair is coded channel 0 first).
func TestMultichannel(t *testing.T) {
	t.Parallel()
	const n = 300
	info := taktest.Info{Codec: CodecMultichannel, SampleRate: 22050, Channels: 3, BPS: 16, FrameType: 7}
	res := [3][]int32{residues(n-1, 11), residues(n-1, 12), residues(n-1, 13)}
	first := [3]int32{3, -4, 5}
	channel := func(w *taktest.BitWriter, c int) {
		taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: first[c], Residues: res[c]})
	}
	whole := func(c int) []int32 { return append([]int32{first[c]}, res[c]...) }

	plain := taktest.Frame(0, n, nil, func(w *taktest.BitWriter) {
		w.Flag(false) // no channel list
		for c := range 3 {
			channel(w, c)
		}
	})
	got, _ := decodeStream(t, append(taktest.Metadata(info), plain...))
	if want := pcm16(whole(0), whole(1), whole(2)); !bytes.Equal(got, want) {
		t.Error("channels without a list: output differs")
	}

	joined := taktest.Frame(0, n, nil, func(w *taktest.BitWriter) {
		w.Flag(true)
		w.Put(1, 4) // 2 groups
		w.Put(2, 4) // group 0: channel 2,
		w.Flag(true)
		w.Put(1, 2) // joined with a channel coded before it (index 1: side/mid),
		w.Put(0, 4) // channel 0
		w.Put(1, 4) // group 1: channel 1,
		w.Flag(false)
		channel(w, 0)
		channel(w, 2)
		channel(w, 1)
	})
	got, _ = decodeStream(t, append(taktest.Metadata(info), joined...))
	a, b := whole(0), whole(2)
	for i := 1; i < n; i++ { // the first sample is never joined
		m := a[i] - b[i]>>1
		a[i], b[i] = m, m+b[i]
	}
	if want := pcm16(a, whole(1), b); !bytes.Equal(got, want) {
		t.Error("channels in a list: output differs")
	}
}
