package ppmd

import (
	"math/rand/v2"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

// TestReuse checks that a decoder from NewDecoder decodes every stream as a new one does, with garbage in
// the model's heap and the stream buffers it keeps. With 1 MB of memory the larger streams fill the model,
// which restarts in place (fagen's 64-bit model then diverges from the encoder, so those streams decode to
// garbage or fail; only the agreement of the two decoders counts).
func TestReuse(t *testing.T) {
	t.Parallel()
	if !codectest.HaveFagen(t) {
		t.Skip("needs .build/fagen")
	}
	for _, m := range []string{"ppmd:8:1mb", "ppmd:4:16mb:r1", "ppmd:16:4mb:r2"} {
		c := codectest.Parse(t, m)
		streams := codectest.ReuseStreams(t, m, 600<<10, 100<<10, 5000, 70<<10, 1)
		codectest.CheckReuse(t, c, streams, func(d method.Decoder, rng *rand.Rand) {
			s := d.(*streamDecoder)
			codectest.FillBytes(rng, s.d.heap)
			codectest.FillBytes(rng, s.d.inBuf)
			codectest.FillBytes(rng, s.d.outBuf)
		})
	}
}
