package tornado

import (
	"math/rand/v2"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

// TestReuse checks that a decoder from NewDecoder decodes every stream as a new one does, with garbage in
// the ring and the input buffer it keeps. The 9 MB stream wraps the 8 MB ring; tor:1, tor:3 and tor:7 use
// the byte, Huffman and arithmetic coders.
func TestReuse(t *testing.T) {
	t.Parallel()
	if !codectest.HaveFagen(t) {
		t.Skip("needs .build/fagen")
	}
	for _, m := range []string{"tor:1", "tor:3", "tor:7"} {
		c := codectest.Parse(t, m)
		streams := codectest.ReuseStreams(t, m, 9<<20, 100<<10, 5000, 70<<10, 1)
		codectest.CheckReuse(t, c, streams, func(d method.Decoder, rng *rand.Rand) {
			s := d.(*streamDecoder)
			codectest.FillBytes(rng, s.in[:cap(s.in)])
			codectest.FillBytes(rng, s.buf[:cap(s.buf)])
		})
	}
}
