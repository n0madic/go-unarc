package tta

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/n0madic/go-unarc/internal/codectest"
	"math/rand/v2"
	"testing"
)

// rawStream builds a stream in raw mode: the planes hold arbitrary int32 values, which the decoder runs through
// the filters. Nothing in it needs the entropy coder, so any bytes are a valid stream.
func rawStream(r *rand.Rand, level int, isFloat bool, numChan, wordSize, frameLen, rest int, wide uint32) []byte {
	flags := byte(2)
	planes := numChan
	if isFloat {
		flags |= 1
		planes *= 2
	}
	unit := numChan * ((wordSize + 7) / 8)
	rest = min(rest, unit-1)
	s := []byte{byte(level), flags, byte(numChan), byte(wordSize)}
	s = binary.LittleEndian.AppendUint32(s, 0) // no header of the original file
	s = binary.LittleEndian.AppendUint32(s, uint32(frameLen*unit+rest))
	for range frameLen * planes {
		var v int32
		switch x := r.Uint32(); {
		case x%100 < wide:
			v = int32(r.Uint32())
		case x%3 == 0:
			v = int32(r.IntN(1<<20) - 1<<19)
		default:
			v = int32(r.IntN(41) - 20)
		}
		s = binary.LittleEndian.AppendUint32(s, uint32(v))
	}
	for i := range rest {
		s = append(s, byte(0x30+i))
	}
	return s
}

// The C decoder is the reference for the arithmetic of the filters, wrap-around included: feed both the same
// raw frames full of arbitrary values.
func TestRawStreamsMatchC(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	r := rand.New(rand.NewPCG(11, 12))
	for _, level := range []int{1, 2, 3} {
		for _, shape := range []struct {
			name              string
			float             bool
			channels, wordLen int
		}{
			{"8bit mono", false, 1, 8}, {"8bit stereo", false, 2, 8}, {"16bit mono", false, 1, 16},
			{"16bit stereo", false, 2, 16}, {"24bit stereo", false, 2, 24}, {"24bit 3ch", false, 3, 24},
			{"float mono", true, 1, 32}, {"float stereo", true, 2, 32},
		} {
			for _, wide := range []uint32{0, 20, 100} {
				name := fmt.Sprintf("m%d/%s/wide%d", level, shape.name, wide)
				stream := rawStream(r, level, shape.float, shape.channels, shape.wordLen, 3000, 1, wide)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					got, err := decodeStream(stream)
					if err != nil {
						t.Fatalf("Decode: %v", err)
					}
					res := codectest.FagenTry(t, stream, "d", "tta")
					want := res.Out
					if !finished(res) {
						t.Fatalf("fagen d: exit %d: %s", res.Exit, res.Stderr)
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("output differs from the C decoder (%d bytes, want %d)", len(got), len(want))
					}
				})
			}
		}
	}
}

// mutate returns a copy of stream with the bit array of one frame damaged.
func mutate(r *rand.Rand, stream []byte, f frameInfo) []byte {
	out := bytes.Clone(stream)
	payload := out[f.payload : f.payload+f.compressed]
	switch r.IntN(5) {
	case 0: // one flipped bit
		payload[r.IntN(len(payload))] ^= 1 << r.IntN(8)
	case 1: // a few random bytes
		for range 1 + r.IntN(4) {
			payload[r.IntN(len(payload))] = byte(r.Uint32())
		}
	case 2: // a run of ones: long unary parts and the unary escape
		at := r.IntN(len(payload))
		for i := range 5 + r.IntN(10) {
			if at+i < len(payload) {
				payload[at+i] = 0xFF
			}
		}
	case 3: // a run of zeros
		at := r.IntN(len(payload))
		for i := range 1 + r.IntN(20) {
			if at+i < len(payload) {
				payload[at+i] = 0
			}
		}
	case 4: // a burst of garbage
		at := r.IntN(len(payload))
		for i := range 16 {
			if at+i < len(payload) {
				payload[at+i] = byte(r.Uint32())
			}
		}
	}
	return out
}

// Damaged bit arrays decode to garbage: sample values far outside anything an encoder makes, huge Rice
// parameters, sums that wrap. The Go decoder must give the C decoder's garbage, as long as the C decoder does not read
// past its bit array (the Go decoder refuses those).
func TestMutatedStreamsMatchC(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	for _, base := range []struct {
		method   string
		channels int
		bits     int
		kind     string
	}{
		{"tta:m3:2*16:o44", 2, 16, sigTone},
		{"tta:m1:1*8:o44", 1, 8, sigWalk},
		{"tta:m2:2*24:o44", 2, 24, sigTone},
		{"tta:m3:2*32f:o44", 2, 32, sigTone},
		{"tta:m3:3*16:o44", 3, 16, sigWalk},
	} {
		t.Run(base.method, func(t *testing.T) {
			t.Parallel()
			data := makeWAV(9, base.kind, base.channels, base.bits, 3000, 2)
			stream := codectest.Fagen(t, data, "c", base.method)
			_, _, frames := parseFrames(t, stream)
			r := rand.New(rand.NewPCG(21, uint64(base.bits)))
			compared, skipped := 0, 0
			for i := range 150 {
				bad := mutate(r, stream, frames[0])
				got, err := decodeStream(bad)
				if err != nil {
					skipped++ // the bit array ran out: the C decoder reads whatever follows it
					continue
				}
				res := codectest.FagenTry(t, bad, "d", "tta")
				want := res.Out
				if !finished(res) {
					t.Fatalf("mutation %d: fagen d: exit %d: %s", i, res.Exit, res.Stderr)
				}
				if !bytes.Equal(got, want) {
					first := 0
					for first < min(len(got), len(want)) && got[first] == want[first] {
						first++
					}
					t.Fatalf("mutation %d: output differs from the C decoder (%d bytes, want %d; first difference at %d: % x vs % x)",
						i, len(got), len(want), first, got[first:min(first+12, len(got))], want[first:min(first+12, len(want))])
				}
				compared++
			}
			t.Logf("compared %d damaged streams, %d refused by the Go decoder", compared, skipped)
			if compared < 30 {
				t.Errorf("only %d damaged streams were comparable", compared)
			}
		})
	}
}

// A stream cut anywhere decodes to the same output as in C up to the failure, and fails except right after
// a complete frame.
func TestTruncatedStreamsMatchC(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	for _, base := range []struct {
		method string
		frames int
		raw    bool
	}{
		{"tta:m3:2*16:o44", 1<<18 + 300, false},
		{"tta:r1:m2:2*16:o44", 1<<18 + 300, true},
		{"tta:m3:2*16:o44", 3, false}, // a stored first frame (the only one is shorter than its bit array)
	} {
		t.Run(base.method, func(t *testing.T) {
			t.Parallel()
			kind := sigTone
			if base.frames < 10 {
				kind = sigNoise
			}
			data := makeWAV(4, kind, 2, 16, base.frames, 3)
			stream := codectest.Fagen(t, data, "c", base.method)
			_, offset, frames := parseFrames(t, stream)

			cuts := map[int]bool{}
			for i := 0; i <= 12+offset; i++ {
				cuts[i] = true
			}
			boundaries := map[int]bool{}
			for _, f := range frames {
				boundaries[f.start] = true
				for d := -6; d <= 9; d++ {
					cuts[f.start+d] = true
					cuts[f.end+d] = true
				}
				cuts[f.payload+(f.end-f.payload)/2] = true
			}
			r := rand.New(rand.NewPCG(8, 9))
			for range 15 {
				cuts[r.IntN(len(stream))] = true
			}
			for cut := range cuts {
				if cut < 0 || cut > len(stream) {
					continue
				}
				prefix := stream[:cut]
				got, err := decodeStream(prefix)
				res := codectest.FagenTry(t, prefix, "d", "tta")
				want := res.Out
				if !finished(res) {
					t.Fatalf("cut %d: fagen d: exit %d: %s", cut, res.Exit, res.Stderr)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("cut %d: output differs from the C decoder (%d bytes, want %d)", cut, len(got), len(want))
				}
				// C ends with -6 (a read ran short) in every case but a stored stream; Go only at a frame boundary.
				atBoundary := cut == len(stream) || boundaries[cut]
				if (err == nil) != atBoundary {
					t.Fatalf("cut %d: err = %v, at a frame boundary: %v", cut, err, atBoundary)
				}
			}
		})
	}
}
