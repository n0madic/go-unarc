package tta

import (
	"bytes"
	"encoding/binary"
	"github.com/n0madic/go-unarc/internal/codectest"
	"io"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

// readEnd reports whether the C decoder gave up with FREEARC_ERRCODE_READ (-6), which it does at the clean end
// of a stream and when the stream is truncated; the output before that point is still delivered.
func readEnd(r codectest.Result) bool { return r.Exit == 1 && strings.Contains(r.Stderr, "failed: -6") }

// finished reports that the C decoder ran to its end or to a read failure (it did not crash).
func finished(r codectest.Result) bool { return r.Exit == 0 || readEnd(r) }

// decodeStream decodes a stream with the Go decoder.
func decodeStream(stream []byte) ([]byte, error) {
	var out bytes.Buffer
	err := codec{}.Decode(&out, bytes.NewReader(stream))
	return out.Bytes(), err
}

// signal kinds of genPCM
const (
	sigTone    = "tone"    // two sines and a little noise
	sigNoise   = "noise"   // uniform noise over the whole range
	sigExtreme = "extreme" // runs of the extreme values
	sigWalk    = "walk"    // random walk
	sigSilence = "silence"
)

// genPCM generates frames sample frames of interleaved PCM: unsigned bytes for 8 bits, little-endian
// two's complement for 16 and 24 bits and IEEE single precision for 32 bits.
func genPCM(seed uint64, kind string, channels, bits, frames int) []byte {
	r := rand.New(rand.NewPCG(seed, 0x7a7a))
	width := bits / 8
	out := make([]byte, 0, frames*channels*width)
	// the range of the integer sample is [-peak-1, peak]
	peak := float64(int64(1)<<(min(bits, 24)-1) - 1)
	// The C encoder corrupts frames whose bit array outgrows its first megabyte (put_unary/put_binary leave stale bits
	// in the memory realloc adds), so the signals are made to compress to less than that: little noise at 24 bits,
	// floats with the precision of 16 bits.
	noise := 0.01
	if bits == 24 {
		noise = 0.0002
	}
	walk := make([]float64, channels)
	run := make([]int, channels)
	level := make([]float64, channels)
	for i := range frames {
		for c := range channels {
			var v float64 // in [-1, 1]
			switch kind {
			case sigTone:
				ph := float64(i) * 0.011 * float64(c+1)
				v = 0.45*math.Sin(ph) + 0.2*math.Sin(3.1*ph+1) + noise*r.NormFloat64()
			case sigNoise:
				v = r.Float64()*2 - 1
			case sigExtreme:
				if run[c] == 0 {
					run[c] = 1 + r.IntN(40)
					level[c] = float64(r.IntN(3)-1) + 0.0
				}
				run[c]--
				v = level[c]
			case sigWalk:
				walk[c] += r.NormFloat64() * 0.02
				walk[c] = max(-1, min(1, walk[c]))
				v = walk[c]
			case sigSilence:
			}
			v = max(-1, min(1, v))
			switch bits {
			case 8:
				out = append(out, byte(int(math.Round(v*127))+128))
			case 16:
				out = binary.LittleEndian.AppendUint16(out, uint16(int16(math.Round(v*peak))))
			case 24:
				x := int32(math.Round(v * peak))
				out = append(out, byte(x), byte(x>>8), byte(x>>16))
			case 32:
				if kind != sigNoise {
					v = math.Round(v*32768) / 32768
				}
				out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(v)))
			}
		}
	}
	return out
}

// wavHeader is the 44-byte header of a PCM WAV file.
func wavHeader(channels, bits, dataLen int, float bool) []byte {
	h := make([]byte, 0, 44)
	h = append(h, "RIFF"...)
	h = binary.LittleEndian.AppendUint32(h, uint32(36+dataLen))
	h = append(h, "WAVEfmt "...)
	h = binary.LittleEndian.AppendUint32(h, 16)
	format := uint16(1)
	if float {
		format = 3
	}
	h = binary.LittleEndian.AppendUint16(h, format)
	h = binary.LittleEndian.AppendUint16(h, uint16(channels))
	h = binary.LittleEndian.AppendUint32(h, 44100)
	h = binary.LittleEndian.AppendUint32(h, uint32(44100*channels*bits/8))
	h = binary.LittleEndian.AppendUint16(h, uint16(channels*bits/8))
	h = binary.LittleEndian.AppendUint16(h, uint16(bits))
	h = append(h, "data"...)
	return binary.LittleEndian.AppendUint32(h, uint32(dataLen))
}

// makeWAV returns a WAV file of the given signal; tail extra bytes follow the data.
func makeWAV(seed uint64, kind string, channels, bits, frames, tail int) []byte {
	pcm := genPCM(seed, kind, channels, bits, frames)
	wav := append(wavHeader(channels, bits, len(pcm), bits == 32), pcm...)
	for i := range tail {
		wav = append(wav, byte(0xA0+i))
	}
	return wav
}

// stream layout helpers -------------------------------------------------------------------------------

// frameInfo locates the parts of one frame of a (non-raw, level>0) stream.
type frameInfo struct {
	start      int // offset of the frame header
	bytesRead  int
	compressed int // size of the bit array; 0 for a stored frame
	payload    int // offset of the bit array (or of the stored bytes)
	end        int // offset after the frame, rest bytes included
}

// parseFrames walks over the frames of a valid stream and returns the header fields and the frames.
func parseFrames(t testing.TB, stream []byte) (hdr [4]byte, offset int, frames []frameInfo) {
	t.Helper()
	copy(hdr[:], stream)
	unit := int(hdr[2]) * ((int(hdr[3]) + 7) / 8)
	raw := hdr[1]/2 != 0
	planes := int(hdr[2]) * (1 + int(hdr[1]&1))
	offset = int(binary.LittleEndian.Uint32(stream[4:]))
	pos := 8 + offset
	for pos < len(stream) {
		f := frameInfo{start: pos}
		f.bytesRead = int(binary.LittleEndian.Uint32(stream[pos:]))
		pos += 4
		switch {
		case raw:
			f.payload = pos
			pos += f.bytesRead/unit*planes*4 + f.bytesRead%unit
		default:
			f.compressed = int(binary.LittleEndian.Uint32(stream[pos:]))
			pos += 4
			f.payload = pos
			if f.compressed == 0 {
				pos += f.bytesRead
			} else {
				pos += f.compressed + f.bytesRead%unit
			}
		}
		f.end = pos
		frames = append(frames, f)
	}
	return hdr, offset, frames
}

// readAll is io.ReadAll that fails the test.
func readAll(t testing.TB, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
