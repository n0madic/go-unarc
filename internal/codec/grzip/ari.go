package grzip

// The arithmetic decoders of MTF_Ari.c and WFC_Ari.c. Both code the same symbols (a rank in the recency list and
// a run length, preceded by an end-of-block marker rank) with the same models; they differ only in how the
// recency list is kept: plain move-to-front, or the weighted frequency count of WFC.

const (
	modelBits = 11
	modelMax  = 1 << modelBits // total of every binary model
	rangeTop  = 1 << 24
	rleLevels = log2MaxBlock + 1 // run-length classes: runs of 2^k..2^(k+1)-1 bytes, k = 0..23

	// Adaptation shifts and rescaling thresholds of the models.
	rleShift0  = 6
	rleShift1  = 3
	rleShift2  = 6
	l1Shift0   = 4
	l1Shift1   = 6
	l2Shift    = 7
	l00MaxFreq = 58
	l01MaxFreq = 62
	l02MaxFreq = 204

	// A rank of 4 and more is coded as a group number (Elias-gamma like) plus a position inside the group.
	lastGroup  = 6
	endOfBlock = 127 // position 127 in the last group is the end marker
	rankMask   = 0xFF
)

// groupBegin is WFCMTF_GrNum2GrBegin: the first rank of each group.
var groupBegin = [lastGroup + 1]int{3, 5, 9, 17, 33, 65, 129}

// models is the adaptive state of the arithmetic coder.
type models struct {
	l00  [5]uint32                    // rank 0..2, 3 = "longer", [4] = total; shared by all contexts
	l01  [256][5]uint32               // the same, in the context of the previous character
	l02  [1024][5]uint32              // the same, in the context of the previous ranks and run classes
	l10  [8]uint32                    // group number, bit by bit
	l11  [8][8]uint32                 // the same, in the context of the previous group
	l20  [8][128]uint32               // position in group, bit by bit
	rle0 [64][rleLevels]uint32        // run class, in the context of the previous run classes and rank
	rle1 [256][rleLevels]uint32       // the same, in the context of the character
	rle2 [rleLevels][rleLevels]uint32 // low bits of the run length
}

// init resets the models to the encoder's initial state (Init_Models).
func (m *models) init() {
	m.l00 = [5]uint32{1, 1, 1, 1, 4}
	m.l01 = [256][5]uint32{}
	m.l02 = [1024][5]uint32{}
	fill := func(s []uint32) {
		for i := range s {
			s[i] = modelMax >> 1
		}
	}
	fill(m.l10[:])
	for i := range m.l11 {
		fill(m.l11[i][:])
	}
	for i := range m.l20 {
		fill(m.l20[i][:])
	}
	for i := range m.rle0 {
		fill(m.rle0[i][:])
	}
	for i := range m.rle1 {
		fill(m.rle1[i][:])
	}
	for i := range m.rle2 {
		fill(m.rle2[i][:])
	}
}

// rangeDecoder is the carry-less range decoder of the format. Reading past the end of the input yields zeros
// (the C decoder reads whatever follows the block), so a truncated stream decodes to garbage that the output
// size check rejects.
type rangeDecoder struct {
	in   []byte
	pos  int
	code uint32
	rng  uint32
}

func (r *rangeDecoder) init(in []byte) {
	r.in, r.pos, r.rng = in, 0, 0xFFFFFFFF
	r.code = 0
	r.next() // the first byte is the encoder's initial carry and drops out of the 32-bit code
	for range 4 {
		r.code = r.code<<8 | r.next()
	}
}

func (r *rangeDecoder) next() uint32 {
	var b uint32
	if r.pos < len(r.in) {
		b = uint32(r.in[r.pos])
	}
	r.pos++
	return b
}

func (r *rangeDecoder) normalize() {
	for r.rng < rangeTop {
		r.code = r.code<<8 | r.next()
		r.rng <<= 8
	}
}

// bit decodes a binary decision; p is the probability of a 0 out of modelMax.
func (r *rangeDecoder) bit(p uint32) uint32 {
	r.rng >>= modelBits
	if r.code/r.rng < p {
		r.rng *= p
		r.normalize()
		return 0
	}
	r.code -= p * r.rng
	r.rng *= modelMax - p
	r.normalize()
	return 1
}

// adapt moves a model probability towards the bit just coded.
func adapt(p *uint32, bit, shift uint32) {
	if bit == 0 {
		*p += (modelMax - *p) >> shift
	} else {
		*p -= *p >> shift
	}
}

// bit1 decodes a bit with probability p and adapts the single model.
func (r *rangeDecoder) bit1(p *uint32, shift uint32) uint32 {
	b := r.bit(*p)
	adapt(p, b, shift)
	return b
}

// bit2 decodes a bit with the average of two models and adapts both.
func (r *rangeDecoder) bit2(v, u *uint32, shiftV, shiftU uint32) uint32 {
	b := r.bit((*v + *u) >> 1)
	adapt(v, b, shiftV)
	adapt(u, b, shiftU)
	return b
}

// updateL0 halves the frequency counts of the first-level models that grew past their limits (Update_Model_L0).
func updateL0(m0, v, u *[5]uint32) {
	if m0[4] > l00MaxFreq {
		m0[0] = (m0[0] + 1) >> 1
		m0[1] = (m0[1] + 1) >> 1
		m0[2] = (m0[2] + 1) >> 1
		m0[3] = (m0[3] + 1) >> 1
		m0[4] = m0[0] + m0[1] + m0[2] + m0[3]
	}
	if v[4] > l01MaxFreq {
		v[0] >>= 1
		v[1] >>= 1
		v[2] >>= 1
		v[3] >>= 1
		v[4] = v[0] + v[1] + v[2] + v[3]
	}
	if u[4] > l02MaxFreq {
		u[0] >>= 1
		u[1] >>= 1
		u[2] >>= 1
		u[3] >>= 1
		u[4] = u[0] + u[1] + u[2] + u[3]
	}
}

// ariDecoder is the state of one arithmetic decoding run; a worker reuses it for every block.
type ariDecoder struct {
	rangeDecoder
	m   models
	mtf [256]byte
	wfc wfcList
}

// decode decodes the arithmetic coded text in into out and returns the number of bytes produced, which the
// caller compares with the size it expects. wfc selects the weighted frequency count list instead of
// move-to-front.
func (a *ariDecoder) decode(in, out []byte, wfc bool) (int, error) {
	m := &a.m
	m.init()
	if wfc {
		a.wfc.init()
	} else {
		for i := range a.mtf {
			a.mtf[i] = byte(i)
		}
	}
	a.rangeDecoder.init(in)

	var (
		char                  byte
		ctxRLE, ctxL0, ctxL1  uint32
		outPos, groupsDecoded int
	)
	for {
		// First level: rank 0..2 directly, 3 means "escape to the group coding".
		v := &m.l01[char]
		u := &m.l02[(4*ctxL0+ctxRLE&3)&1023]
		rank, ok := a.rank(&m.l00, v, u)
		if !ok {
			return outPos, errCorrupt("invalid symbol")
		}
		if rank == 3 {
			grNum, grPos := a.group(&ctxL1)
			if grNum == lastGroup && grPos == endOfBlock {
				return outPos, nil
			}
			rank = groupBegin[grNum] + grPos
		}
		pos := (rank + 1) & rankMask // rank 255 wraps to list position 0

		if wfc {
			char = a.wfc.pick(pos, groupsDecoded)
			groupsDecoded++
		} else {
			char = a.mtf[pos]
			copy(a.mtf[1:pos+1], a.mtf[:pos])
			a.mtf[0] = char
		}

		rank = min((pos-1)&rankMask, 3)
		ctxL0 = (ctxL0<<2 | uint32(rank)) & 0xFF

		// Run length: class k (unary, adaptive) and the k low bits of the run.
		log2 := 0
		vr := &m.rle0[(int(ctxRLE)+16*rank)&63]
		ur := &m.rle1[char]
		for {
			if log2 >= rleLevels {
				return outPos, errCorrupt("run too long")
			}
			if a.bit2(&vr[log2], &ur[log2], rleShift0, rleShift1) == 0 {
				break
			}
			log2++
		}
		ctxRLE = (ctxRLE << 1) & 0xF
		if log2 >= 2 {
			ctxRLE |= 1
		}
		low := &m.rle2[log2]
		run := 0
		for i := range log2 {
			run = run<<1 | int(a.bit1(&low[i], rleShift2))
		}
		run += 1 << log2
		if run > len(out)-outPos {
			return outPos, errCorrupt("decoded text exceeds its size")
		}
		s := out[outPos : outPos+run]
		for i := range s {
			s[i] = char
		}
		outPos += run
	}
}

// rank decodes the first-level symbol: 0..3 from the sum of the three frequency models.
func (a *ariDecoder) rank(m0, v, u *[5]uint32) (int, bool) {
	r := &a.rangeDecoder
	r.rng /= m0[4] + u[4] + v[4]
	freq := r.code / r.rng
	var cum uint32
	for rank := range 4 {
		f := m0[rank] + u[rank] + v[rank]
		if freq < cum+f {
			r.code -= cum * r.rng
			r.rng *= f
			r.normalize()
			m0[rank] += 2
			u[rank] += 2
			v[rank] += 2
			m0[4] += 2
			u[4] += 2
			v[4] += 2
			updateL0(m0, v, u)
			return rank, true
		}
		cum += f
	}
	return 0, false // only reachable with corrupt data
}

// group decodes a group number (unary) and the position inside the group (binary tree). The previous group
// number is the context of the first and is replaced by the new one.
func (a *ariDecoder) group(ctxL1 *uint32) (grNum, grPos int) {
	m := &a.m
	v := &m.l10
	u := &m.l11[*ctxL1&7]
	for grNum < lastGroup {
		if a.bit2(&v[grNum], &u[grNum], l1Shift0, l1Shift1) == 0 {
			break
		}
		grNum++
	}
	*ctxL1 = uint32(grNum)
	pm := &m.l20[grNum]
	ctx := 1
	for range grNum + 1 {
		b := int(a.bit1(&pm[ctx&127], l2Shift))
		ctx = ctx<<1 | b
		grPos = grPos<<1 | b
	}
	return grNum, grPos
}
