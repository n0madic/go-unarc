package grzip

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/method"
)

// lzpEncode is GRZip_LZP_Encode (without its give-up when the output is not smaller), to test lzpDecode on
// arbitrary data and parameters.
func lzpEncode(in []byte, minMatch int, hashLog uint) []byte {
	table := make([]int, 1<<hashLog) // position of the byte that followed a context; 0 = none
	out := append([]byte(nil), in[:4]...)
	ctx := uint32(in[3]) | uint32(in[2])<<8 | uint32(in[1])<<16 | uint32(in[0])<<24
	mask := uint32(1)<<hashLog - 1
	for i := 4; i < len(in); {
		h := (ctx>>15 ^ ctx ^ ctx>>3) & mask
		p := table[h]
		table[h] = i
		if p == 0 {
			out = append(out, in[i])
			ctx = ctx<<8 | uint32(in[i])
			i++
			continue
		}
		common := 0
		for i+common < len(in) && in[i+common] == in[p+common] {
			common++
		}
		if common < minMatch {
			common = 0
		}
		if common == 0 {
			c := in[i]
			out = append(out, c)
			ctx = ctx<<8 | uint32(c)
			if c == lzpMatchFlag {
				out = append(out, lzpXorFlag)
			}
			i++
			continue
		}
		i += common
		ctx = uint32(in[i-1]) | uint32(in[i-2])<<8 | uint32(in[i-3])<<16 | uint32(in[i-4])<<24
		n := common - minMatch + 1
		out = append(out, lzpMatchFlag)
		for ; n > 254; n -= 255 {
			out = append(out, lzpRunFlag)
		}
		out = append(out, byte(n)^lzpXorFlag)
	}
	return out
}

func lzpInputs() map[string][]byte {
	rng := rand.New(rand.NewSource(3))
	random := make([]byte, 5000)
	rng.Read(random)
	flags := make([]byte, 3000) // many match flags and run flags, to exercise the escapes
	for i := range flags {
		flags[i] = []byte{lzpMatchFlag, lzpRunFlag, 'a', lzpXorFlag}[rng.Intn(4)]
	}
	return map[string][]byte{
		"text":   []byte(strings.Repeat("the quick brown fox jumps over the lazy dog; ", 60)),
		"zeros":  make([]byte, 100000),
		"random": random,
		"flags":  flags,
		"runs":   append(bytes.Repeat([]byte{7}, 1000), bytes.Repeat([]byte("xy"), 700)...),
		"tiny":   []byte("abcde"),
		"four":   []byte("abcd"),
	}
}

func TestLZPDecode(t *testing.T) {
	t.Parallel()
	for name, in := range lzpInputs() {
		for _, minMatch := range []int{1, 2, 3, 4, 8, 32, 100} {
			for _, hashLog := range []uint{0, 1, 4, 10, 16} {
				t.Run(fmt.Sprintf("%s/min%d/h%d", name, minMatch, hashLog), func(t *testing.T) {
					enc := lzpEncode(in, minMatch, hashLog)
					var tab lzpTable
					tab.reset(hashLog)
					got := make([]byte, len(in))
					if err := lzpDecode(got, enc, minMatch, hashLog, &tab); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, in) {
						t.Fatal("round trip differs")
					}
				})
			}
		}
	}
}

// TestLZPTableReuse decodes many blocks with one table, with sizes that change and enough blocks to wrap the
// generation counter: stale entries must never leak into a later block.
func TestLZPTableReuse(t *testing.T) {
	t.Parallel()
	inputs := lzpInputs()
	names := []string{"text", "runs", "flags", "random", "text"}
	var tab lzpTable
	for i := 0; i < 700; i++ {
		in := inputs[names[i%len(names)]]
		hashLog := uint(6 + i%7)
		if i%101 == 0 {
			hashLog = 14
		}
		minMatch := 4 + i%5
		enc := lzpEncode(in, minMatch, hashLog)
		tab.reset(hashLog)
		got := make([]byte, len(in))
		if err := lzpDecode(got, enc, minMatch, hashLog, &tab); err != nil || !bytes.Equal(got, in) {
			t.Fatalf("block %d (%s, h%d, min%d): %v", i, names[i%len(names)], hashLog, minMatch, err)
		}
	}
}

func TestLZPDecodeErrors(t *testing.T) {
	t.Parallel()
	in := lzpInputs()["text"]
	enc := lzpEncode(in, 8, 10)
	run := func(src []byte, size, minMatch int) error {
		var tab lzpTable
		tab.reset(10)
		return lzpDecode(make([]byte, size), src, minMatch, 10, &tab)
	}
	if err := run(enc, len(in), 8); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"dst too small":    run(enc, len(in)-1, 8),
		"dst too large":    run(enc, len(in)+1, 8),
		"truncated":        run(enc[:len(enc)-1], len(in), 8),
		"short src":        run(enc[:3], 4, 8),
		"short dst":        run(enc, 3, 8),
		"empty":            run(nil, 0, 8),
		"wrong minMatch":   run(enc, len(in), 40),
		"match at end":     run([]byte{'a', 'b', 'c', 'd', lzpMatchFlag}, 100, 1),
		"zero min match":   run(append(bytes.Clone(enc[:4]), bytes.Repeat([]byte{lzpMatchFlag, 5 ^ lzpXorFlag}, 20)...), 1000, 0),
		"run flags to end": run(append(bytes.Clone(enc[:4]), bytes.Repeat([]byte{lzpMatchFlag, lzpRunFlag}, 30)...), 100000, 8),
	} {
		if !errors.Is(err, method.ErrBadData) {
			t.Errorf("%s: err = %v, want ErrBadData", name, err)
		}
	}
}
