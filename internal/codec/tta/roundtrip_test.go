package tta

import (
	"bytes"
	"fmt"
	"github.com/n0madic/go-unarc/internal/codectest"
	"testing"
)

// checkRoundTrip compresses data with the C encoder, decodes it with the Go decoder and the C decoder and
// compares everything. It returns the stream.
func checkRoundTrip(t *testing.T, method string, data []byte) []byte {
	t.Helper()
	stream := codectest.Fagen(t, data, "c", method)
	got, err := decodeStream(stream)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("Decode output differs from the original (%d bytes, want %d)", len(got), len(data))
	}
	// The C decoder is the reference; it reports FREEARC_ERRCODE_READ at the clean end of the stream.
	res := codectest.FagenTry(t, stream, "d", method)
	want := res.Out
	if !finished(res) {
		t.Fatalf("fagen d: exit %d: %s", res.Exit, res.Stderr)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Decode output differs from the C decoder (%d bytes, want %d)", len(got), len(want))
	}
	t.Logf("%s: %d -> %d bytes, header % x", method, len(data), len(stream), stream[:min(4, len(stream))])
	return stream
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)

	type tc struct {
		name     string
		method   string
		channels int
		bits     int
		frames   int
		kind     string
		tail     int
	}
	var tests []tc
	// Every level, sample size and channel count the encoder takes.
	for _, level := range []int{1, 2, 3} {
		for _, bits := range []int{8, 16, 24} {
			for _, ch := range []int{1, 2} {
				tests = append(tests, tc{
					name:     fmt.Sprintf("m%d/%dbit/%dch", level, bits, ch),
					method:   fmt.Sprintf("tta:m%d:%d*%d:o44", level, ch, bits),
					channels: ch, bits: bits, frames: 20000, kind: sigTone,
				})
			}
		}
	}
	tests = append(tests,
		// WAV header detection and the default level.
		tc{"autodetect/16bit stereo", "tta", 2, 16, 30000, sigTone, 0},
		tc{"autodetect/8bit mono", "tta", 1, 8, 30000, sigWalk, 0},
		tc{"autodetect/24bit stereo", "tta", 2, 24, 30000, sigTone, 0},
		tc{"skip header detection", "tta:s", 2, 16, 30000, sigTone, 0},
		// More channels than a WAV usually has.
		tc{"3ch", "tta:3*16:o44", 3, 16, 5000, sigTone, 0},
		tc{"4ch 8bit", "tta:c4:w8:o44", 4, 8, 5000, sigWalk, 0},
		tc{"6ch 24bit", "tta:6*24:o44", 6, 24, 3000, sigTone, 0},
		tc{"255ch", "tta:255*8:o44", 255, 8, 300, sigWalk, 0},
		// Sizes: empty, tiny, odd, around the frame length of 2^18 samples, trailing bytes that do not fill a sample.
		tc{"no samples", "tta:2*16:o44", 2, 16, 0, sigTone, 0},
		tc{"one sample", "tta:2*16:o44", 2, 16, 1, sigTone, 0},
		tc{"one sample and a tail", "tta:2*16:o44", 2, 16, 1, sigTone, 3},
		tc{"tail only", "tta:2*16:o44", 2, 16, 0, sigTone, 3},
		tc{"3 samples 24bit tail", "tta:2*24:o44", 2, 24, 3, sigTone, 5},
		tc{"odd length", "tta:2*16:o44", 2, 16, 7001, sigTone, 1},
		tc{"frame minus one", "tta:2*16:o44", 2, 16, 1<<18 - 1, sigTone, 0},
		tc{"frame", "tta:2*16:o44", 2, 16, 1 << 18, sigWalk, 3},
		tc{"frame plus one", "tta:2*16:o44", 2, 16, 1<<18 + 1, sigTone, 0},
		tc{"two frames mono 8bit", "tta:1*8:o44", 1, 8, 2<<18 + 77, sigWalk, 0},
		// Signals: incompressible (stored frames), clipping and silence.
		tc{"noise 16bit", "tta:2*16:o44", 2, 16, 9000, sigNoise, 0},
		tc{"noise 8bit", "tta:1*8:o44", 1, 8, 9000, sigNoise, 0},
		tc{"noise 24bit", "tta:2*24:o44", 2, 24, 9000, sigNoise, 0},
		tc{"extreme 16bit", "tta:2*16:o44", 2, 16, 20000, sigExtreme, 0},
		tc{"extreme 24bit", "tta:2*24:o44", 2, 24, 20000, sigExtreme, 0},
		tc{"extreme 8bit", "tta:2*8:o44", 2, 8, 20000, sigExtreme, 0},
		tc{"silence", "tta:2*16:o44", 2, 16, 30000, sigSilence, 0},
		// The offset of the audio data is copied as it is, with no header detection at all.
		tc{"offset 0", "tta:2*16", 2, 16, 4000, sigTone, 0},
		tc{"offset 100", "tta:2*16:o100", 2, 16, 4000, sigTone, 0},
		// Raw predictor output, no entropy coder.
		tc{"raw 16bit", "tta:r1:2*16:o44", 2, 16, 20000, sigTone, 5},
		tc{"raw 8bit mono", "tta:r1:1*8:o44", 1, 8, 5000, sigWalk, 0},
		tc{"raw 24bit", "tta:r1:m1:2*24:o44", 2, 24, 5000, sigTone, 1},
		tc{"raw empty", "tta:r1:2*16:o44", 2, 16, 0, sigTone, 1},
		// Floating point: split into an exponent and a mantissa channel.
		tc{"float stereo", "tta:2*32f:o44", 2, 32, 20000, sigTone, 0},
		tc{"float mono level 1", "tta:m1:1*32f:o44", 1, 32, 20000, sigWalk, 0},
		tc{"float flag", "tta:f:c2:w32:o44", 2, 32, 5000, sigTone, 2},
		tc{"float noise", "tta:2*32f:o44", 2, 32, 5000, sigNoise, 0},
		tc{"float extreme", "tta:3*32f:o44", 3, 32, 5000, sigExtreme, 0},
		tc{"float raw", "tta:r1:2*32f:o44", 2, 32, 3000, sigTone, 0},
		// Stored.
		tc{"level 0", "tta:m0", 2, 16, 3000, sigTone, 0},
	)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := makeWAV(uint64(len(tt.name)), tt.kind, tt.channels, tt.bits, tt.frames, tt.tail)
			checkRoundTrip(t, tt.method, data)
		})
	}
}

// Random bit patterns exercise every float: NaNs, infinities, denormals, negative zero.
func TestRoundTripFloatPatterns(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	data := makeWAV(5, sigNoise, 2, 32, 4000, 0)
	for i := 44; i+4 <= len(data); i += 4 {
		// reinterpret the noise as raw bits for half of the samples
		if i%8 == 0 {
			data[i], data[i+1] = data[i+1]^0x5a, data[i]^0xa5
			data[i+3] = data[i+3] ^ byte(i)
		}
	}
	checkRoundTrip(t, "tta:2*32f:o44", data)
}
