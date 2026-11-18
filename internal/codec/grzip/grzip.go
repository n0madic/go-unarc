package grzip

import (
	"fmt"
	"io"
	"runtime"

	"github.com/n0madic/go-unarc/internal/codec/parallel"
	"github.com/n0madic/go-unarc/internal/method"
)

// defaultBlockSize is the block size of a method string that does not give one (GRZIP_METHOD::GRZIP_METHOD).
const defaultBlockSize = 8 * method.MB

func init() { method.Register(Parse) }

// codec is a parsed "grzip" method. Only the block size matters for decoding (it feeds the memory estimate);
// the other parameters are checked by Parse and then ignored, because every block is self-describing.
type codec struct {
	blockSize uint32
	threads   int // decoding workers; C unarc uses the processor count
	env       *method.Env
}

// Parse mirrors parse_GRZIP: "grzip" followed by the parameters
//
//	s a l d p        one-letter switches (the encoder's alternative sort, adaptive block size, no LZP, delta filter, p = a:0,l:0,d:1)
//	mN bSIZE lN hN   method 1..4 (not range checked), block size, LZP match length, LZP hash size log
//	N                a bare number is the LZP match length
//	SIZE             a bare size ("8mb", "64kb", "24" = 2^24) is the block size
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	if params[0] != "grzip" {
		return nil, false
	}
	blockSize := uint32(defaultBlockSize)
	for _, p := range params[1:] {
		if len(p) == 1 && isSwitch(p[0]) {
			continue
		}
		if len(p) > 1 {
			switch p[0] {
			case 'm', 'l', 'h':
				if _, ok := method.ParseInt(p[1:]); !ok {
					return nil, false
				}
				continue
			case 'b':
				n, ok := method.ParseMem(p[1:])
				if !ok {
					return nil, false
				}
				blockSize = n
				continue
			}
		}
		// A parameter without a name: digits only is the match length, otherwise a block size.
		if _, ok := method.ParseInt(p); ok {
			continue
		}
		n, ok := method.ParseMem(p)
		if !ok {
			return nil, false
		}
		blockSize = n
	}
	return &codec{blockSize: blockSize, threads: runtime.NumCPU(), env: env}, true
}

func isSwitch(c byte) bool {
	switch c {
	case 's', 'a', 'l', 'd', 'p':
		return true
	}
	return false
}

// DecompressionMem mirrors GRZIP_METHOD::GetDecompressionMem: BlockSize*5*threads in 32-bit arithmetic.
func (c *codec) DecompressionMem() uint64 {
	return uint64(c.blockSize * 5 * uint32(c.threads))
}

// Dictionary mirrors GRZIP_METHOD::GetDictionary: the block size.
func (c *codec) Dictionary() uint64 { return uint64(c.blockSize) }

// Decode decodes blocks until the end of src: this goroutine reads them, a pool of workers decodes them and
// the results are written in stream order. The first error in stream order ends the output.
func (c *codec) Decode(dst io.Writer, src io.Reader) error {
	// One worker can retain an LZP table, sort tables and frames, including container scratch.
	const workerBytes = 192 * method.MB
	workers := int(min(uint64(max(c.threads, 1)), max(1, c.env.DecoderLimit()/workerBytes)))
	return parallel.Decode(dst, workers, readBlock(src, c.env), func(d *decoder, j *parallel.Job[header]) {
		d.env = c.env
		d.run(j)
	})
}

// readBlock returns the reader of the next block: j.In receives the header followed by the payload.
func readBlock(src io.Reader, env *method.Env) parallel.Read[header] {
	return func(j *parallel.Job[header]) (bool, error) {
		var raw [headerSize]byte
		switch _, err := io.ReadFull(src, raw[:]); err {
		case nil:
		case io.EOF:
			return false, nil // clean end of data
		case io.ErrUnexpectedEOF:
			return false, fmt.Errorf("%w: truncated block header", method.ErrBadData)
		default:
			return false, err
		}
		h, err := parseHeader(raw[:])
		if err != nil {
			return false, err
		}
		if err := env.CheckDecoderMemory(uint64(headerSize + h.payload + h.outLen())); err != nil {
			return false, err
		}
		j.In = resize(j.In, headerSize+h.payload)
		copy(j.In, raw[:])
		if _, err := io.ReadFull(src, j.In[headerSize:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				err = fmt.Errorf("%w: truncated block", method.ErrBadData)
			}
			return false, err
		}
		j.Header = h
		return true, nil
	}
}
