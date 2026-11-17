// Package bcj implements the exe method: the x86 BCJ filter, which turned the relative addresses of
// CALL/JMP (E8/E9) instructions into absolute ones when compressing. Decoding converts them back.
package bcj

import (
	"errors"
	"io"

	"github.com/n0madic/go-unarc/internal/method"
)

// bufferSize is the C filter's buffer (LARGE_BUFFER_SIZE).
const bufferSize = 256 * method.KB

func init() { method.Register(Parse) }

type codec struct{}

// Parse accepts "exe" without parameters.
func Parse(params []string, _ *method.Env) (method.Codec, bool) {
	if params[0] != "exe" || len(params) > 1 {
		return nil, false
	}
	return codec{}, true
}

// DecompressionMem is the filter's buffer, as in C.
func (codec) DecompressionMem() uint64 { return bufferSize }

// Decode mirrors bcj_x86_de_compress: blocks of up to 256 KiB, the unconverted tail of a block carried over
// to the next one, and a final piece of at most 5 bytes passed through unchanged.
func (codec) Decode(dst io.Writer, src io.Reader) error {
	buf := make([]byte, bufferSize)
	var state, ip uint32
	remainder := 0
	for {
		n, err := io.ReadFull(src, buf[remainder:])
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		size := n + remainder
		if size == 0 {
			return nil
		}
		out := size
		if size > 5 {
			out = Convert(buf[:size], ip, &state, false)
		}
		ip += uint32(out)
		if _, err := dst.Write(buf[:out]); err != nil {
			return err
		}
		remainder = copy(buf, buf[out:size])
	}
}

func test86MSByte(b byte) bool { return b == 0 || b == 0xFF }

var (
	maskToAllowedStatus = [8]bool{true, true, true, false, true, false, false, false}
	maskToBitNumber     = [8]uint32{0, 1, 2, 2, 3, 3, 3, 3}
)

// Convert is x86_Convert (Bra86.c, 2008-10-04): it converts data in place and returns how many leading
// bytes are final; the rest (at most 4 bytes) must be passed again with more data.
func Convert(data []byte, ip uint32, state *uint32, encoding bool) int {
	size := len(data)
	if size < 5 {
		return 0
	}
	ip += 5
	bufferPos := 0
	prevPosT := -1
	prevMask := *state & 7
	limit := size - 4
	for {
		p := bufferPos
		for p < limit && data[p]&0xFE != 0xE8 {
			p++
		}
		bufferPos = p
		if p >= limit {
			break
		}
		if d := bufferPos - prevPosT; d > 3 {
			prevMask = 0
		} else {
			prevMask = (prevMask << uint(d-1)) & 7
			if prevMask != 0 {
				b := data[p+4-int(maskToBitNumber[prevMask])]
				if !maskToAllowedStatus[prevMask] || test86MSByte(b) {
					prevPosT = bufferPos
					prevMask = (prevMask<<1)&7 | 1
					bufferPos++
					continue
				}
			}
		}
		prevPosT = bufferPos
		if !test86MSByte(data[p+4]) {
			prevMask = (prevMask<<1)&7 | 1
			bufferPos++
			continue
		}
		src := uint32(data[p+4])<<24 | uint32(data[p+3])<<16 | uint32(data[p+2])<<8 | uint32(data[p+1])
		var dest uint32
		for {
			if encoding {
				dest = ip + uint32(bufferPos) + src
			} else {
				dest = src - (ip + uint32(bufferPos))
			}
			if prevMask == 0 {
				break
			}
			index := maskToBitNumber[prevMask] * 8
			if !test86MSByte(byte(dest >> (24 - index))) {
				break
			}
			src = dest ^ (1<<(32-index) - 1)
		}
		data[p+4] = ^byte((dest>>24)&1 - 1)
		data[p+3] = byte(dest >> 16)
		data[p+2] = byte(dest >> 8)
		data[p+1] = byte(dest)
		bufferPos += 5
	}
	if d := bufferPos - prevPosT; d > 3 {
		*state = 0
	} else {
		*state = (prevMask << uint(d-1)) & 7
	}
	return bufferPos
}
