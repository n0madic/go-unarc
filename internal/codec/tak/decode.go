package tak

import "io"

// Limits of the frame format (takdec.c).
const (
	maxSubframes   = 8   // subframes per channel
	maxPredictors  = 256 // taps of the longest LPC filter
	maxCodingModes = 128 // residue coding segments in a subframe
	residueWindow  = 544 // samples of residue history kept for the filters
	maxCodecChans  = 6   // channels the decoder accepts
	minCodedFrame  = 16  // frames with fewer samples are stored raw
)

// predictorSizes are the filter orders a subframe may ask for; the last code means no filter.
var predictorSizes = [16]int{4, 8, 12, 16, 24, 32, 48, 64, 80, 96, 128, 160, 192, 224, 256, 0}

// multichannelDecorrelation maps the 2-bit decorrelation index of a channel pair to a decorrelation mode.
var multichannelDecorrelation = [4]int8{1, 3, 4, 6}

// riceParams are the parameters of the 50 residue coding modes: the width of the first read, the thresholds
// of the escape codes, and the scale and bias added by them.
var riceParams = [50]struct{ init, escape, scale, aescape, bias uint32 }{
	{0x01, 0x0000001, 0x0000001, 0x0000003, 0x0000008},
	{0x02, 0x0000003, 0x0000001, 0x0000007, 0x0000006},
	{0x03, 0x0000005, 0x0000002, 0x000000E, 0x000000D},
	{0x03, 0x0000003, 0x0000003, 0x000000D, 0x0000018},
	{0x04, 0x000000B, 0x0000004, 0x000001C, 0x0000019},
	{0x04, 0x0000006, 0x0000006, 0x000001A, 0x0000030},
	{0x05, 0x0000016, 0x0000008, 0x0000038, 0x0000032},
	{0x05, 0x000000C, 0x000000C, 0x0000034, 0x0000060},
	{0x06, 0x000002C, 0x0000010, 0x0000070, 0x0000064},
	{0x06, 0x0000018, 0x0000018, 0x0000068, 0x00000C0},
	{0x07, 0x0000058, 0x0000020, 0x00000E0, 0x00000C8},
	{0x07, 0x0000030, 0x0000030, 0x00000D0, 0x0000180},
	{0x08, 0x00000B0, 0x0000040, 0x00001C0, 0x0000190},
	{0x08, 0x0000060, 0x0000060, 0x00001A0, 0x0000300},
	{0x09, 0x0000160, 0x0000080, 0x0000380, 0x0000320},
	{0x09, 0x00000C0, 0x00000C0, 0x0000340, 0x0000600},
	{0x0A, 0x00002C0, 0x0000100, 0x0000700, 0x0000640},
	{0x0A, 0x0000180, 0x0000180, 0x0000680, 0x0000C00},
	{0x0B, 0x0000580, 0x0000200, 0x0000E00, 0x0000C80},
	{0x0B, 0x0000300, 0x0000300, 0x0000D00, 0x0001800},
	{0x0C, 0x0000B00, 0x0000400, 0x0001C00, 0x0001900},
	{0x0C, 0x0000600, 0x0000600, 0x0001A00, 0x0003000},
	{0x0D, 0x0001600, 0x0000800, 0x0003800, 0x0003200},
	{0x0D, 0x0000C00, 0x0000C00, 0x0003400, 0x0006000},
	{0x0E, 0x0002C00, 0x0001000, 0x0007000, 0x0006400},
	{0x0E, 0x0001800, 0x0001800, 0x0006800, 0x000C000},
	{0x0F, 0x0005800, 0x0002000, 0x000E000, 0x000C800},
	{0x0F, 0x0003000, 0x0003000, 0x000D000, 0x0018000},
	{0x10, 0x000B000, 0x0004000, 0x001C000, 0x0019000},
	{0x10, 0x0006000, 0x0006000, 0x001A000, 0x0030000},
	{0x11, 0x0016000, 0x0008000, 0x0038000, 0x0032000},
	{0x11, 0x000C000, 0x000C000, 0x0034000, 0x0060000},
	{0x12, 0x002C000, 0x0010000, 0x0070000, 0x0064000},
	{0x12, 0x0018000, 0x0018000, 0x0068000, 0x00C0000},
	{0x13, 0x0058000, 0x0020000, 0x00E0000, 0x00C8000},
	{0x13, 0x0030000, 0x0030000, 0x00D0000, 0x0180000},
	{0x14, 0x00B0000, 0x0040000, 0x01C0000, 0x0190000},
	{0x14, 0x0060000, 0x0060000, 0x01A0000, 0x0300000},
	{0x15, 0x0160000, 0x0080000, 0x0380000, 0x0320000},
	{0x15, 0x00C0000, 0x00C0000, 0x0340000, 0x0600000},
	{0x16, 0x02C0000, 0x0100000, 0x0700000, 0x0640000},
	{0x16, 0x0180000, 0x0180000, 0x0680000, 0x0C00000},
	{0x17, 0x0580000, 0x0200000, 0x0E00000, 0x0C80000},
	{0x17, 0x0300000, 0x0300000, 0x0D00000, 0x1800000},
	{0x18, 0x0B00000, 0x0400000, 0x1C00000, 0x1900000},
	{0x18, 0x0600000, 0x0600000, 0x1A00000, 0x3000000},
	{0x19, 0x1600000, 0x0800000, 0x3800000, 0x3200000},
	{0x19, 0x0C00000, 0x0C00000, 0x3400000, 0x6000000},
	{0x1A, 0x2C00000, 0x1000000, 0x7000000, 0x6400000},
	{0x1A, 0x1800000, 0x1800000, 0x6800000, 0xC000000},
}

// mcdParam is the decorrelation of one channel group of a multichannel frame.
type mcdParam struct {
	present bool
	index   int8
	chan1   int8
	chan2   int8
}

// Decoder decodes the frames of one TAK stream held in memory. It is not safe for concurrent use.
type Decoder struct {
	data []byte
	pos  int // offset of the next frame
	done bool
	info StreamInfo
	gb   bitReader

	// the current frame
	flags            int
	lastFrameSamples int
	nbSamples        int
	uval             int
	subframeScale    int
	dmode            int8
	nbSubframes      int
	subframeLen      [maxSubframes]int
	sampleShift      [maxChannels]int8
	lpcMode          [maxChannels]int8
	mcd              [maxChannels]mcdParam
	codingMode       [maxCodingModes]int8
	predictors       [maxPredictors]int16
	filter           [maxPredictors]int16
	residues         [residueWindow]int16
	decoded          [maxChannels][]int32
	decodeBuf        []int32
	bytesPerSample   int
}

// NewDecoder reads the metadata at the start of data, which holds one whole stream (a trailing part after
// the last frame is ignored).
func NewDecoder(data []byte) (*Decoder, error) {
	info, off, err := parseMetadata(data)
	if err != nil {
		return nil, err
	}
	return &Decoder{data: data, pos: off, info: info}, nil
}

// Info returns the stream info: from the metadata, updated by the frame headers that carry some.
func (d *Decoder) Info() StreamInfo { return d.info }

// Consumed is the number of bytes of the stream used up to the end of the last decoded frame.
func (d *Decoder) Consumed() int { return d.pos }

// Next decodes the next frame and appends its samples to dst, interleaved, little-endian, one byte per
// sample for 8-bit streams (unsigned, as in WAV) and 2 or 3 bytes for 16 and 24 bits. It returns io.EOF
// after the last frame.
func (d *Decoder) Next(dst []byte) ([]byte, error) {
	if d.done {
		return dst, io.EOF
	}
	if len(d.data)-d.pos < minFrameHeaderBytes {
		return dst, corrupt("stream ends before the last frame")
	}
	d.gb = bitReader{buf: d.data[d.pos:]}
	if err := d.decodeFrame(); err != nil {
		return dst, err
	}
	d.pos += d.gb.pos / 8
	if d.flags&flagLast != 0 {
		d.done = true
	}
	return d.appendPCM(dst), nil
}

// decodeFrame is tak_decode_frame up to the conversion of the samples.
func (d *Decoder) decodeFrame() error {
	gb := &d.gb
	if err := d.decodeFrameHeader(); err != nil {
		return err
	}
	headerSize := gb.pos / 8
	if !checkCRC(gb.buf[:headerSize]) {
		return corrupt("frame header: CRC mismatch")
	}
	ti := &d.info
	if ti.Codec != CodecMonoStereo && ti.Codec != CodecMultichannel {
		return unsupported("codec type %d", ti.Codec)
	}
	if ti.DataType != 0 {
		return unsupported("data type %d", ti.DataType)
	}
	if ti.Codec == CodecMonoStereo && ti.Channels > 2 {
		return corrupt("%d channels in a mono/stereo stream", ti.Channels)
	}
	if ti.Channels > maxCodecChans {
		return unsupported("%d channels", ti.Channels)
	}
	if ti.FrameSamples <= 0 {
		return corrupt("no samples in a frame")
	}
	switch ti.BitsPerSample {
	case 8, 16, 24:
		d.bytesPerSample = ti.BitsPerSample / 8
	default:
		return unsupported("%d bits per sample", ti.BitsPerSample)
	}
	d.setSampleRateParams()

	d.nbSamples = ti.FrameSamples
	if d.lastFrameSamples != 0 {
		d.nbSamples = d.lastFrameSamples
	}
	channels := ti.Channels
	if need := channels * d.nbSamples; cap(d.decodeBuf) < need {
		d.decodeBuf = make([]int32, need)
	} else {
		d.decodeBuf = d.decodeBuf[:need]
		clear(d.decodeBuf)
	}
	for c := range channels {
		d.decoded[c] = d.decodeBuf[c*d.nbSamples : (c+1)*d.nbSamples : (c+1)*d.nbSamples]
	}

	if d.nbSamples < minCodedFrame {
		for c := range channels {
			for i := range d.nbSamples {
				d.decoded[c][i] = gb.readSigned(uint(ti.BitsPerSample))
			}
		}
	} else {
		var err error
		if ti.Codec == CodecMonoStereo {
			err = d.decodeMonoStereo(channels)
		} else {
			err = d.decodeMultichannel(channels)
		}
		if err != nil {
			return err
		}
		for c := range channels {
			samples := d.decoded[c]
			if d.lpcMode[c] != 0 {
				decodeLPC(samples, int(d.lpcMode[c]))
			}
			if shift := d.sampleShift[c]; shift > 0 {
				for i := range samples {
					samples[i] = int32(uint32(samples[i]) << shift)
				}
			}
		}
	}

	gb.align()
	gb.skip(bitsCRC)
	if gb.overrun() {
		return corrupt("frame runs past the end of the data")
	}
	if !checkCRC(gb.buf[headerSize : gb.pos/8]) {
		return corrupt("frame: CRC mismatch")
	}
	return nil
}

func (d *Decoder) setSampleRateParams() {
	rate := d.info.SampleRate
	shift := uint(0)
	switch {
	case rate < 11025:
		shift = 3
	case rate < 22050:
		shift = 2
	case rate < 44100:
		shift = 1
	}
	aligned := ((rate+511)>>9 + 3) &^ 3
	d.uval = aligned << shift
	d.subframeScale = aligned << 1
}

// decodeFrameHeader is ff_tak_decode_frame_header.
func (d *Decoder) decodeFrameHeader() error {
	gb := &d.gb
	if gb.read(bitsSyncID) != frameSyncID {
		return corrupt("missing frame sync")
	}
	d.flags = int(gb.read(bitsFrameFlags))
	gb.skip(bitsFrameNumber)
	d.lastFrameSamples = 0
	if d.flags&flagLast != 0 {
		d.lastFrameSamples = int(gb.read(bitsLastSamples)) + 1
		gb.skip(2)
	}
	if d.flags&flagInfo != 0 {
		if err := parseStreamInfo(gb, &d.info); err != nil {
			return err
		}
		if gb.read(6) != 0 {
			gb.skip(25)
		}
		gb.align()
	}
	if d.flags&flagMetadata != 0 {
		return corrupt("frame metadata")
	}
	if gb.bitsLeft() < bitsCRC {
		return corrupt("frame header is cut short")
	}
	gb.skip(bitsCRC)
	return nil
}

func (d *Decoder) decodeMonoStereo(channels int) error {
	gb := &d.gb
	for c := range channels {
		if err := d.decodeChannel(c); err != nil {
			return err
		}
	}
	if channels == 2 {
		// A subframe count and a length follow the channels in the C code, which nothing reads: skip them.
		if gb.bit() {
			gb.skip(6)
		}
		d.dmode = int8(gb.read(3))
		return d.decorrelate(0, 1, d.nbSamples-1)
	}
	return nil
}

func (d *Decoder) decodeMultichannel(channels int) error {
	gb := &d.gb
	groups := channels
	if gb.bit() {
		mask := 0
		groups = int(gb.read(4)) + 1
		if groups > channels {
			return corrupt("%d channel groups for %d channels", groups, channels)
		}
		for i := range groups {
			nbit := int(gb.read(4))
			if nbit >= channels {
				return corrupt("channel %d of %d", nbit, channels)
			}
			if mask&(1<<nbit) != 0 {
				return corrupt("channel %d listed twice", nbit)
			}
			m := &d.mcd[i]
			m.present = gb.bit()
			if m.present {
				m.index = int8(gb.read(2))
				m.chan2 = int8(gb.read(4))
				if int(m.chan2) >= channels {
					return corrupt("channel %d of %d", m.chan2, channels)
				}
				if m.index == 1 {
					if nbit == int(m.chan2) || mask&(1<<m.chan2) != 0 {
						return corrupt("bad channel pair")
					}
					mask |= 1 << m.chan2
				} else if mask&(1<<m.chan2) == 0 {
					return corrupt("bad channel pair")
				}
			}
			m.chan1 = int8(nbit)
			mask |= 1 << nbit
		}
		if mask != 1<<channels-1 {
			return unsupported("channel list not covering all channels")
		}
	} else {
		for i := range groups {
			d.mcd[i] = mcdParam{chan1: int8(i)}
		}
	}

	for i := range groups {
		m := d.mcd[i]
		if m.present && m.index == 1 {
			if err := d.decodeChannel(int(m.chan2)); err != nil {
				return err
			}
		}
		if err := d.decodeChannel(int(m.chan1)); err != nil {
			return err
		}
		if m.present {
			d.dmode = multichannelDecorrelation[m.index]
			if err := d.decorrelate(int(m.chan2), int(m.chan1), d.nbSamples-1); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodeLPC undoes the first-stage prediction of a block of samples in place (decode_lpc).
func decodeLPC(c []int32, mode int) {
	length := len(c)
	if length < 2 {
		return
	}
	u := func(i int) uint32 { return uint32(c[i]) }
	switch mode {
	case 1:
		a1 := u(0)
		i := 1
		for range (length - 1) >> 1 {
			c[i] = int32(u(i) + a1)
			c[i+1] = int32(u(i+1) + u(i))
			a1 = u(i + 1)
			i += 2
		}
		if (length-1)&1 != 0 {
			c[i] = int32(u(i) + a1)
		}
	case 2:
		a1 := u(1)
		a2 := a1 + u(0)
		c[1] = int32(a2)
		if length > 2 {
			i := 2
			for range (length - 2) >> 1 {
				a3 := u(i) + a1
				a4 := a3 + a2
				c[i] = int32(a4)
				a1 = u(i+1) + a3
				a2 = a1 + a4
				c[i+1] = int32(a2)
				i += 2
			}
			if length&1 != 0 {
				c[i] = int32(u(i) + a1 + a2)
			}
		}
	case 3:
		a1 := u(1)
		a2 := a1 + u(0)
		c[1] = int32(a2)
		if length > 2 {
			a3 := u(2)
			a4 := a3 + a1
			a5 := a4 + a2
			c[2] = int32(a5)
			for i := 3; i < length; i++ {
				a3 += u(i)
				a4 += a3
				a5 += a4
				c[i] = int32(a5)
			}
		}
	}
}

// decodeSegment reads len residues coded with one coding mode (decode_segment).
func (d *Decoder) decodeSegment(mode int, out []int32) error {
	gb := &d.gb
	if mode == 0 {
		clear(out)
		return nil
	}
	if mode < 0 || mode > len(riceParams) {
		return corrupt("residue coding mode %d", mode)
	}
	code := riceParams[mode-1]
	for i := range out {
		x := gb.read(uint(code.init))
		if x >= code.escape && gb.bit() {
			x |= 1 << code.init
			if x >= code.aescape {
				scale := uint32(gb.unary(1, 9))
				if scale == 9 {
					scaleBits := gb.read(3)
					if scaleBits > 0 {
						if scaleBits == 7 {
							scaleBits += gb.read(5)
							if scaleBits > 29 {
								return corrupt("residue scale of %d bits", scaleBits)
							}
						}
						scale = gb.read(uint(scaleBits)) + 1
						x += code.scale * scale
					}
					x += code.bias
				} else {
					x += code.scale*scale - code.escape
				}
			} else {
				x -= code.escape
			}
		}
		out[i] = int32(x>>1 ^ -(x & 1))
	}
	return nil
}

// decodeResidues reads the residues of length samples, in one or more segments with their own coding modes
// (decode_residues).
func (d *Decoder) decodeResidues(out []int32, length int) error {
	gb := &d.gb
	if length > d.nbSamples || length > len(out) {
		return corrupt("%d residues in a frame of %d samples", length, d.nbSamples)
	}
	if !gb.bit() {
		return d.decodeSegment(int(gb.read(6)), out[:length])
	}

	wlength := length / d.uval
	rval := length - wlength*d.uval
	if rval < d.uval/2 {
		rval += d.uval
	} else {
		wlength++
	}
	if wlength <= 1 || wlength > maxCodingModes {
		return corrupt("%d residue segments", wlength)
	}

	mode := int(gb.read(6))
	d.codingMode[0] = int8(mode)
	for i := 1; i < wlength; i++ {
		c := gb.unary(1, 6)
		switch c {
		case 6:
			mode = int(gb.read(6))
		case 5, 4, 3:
			sign := int(gb.read(1))
			mode += (-sign ^ (c - 1)) + sign
		case 2:
			mode++
		case 1:
			mode--
		}
		d.codingMode[i] = int8(mode)
	}

	for i := 0; i < wlength; {
		n := 0
		mode = int(d.codingMode[i])
		for {
			if i >= wlength-1 {
				n += rval
			} else {
				n += d.uval
			}
			i++
			if i == wlength || int(d.codingMode[i]) != mode {
				break
			}
		}
		if n > len(out) {
			return corrupt("residue segments run past the frame")
		}
		if err := d.decodeSegment(mode, out[:n]); err != nil {
			return err
		}
		out = out[n:]
	}
	return nil
}

// decodeSubframe reads the samples of one subframe, starting at ch[off] (decode_subframe). A subframe that
// continues the filter of the previous one first re-reads its last filter-order samples.
func (d *Decoder) decodeSubframe(ch []int32, off, size, prevSize int) error {
	gb := &d.gb
	if !gb.bit() {
		if off+size > len(ch) {
			return corrupt("subframe runs past the frame")
		}
		return d.decodeResidues(ch[off:], size)
	}

	order := predictorSizes[gb.read(4)]
	if prevSize > 0 && gb.bit() {
		if order > prevSize {
			return corrupt("filter of %d taps after a subframe of %d samples", order, prevSize)
		}
		off -= order
		size += order
		if order > size {
			return corrupt("filter longer than its subframe")
		}
	} else {
		if order > size {
			return corrupt("filter of %d taps in a subframe of %d samples", order, size)
		}
		lpcMode := int(gb.read(2))
		if lpcMode > 2 {
			return corrupt("LPC mode %d", lpcMode)
		}
		if off+order > len(ch) {
			return corrupt("subframe runs past the frame")
		}
		if err := d.decodeResidues(ch[off:], order); err != nil {
			return err
		}
		if lpcMode != 0 {
			decodeLPC(ch[off:off+order], lpcMode)
		}
	}
	if off+size > len(ch) {
		return corrupt("subframe runs past the frame")
	}

	dshift := uint(gb.escape4())
	psize := int(gb.read(1)) + 6
	filterQuant := 10
	if gb.bit() {
		filterQuant -= int(gb.read(3)) + 1
		if filterQuant < 3 {
			return corrupt("filter quantisation %d", filterQuant)
		}
	}
	if gb.bitsLeft() < 2*10+2*psize {
		return corrupt("subframe header is cut short")
	}

	pred := &d.predictors
	pred[0] = int16(gb.readSigned(10))
	pred[1] = int16(gb.readSigned(10))
	pred[2] = int16(gb.readSigned(uint(psize)) * (1 << (10 - psize)))
	pred[3] = int16(gb.readSigned(uint(psize)) * (1 << (10 - psize)))
	if order > 4 {
		tmp := psize - int(gb.read(1))
		x := 0
		for i := 4; i < order; i++ {
			if i&3 == 0 {
				x = tmp - int(gb.read(2))
			}
			pred[i] = int16(gb.readSigned(uint(x)) * (1 << (10 - psize)))
		}
	}

	var tfilter [maxPredictors]uint32
	tfilter[0] = uint32(int32(pred[0]) * 64)
	for i := 1; i < order; i++ {
		pi := uint32(int32(pred[i]))
		p1, p2 := 0, i-1
		for range (i + 1) / 2 {
			x := tfilter[p1] + uint32(int32(pi*tfilter[p2]+256)>>9)
			tfilter[p2] += uint32(int32(pi*tfilter[p1]+256) >> 9)
			tfilter[p1] = x
			p1++
			p2--
		}
		tfilter[i] = uint32(int32(pred[i]) * 64)
	}

	qshift := uint(15 - filterQuant)
	x := int32(1) << (32 - qshift)
	y := int32(1) << (qshift - 1)
	for i, j := 0, order-1; i < order/2; i, j = i+1, j-1 {
		d.filter[j] = int16(x - (int32(tfilter[i])+y)>>qshift)
		d.filter[i] = int16(x - (int32(tfilter[j])+y)>>qshift)
	}

	if err := d.decodeResidues(ch[off+order:], size-order); err != nil {
		return err
	}

	res := &d.residues
	pos := off
	for i := range order {
		res[i] = int16(ch[pos] >> dshift)
		pos++
	}

	window := len(res) - order
	remaining := size - order
	for remaining > 0 {
		tmp := min(window, remaining)
		for i := range tmp {
			v := uint32(1) << (filterQuant - 1)
			vec := order &^ 15
			if vec != 0 {
				v += uint32(dot16(res[i:i+vec], d.filter[:vec]))
			}
			for j := vec; j < order; j += 4 {
				v += uint32(int32(res[i+j+3]))*uint32(int32(d.filter[j+3])) +
					uint32(int32(res[i+j+2]))*uint32(int32(d.filter[j+2])) +
					uint32(int32(res[i+j+1]))*uint32(int32(d.filter[j+1])) +
					uint32(int32(res[i+j]))*uint32(int32(d.filter[j]))
			}
			out := uint32(clip13(int32(v)>>uint(filterQuant))*(1<<dshift)) - uint32(ch[pos])
			ch[pos] = int32(out)
			pos++
			res[order+i] = int16(int32(out) >> dshift)
		}
		remaining -= tmp
		if remaining > 0 {
			copy(res[:order], res[window:window+order])
		}
	}
	return nil
}

// dot16 is the scalar product of two int16 vectors with 32-bit wrap-around.
func dot16(a, b []int16) int32 {
	var s uint32
	for i := range a {
		s += uint32(int32(a[i])) * uint32(int32(b[i]))
	}
	return int32(s)
}

// clip13 clips to the range of a 14-bit signed number (av_clip_intp2(v, 13)).
func clip13(v int32) int32 { return max(-1<<13, min(v, 1<<13-1)) }

// decodeChannel reads one channel of a coded frame (decode_channel).
func (d *Decoder) decodeChannel(c int) error {
	gb := &d.gb
	ch := d.decoded[c]
	bps := d.info.BitsPerSample
	left := d.nbSamples - 1

	d.sampleShift[c] = int8(gb.escape4())
	if int(d.sampleShift[c]) >= bps {
		return corrupt("sample shift of %d bits", d.sampleShift[c])
	}
	ch[0] = gb.readSigned(uint(bps - int(d.sampleShift[c])))
	d.lpcMode[c] = int8(gb.read(2))
	d.nbSubframes = int(gb.read(3)) + 1

	i, prev := 0, 0
	if d.nbSubframes > 1 {
		if gb.bitsLeft() < (d.nbSubframes-1)*6 {
			return corrupt("subframe table is cut short")
		}
		for ; i < d.nbSubframes-1; i++ {
			v := int(gb.read(6))
			// the C code keeps the length in an int16
			d.subframeLen[i] = int(int16((v - prev) * d.subframeScale))
			if d.subframeLen[i] <= 0 {
				return corrupt("subframe of %d samples", d.subframeLen[i])
			}
			left -= d.subframeLen[i]
			prev = v
		}
		if left <= 0 {
			return corrupt("subframes cover more than the frame")
		}
	}
	d.subframeLen[i] = left

	off, prevSize := 1, 0
	for i := range d.nbSubframes {
		if err := d.decodeSubframe(ch, off, d.subframeLen[i], prevSize); err != nil {
			return err
		}
		off += d.subframeLen[i]
		prevSize = d.subframeLen[i]
	}
	return nil
}

// decorrelate undoes the joint coding of two channels (decorrelate).
func (d *Decoder) decorrelate(c1, c2, length int) error {
	gb := &d.gb
	skip := 0
	if d.dmode > 5 {
		skip = 1
	}
	p1, p2 := d.decoded[c1][skip:], d.decoded[c2][skip:]
	bp1, bp2 := p1[0], p2[0]
	if d.dmode < 6 {
		length++
	}
	if length > len(p1) {
		return corrupt("decorrelation of %d samples", length)
	}

	switch d.dmode {
	case 1: // left/side
		for i := range length {
			p2[i] = int32(uint32(p1[i]) + uint32(p2[i]))
		}
	case 2: // side/right
		for i := range length {
			p1[i] = int32(uint32(p2[i]) - uint32(p1[i]))
		}
	case 3: // side/mid
		for i := range length {
			a, b := uint32(p1[i]), p2[i]
			a -= uint32(b >> 1)
			p1[i] = int32(a)
			p2[i] = int32(a + uint32(b))
		}
	case 4, 5: // side/left and side/right with a scale factor
		if d.dmode == 4 {
			p1, p2 = p2, p1
			bp1, bp2 = bp2, bp1
		}
		dshift := uint(gb.escape4())
		dfactor := uint32(gb.readSigned(10))
		for i := range length {
			a, b := uint32(p1[i]), p2[i]
			b = int32(uint32(int32(dfactor*uint32(b>>dshift)+128)>>8) << dshift)
			p1[i] = int32(uint32(b) - a)
		}
	case 6, 7:
		if d.dmode == 6 {
			p1, p2 = p2, p1
		}
		if err := d.decorrelateFiltered(p1, p2, length); err != nil {
			return err
		}
	}

	if d.dmode > 0 && d.dmode < 6 {
		p1[0] = bp1
		p2[0] = bp2
	}
	return nil
}

// decorrelateFiltered is the FIR-filtered decorrelation of modes 6 and 7: the first channel gets the filtered
// second channel subtracted.
func (d *Decoder) decorrelateFiltered(p1, p2 []int32, length int) error {
	gb := &d.gb
	if length < 256 {
		return corrupt("filtered decorrelation of %d samples", length)
	}
	dshift := uint(gb.escape4())
	order := 8 << gb.read(1)
	dval1 := gb.bit()
	dval2 := gb.bit()

	codeSize := 0
	for i := range order {
		if i&3 == 0 {
			codeSize = 14 - int(gb.read(3))
		}
		d.filter[i] = int16(gb.readSigned(uint(codeSize)))
	}

	half := order / 2
	length2 := length - (order - 1)
	if length2 <= 0 || length > len(p1) || length > len(p2) {
		return corrupt("filtered decorrelation of %d samples", length)
	}

	if dval1 { // the beginning
		for i := range half {
			p1[i] = int32(uint32(p1[i]) + uint32(p2[i]))
		}
	}
	if dval2 { // the end
		for i := length2 + half; i < length; i++ {
			p1[i] = int32(uint32(p1[i]) + uint32(p2[i]))
		}
	}

	res := &d.residues
	in := 0 // read position in p2
	for i := range order {
		res[i] = int16(p2[in] >> dshift)
		in++
	}

	out := half
	window := len(res) - order
	for length2 > 0 {
		tmp := min(length2, window)
		n := tmp
		if tmp == length2 {
			n--
		}
		for i := range n {
			res[order+i] = int16(p2[in] >> dshift)
			in++
		}
		for i := range tmp {
			v := int32(1 << 9)
			if order == 16 {
				v += dot16(res[i:i+16], d.filter[:16])
			} else {
				for j := range 8 {
					v += int32(res[i+j]) * int32(d.filter[j])
				}
			}
			p1[out] = int32(uint32(clip13(v>>10)*int32(1<<dshift)) - uint32(p1[out]))
			out++
		}
		copy(res[:order], res[tmp:tmp+order])
		length2 -= tmp
	}
	return nil
}

// appendPCM converts the decoded frame to interleaved bytes.
func (d *Decoder) appendPCM(dst []byte) []byte {
	channels := d.info.Channels
	n := d.nbSamples
	dst = append(dst, make([]byte, n*channels*d.bytesPerSample)...)
	out := dst[len(dst)-n*channels*d.bytesPerSample:]
	switch d.bytesPerSample {
	case 1:
		for c := range channels {
			for i, v := range d.decoded[c][:n] {
				out[i*channels+c] = byte(uint32(v) + 0x80)
			}
		}
	case 2:
		for c := range channels {
			for i, v := range d.decoded[c][:n] {
				k := (i*channels + c) * 2
				out[k], out[k+1] = byte(v), byte(v>>8)
			}
		}
	case 3:
		for c := range channels {
			for i, v := range d.decoded[c][:n] {
				k := (i*channels + c) * 3
				out[k], out[k+1], out[k+2] = byte(v), byte(v>>8), byte(v>>16)
			}
		}
	}
	return dst
}
