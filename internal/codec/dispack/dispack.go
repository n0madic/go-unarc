// Package dispack decodes the "dispack070" method of FreeArc 0.67 (Compression/DisPack): the x86 code filter
// of kkrunchy by Fabian Giesen, which FreeArc 0.67 runs as "exe2" (-mc:exe/exe2) and repacks use before
// delta and lzma. The C unarc does not know this method.
//
// The stream is a little-endian int32 chunk size (16 KB from the encoder), then records until the end:
//
//	TAG_EXE  OutSize InSize  data      InSize bytes of filtered code that unfilter to OutSize bytes
//	TAG_DATA len             data      len bytes stored as is
//	data                               any other first word: a stored chunk of the chunk size, that word included
//
// TAG_DATA is 0xC71B3AE1 and TAG_EXE the next value; the 16 values from TAG_DATA are reserved, so a stored
// chunk starting with one of them gets a TAG_DATA header. The code of each TAG_EXE record is filtered as if
// loaded at an address that starts at 1 GB and advances by every record's decoded size.
package dispack

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/n0madic/go-unarc/internal/codec/frame"
	"github.com/n0madic/go-unarc/internal/method"
)

const (
	tagData = 0xC71B3AE1 // TAG_DATA
	tagExe  = tagData + 1

	defaultBlockSize = 8 * method.MB // DISPACK_METHOD::DISPACK_METHOD

	baseStart = 1 << 30 // the address of the first record
	baseWrap  = 3 << 30 // past it, the address goes back by 2 GB
)

// isTag mirrors is_tag: the 16 words from TAG_DATA are reserved.
func isTag(x uint32) bool { return x^tagData < 0x10 }

func init() { method.Register(Parse) }

// codec is a parsed dispack method. The block size bounds the records; "x" (ExtendedTables) is only shown in
// the method string, neither the C encoder nor the decoder reads it.
type codec struct {
	blockSize uint32
}

// Parse mirrors parse_DISPACK: "dispack" or "dispack070", then "x", "bSIZE" or a bare SIZE (the block size).
func Parse(params []string, _ *method.Env) (method.Codec, bool) {
	if params[0] != "dispack" && params[0] != "dispack070" {
		return nil, false
	}
	c := &codec{blockSize: defaultBlockSize}
	for _, p := range params[1:] {
		if p == "x" {
			continue
		}
		var ok bool
		if c.blockSize, ok = method.ParseMem(strings.TrimPrefix(p, "b")); !ok {
			return nil, false
		}
	}
	return c, true
}

// inBufferSize is InBufferSize of the C decoder: the largest record it reads.
func (c *codec) inBufferSize() int64 { return int64(c.blockSize) + int64(c.blockSize/4) + 1024 }

// DecompressionMem mirrors DISPACK_METHOD::GetDecompressionMem, the input and the output buffer (32-bit
// MemSize).
func (c *codec) DecompressionMem() uint64 { return uint64(c.blockSize + uint32(c.inBufferSize())) }

// Decode mirrors DISPACK_METHOD::decompress. Where the C code trusts the stream (a TAG_DATA length, negative
// sizes, a chunk size below 4, code that unfilters to fewer bytes than OutSize) this one reports corrupt data.
func (c *codec) Decode(dst io.Writer, src io.Reader) error {
	chunk, err := frame.ReadInt32(src)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	if uint32(chunk) > c.blockSize {
		return badData("chunk size %d is larger than the block size %d", chunk, c.blockSize)
	}
	inLimit := c.inBufferSize()
	var in, out []byte
	base := uint32(baseStart)
	for {
		tag, err := frame.ReadInt32(src)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var size uint32
		switch t := uint32(tag); {
		case !isTag(t) || t == tagData:
			// A stored chunk: its first word was read as the tag. A TAG_DATA record gives its length.
			n, word := int64(chunk), t != tagData
			if !word {
				v, err := frame.ReadInt32(src)
				if err != nil {
					return frame.Truncated(err)
				}
				n = int64(v)
			}
			if n < 0 || n > inLimit || (word && n < 4) {
				return badData("stored record of %d bytes", n)
			}
			size = uint32(n)
			if word {
				n -= 4
			}
			// The whole record is read before any of it is written, as the C code does.
			if in, err = frame.ReadN(in, src, int(n)); err != nil {
				return err
			}
			if word {
				var w [4]byte
				binary.LittleEndian.PutUint32(w[:], t)
				if _, err := dst.Write(w[:]); err != nil {
					return err
				}
			}
			if _, err := dst.Write(in); err != nil {
				return err
			}
		case t == tagExe:
			outSize, err := frame.ReadInt32(src)
			if err != nil {
				return frame.Truncated(err)
			}
			inSize, err := frame.ReadInt32(src)
			if err != nil {
				return frame.Truncated(err)
			}
			// A stream byte rebuilds at most 4 bytes of code (a call table index gives a whole address), so a
			// larger OutSize is corrupt, and the output buffer never grows past 4 times the data read.
			if outSize < 0 || uint32(outSize) > c.blockSize || inSize < 0 || int64(inSize) > inLimit ||
				int64(outSize) > 4*int64(inSize) {
				return badData("code record of %d bytes from %d", outSize, inSize)
			}
			if in, err = frame.ReadN(in, src, int(inSize)); err != nil {
				return err
			}
			if cap(out) < int(outSize) {
				out = make([]byte, outSize)
			}
			out = out[:outSize]
			if err := unfilter(in, out, base); err != nil {
				return err
			}
			if _, err := dst.Write(out); err != nil {
				return err
			}
			size = uint32(outSize)
		default:
			return badData("unknown record tag %#x", t)
		}
		base = advance(base, size)
	}
}

// advance moves the load address past a record of size bytes; from 3 GB it goes back by 2 GB (uint32 as in C).
func advance(base, size uint32) uint32 {
	base += size
	if base >= baseWrap {
		base -= 2 << 30
	}
	return base
}

func badData(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{method.ErrBadData}, args...)...)
}
