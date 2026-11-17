package tta

import (
	"bytes"
	"github.com/n0madic/go-unarc/internal/codectest"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// BenchmarkDecode decodes streams of the C encoder into io.Discard; SetBytes counts the decoded bytes.
// TTA_BENCH_FRAMES sets the signal length, TTA_BENCH_SAVE a folder to keep the streams in and TTA_BENCH_DIR one
// to read them from instead of generating them.
//
// benchFrames is the length of the benchmark signal in sample frames: TTA_BENCH_FRAMES, by default 2M.
func benchFrames() int {
	if n, err := strconv.Atoi(os.Getenv("TTA_BENCH_FRAMES")); err == nil && n > 0 {
		return n
	}
	return 2_000_000
}

func BenchmarkDecode(b *testing.B) {
	for _, bb := range []struct {
		name     string
		method   string
		channels int
		bits     int
	}{
		{"stereo16", "tta:2*16:o44", 2, 16},
		{"mono8", "tta:1*8:o44", 1, 8},
		{"stereo24", "tta:2*24:o44", 2, 24},
		{"stereo float", "tta:2*32f:o44", 2, 32},
	} {
		b.Run(bb.name, func(b *testing.B) {
			var stream []byte
			var size int
			if dir := os.Getenv("TTA_BENCH_DIR"); dir != "" {
				// a stream saved by an earlier run, to time the same one against the C decoder
				var err error
				if stream, err = os.ReadFile(filepath.Join(dir, bb.name+".tta")); err != nil {
					b.Skip(err)
				}
				size = len(stream) // the original size is not known here
				if out, err := decodeStream(stream); err == nil {
					size = len(out)
				}
			} else {
				data := makeWAV(77, sigTone, bb.channels, bb.bits, benchFrames(), 0)
				stream = codectest.Fagen(b, data, "c", bb.method)
				size = len(data)
				b.Logf("%d bytes -> %d", len(data), len(stream))
				if p := os.Getenv("TTA_BENCH_SAVE"); p != "" {
					if err := os.WriteFile(filepath.Join(p, bb.name+".tta"), stream, 0o644); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				if err := (codec{}).Decode(io.Discard, bytes.NewReader(stream)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkEntropy times the entropy decoder alone on the first frame of a stream saved by BenchmarkDecode.
func BenchmarkEntropy(b *testing.B) {
	dir := os.Getenv("TTA_BENCH_DIR")
	if dir == "" {
		b.Skip("TTA_BENCH_DIR not set")
	}
	stream, err := os.ReadFile(filepath.Join(dir, "stereo16.tta"))
	if err != nil {
		b.Skip(err)
	}
	_, _, frames := parseFrames(b, stream)
	f := frames[0]
	data := append(append([]byte{}, stream[f.payload:f.payload+f.compressed]...), make([]byte, bitArrayPad)...)
	frameLen := f.bytesRead / 4
	out := make([]int32, 2*frameLen)
	b.SetBytes(int64(f.bytesRead))
	for b.Loop() {
		pos := uint64(0)
		for p := range 2 {
			var err error
			if pos, err = decodeFrame(out[p*frameLen:(p+1)*frameLen], data, pos, uint64(f.compressed)*8); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkFilters times the filters alone (level 3, 16 bits) on the decoded frame.
func BenchmarkFilters(b *testing.B) {
	dir := os.Getenv("TTA_BENCH_DIR")
	if dir == "" {
		b.Skip("TTA_BENCH_DIR not set")
	}
	stream, err := os.ReadFile(filepath.Join(dir, "stereo16.tta"))
	if err != nil {
		b.Skip(err)
	}
	_, _, frames := parseFrames(b, stream)
	f := frames[0]
	data := append(append([]byte{}, stream[f.payload:f.payload+f.compressed]...), make([]byte, bitArrayPad)...)
	frameLen := f.bytesRead / 4
	src := make([]int32, frameLen)
	if _, err := decodeFrame(src, data, 0, uint64(f.compressed)*8); err != nil {
		b.Fatal(err)
	}
	work := make([]int32, frameLen)
	var fs filterSet
	b.SetBytes(int64(frameLen) * 2)
	for b.Loop() {
		copy(work, src)
		fs.decodeChannel(work, 3, 2)
	}
}
