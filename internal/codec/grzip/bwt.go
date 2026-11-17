package grzip

// strongFlag is set in a block's FBP when the encoder used the "strong" sort, which keeps one more row (the
// original string) than the fast sort and so numbers the rows differently.
const strongFlag = 0x40000000

// bwtDecode inverts the Burrows-Wheeler transform of buf in place (GRZip_BWT_Decode).
func (d *decoder) bwtDecode(buf []byte, fbp int32) error {
	if fbp < 0 {
		return errCorrupt("bad BWT index %d", fbp)
	}
	if len(buf) == 0 {
		return nil
	}
	if fbp&strongFlag == 0 {
		if int(fbp) >= len(buf) {
			return errCorrupt("BWT index %d out of range", fbp)
		}
		d.table = resize(d.table, len(buf))
		fastBWTDecode(buf, int(fbp), d.table)
		return nil
	}
	fbp &^= strongFlag
	if int(fbp) > len(buf) {
		return errCorrupt("BWT index %d out of range", fbp)
	}
	d.table = resize(d.table, len(buf)+1)
	strongBWTDecode(buf, int(fbp), d.table)
	return nil
}

// fastBWTDecode is GRZip_FastBWT_Decode. t has len(buf) entries; fbp < len(buf).
// Every index followed below is rank + start of a character's range, so it stays below len(buf) whatever buf
// holds.
func fastBWTDecode(buf []byte, fbp int, t []uint32) {
	var count [256]uint32
	for i, c := range buf {
		t[i] = count[c]<<8 | uint32(c)
		count[c]++
	}
	var sum uint32
	for i := range count {
		sum += count[i]
		count[i] = sum - count[i]
	}
	idx := uint32(fbp)
	for i := len(buf) - 1; i >= 0; i-- {
		u := t[idx]
		c := byte(u)
		idx = u>>8 + count[c]
		buf[i] = c
	}
}

// strongBWTDecode is GRZip_StrongBWT_Decode. t has len(buf)+1 entries; the entry at fbp stands for the original
// string and is never reached from valid data (the C code leaves it uninitialised).
func strongBWTDecode(buf []byte, fbp int, t []uint32) {
	var count [256]uint32
	for i, c := range buf[:fbp] {
		t[i] = count[c]<<8 | uint32(c)
		count[c]++
	}
	for i, c := range buf[fbp:] {
		t[fbp+i+1] = count[c]<<8 | uint32(c)
		count[c]++
	}
	t[fbp] = 0
	sum := uint32(1)
	for i := range count {
		sum += count[i]
		count[i] = sum - count[i]
	}
	var idx uint32
	for i := len(buf) - 1; i >= 0; i-- {
		u := t[idx]
		c := byte(u)
		idx = u>>8 + count[c]
		buf[i] = c
	}
}
