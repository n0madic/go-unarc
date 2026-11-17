package msc

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/n0madic/go-unarc/internal/codec/frame"
	"github.com/n0madic/go-unarc/internal/codec/tak"
	"github.com/n0madic/go-unarc/internal/method"
)

func init() { method.Register(Parse) }

const (
	magic   = "MSC "
	version = 1

	kindRaw = 0
	kindWAV = 3

	// maxLengthBytes is the widest length field of an item (a tag's high nibble is at most 3).
	maxLengthBytes = 4

	wavHeaderSize = 44
	fmtChunkSize  = 24
	// wavPrefix is what starts the payload of a WAV item: a zero byte, the WAV header size as a uint32 and two
	// zero bytes.
	wavPrefixSize = 7
	// The payload before the TAK stream: the prefix, the RIFF size, four zero bytes, the fmt chunk, "data" and
	// the data size. These are the offsets of its parts; wavFixedSize is where the TAK stream starts.
	riffSizeAt   = wavPrefixSize
	zerosAt      = riffSizeAt + 4
	fmtChunkAt   = zerosAt + 4
	dataTagAt    = fmtChunkAt + fmtChunkSize
	dataSizeAt   = dataTagAt + 4
	wavFixedSize = dataSizeAt + 4

	// decompressionMem covers an item's payload and one frame of samples, both a few hundred KB for the audio of
	// a game; the C plugin's own figure is unknown (it runs in another process).
	decompressionMem = 32 * method.MB

	// inputBuffer is the size of the buffer the stream is read through.
	inputBuffer = 1 * method.MB

	// maxWAVPayload bounds the payload of a WAV item (compressed audio of one file).
	maxWAVPayload = 1 << 30
)

// wavPrefix is the constant start of a WAV item's payload.
var wavPrefix = [wavPrefixSize]byte{0, wavHeaderSize, 0, 0, 0, 0, 0}

type codec struct{}

func (codec) DecompressionMem() uint64 { return decompressionMem }

// Parse accepts "msc:tak=<n>:bmp=0:dxt=0", the parameters of the streams this decoder was checked with: n is
// the TAK setting of the encoder (the decoder does not need it) and must not be 0, which would turn the audio
// model off. Anything else, parameters left out included, is not claimed, so the plugin fallback runs the DLL.
func Parse(params []string, _ *method.Env) (method.Codec, bool) {
	if params[0] != "msc" {
		return nil, false
	}
	var takLevel, bmp, dxt uint32
	seen := map[string]bool{}
	for _, p := range params[1:] {
		key, value, ok := strings.Cut(p, "=")
		n, numOK := method.ParseInt(value)
		if !ok || !numOK || seen[key] {
			return nil, false
		}
		seen[key] = true
		switch key {
		case "tak":
			takLevel = n
		case "bmp":
			bmp = n
		case "dxt":
			dxt = n
		default:
			return nil, false
		}
	}
	if len(seen) != 3 || takLevel == 0 || bmp != 0 || dxt != 0 {
		return nil, false
	}
	return codec{}, true
}

func badData(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrBadData, fmt.Sprintf(format, args...))
}

// badTAK reports a failure of the TAK decoder as corrupt data and keeps its error in the chain, so that
// errors.Is still tells a stream that is not valid TAK (tak.ErrCorrupt) from one the decoder does not handle
// (tak.ErrUnsupported).
func badTAK(err error) error {
	return fmt.Errorf("%w: %w", method.ErrBadData, err)
}

// Decode rebuilds the original stream from the items.
func (codec) Decode(dst io.Writer, src io.Reader) error {
	in := bufio.NewReaderSize(src, inputBuffer)
	var head [len(magic) + 4]byte
	if _, err := io.ReadFull(in, head[:]); err != nil {
		return frame.Truncated(err)
	}
	if string(head[:len(magic)]) != magic || binary.LittleEndian.Uint32(head[len(magic):]) != version {
		return badData("not an MSC stream")
	}

	var payload, pcm []byte
	for {
		tag, err := in.ReadByte()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		n := int(tag>>4) + 1
		if n > maxLengthBytes {
			return badData("item tag %#x", tag)
		}
		var lb [maxLengthBytes]byte
		if _, err := io.ReadFull(in, lb[:n]); err != nil {
			return frame.Truncated(err)
		}
		length := int64(binary.LittleEndian.Uint32(lb[:]))

		switch tag & 15 {
		case kindRaw:
			if m, err := io.CopyN(dst, in, length); err != nil {
				if m < length && errors.Is(err, io.EOF) {
					return frame.ErrTruncated
				}
				return err
			}
		case kindWAV:
			if length > maxWAVPayload {
				return badData("audio item of %d bytes", length)
			}
			if payload, err = frame.ReadN(payload, in, int(length)); err != nil {
				return err
			}
			if pcm, err = decodeWAV(dst, payload, pcm); err != nil {
				return err
			}
		default:
			// TODO: the other kinds (the bmp and dxt image models, and the raw audio of tak=0) are not known;
			// Parse leaves the streams that may hold them to the DLL.
			return badData("unsupported item kind %d", tag&15)
		}
	}
}

// decodeWAV writes the WAV file of one kind 3 item: its header and the decoded samples. pcm is a scratch
// buffer, returned for reuse.
func decodeWAV(dst io.Writer, payload, pcm []byte) ([]byte, error) {
	if len(payload) < wavFixedSize+4 || [wavPrefixSize]byte(payload[:wavPrefixSize]) != wavPrefix {
		return pcm, badData("audio item with an unknown layout")
	}
	riffSize, zeros := payload[riffSizeAt:zerosAt], payload[zerosAt:fmtChunkAt]
	fmtChunk, dataTag, dataSize := payload[fmtChunkAt:dataTagAt], payload[dataTagAt:dataSizeAt], payload[dataSizeAt:wavFixedSize]
	if string(zeros) != "\x00\x00\x00\x00" || string(dataTag) != "data" ||
		string(fmtChunk[:4]) != "fmt " || binary.LittleEndian.Uint32(fmtChunk[4:8]) != fmtChunkSize-8 {
		return pcm, badData("audio item with an unknown layout")
	}
	want := int64(binary.LittleEndian.Uint32(dataSize))

	dec, err := tak.NewDecoder(payload[wavFixedSize:])
	if err != nil {
		return pcm, badTAK(err)
	}

	header := make([]byte, 0, wavHeaderSize)
	header = append(header, "RIFF"...)
	header = append(header, riffSize...)
	header = append(header, "WAVE"...)
	header = append(header, fmtChunk...)
	header = append(header, dataTag...)
	header = append(header, dataSize...)
	if _, err := dst.Write(header); err != nil {
		return pcm, err
	}

	var done int64
	for {
		pcm, err = dec.Next(pcm[:0])
		if err == io.EOF {
			break
		}
		if err != nil {
			return pcm, badTAK(err)
		}
		done += int64(len(pcm))
		if done > want {
			return pcm, badData("audio item holds more samples than its data size")
		}
		if _, err := dst.Write(pcm); err != nil {
			return pcm, err
		}
	}
	if done != want {
		return pcm, badData("audio item holds %d bytes of samples, its data size is %d", done, want)
	}
	if rest := len(payload) - wavFixedSize - dec.Consumed(); rest != 0 {
		return pcm, badData("%d bytes after the audio data of an item", rest)
	}
	return pcm, nil
}
