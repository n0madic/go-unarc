package ppmd

import (
	"fmt"
	"io"
)

const (
	bufSize = 64 << 10 // BUFFER_SIZE of the C code

	// maxEOFReads bounds the bytes the range decoder may take after the end of the input. Valid
	// streams never need any. Like the C decoder, a stream that ends early is continued with 0xFF
	// bytes, which decodes streams cut short by a byte or two completely and ends the others within a
	// few symbols; the bound only guarantees that.
	maxEOFReads = 64

	eofByte = ^uint32(0) // what the C PRIME_STREAM returns at end of input, ORed into the code
)

// decoder is the PPMd model plus the range decoder and the stream buffers.
type decoder struct {
	model

	// range decoder: low, code, range of the carryless coder and the current sub-range
	low, code, rng             uint32
	lowCount, highCount, scale uint32

	src      io.Reader
	inBuf    []byte
	inPos    int
	inEnd    int
	srcDone  bool
	eofReads int

	dst    io.Writer
	outBuf []byte
	outPos int
}

func newDecoder(order int, mem uint32, mr uint32, dst io.Writer, src io.Reader) (*decoder, error) {
	d := new(decoder)
	return d, d.reset(order, mem, mr, dst, src)
}

// reset prepares d for a new stream. It keeps the storage of the model's heap and of the stream buffers:
// the model never reads heap bytes it has not written since startModel, which is also what lets it restart
// in place when its memory is full (and the C code takes the heap from malloc, dirty).
func (d *decoder) reset(order int, mem uint32, mr uint32, dst io.Writer, src io.Reader) error {
	heap, inBuf, outBuf := d.heap, d.inBuf, d.outBuf
	*d = decoder{src: src, dst: dst, inBuf: inBuf, outBuf: outBuf}
	if d.inBuf == nil {
		d.inBuf = make([]byte, bufSize)
		d.outBuf = make([]byte, bufSize)
	}
	d.model.allocator = newAllocator(mem, heap)
	if !d.startModel(order, mr) {
		return fmt.Errorf("ppmd: %d bytes of model memory are too few", mem)
	}
	return nil
}

// getc returns the next input byte, or eofByte at the end of the input or after a read error.
func (d *decoder) getc() uint32 {
	if d.inPos < d.inEnd {
		b := d.inBuf[d.inPos]
		d.inPos++
		return uint32(b)
	}
	return d.fill()
}

func (d *decoder) fill() uint32 {
	if d.err == nil && !d.srcDone {
		n, err := io.ReadFull(d.src, d.inBuf)
		switch err {
		case nil:
		case io.EOF, io.ErrUnexpectedEOF:
			d.srcDone = true
		default:
			d.err = err
		}
		d.inPos, d.inEnd = 0, n
		if n > 0 {
			d.inPos = 1
			return uint32(d.inBuf[0])
		}
	}
	if d.err == nil {
		if d.eofReads++; d.eofReads > maxEOFReads {
			d.corrupt("unexpected end of data")
		}
	}
	return eofByte
}

func (d *decoder) putc(b byte) {
	if d.outPos == len(d.outBuf) {
		d.flush()
	}
	d.outBuf[d.outPos] = b
	d.outPos++
}

func (d *decoder) flush() {
	if d.err == nil && d.outPos > 0 {
		if _, err := d.dst.Write(d.outBuf[:d.outPos]); err != nil {
			d.err = err
		}
	}
	d.outPos = 0
}

// Range decoder (Coder.hpp).

func (d *decoder) normalize() {
	for {
		if (d.low ^ (d.low + d.rng)) >= rcTop {
			if d.rng >= rcBot {
				return
			}
			d.rng = -d.low & (rcBot - 1)
		}
		d.code = d.code<<8 | d.getc()
		d.rng <<= 8
		d.low <<= 8
	}
}

// currentCount is ariGetCurrentCount.
func (d *decoder) currentCount() uint32 {
	d.rng /= d.scale
	if d.rng == 0 {
		d.corrupt("range coder underflow")
		d.rng = 1
	}
	return (d.code - d.low) / d.rng
}

func (d *decoder) removeSubrange() {
	d.low += d.rng * d.lowCount
	d.rng *= d.highCount - d.lowCount
}

func (d *decoder) decodeBinSymbol(c uint32) {
	h := d.heap
	indx := ns2BSIndx[h[d.suffix(c)+ctxNumStats]] + d.prevSuccess + h[c+ctxFlags]
	rs := c + ctxOne
	bs := &d.binSumm[qTable[uint8(h[rs+stFreq]-1)]][(uint32(indx)+uint32(d.runLength>>26)&0x20)&63]
	d.rng >>= totBits
	if (d.code-d.low)/d.rng < uint32(*bs) {
		d.foundState = rs
		h[rs+stFreq] += b2u8(h[rs+stFreq] < 196)
		d.lowCount = 0
		d.highCount = uint32(*bs)
		*bs += interval - (*bs+1<<(periodBits-2))>>periodBits
		d.prevSuccess = 1
		d.runLength++
	} else {
		d.lowCount = uint32(*bs)
		*bs -= (*bs + 1<<(periodBits-2)) >> periodBits
		d.highCount = binScale
		d.initEsc = uint32(expEscape[*bs>>10])
		d.charMask[h[rs+stSymbol]] = d.escCount
		d.numMasked = 0
		d.prevSuccess = 0
		d.foundState = 0
	}
}

func (d *decoder) update1(c, p uint32) {
	h := d.heap
	d.foundState = p
	h[p+stFreq] += 4
	d.addSummFreq(c, 4)
	if h[p+stFreq] > h[p-stateSize+stFreq] {
		d.swapStates(p, p-stateSize)
		p -= stateSize
		d.foundState = p
		if h[p+stFreq] > maxFreq {
			d.rescale(c)
		}
	}
}

func (d *decoder) decodeSymbol1(c uint32) {
	h := d.heap
	stats := d.stats(c)
	d.scale = d.summFreq(c)
	hiCnt := uint32(h[stats+stFreq])
	count := d.currentCount()
	if count < hiCnt {
		d.highCount = hiCnt
		d.prevSuccess = b2u8(2*hiCnt >= d.scale)
		d.foundState = stats
		hiCnt += 4
		h[stats+stFreq] = uint8(hiCnt)
		d.addSummFreq(c, 4)
		d.runLength += int32(d.prevSuccess)
		if hiCnt > maxFreq {
			d.rescale(c)
		}
		d.lowCount = 0
		return
	}
	d.prevSuccess = 0
	p := stats
	for i := uint32(h[c+ctxNumStats]); ; {
		p += stateSize
		hiCnt += uint32(h[p+stFreq])
		if hiCnt > count {
			break
		}
		if i--; i == 0 {
			d.lowCount = hiCnt
			d.charMask[h[p+stSymbol]] = d.escCount
			d.numMasked = h[c+ctxNumStats]
			d.foundState = 0
			for j := d.numMasked; j > 0; j-- {
				p -= stateSize
				d.charMask[h[p+stSymbol]] = d.escCount
			}
			d.highCount = d.scale
			return
		}
	}
	d.highCount = hiCnt
	d.lowCount = hiCnt - uint32(h[p+stFreq])
	d.update1(c, p)
}

func (d *decoder) update2(c, p uint32) {
	h := d.heap
	d.foundState = p
	h[p+stFreq] += 4
	d.addSummFreq(c, 4)
	if h[p+stFreq] > maxFreq {
		d.rescale(c)
	}
	d.escCount++
	d.runLength = d.initRL
}

// makeEscFreq2 picks the SEE context of c and sets the escape frequency in d.scale.
func (d *decoder) makeEscFreq2(c uint32) *see2Context {
	h := d.heap
	ns := uint32(h[c+ctxNumStats])
	if ns == 0xFF {
		d.scale = 1
		return &d.dummySEE2
	}
	t := uint32(h[d.suffix(c)+ctxNumStats])
	idx := 2*b2u(2*ns < t+uint32(d.numMasked)) + b2u(d.summFreq(c) > 11*(ns+1)) + uint32(h[c+ctxFlags])
	psee2c := &d.see2[qTable[ns+2]-3][idx&31]
	d.scale = psee2c.mean()
	return psee2c
}

func (d *decoder) decodeSymbol2(c uint32) {
	h := d.heap
	psee2c := d.makeEscFreq2(c)
	ps := &d.psSymbol
	n := 0
	hiCnt := uint32(0)
	ns := uint32(h[c+ctxNumStats])
	i := ns - uint32(d.numMasked)
	p := d.stats(c) - stateSize
	for {
		for {
			p += stateSize
			if d.charMask[h[p+stSymbol]] != d.escCount {
				break
			}
		}
		hiCnt += uint32(h[p+stFreq])
		ps[n&255] = p
		n++
		if i--; i == 0 {
			break
		}
	}
	d.scale += hiCnt
	count := d.currentCount()
	if count < hiCnt {
		hiCnt = 0
		k := 0
		p = ps[0]
		for {
			hiCnt += uint32(h[p+stFreq])
			if hiCnt > count {
				break
			}
			k++
			p = ps[k&255]
		}
		d.highCount = hiCnt
		d.lowCount = hiCnt - uint32(h[p+stFreq])
		psee2c.update()
		d.update2(c, p)
	} else {
		d.lowCount = hiCnt
		d.highCount = d.scale
		for k := 0; k < n; k++ {
			d.charMask[h[ps[k]+stSymbol]] = d.escCount
		}
		d.numMasked = uint8(ns)
		psee2c.summ += uint16(d.scale)
	}
}

func (d *decoder) clearMask() {
	d.escCount = 1
	d.charMask = [256]uint8{}
}

// run decodes until the end-of-data escape or an error.
func (d *decoder) run() {
	d.low, d.code, d.rng = 0, 0, ^uint32(0)
	for range 4 {
		d.code = d.code<<8 | d.getc()
	}
	h := d.heap
	minCtx := d.maxContext
	ns := h[minCtx+ctxNumStats]
	for {
		if ns != 0 {
			d.decodeSymbol1(minCtx)
		} else {
			d.decodeBinSymbol(minCtx)
		}
		d.removeSubrange()
		for d.foundState == 0 {
			d.normalize()
			for {
				d.orderFall++
				minCtx = d.suffix(minCtx)
				if minCtx == 0 {
					return
				}
				if h[minCtx+ctxNumStats] != d.numMasked {
					break
				}
			}
			d.decodeSymbol2(minCtx)
			d.removeSubrange()
		}
		d.putc(h[d.foundState+stSymbol])
		if s := d.succ(d.foundState); d.orderFall == 0 && s >= d.unitsStart {
			d.maxContext = s
		} else {
			d.updateModel(minCtx)
			if d.escCount == 0 {
				d.clearMask()
			}
		}
		minCtx = d.maxContext
		ns = h[minCtx+ctxNumStats]
		d.normalize()
		if d.err != nil {
			return
		}
	}
}
