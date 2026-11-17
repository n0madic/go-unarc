// Package taktest builds small TAK streams for tests. There is no TAK encoder to make test data with, so
// this writes the format by hand: metadata, frame headers, stored samples and channels with their residues
// coded in one fixed mode. It shares no code with package tak (the CRC is computed bit by bit), so a stream
// built here and decoded there checks both.
package taktest

// BitWriter writes bit fields least significant bit first, the order TAK reads them in.
type BitWriter struct {
	buf  []byte
	bits int
}

// Put appends the n low bits of v.
func (w *BitWriter) Put(v uint64, n int) {
	for i := range n {
		if w.bits%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		w.buf[w.bits/8] |= byte(v>>i&1) << (w.bits % 8)
		w.bits++
	}
}

// PutSigned appends v as an n-bit two's complement number.
func (w *BitWriter) PutSigned(v int64, n int) { w.Put(uint64(v)&(1<<n-1), n) }

// Flag appends one bit.
func (w *BitWriter) Flag(b bool) {
	if b {
		w.Put(1, 1)
	} else {
		w.Put(0, 1)
	}
}

// Align pads with zero bits to a byte boundary.
func (w *BitWriter) Align() { w.bits = (w.bits + 7) &^ 7 }

// Bytes returns what has been written, the last byte padded with zero bits.
func (w *BitWriter) Bytes() []byte { return w.buf }

// CRC24 is the CRC TAK puts after metadata blocks, frame headers and frames, as the three bytes to store: the
// CRC-24 of polynomial 0x864CFB, most significant bit first, started at 0xB704CE, with the least significant
// byte first.
func CRC24(p []byte) []byte {
	crc := uint32(0xB704CE)
	for _, b := range p {
		crc ^= uint32(b) << 16
		for range 8 {
			if crc&0x800000 != 0 {
				crc = crc<<1 ^ 0x864CFB
			} else {
				crc <<= 1
			}
		}
	}
	return []byte{byte(crc), byte(crc >> 8), byte(crc >> 16)}
}

// Info is what a stream info block says. FrameType is the index of the frame size (7 is 512 samples, 8 is
// 1024, 9 is 2048).
type Info struct {
	Codec      int // 2 for mono and stereo, 4 for multichannel
	SampleRate int
	Channels   int
	BPS        int
	FrameType  int
	Samples    int64
}

// put writes the stream info fields.
func (i Info) put(w *BitWriter) {
	w.Put(uint64(i.Codec), 6)
	w.Put(0, 4) // profile
	w.Put(uint64(i.FrameType), 4)
	w.Put(uint64(i.Samples), 35)
	w.Put(0, 3) // data type
	w.Put(uint64(i.SampleRate-6000), 18)
	w.Put(uint64(i.BPS-8), 5)
	w.Put(uint64(i.Channels-1), 4)
	w.Put(0, 1) // no valid bits, no channel layout
}

// Metadata is the start of a stream: the magic, a stream info block and the end block.
func Metadata(i Info) []byte {
	var w BitWriter
	i.put(&w)
	payload := w.Bytes()
	out := []byte("tBaK")
	out = append(out, 1, byte(len(payload)+3), 0, 0)
	out = append(out, payload...)
	out = append(out, CRC24(payload)...)
	return append(out, 0, 0, 0, 0)
}

// Frame is the header and the body of one frame. The body is written by body, which gets a writer at the
// first bit after the header. last is the number of samples in a last frame (0 for any other); info, if not
// nil, is put into the header.
func Frame(number, last int, info *Info, body func(w *BitWriter)) []byte {
	var h BitWriter
	h.Put(0xA0FF, 16)
	flags := 0
	if last != 0 {
		flags |= 1
	}
	if info != nil {
		flags |= 2
	}
	h.Put(uint64(flags), 3)
	h.Put(uint64(number), 21)
	if last != 0 {
		h.Put(uint64(last-1), 14)
		h.Put(0, 2)
	}
	if info != nil {
		info.put(&h)
		h.Put(0, 6)
		h.Align()
	}
	out := append([]byte(nil), h.Bytes()...)
	out = append(out, CRC24(out)...)

	var b BitWriter
	body(&b)
	b.Align()
	out = append(out, b.Bytes()...)
	return append(out, CRC24(b.Bytes())...)
}

// StoredSamples writes the body of a frame of fewer than 16 samples: every sample as a signed number of bps
// bits, channel after channel.
func StoredSamples(w *BitWriter, bps int, channels [][]int32) {
	for _, ch := range channels {
		for _, v := range ch {
			w.PutSigned(int64(v), bps)
		}
	}
}

// Mode9 is the residue coding mode CodedChannel uses: 6-bit codes, and a residue of zigzag value 44 or more
// is followed by a zero flag. It takes residues from -32 to 31.
const Mode9 = 9

// Channel describes one channel of a coded frame.
type Channel struct {
	Shift     int     // low bits that are zero in every sample
	First     int32   // the first sample, before the shift
	LPC       int     // first-stage predictor, 0 to 3
	Residues  []int32 // the other samples, as residues of Mode9
	Subframes []int   // where the subframes but the last end, counted in subframe scales from the second sample
}

// WriteChannel writes a channel: a subframe per entry of Subframes and one more, none of them filtered.
// The residues are split between the subframes at the places Subframes gives; subframeScale is the number of
// samples in a unit of it, which depends on the sample rate (88 at 22050 Hz).
func WriteChannel(w *BitWriter, bps, subframeScale int, c Channel) {
	if c.Shift > 0 {
		w.Put(1, 1)
		w.Put(uint64(c.Shift-1), 4)
	} else {
		w.Put(0, 1)
	}
	w.PutSigned(int64(c.First), bps-c.Shift)
	w.Put(uint64(c.LPC), 2)
	w.Put(uint64(len(c.Subframes)), 3) // number of subframes - 1
	for _, v := range c.Subframes {
		w.Put(uint64(v), 6)
	}
	// the subframes: lengths (v - prev) * scale, then the rest
	lengths := make([]int, 0, len(c.Subframes)+1)
	prev, total := 0, 0
	for _, v := range c.Subframes {
		lengths = append(lengths, (v-prev)*subframeScale)
		total += (v - prev) * subframeScale
		prev = v
	}
	lengths = append(lengths, len(c.Residues)-total)
	pos := 0
	for _, n := range lengths {
		w.Put(0, 1) // no filter
		w.Put(0, 1) // one residue segment
		w.Put(Mode9, 6)
		for _, r := range c.Residues[pos : pos+n] {
			zigzag := uint64(uint32(r<<1) ^ uint32(r>>31))
			w.Put(zigzag, 6)
			if zigzag >= 0x2C {
				w.Put(0, 1)
			}
		}
		pos += n
	}
}
