package tta

// The predictor of TTA, run in reverse by the decoder: up to three cascaded adaptive "hybrid" filters
// (sign-sign LMS with a history of the last samples) followed by a fixed first-order predictor.
// All arithmetic is int32 with wrap-around, as in the C code.

const (
	maxOrder = 32  // the longest filter
	bufSize  = 256 // history buffer of a filter; it slides back when the window reaches the end
)

// filterSpec describes one hybrid filter; order 0 switches the stage off.
type filterSpec struct {
	order int
	shift uint
	// plain filters skip the adaptive polynomial predictors that the order-8 filters (mode 0 in C) apply to
	// the last three history entries.
	plain bool
}

// filterSpecs[stage][level-1][byteSize-1] is the table flt_set of the C code.
var filterSpecs = [3][3][4]filterSpec{
	{
		{{8, 10, false}, {8, 9, false}, {8, 10, false}, {8, 12, true}},
		{{8, 10, false}, {8, 11, false}, {8, 10, false}, {16, 12, true}},
		{{8, 10, false}, {8, 11, false}, {8, 10, false}, {32, 12, true}},
	},
	{
		{{}, {}, {}, {}},
		{{16, 10, true}, {16, 9, true}, {16, 10, true}, {}},
		{{32, 11, true}, {32, 11, true}, {32, 11, true}, {}},
	},
	{
		{{}, {}, {}, {}},
		{{}, {}, {}, {}},
		{{}, {16, 9, true}, {16, 10, true}, {}},
	},
}

// filter is the state of one hybrid filter: the weights qm, the history dl of the output (the samples the
// weights are applied to) and dx of its signs (what the weights are adapted by). The window of the last
// order entries starts at px/pl.
type filter struct {
	order  int
	shift  uint
	round  int32
	plain  bool
	sum    int32 // round plus the dot product of weights and history for the next sample
	px, pl int
	qm     [maxOrder]int32
	dx     [bufSize]int32
	dl     [bufSize]int32
}

func (f *filter) init(s filterSpec) {
	*f = filter{order: s.order, shift: s.shift, plain: s.plain}
	if s.order != 0 {
		f.round = 1 << (s.shift - 1)
		f.sum = f.round // the weights are zero
	}
}

// adaptDot moves the weights w against the sign of the filter input (w -= sign*x, with sign -1, 0 or 1; a
// multiplication instead of a branch because the sign of audio is not predictable) and returns the dot product
// of the new weights with y. The three slices have the same length, a multiple of 8.
//
// The dot product is the one of the next sample, which the C code computes at its start: nothing changes the
// weights and the history between the two. The partial sums are independent so that the multiplications
// overlap, which wrap-around arithmetic allows.
func adaptDot(w, x, y []int32, sign int32) int32 {
	var s0, s1, s2, s3 int32
	for len(w) >= 8 && len(x) >= 8 && len(y) >= 8 {
		p, q, r := (*[8]int32)(w), (*[8]int32)(x), (*[8]int32)(y)
		w0 := p[0] - sign*q[0]
		w1 := p[1] - sign*q[1]
		w2 := p[2] - sign*q[2]
		w3 := p[3] - sign*q[3]
		w4 := p[4] - sign*q[4]
		w5 := p[5] - sign*q[5]
		w6 := p[6] - sign*q[6]
		w7 := p[7] - sign*q[7]
		p[0], p[1], p[2], p[3], p[4], p[5], p[6], p[7] = w0, w1, w2, w3, w4, w5, w6, w7
		s0 += w0*r[0] + w4*r[4]
		s1 += w1*r[1] + w5*r[5]
		s2 += w2*r[2] + w6*r[6]
		s3 += w3*r[3] + w7*r[7]
		w, x, y = w[8:], x[8:], y[8:]
	}
	return s0 + s1 + s2 + s3
}

// decode undoes the filter for one sample: filter_decompress in C.
func (f *filter) decode(in int32) int32 {
	n := f.order
	pl := f.dl[f.pl : f.pl+n+1]
	px := f.dx[f.px : f.px+n+1]

	out := in + f.sum>>f.shift
	pl[n] = out

	if !f.plain {
		// adaptive polynomial predictors
		pl[n-1] = pl[n] - pl[n-1]
		pl[n-2] = pl[n-1] - pl[n-2]
		pl[n-3] = pl[n-2] - pl[n-3]
	}

	// Adapt the weights by the sign of the filter input: in > 0 subtracts the signs, in < 0 adds them.
	// The history of the next sample is the window shifted by one.
	f.sum = f.round + adaptDot(f.qm[:n], px[:n], pl[1:], sign(in))

	px[n] = (pl[n]>>28)&8 - 4
	px[n-1] = (pl[n-1]>>29)&4 - 2
	px[n-2] = (pl[n-2]>>29)&4 - 2
	px[n-3] = (pl[n-3]>>30)&2 - 1

	// Slide the windows; at the end of a buffer the last order entries move back to its start.
	if f.px+n == bufSize-1 {
		copy(f.dx[:n], f.dx[f.px+1:f.px+1+n])
		f.px = 0
	} else {
		f.px++
	}
	if f.pl+n == bufSize-1 {
		copy(f.dl[:n], f.dl[f.pl+1:f.pl+1+n])
		f.pl = 0
	} else {
		f.pl++
	}
	return out
}

// sign returns -1, 0 or 1.
func sign(x int32) int32 {
	return int32(btoi32(x > 0) - btoi32(x < 0))
}

func btoi32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// predictor1 is the C macro PREDICTOR1: x*(1-2^-k) computed in 64 bits and truncated.
func predictor1(x int32, k uint) int32 {
	u := uint64(int64(x))
	return int32((u<<k - u) >> k)
}

// filterSet holds the three filters of a channel; the decoder reuses it for every channel of every frame.
type filterSet [3]filter

// decodeChannel undoes the predictor of one channel in place: filters_decompress in C.
// level is 1..3 and byteSize 1..4.
func (fs *filterSet) decodeChannel(data []int32, level, byteSize int) {
	for i := range fs {
		fs[i].init(filterSpecs[i][level-1][byteSize-1])
	}
	f1, f2, f3 := &fs[0], &fs[1], &fs[2]

	// byteSize 4 predicts by the previous sample alone.
	k := uint(5)
	switch byteSize {
	case 1:
		k = 4
	case 4:
		k = 0
	}

	var last int32
	for i, v := range data {
		if f3.order != 0 {
			v = f3.decode(v)
		}
		if f2.order != 0 {
			v = f2.decode(v)
		}
		if f1.order != 0 {
			v = f1.decode(v)
		}
		if k == 0 {
			v += last
		} else {
			v += predictor1(last, k)
		}
		data[i] = v
		last = v
	}
}
