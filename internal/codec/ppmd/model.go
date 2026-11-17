package ppmd

import (
	"fmt"

	"github.com/n0madic/go-unarc/internal/method"
)

// The PPMII model (Model.cpp): contexts, states and their update. All structures live in the
// allocator's heap and are addressed by offsets.

// see2Context is an SEE context for PPM contexts with masked symbols.
type see2Context struct {
	summ  uint16
	shift uint8
	count uint8
}

func (s *see2Context) init(initVal uint32) {
	s.shift = periodBits - 4
	s.summ = uint16(initVal << s.shift)
	s.count = 7
}

func (s *see2Context) mean() uint32 {
	r := uint32(s.summ >> s.shift)
	s.summ -= uint16(r)
	if r == 0 {
		r++
	}
	return r
}

func (s *see2Context) update() {
	if s.shift < periodBits {
		if s.count--; s.count == 0 {
			s.summ += s.summ
			s.count = uint8(3 << s.shift)
			s.shift++
		}
	}
}

// maxStalledCutOffs is the number of consecutive cut-off passes that free no memory after which the
// model is declared stuck.
const maxStalledCutOffs = 8

// counters count the rare events of a stream, the tests use them to check that a stream really
// exercises memory exhaustion.
type counters struct {
	restarts, cutOffs, freezes, frozen int
}

// model is the state of the PPMd model. Fields correspond to the static variables of Model.cpp.
type model struct {
	allocator

	maxContext uint32 // the longest context of the current position
	foundState uint32 // state found by the last decoding step, 0 after an escape

	initEsc   uint32
	orderFall int
	runLength int32
	initRL    int32
	maxOrder  int
	mrMethod  uint32

	charMask    [256]uint8
	numMasked   uint8
	prevSuccess uint8
	escCount    uint8

	binSumm   [25][64]uint16
	see2      [24][32]see2Context
	dummySEE2 see2Context

	counts counters

	// err is the first error of the stream (corrupt data, a failed write); decoding stops at it.
	err error

	// Scratch stacks of states, reused between calls (the C code keeps them on the stack).
	psReduce [maxO + 2]uint32
	psCreate [maxO + 2]uint32
	psSymbol [256]uint32
}

// corrupt records that the stream cannot be decoded.
func (m *model) corrupt(why string) {
	if m.err == nil {
		m.err = fmt.Errorf("%w: %s", method.ErrBadData, why)
	}
}

// Typed accessors of the context fields.

func (m *model) summFreq(c uint32) uint32 { return uint32(m.get16(c + ctxSummFreq)) }
func (m *model) setSummFreq(c uint32, v uint32) {
	m.set16(c+ctxSummFreq, uint16(v))
}
func (m *model) addSummFreq(c uint32, v uint32) {
	m.set16(c+ctxSummFreq, m.get16(c+ctxSummFreq)+uint16(v))
}
func (m *model) stats(c uint32) uint32  { return m.get32(c + ctxStats) }
func (m *model) suffix(c uint32) uint32 { return m.get32(c + ctxSuffix) }
func (m *model) succ(s uint32) uint32   { return m.get32(s + stSucc) }

func b2u(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// swapStates exchanges two states.
func (m *model) swapStates(s1, s2 uint32) {
	a := (*[stateSize]byte)(m.heap[s1 : s1+stateSize])
	b := (*[stateSize]byte)(m.heap[s2 : s2+stateSize])
	*a, *b = *b, *a
}

// stateCpy copies state src to dst.
func (m *model) stateCpy(dst, src uint32) {
	*(*[stateSize]byte)(m.heap[dst : dst+stateSize]) = *(*[stateSize]byte)(m.heap[src : src+stateSize])
}

// startModel (StartModelRare) resets the model for a stream of the given maximum order. It reports
// false when the heap is too small for the root context.
func (m *model) startModel(order int, mr uint32) bool {
	m.charMask = [256]uint8{}
	m.escCount = 1
	m.orderFall = order
	m.maxOrder = order
	m.mrMethod = mr
	m.initSubAllocator()
	m.initRL = -int32(min(order, 12)) - 1
	m.runLength = m.initRL
	root := m.allocContext()
	if root == 0 {
		return false
	}
	m.maxContext = root
	h := m.heap
	h[root+ctxNumStats] = 255
	h[root+ctxFlags] = 0
	m.set32(root+ctxSuffix, 0)
	m.setSummFreq(root, 255+2)
	stats := m.allocUnits(256 / 2)
	if stats == 0 {
		return false
	}
	m.set32(root+ctxStats, stats)
	for i := range 256 {
		s := stats + uint32(i)*stateSize
		h[s+stSymbol] = uint8(i)
		h[s+stFreq] = 1
		m.set32(s+stSucc, 0)
	}
	m.prevSuccess = 0
	var i uint32
	for k := range m.binSumm {
		for uint32(qTable[i]) == uint32(k) {
			i++
		}
		row := &m.binSumm[k]
		for j := range 8 {
			row[j] = uint16(binScale - uint32(initBinEsc[j])/(i+1))
		}
		for j := 8; j < 64; j += 8 {
			copy(row[j:j+8], row[:8])
		}
	}
	i = 0
	for k := range m.see2 {
		for uint32(qTable[i+3]) == uint32(k+3) {
			i++
		}
		m.see2[k][0].init(2*i + 5)
		for j := 1; j < 32; j++ {
			m.see2[k][j] = m.see2[k][0]
		}
	}
	m.dummySEE2 = see2Context{summ: 0xAF8F, shift: 0xAC, count: 0x84}
	return true
}

// refresh rebuilds the states array of a context after states were removed or frequencies scaled.
func (m *model) refresh(c, oldNU uint32, scale uint32) {
	h := m.heap
	i := uint32(h[c+ctxNumStats])
	p := m.shrinkUnits(m.stats(c), oldNU, (i+2)>>1)
	m.set32(c+ctxStats, p)
	flags := (h[c+ctxFlags] & uint8(0x10+0x04*scale)) + 0x08*b2u8(h[p+stSymbol] >= 0x40)
	escFreq := int32(m.summFreq(c)) - int32(h[p+stFreq])
	f := (uint32(h[p+stFreq]) + scale) >> scale
	h[p+stFreq] = uint8(f)
	summ := f
	for ; i > 0; i-- {
		p += stateSize
		escFreq -= int32(h[p+stFreq])
		f = (uint32(h[p+stFreq]) + scale) >> scale
		h[p+stFreq] = uint8(f)
		summ += f
		flags |= 0x08 * b2u8(h[p+stSymbol] >= 0x40)
	}
	escFreq = (escFreq + int32(scale)) >> scale
	summ += uint32(escFreq)
	h[c+ctxFlags] = flags
	m.setSummFreq(c, summ)
}

// cutOff removes the least useful part of the context tree below c (order is c's depth) and returns
// c, or 0 when c itself was removed.
func (m *model) cutOff(c uint32, order int) uint32 {
	h := m.heap
	ns := int(h[c+ctxNumStats])
	if ns == 0 {
		one := c + ctxOne
		if s := m.succ(one); s >= m.unitsStart {
			if order < m.maxOrder {
				s = m.cutOff(s, order+1)
			} else {
				s = 0
			}
			m.set32(one+stSucc, s)
			if s == 0 && order > oBound {
				m.specialFreeUnit(c)
				return 0
			}
			return c
		}
		m.specialFreeUnit(c)
		return 0
	}
	tmp := uint32(ns+2) >> 1
	stats := m.moveUnitsUp(m.stats(c), tmp)
	m.set32(c+ctxStats, stats)
	i := ns
	for p := stats + uint32(i)*stateSize; p >= stats; p -= stateSize {
		s := m.succ(p)
		switch {
		case s < m.unitsStart:
			m.set32(p+stSucc, 0)
			m.swapStates(p, stats+uint32(i)*stateSize)
			i--
		case order < m.maxOrder:
			m.set32(p+stSucc, m.cutOff(s, order+1))
		default:
			m.set32(p+stSucc, 0)
		}
	}
	if i != ns && order != 0 {
		h[c+ctxNumStats] = uint8(i)
		p := stats
		switch {
		case i < 0:
			m.freeUnits(p, tmp)
			m.specialFreeUnit(c)
			return 0
		case i == 0:
			h[c+ctxFlags] = (h[c+ctxFlags] & 0x10) + 0x08*b2u8(h[p+stSymbol] >= 0x40)
			m.stateCpy(c+ctxOne, p)
			m.freeUnits(p, tmp)
			h[c+ctxOne+stFreq] = (h[c+ctxOne+stFreq] + 11) >> 3
		default:
			m.refresh(c, tmp, b2u(m.summFreq(c) > uint32(16*i)))
		}
	}
	return c
}

// removeBinConts drops the binary contexts below c that have no use (freeze method).
func (m *model) removeBinConts(c uint32, order int) uint32 {
	h := m.heap
	if h[c+ctxNumStats] == 0 {
		one := c + ctxOne
		s := m.succ(one)
		if s >= m.unitsStart && order < m.maxOrder {
			s = m.removeBinConts(s, order+1)
		} else {
			s = 0
		}
		m.set32(one+stSucc, s)
		if s == 0 {
			suf := m.suffix(c)
			if h[suf+ctxNumStats] == 0 || h[suf+ctxFlags] == 0xFF {
				m.freeUnits(c, 1)
				return 0
			}
		}
		return c
	}
	stats := m.stats(c)
	for p := stats + uint32(h[c+ctxNumStats])*stateSize; p >= stats; p -= stateSize {
		s := m.succ(p)
		if s >= m.unitsStart && order < m.maxOrder {
			m.set32(p+stSucc, m.removeBinConts(s, order+1))
		} else {
			m.set32(p+stSucc, 0)
		}
	}
	return c
}

// restoreModelRare is called when memory is exhausted in updateModel; pc1 is the context where
// the update stopped, minCtx the one the symbol was found in.
func (m *model) restoreModelRare(pc1, minCtx, fSuccessor uint32) {
	h := m.heap
	pc := m.maxContext
	m.pText = m.heapStart
	for ; pc != pc1; pc = m.suffix(pc) {
		if h[pc+ctxNumStats]--; h[pc+ctxNumStats] == 0 {
			p := m.stats(pc)
			h[pc+ctxFlags] = (h[pc+ctxFlags] & 0x10) + 0x08*b2u8(h[p+stSymbol] >= 0x40)
			m.stateCpy(pc+ctxOne, p)
			m.specialFreeUnit(p)
			h[pc+ctxOne+stFreq] = (h[pc+ctxOne+stFreq] + 11) >> 3
		} else {
			m.refresh(pc, (uint32(h[pc+ctxNumStats])+3)>>1, 0)
		}
	}
	for ; pc != minCtx; pc = m.suffix(pc) {
		if h[pc+ctxNumStats] == 0 {
			h[pc+ctxOne+stFreq] -= h[pc+ctxOne+stFreq] >> 1
		} else {
			m.addSummFreq(pc, 4)
			if m.summFreq(pc) > 128+4*uint32(h[pc+ctxNumStats]) {
				m.refresh(pc, (uint32(h[pc+ctxNumStats])+2)>>1, 1)
			}
		}
	}
	switch {
	case m.mrMethod > mrFreeze:
		m.counts.frozen++
		m.maxContext = fSuccessor
		m.glueCount += 1 - m.bList[1].stamp&1
	case m.mrMethod == mrFreeze:
		m.counts.freezes++
		for m.suffix(m.maxContext) != 0 {
			m.maxContext = m.suffix(m.maxContext)
		}
		m.removeBinConts(m.maxContext, 0)
		m.mrMethod++
		m.glueCount = 0
		m.orderFall = m.maxOrder
	case m.mrMethod == mrRestart || m.usedMemory() < m.size>>1:
		m.counts.restarts++
		// The heap held the root context of the same size when the stream started, so this cannot fail.
		m.startModel(m.maxOrder, m.mrMethod)
		m.escCount = 0
	default:
		m.counts.cutOffs++
		for m.suffix(m.maxContext) != 0 {
			m.maxContext = m.suffix(m.maxContext)
		}
		// The C code repeats this until enough memory is free, which never happens when the heap
		// is too small for even the order-0 model; give up when the passes stop helping.
		used, stalled := m.usedMemory(), 0
		for {
			m.cutOff(m.maxContext, 0)
			m.expandTextArea()
			cur := m.usedMemory()
			if cur <= 3*(m.size>>2) {
				break
			}
			if cur < used {
				stalled = 0
			} else if stalled++; stalled > maxStalledCutOffs {
				m.corrupt("model memory too small to cut the model off")
				break
			}
			used = cur
		}
		m.glueCount = 0
		m.orderFall = m.maxOrder
	}
}

// freezeChain points the successors of the stacked states at pc and resets the text area (FROZEN
// label of ReduceOrder).
func (m *model) freezeChain(ps []uint32, pc uint32) {
	for i := len(ps) - 1; i >= 0; i-- {
		m.set32(ps[i]+stSucc, pc)
	}
	m.pText = m.heapStart + 1
	m.orderFall = 1
}

// reduceOrder finds the context a symbol should link to when the successor is still a raw text pointer.
func (m *model) reduceOrder(p, pc uint32) uint32 {
	h := m.heap
	ps := &m.psReduce
	n := 0
	pc1 := pc
	upBranch := m.pText
	fs := m.foundState
	sym := h[fs+stSymbol]
	ps[n] = fs
	n++
	m.set32(fs+stSucc, upBranch)
	m.orderFall++
	first := p != 0
	if first {
		pc = m.suffix(pc)
	}
	for {
		if !first {
			suf := m.suffix(pc)
			if suf == 0 {
				if m.mrMethod > mrFreeze {
					m.freezeChain(ps[:n], pc)
				}
				return pc
			}
			pc = suf
			if h[pc+ctxNumStats] != 0 {
				p = m.stats(pc)
				if h[p+stSymbol] != sym {
					for {
						t := h[p+stateSize+stSymbol]
						p += stateSize
						if t == sym {
							break
						}
					}
				}
				tmp := 2 * b2u8(h[p+stFreq] < maxFreq-9)
				h[p+stFreq] += tmp
				m.addSummFreq(pc, uint32(tmp))
			} else {
				p = pc + ctxOne
				h[p+stFreq] += b2u8(h[p+stFreq] < 32)
			}
		}
		first = false
		if m.succ(p) != 0 {
			break
		}
		ps[n] = p
		n++
		m.set32(p+stSucc, upBranch)
		m.orderFall++
	}
	if m.mrMethod > mrFreeze {
		pc = m.succ(p)
		m.freezeChain(ps[:n], pc)
		return pc
	} else if m.succ(p) <= upBranch {
		p1 := m.foundState
		m.foundState = p
		m.set32(p+stSucc, m.createSuccessors(false, 0, pc))
		m.foundState = p1
	}
	if m.orderFall == 1 && pc1 == m.maxContext {
		m.set32(m.foundState+stSucc, m.succ(p))
		m.pText--
	}
	return m.succ(p)
}

// rescale halves the frequencies of the context c, whose state m.foundState has just overflowed.
func (m *model) rescale(c uint32) {
	h := m.heap
	i := uint32(h[c+ctxNumStats])
	stats := m.stats(c)
	p := m.foundState
	for ; p != stats; p -= stateSize {
		m.swapStates(p, p-stateSize)
	}
	h[p+stFreq] += 4
	m.addSummFreq(c, 4)
	escFreq := m.summFreq(c) - uint32(h[p+stFreq])
	adder := b2u(m.orderFall != 0 || m.mrMethod > mrFreeze)
	f := (uint32(h[p+stFreq]) + adder) >> 1
	h[p+stFreq] = uint8(f)
	summ := f
	for ; i > 0; i-- {
		p += stateSize
		escFreq -= uint32(h[p+stFreq])
		f = (uint32(h[p+stFreq]) + adder) >> 1
		h[p+stFreq] = uint8(f)
		summ += f
		if h[p+stFreq] > h[p-stateSize+stFreq] {
			tmpW, tmpS, tmpF := m.get16(p), m.get32(p+2), h[p+stFreq]
			p1 := p
			for {
				m.stateCpy(p1, p1-stateSize)
				p1 -= stateSize
				if tmpF <= h[p1-stateSize+stFreq] {
					break
				}
			}
			m.set16(p1, tmpW)
			m.set32(p1+2, tmpS)
		}
	}
	if h[p+stFreq] == 0 {
		i = 0
		for {
			i++
			p -= stateSize
			if h[p+stFreq] != 0 {
				break
			}
		}
		escFreq += i
		oldNU := (uint32(h[c+ctxNumStats]) + 2) >> 1
		ns := uint32(h[c+ctxNumStats]) - i
		h[c+ctxNumStats] = uint8(ns)
		if ns == 0 {
			tmpW, tmpS := m.get16(stats), m.get32(stats+2)
			tmpSym, tmpF := uint8(tmpW), uint8(tmpW>>8)
			tmpF = uint8((2*uint32(tmpF) + escFreq - 1) / escFreq)
			if tmpF > maxFreq/3 {
				tmpF = maxFreq / 3
			}
			m.freeUnits(stats, oldNU)
			m.set16(c+ctxOne, uint16(tmpSym)|uint16(tmpF)<<8)
			m.set32(c+ctxOne+stSucc, tmpS)
			h[c+ctxFlags] = (h[c+ctxFlags] & 0x10) + 0x08*b2u8(tmpSym >= 0x40)
			m.foundState = c + ctxOne
			return
		}
		stats = m.shrinkUnits(stats, oldNU, (ns+2)>>1)
		m.set32(c+ctxStats, stats)
		flags := h[c+ctxFlags] &^ 0x08
		p = stats
		flags |= 0x08 * b2u8(h[p+stSymbol] >= 0x40)
		for j := ns; j > 0; j-- {
			p += stateSize
			flags |= 0x08 * b2u8(h[p+stSymbol] >= 0x40)
		}
		h[c+ctxFlags] = flags
	}
	escFreq -= escFreq >> 1
	m.setSummFreq(c, summ+escFreq)
	h[c+ctxFlags] |= 0x04
	m.foundState = stats
}

// createSuccessors creates the chain of contexts for the state m.foundState (and the suffix states
// that still point at the raw text) and returns the deepest new context, or 0 when out of memory.
func (m *model) createSuccessors(skip bool, p, pc uint32) uint32 {
	h := m.heap
	fs := m.foundState
	upBranch := m.succ(fs)
	sym := h[fs+stSymbol]
	ps := &m.psCreate
	n := 0
	scan := true
	if !skip {
		ps[n] = fs
		n++
		scan = m.suffix(pc) != 0
	}
	if scan {
		first := p != 0
		if first {
			pc = m.suffix(pc)
		}
		for {
			if !first {
				pc = m.suffix(pc)
				if h[pc+ctxNumStats] != 0 {
					p = m.stats(pc)
					if h[p+stSymbol] != sym {
						for {
							t := h[p+stateSize+stSymbol]
							p += stateSize
							if t == sym {
								break
							}
						}
					}
					tmp := b2u8(h[p+stFreq] < maxFreq-9)
					h[p+stFreq] += tmp
					m.addSummFreq(pc, uint32(tmp))
				} else {
					p = pc + ctxOne
					h[p+stFreq] += b2u8(h[m.suffix(pc)+ctxNumStats] == 0 && h[p+stFreq] < 24)
				}
			}
			first = false
			if s := m.succ(p); s != upBranch {
				pc = s
				break
			}
			ps[n] = p
			n++
			if m.suffix(pc) == 0 {
				break
			}
		}
	}
	if n == 0 {
		return pc
	}
	flags := 0x10 * b2u8(sym >= 0x40)
	sym = h[upBranch]
	flags |= 0x08 * b2u8(sym >= 0x40)
	var freq uint8
	if h[pc+ctxNumStats] != 0 {
		p = m.stats(pc)
		if h[p+stSymbol] != sym {
			for {
				t := h[p+stateSize+stSymbol]
				p += stateSize
				if t == sym {
					break
				}
			}
		}
		cf := uint32(h[p+stFreq]) - 1
		s0 := m.summFreq(pc) - uint32(h[pc+ctxNumStats]) - cf
		if 2*cf <= s0 {
			freq = 1 + b2u8(5*cf > s0)
		} else {
			freq = uint8(1 + (cf+2*s0-3)/s0)
		}
	} else {
		freq = h[pc+ctxOne+stFreq]
	}
	for n > 0 {
		pc1 := m.allocContext()
		if pc1 == 0 {
			return 0
		}
		h[pc1+ctxNumStats] = 0
		h[pc1+ctxFlags] = flags
		h[pc1+ctxOne+stSymbol] = sym
		h[pc1+ctxOne+stFreq] = freq
		m.set32(pc1+ctxOne+stSucc, upBranch+1)
		m.set32(pc1+ctxSuffix, pc)
		n--
		m.set32(ps[n]+stSucc, pc1)
		pc = pc1
	}
	return pc
}

// updateModel adds the symbol of m.foundState, found in minCtx, to the contexts that escaped to it.
func (m *model) updateModel(minCtx uint32) {
	h := m.heap
	var p, successor, fSuccessor, pc, pc1 uint32
	var ns, ns1, cf, sf, s0, fFreq uint32
	var fSymbol, flag uint8
	fs := m.foundState
	pc1 = m.maxContext
	fFreq = uint32(h[fs+stFreq])
	fSymbol = h[fs+stSymbol]
	fSuccessor = m.succ(fs)
	pc = m.suffix(minCtx)
	if fFreq < maxFreq/4 && pc != 0 {
		if h[pc+ctxNumStats] != 0 {
			p = m.stats(pc)
			if h[p+stSymbol] != fSymbol {
				for {
					t := h[p+stateSize+stSymbol]
					p += stateSize
					if t == fSymbol {
						break
					}
				}
				if h[p+stFreq] >= h[p-stateSize+stFreq] {
					m.swapStates(p, p-stateSize)
					p -= stateSize
				}
			}
			cf = 2 * b2u(h[p+stFreq] < maxFreq-9)
			h[p+stFreq] += uint8(cf)
			m.addSummFreq(pc, cf)
		} else {
			p = pc + ctxOne
			h[p+stFreq] += b2u8(h[p+stFreq] < 32)
		}
	}
	if m.orderFall == 0 && fSuccessor != 0 {
		s := m.createSuccessors(true, p, minCtx)
		m.set32(m.foundState+stSucc, s)
		if s == 0 {
			goto restart
		}
		m.maxContext = s
		return
	}
	h[m.pText] = fSymbol
	m.pText++
	successor = m.pText
	if m.pText >= m.unitsStart {
		goto restart
	}
	if fSuccessor != 0 {
		if fSuccessor < m.unitsStart {
			fSuccessor = m.createSuccessors(false, p, minCtx)
		}
	} else {
		fSuccessor = m.reduceOrder(p, minCtx)
	}
	if fSuccessor == 0 {
		goto restart
	}
	if m.orderFall--; m.orderFall == 0 {
		successor = fSuccessor
		if m.maxContext != minCtx {
			m.pText--
		}
	} else if m.mrMethod > mrFreeze {
		successor = fSuccessor
		m.pText = m.heapStart
		m.orderFall = 0
	}
	ns = uint32(h[minCtx+ctxNumStats])
	s0 = m.summFreq(minCtx) - ns - fFreq
	flag = 0x08 * b2u8(fSymbol >= 0x40)
	for ; pc1 != minCtx; pc1 = m.suffix(pc1) {
		if ns1 = uint32(h[pc1+ctxNumStats]); ns1 != 0 {
			if ns1&1 != 0 {
				p = m.expandUnits(m.stats(pc1), (ns1+1)>>1)
				if p == 0 {
					goto restart
				}
				m.set32(pc1+ctxStats, p)
			}
			m.addSummFreq(pc1, b2u(3*ns1+1 < ns))
		} else {
			p = m.allocUnits(1)
			if p == 0 {
				goto restart
			}
			m.stateCpy(p, pc1+ctxOne)
			m.set32(pc1+ctxStats, p)
			if h[p+stFreq] < maxFreq/4-1 {
				h[p+stFreq] += h[p+stFreq]
			} else {
				h[p+stFreq] = maxFreq - 4
			}
			m.setSummFreq(pc1, uint32(h[p+stFreq])+m.initEsc+b2u(ns > 2))
		}
		cf = 2 * fFreq * (m.summFreq(pc1) + 6)
		sf = s0 + m.summFreq(pc1)
		if cf < 6*sf {
			cf = 1 + b2u(cf > sf) + b2u(cf >= 4*sf)
			m.addSummFreq(pc1, 4)
		} else {
			cf = 4 + b2u(cf > 9*sf) + b2u(cf > 12*sf) + b2u(cf > 15*sf)
			m.addSummFreq(pc1, cf)
		}
		h[pc1+ctxNumStats]++
		p = m.stats(pc1) + uint32(h[pc1+ctxNumStats])*stateSize
		m.set32(p+stSucc, successor)
		h[p+stSymbol] = fSymbol
		h[p+stFreq] = uint8(cf)
		h[pc1+ctxFlags] |= flag
	}
	m.maxContext = fSuccessor
	return
restart:
	m.restoreModelRare(pc1, minCtx, fSuccessor)
}
