package tak

import (
	"errors"
	"fmt"
)

// ErrCorrupt reports a stream that is not valid TAK.
var ErrCorrupt = errors.New("tak: corrupt stream")

// ErrUnsupported reports valid TAK that the decoder does not handle (a codec type, sample format or channel
// count FFmpeg's decoder does not decode either).
var ErrUnsupported = errors.New("tak: unsupported stream")

func corrupt(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}

// Sizes of the fields of stream info and frame headers, in bits (tak.h).
const (
	bitsDataType      = 3
	bitsSampleRate    = 18
	bitsBPS           = 5
	bitsChannels      = 4
	bitsValid         = 5
	bitsChannelLayout = 6
	bitsFrameDuration = 4
	bitsSamples       = 35
	bitsCodec         = 6
	bitsProfile       = 4

	sampleRateMin = 6000
	channelsMin   = 1
	bpsMin        = 8

	bitsFrameFlags  = 3
	frameSyncID     = 0xA0FF
	bitsSyncID      = 16
	bitsLastSamples = 14
	bitsFrameNumber = 21
	durationShift   = 5
	bitsCRC         = 24

	minFrameHeaderBytes = (bitsSyncID + bitsFrameFlags + bitsFrameNumber + bitsCRC) / 8
	maxChannels         = 1 << bitsChannels
)

// Frame header flags.
const (
	flagLast     = 1 // the frame is the last one and holds fewer samples
	flagInfo     = 2 // stream info follows the header
	flagMetadata = 4 // not supported
)

// Codec types.
const (
	CodecMonoStereo   = 2
	CodecMultichannel = 4
)

// Metadata block types.
const (
	metaEnd = iota
	metaStreamInfo
	metaSeekTable
	metaSimpleWaveData
	metaEncoder
	metaPadding
	metaMD5
	metaLastFrame
)

// StreamInfo describes the audio of a stream.
type StreamInfo struct {
	Codec         int
	DataType      int
	SampleRate    int
	Channels      int
	BitsPerSample int
	FrameSamples  int   // samples per channel in a full frame
	Samples       int64 // samples per channel in the stream, 0 if unknown
}

// frameDurationQuants gives the frame durations of the frame size types (tak.c).
var frameDurationQuants = [...]int{3, 4, 6, 8, 4096, 8192, 16384, 512, 1024, 2048}

// frameSizeTypeMsLast is the last frame size type given as a duration (TAK_FST_250ms); later ones are sample counts.
const frameSizeTypeMsLast = 3

// frameSamples is tak_get_nb_samples: the samples per frame for a frame size type, or -1.
func frameSamples(sampleRate, typ int) int {
	var n, maxN int
	switch {
	case typ <= frameSizeTypeMsLast:
		n = sampleRate * frameDurationQuants[typ] >> durationShift
		maxN = 16384
	case typ < len(frameDurationQuants):
		n = frameDurationQuants[typ]
		maxN = sampleRate * frameDurationQuants[frameSizeTypeMsLast] >> durationShift
	default:
		return -1
	}
	if n <= 0 || n > maxN {
		return -1
	}
	return n
}

// parseStreamInfo reads stream info from a metadata block or a frame header (tak_parse_streaminfo). The channel
// layout is skipped: the channels come out in the order they are coded.
func parseStreamInfo(r *bitReader, s *StreamInfo) error {
	s.Codec = int(r.read(bitsCodec))
	r.skip(bitsProfile)
	frameType := int(r.read(bitsFrameDuration))
	s.Samples = int64(r.read64(bitsSamples))
	s.DataType = int(r.read(bitsDataType))
	s.SampleRate = int(r.read(bitsSampleRate)) + sampleRateMin
	s.BitsPerSample = int(r.read(bitsBPS)) + bpsMin
	s.Channels = int(r.read(bitsChannels)) + channelsMin
	if r.bit() {
		r.skip(bitsValid)
		if r.bit() {
			r.skip(s.Channels * bitsChannelLayout)
		}
	}
	if r.overrun() {
		return corrupt("stream info is cut short")
	}
	n := frameSamples(s.SampleRate, frameType)
	if n < 0 {
		return corrupt("frame size type %d at %d Hz", frameType, s.SampleRate)
	}
	s.FrameSamples = n
	return nil
}

// parseMetadata reads the metadata blocks at the start of data and returns the stream info they hold and the
// offset of the first frame. Data without the "tBaK" magic is bare frames: the first one must carry the info.
func parseMetadata(data []byte) (info StreamInfo, off int, err error) {
	if len(data) < 4 || string(data[:4]) != "tBaK" {
		return info, 0, nil
	}
	p := 4
	haveInfo := false
	for {
		if len(data)-p < 4 {
			return info, 0, corrupt("metadata ends inside a block header")
		}
		typ := int(data[p] & 0x7f)
		size := int(data[p+1]) | int(data[p+2])<<8 | int(data[p+3])<<16
		p += 4
		if typ == metaEnd {
			return info, p, nil
		}
		if size > len(data)-p {
			return info, 0, corrupt("metadata block of %d bytes runs past the data", size)
		}
		block := data[p : p+size]
		p += size
		switch typ {
		case metaStreamInfo, metaLastFrame, metaEncoder:
			if size <= 3 {
				return info, 0, corrupt("metadata block %d of %d bytes", typ, size)
			}
			if !checkCRC(block) {
				return info, 0, corrupt("metadata block %d: CRC mismatch", typ)
			}
			switch typ {
			case metaStreamInfo:
				if haveInfo {
					return info, 0, corrupt("second stream info block")
				}
				haveInfo = true
				if err := parseStreamInfo(&bitReader{buf: block[:size-3]}, &info); err != nil {
					return info, 0, err
				}
			case metaLastFrame:
				if size != 11 {
					return info, 0, corrupt("last frame block of %d bytes", size)
				}
			}
		case metaMD5:
			// TODO: check the MD5 of the decoded samples against this block; only its CRC is checked now.
			if size != 19 {
				return info, 0, corrupt("MD5 block of %d bytes", size)
			}
			if !checkCRC(block) {
				return info, 0, corrupt("MD5 block: CRC mismatch")
			}
		}
	}
}
