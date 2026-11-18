package ppmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/n0madic/go-unarc/internal/method"
)

func init() { method.Register(Parse) }

// Defaults of PPMD_METHOD::PPMD_METHOD.
const (
	defaultOrder = 10
	defaultMem   = 48 * method.MB

	minOrder = 2 // the C code crashes on lower orders (it has no "solid mode" initial context)
	maxOrder = maxO

	// minMem is the smallest heap that can hold the root context (see newAllocator and
	// startModel): the units area must take the 128 units of the order-0 states.
	minMem = 1824
	// maxMem keeps every heap offset within uint32.
	maxMem = 1<<32 - 64
)

// codec is the ppmd method with its parameters.
type codec struct {
	order int
	mem   uint32
	mr    uint32
	env   *method.Env
}

// Parse mirrors parse_PPMD: "ppmd[:order][:mem][:r[N]]" where each parameter is either named
// (oN, mN or memN, rN) or recognised by its form (a bare number is the order, anything else a size).
// Parameters the C decoder would crash on (order outside 2..128, a model too small for its root
// context) are rejected.
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	c, ok := parseParams(params)
	if !ok || c.order < minOrder || c.order > maxOrder || c.mem < minMem || c.mem > maxMem {
		return nil, false
	}
	c.env = env
	return c, true
}

// parseParams is the parameter loop of parse_PPMD without any range checks. The order is the C int,
// so values of 2^31 and above are negative.
func parseParams(params []string) (*codec, bool) {
	if len(params) == 0 || params[0] != "ppmd" {
		return nil, false
	}
	c := &codec{order: defaultOrder, mem: defaultMem}
	for _, param := range params[1:] {
		if strings.HasPrefix(param, "mem") {
			param = param[2:] // "mem..." is handled as "m..."
		}
		var ok bool
		if len(param) == 1 {
			if param[0] == 'r' {
				c.mr = 1
				continue
			}
		} else if len(param) > 1 {
			switch param[0] {
			case 'm':
				if c.mem, ok = method.ParseMem(param[1:]); !ok {
					return nil, false
				}
				continue
			case 'o':
				var n uint32
				if n, ok = method.ParseInt(param[1:]); !ok {
					return nil, false
				}
				c.order = int(int32(n))
				continue
			case 'r':
				if c.mr, ok = method.ParseInt(param[1:]); !ok {
					return nil, false
				}
				continue
			}
		}
		// No parameter name: a number is the order, anything else the memory size.
		if n, ok := method.ParseInt(param); ok {
			c.order = int(int32(n))
		} else if c.mem, ok = method.ParseMem(param); !ok {
			return nil, false
		}
	}
	return c, true
}

// DecompressionMem is the size of the model heap (GetDecompressionMem).
func (c *codec) DecompressionMem() uint64 { return uint64(c.mem) }

// Decode decodes a PPMd stream until its end-of-data marker.
func (c *codec) Decode(dst io.Writer, src io.Reader) error { return c.NewDecoder().Decode(dst, src) }

// NewDecoder returns a decoder for one goroutine that keeps the model's memory and the stream buffers from
// one stream to the next.
func (c *codec) NewDecoder() method.Decoder { return &streamDecoder{c: c} }

// streamDecoder is the memory of a decoding, kept for the next stream.
type streamDecoder struct {
	c *codec
	d decoder
}

func (s *streamDecoder) Decode(dst io.Writer, src io.Reader) (err error) {
	if err := s.c.env.CheckDecoderMemory(uint64(s.c.mem)); err != nil {
		return err
	}
	defer func() {
		// The model never indexes outside its heap whatever the input is; this is a safety net so
		// that a defect cannot take the process down.
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", method.ErrBadData, r)
		}
	}()
	d := &s.d
	if err := d.reset(s.c.order, s.c.mem, s.c.mr, dst, src); err != nil {
		return err
	}
	d.run()
	d.flush()
	return d.err
}
