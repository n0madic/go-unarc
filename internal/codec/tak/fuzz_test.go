package tak

import (
	"errors"
	"io"
	"testing"

	"github.com/n0madic/go-unarc/internal/codec/tak/taktest"
)

// codedBody writes the body of a stereo frame of 300 samples with coded channels (subframes, a predictor, a
// shift) joined in one of the 8 decorrelation modes.
func codedBody(mode int) func(w *taktest.BitWriter) {
	const n = 300
	return func(w *taktest.BitWriter) {
		taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: 1, LPC: mode % 4, Residues: residues(n-1, 1), Subframes: []int{1}})
		taktest.WriteChannel(w, 16, scale22, taktest.Channel{First: 2, Shift: mode % 3, Residues: residues(n-1, 2)})
		w.Put(0, 1)
		w.Put(uint64(mode), 3)
		switch mode {
		case 4, 5:
			w.Put(1, 1)
			w.Put(1, 4)
			w.PutSigned(100, 10)
		case 6, 7:
			w.Put(0, 1)
			w.Put(0, 3)
			w.Put(0, 3)
			for i := range 8 {
				if i&3 == 0 {
					w.Put(0, 3)
				}
				w.PutSigned(int64(i), 14)
			}
		}
	}
}

// fuzzSeeds are valid streams for the fuzzer to mutate: stored samples, and coded channels joined each way.
func fuzzSeeds() [][]byte {
	stored := append(taktest.Metadata(info22(2, 16)), taktest.Frame(0, 4, nil, func(w *taktest.BitWriter) {
		taktest.StoredSamples(w, 16, [][]int32{{1, 2, 3, 4}, {5, 6, 7, 8}})
	})...)
	seeds := [][]byte{realMetadata, stored}
	for mode := range 8 {
		seeds = append(seeds, append(taktest.Metadata(info22(2, 16)), taktest.Frame(0, 300, nil, codedBody(mode))...))
	}
	return seeds
}

// FuzzDecoder feeds arbitrary bytes to the decoder: it must neither panic nor hang, and may only fail with
// ErrCorrupt or ErrUnsupported.
func FuzzDecoder(f *testing.F) {
	for _, s := range fuzzSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		check := func(err error) bool {
			if err != nil && !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrUnsupported) && err != io.EOF {
				t.Fatalf("unexpected error: %v", err)
			}
			return err == nil
		}
		d, err := NewDecoder(data)
		if !check(err) {
			return
		}
		var buf []byte
		for range 64 {
			buf, err = d.Next(buf[:0])
			if !check(err) {
				return
			}
		}
	})
}

// FuzzFrameBody gives the decoder a frame whose header and CRCs are right and whose body is arbitrary, which
// is what reaches the decoding of residues, predictors and channel joining: damaged streams stop at a CRC.
func FuzzFrameBody(f *testing.F) {
	for mode := range 8 {
		var w taktest.BitWriter
		codedBody(mode)(&w)
		f.Add(byte(0), uint16(300), w.Bytes())
	}
	f.Add(byte(1), uint16(20), []byte{})
	f.Fuzz(func(t *testing.T, format byte, last uint16, body []byte) {
		info := taktest.Info{
			Codec:      []int{CodecMonoStereo, CodecMultichannel}[format&1],
			SampleRate: []int{22050, 44100, 8000, 192000}[format>>1&3],
			Channels:   int(format>>3&7)%6 + 1,
			BPS:        8 * (int(format>>6&3)%3 + 1),
			FrameType:  7 + int(format>>2&1),
		}
		samples := int(last)%2048 + 1
		frame := taktest.Frame(0, samples, nil, func(w *taktest.BitWriter) {
			for _, b := range body {
				w.Put(uint64(b), 8)
			}
		})
		d, err := NewDecoder(append(taktest.Metadata(info), frame...))
		if err != nil {
			t.Fatalf("NewDecoder: %v", err)
		}
		if _, err := d.Next(nil); err != nil && !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
