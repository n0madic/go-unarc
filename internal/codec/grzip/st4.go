package grzip

const (
	st4Indirect = 0x800000 // flag in a table entry: the low bits point to an earlier entry of the same context
	st4Mask     = st4Indirect - 1
)

// st4Scratch is the working memory of st4Decode besides the table.
type st4Scratch struct {
	ctx  [65536]uint32 // counts of (previous byte, byte) pairs
	flag []byte        // bit set at the first row of every context
}

// st4Decode inverts the order-4 sort transform of buf in place (GRZip_ST4_Decode).
func (d *decoder) st4Decode(buf []byte, fbp int32) error {
	n := len(buf)
	if n == 0 {
		return nil
	}
	if fbp < 0 || int(fbp) > n {
		return errCorrupt("ST4 index %d out of range", fbp)
	}
	if d.st4 == nil {
		d.st4 = new(st4Scratch)
	}
	s := d.st4
	d.table = resize(d.table, n+1)
	t := d.table
	clear(s.ctx[:])
	s.flag = resize(s.flag, (n+8)>>3)
	clear(s.flag)

	var (
		start, next [256]int32 // first row of each character, then the running position inside its range
		lastSeen    [256]int32
	)
	for i := range lastSeen {
		lastSeen[i] = -1
	}
	for _, c := range buf {
		start[c]++
	}
	var sum int32
	j := 0
	for i := range start {
		sum += start[i]
		start[i] = sum - start[i]
		for ; j < int(sum); j++ {
			s.ctx[int(buf[j])<<8|i]++
		}
	}
	next = start

	// Mark where the rows of a new context begin.
	sum, j = 0, 0
	for i := range s.ctx {
		ctxStart := sum
		sum += int32(s.ctx[i])
		for ; j < int(sum); j++ {
			c := buf[j]
			if lastSeen[c] != ctxStart {
				lastSeen[c] = ctxStart
				p := start[c]
				s.flag[p>>3] |= 1 << (p & 7)
			}
			start[c]++
		}
	}

	// Build the table: the next row of a character within its context, or a link to the first one.
	clear(lastSeen[:])
	var ctxStart int32
	for i, c := range buf {
		if s.flag[i>>3]&(1<<(i&7)) != 0 {
			ctxStart = int32(i)
		}
		var e uint32
		if lastSeen[c] <= ctxStart {
			e = uint32(next[c])
			lastSeen[c] = int32(i) + 1
		} else {
			e = uint32(lastSeen[c]-1) | st4Indirect
		}
		next[c]++
		t[i] = e | uint32(c)<<24
	}
	t[n] = st4Indirect

	// Walk the table. Entries are bumped as they are used, so with a bad index or bad data the values can leave
	// the table; every access is checked.
	idx := int(fbp)
	e := t[idx]
	for i := range buf {
		if e&st4Indirect != 0 {
			k := int(e & st4Mask)
			if k > n {
				return errCorrupt("bad ST4 data")
			}
			old := t[k]
			t[k] = old + 1
			idx = int(old & st4Mask)
		} else {
			t[idx]++
			idx = int(e & st4Mask)
		}
		if idx > n {
			return errCorrupt("bad ST4 data")
		}
		e = t[idx]
		buf[i] = byte(e >> 24)
	}
	return nil
}
