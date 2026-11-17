package lzma

import (
	"math/rand/v2"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

// TestReuse checks that a decoder from NewDecoder decodes every stream as a new one does, with garbage in
// the dictionary, the probabilities and the input buffer it keeps. The 64 KB dictionary wraps on the larger
// streams.
func TestReuse(t *testing.T) {
	t.Parallel()
	if !codectest.HaveFagen(t) {
		t.Skip("needs .build/fagen")
	}
	for _, m := range []string{"lzma:64kb:normal", "lzma:1mb:normal:bt4:32:lc8"} {
		c := codectest.Parse(t, m)
		streams := codectest.ReuseStreams(t, m, 300<<10, 100<<10, 5000, 70<<10, 1)
		codectest.CheckReuse(t, c, streams, func(d method.Decoder, rng *rand.Rand) {
			s := d.(*streamDecoder)
			codectest.FillBytes(rng, s.d.dic[:cap(s.d.dic)])
			codectest.FillBytes(rng, s.in)
			for i := range s.d.probs[:cap(s.d.probs)] {
				s.d.probs[:cap(s.d.probs)][i] = uint16(rng.Uint32())
			}
		})
	}
}
