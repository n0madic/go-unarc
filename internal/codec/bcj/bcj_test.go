package bcj

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

// x86 returns data dense in E8/E9 opcodes with addresses of every kind, including runs of E8 bytes.
func x86(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 9))
	b := make([]byte, 0, n+8)
	for len(b) < n {
		switch r.IntN(5) {
		case 0, 1:
			b = append(b, 0xE8+byte(r.IntN(2)))
			b = binary.LittleEndian.AppendUint32(b, uint32(int32(r.IntN(1<<20)-1<<19)))
		case 2:
			b = append(b, 0xE8, 0xE8, 0xE9, byte(r.IntN(256)), 0xFF, 0x00)
		default:
			b = append(b, byte(r.IntN(256)))
		}
	}
	return b[:n]
}

func TestRoundTripFagen(t *testing.T) {
	codectest.FagenPath(t)
	exe, _ := os.ReadFile(filepath.Join(runtime.GOROOT(), "bin", "gofmt"))
	inputs := map[string][]byte{
		"empty": nil, "1": {0xE8}, "5": {0xE8, 1, 2, 3, 0}, "6": {0xE8, 1, 2, 3, 0, 9},
		"synthetic": x86(1, 1<<20+3), "block edge": x86(2, bufferSize+2), "binary": exe,
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			packed := codectest.Fagen(t, in, "c", "exe")
			var out bytes.Buffer
			if err := (codec{}).Decode(&out, bytes.NewReader(packed)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), in) {
				t.Fatal("decoded data differs")
			}
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	for m, ok := range map[string]bool{"exe": true, "exe:1": false, "exe2": false} {
		if _, got := Parse(method.Params(m), nil); got != ok {
			t.Errorf("Parse(%q) = %v, want %v", m, got, ok)
		}
	}
}

type stopWriter struct{}

func (stopWriter) Write(p []byte) (int, error) { return 0, method.ErrStop }

func TestStop(t *testing.T) {
	t.Parallel()
	err := (codec{}).Decode(stopWriter{}, bytes.NewReader(x86(3, 100)))
	if !errors.Is(err, method.ErrStop) {
		t.Fatalf("err = %v, want ErrStop", err)
	}
}

func FuzzConvert(f *testing.F) {
	f.Add(x86(4, 64))
	f.Fuzz(func(t *testing.T, data []byte) {
		enc := append([]byte(nil), data...)
		var s1, s2 uint32
		n := Convert(enc, 0, &s1, true)
		Convert(enc[:n], 0, &s2, false)
		if !bytes.Equal(enc[:n], data[:n]) {
			t.Fatal("encode+decode is not the identity")
		}
	})
}

func BenchmarkDecode(b *testing.B) {
	in := x86(5, 16<<20)
	b.SetBytes(int64(len(in)))
	for b.Loop() {
		if err := (codec{}).Decode(io.Discard, bytes.NewReader(in)); err != nil {
			b.Fatal(err)
		}
	}
}
