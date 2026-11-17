package method_test

import (
	"io"
	"testing"

	_ "github.com/n0madic/go-unarc/internal/codec/all"
	"github.com/n0madic/go-unarc/internal/method"
)

// FuzzParse feeds arbitrary compressor strings, as read from an archive, to every parser and to AddTempfile.
// Decoding is left to the codecs' own fuzzers: the parameters may legitimately ask for gigabytes of memory.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"lzma:1mb:normal:bt4:32+aes-256/ctr:n1000:s0123:c0a1b:i00112233445566778899aabbccddeeff",
		"exe+delta+rep:64mb+lzp:h20:96+dict:p+ppmd:o10:m48mb:r1+tor:7:c3+grzip:m4:b8mb:l32:h18:a",
		"mm:d1+tta:m2+tempfile+pmm:o16:m192mb+srep:m3f+blowfish-448/cfb:k00",
		"lzma:*lcx:bt4", "ppmd:o128:mem2gb", "tor:16:h1gb:l256", "grzip:b0", "rep:m0:l0", "storing+storing",
		"aes-0", "serpent-256/ctr:k" + string(make([]byte, 200)), "dispack070:b4mb:x+delta+lzma",
	} {
		f.Add(s)
	}
	env := &method.Env{Stderr: io.Discard}
	f.Fuzz(func(t *testing.T, compressor string) {
		for _, m := range method.Split(compressor) {
			if c, ok := method.Lookup(m, env); ok {
				_ = c.DecompressionMem()
			}
		}
		_ = method.AddTempfile(compressor, method.MaxMem(), env)
	})
}
