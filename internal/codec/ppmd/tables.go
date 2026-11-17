package ppmd

// Compile-time parameters of PPMd var.I rev.1 (Model.cpp, SubAlloc.hpp) for the 32-bit build.
const (
	// unitSize is the allocation unit of the model heap: 4+2*sizeof(char*) with 32-bit pointers.
	// A PPM context is exactly one unit, a state (symbol, frequency, successor) is half of one.
	unitSize = 12

	n1, n2, n3 = 4, 4, 4
	n4         = (128 + 3 - 1*n1 - 2*n2 - 3*n3) / 4
	nIndexes   = n1 + n2 + n3 + n4

	maxO = 128 // maximum model order

	upFreq     = 5
	intBits    = 7
	periodBits = 7
	totBits    = intBits + periodBits
	interval   = 1 << intBits
	binScale   = 1 << totBits
	maxFreq    = 124
	oBound     = 9

	// Range coder (Subbotin's carryless coder).
	rcTop = 1 << 24
	rcBot = 1 << 15
)

// Model restoration methods (the "r" parameter). Values above mrFreeze (including the one the
// freeze method turns itself into once the model is frozen) select the frozen model.
const (
	mrRestart = 0
	mrCutOff  = 1
	mrFreeze  = 2
)

// Layout of a PPM context (one unit) and of a state inside the model heap. All fields are
// little-endian and unaligned, offsets are relative to the start of the context or state.
const (
	ctxNumStats = 0 // uint8: number of symbols minus 1
	ctxFlags    = 1 // uint8
	ctxSummFreq = 2 // uint16
	ctxStats    = 4 // uint32: offset of the states array
	ctxSuffix   = 8 // uint32: offset of the suffix context, 0 for the root
	ctxOne      = 2 // a context with a single symbol keeps that state inline here (overlays SummFreq and Stats)

	stSymbol  = 0 // uint8
	stFreq    = 1 // uint8
	stSucc    = 2 // uint32: context or raw text offset
	stateSize = 6
)

// Layout of a free block (SubAlloc.hpp MEM_BLK), also one unit.
const (
	blkStamp = 0 // uint32: ^0 while the block is free
	blkNext  = 4 // uint32: next block of the same free list
	blkNU    = 8 // uint32: size in units
)

// Constant tables, filled once by init and never modified afterwards.
var (
	indx2Units [nIndexes]uint8
	units2Indx [128]uint8
	ns2BSIndx  [256]uint8
	qTable     [260]uint8
)

var initBinEsc = [8]uint16{0x3CDD, 0x1F3F, 0x59BF, 0x48F3, 0x64A1, 0x5ABC, 0x6632, 0x6051}

// expEscape: tabulated escapes for exponential symbol distribution.
var expEscape = [16]uint8{25, 14, 9, 7, 5, 5, 4, 4, 4, 3, 3, 3, 2, 2, 2, 2}

func init() {
	var i, k int
	for i, k = 0, 1; i < n1; i, k = i+1, k+1 {
		indx2Units[i] = uint8(k)
	}
	for k++; i < n1+n2; i, k = i+1, k+2 {
		indx2Units[i] = uint8(k)
	}
	for k++; i < n1+n2+n3; i, k = i+1, k+3 {
		indx2Units[i] = uint8(k)
	}
	for k++; i < n1+n2+n3+n4; i, k = i+1, k+4 {
		indx2Units[i] = uint8(k)
	}
	i = 0
	for k = 0; k < 128; k++ {
		if int(indx2Units[i]) < k+1 {
			i++
		}
		units2Indx[k] = uint8(i)
	}
	ns2BSIndx[0] = 2 * 0
	ns2BSIndx[1] = 2 * 1
	for j := 2; j < 11; j++ {
		ns2BSIndx[j] = 2 * 2
	}
	for j := 11; j < 256; j++ {
		ns2BSIndx[j] = 2 * 3
	}
	for i = 0; i < upFreq; i++ {
		qTable[i] = uint8(i)
	}
	m, step := upFreq, 1
	k = 1
	for i = upFreq; i < 260; i++ {
		qTable[i] = uint8(m)
		if k--; k == 0 {
			step++
			k = step
			m++
		}
	}
}
