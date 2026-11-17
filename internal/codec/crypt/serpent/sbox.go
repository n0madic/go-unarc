// Code generated from the sb0..sb7/ib0..ib7 macros of FreeArc's serpent.c (Gladman); DO NOT EDIT.

package serpent

// sb0 is S-box 0.
func sb0(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := a ^ d
	t2 := a & d
	t3 := c ^ t1
	t6 := b & t1
	t4 := b ^ t3
	t10 := ^t3
	h = t2 ^ t4
	t7 := a ^ t6
	t14 := ^t7
	t8 := c | t7
	t11 := t3 ^ t7
	g = t4 ^ t8
	t12 := h & t11
	f = t10 ^ t12
	e = t12 ^ t14
	return
}

// sb1 is S-box 1.
func sb1(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := ^a
	t2 := b ^ t1
	t3 := a | t2
	t4 := d | t2
	t5 := c ^ t3
	g = d ^ t5
	t7 := b ^ t4
	t8 := t2 ^ g
	t9 := t5 & t7
	h = t8 ^ t9
	t11 := t5 ^ t7
	f = h ^ t11
	t13 := t8 & t11
	e = t5 ^ t13
	return
}

// sb2 is S-box 2.
func sb2(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := ^a
	t2 := b ^ d
	t3 := c & t1
	t13 := d | t1
	e = t2 ^ t3
	t5 := c ^ t1
	t6 := c ^ e
	t7 := b & t6
	t10 := e | t5
	h = t5 ^ t7
	t9 := d | t7
	t11 := t9 & t10
	t14 := t2 ^ h
	g = a ^ t11
	t15 := g ^ t13
	f = t14 ^ t15
	return
}

// sb3 is S-box 3.
func sb3(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := a ^ c
	t2 := d ^ t1
	t3 := a & t2
	t4 := d ^ t3
	t5 := b & t4
	g = t2 ^ t5
	t7 := a | g
	t8 := b | d
	t11 := a | d
	t9 := t4 & t7
	f = t8 ^ t9
	t12 := b ^ t11
	t13 := g ^ t9
	t15 := t3 ^ t8
	h = t12 ^ t13
	t16 := c & t15
	e = t12 ^ t16
	return
}

// sb4 is S-box 4.
func sb4(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := a ^ d
	t2 := d & t1
	t3 := c ^ t2
	t4 := b | t3
	h = t1 ^ t4
	t6 := ^b
	t7 := t1 | t6
	e = t3 ^ t7
	t9 := a & e
	t10 := t1 ^ t6
	t11 := t4 & t10
	g = t9 ^ t11
	t13 := a ^ t3
	t14 := t10 & g
	f = t13 ^ t14
	return
}

// sb5 is S-box 5.
func sb5(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := ^a
	t2 := a ^ b
	t3 := a ^ d
	t4 := c ^ t1
	t5 := t2 | t3
	e = t4 ^ t5
	t7 := d & e
	t8 := t2 ^ e
	t10 := t1 | e
	f = t7 ^ t8
	t11 := t2 | t7
	t12 := t3 ^ t10
	t14 := b ^ t7
	g = t11 ^ t12
	t15 := f & t12
	h = t14 ^ t15
	return
}

// sb6 is S-box 6.
func sb6(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := ^a
	t2 := a ^ d
	t3 := b ^ t2
	t4 := t1 | t2
	t5 := c ^ t4
	f = b ^ t5
	t13 := ^t5
	t7 := t2 | f
	t8 := d ^ t7
	t9 := t5 & t8
	g = t3 ^ t9
	t11 := t5 ^ t8
	e = g ^ t11
	t14 := t3 & t11
	h = t13 ^ t14
	return
}

// sb7 is S-box 7.
func sb7(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := ^c
	t2 := b ^ c
	t3 := b | t1
	t4 := d ^ t3
	t5 := a & t4
	t7 := a ^ d
	h = t2 ^ t5
	t8 := b ^ t5
	t9 := t2 | t8
	t11 := d & t3
	f = t7 ^ t9
	t12 := t5 ^ f
	t15 := t1 | t4
	t13 := h & t12
	g = t11 ^ t13
	t16 := t12 ^ g
	e = t15 ^ t16
	return
}

// ib0 is inverse S-box 0.
func ib0(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := ^a
	t2 := a ^ b
	t3 := t1 | t2
	t4 := d ^ t3
	t7 := d & t2
	t5 := c ^ t4
	t8 := t1 ^ t7
	g = t2 ^ t5
	t11 := a & t4
	t9 := g & t8
	t14 := t5 ^ t8
	f = t4 ^ t9
	t12 := t5 | f
	h = t11 ^ t12
	e = h ^ t14
	return
}

// ib1 is inverse S-box 1.
func ib1(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := a ^ d
	t2 := a & b
	t3 := b ^ c
	t4 := a ^ t3
	t5 := b | d
	t7 := c | t1
	h = t4 ^ t5
	t8 := b ^ t7
	t11 := ^t2
	t9 := t4 & t8
	f = t1 ^ t9
	t13 := t9 ^ t11
	t12 := h & f
	g = t12 ^ t13
	t15 := a & d
	t16 := c ^ t13
	e = t15 ^ t16
	return
}

// ib2 is inverse S-box 2.
func ib2(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := b ^ d
	t2 := ^t1
	t3 := a ^ c
	t4 := c ^ t1
	t7 := a | t2
	t5 := b & t4
	t8 := d ^ t7
	t11 := ^t4
	e = t3 ^ t5
	t9 := t3 | t8
	t14 := d & t11
	h = t1 ^ t9
	t12 := e | h
	f = t11 ^ t12
	t15 := t3 ^ t12
	g = t14 ^ t15
	return
}

// ib3 is inverse S-box 3.
func ib3(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := b ^ c
	t2 := b | c
	t3 := a ^ c
	t7 := a ^ d
	t4 := t2 ^ t3
	t5 := d | t4
	t9 := t2 ^ t7
	e = t1 ^ t5
	t8 := t1 | t5
	t11 := a & t4
	g = t8 ^ t9
	t12 := e | t9
	f = t11 ^ t12
	t14 := a & g
	t15 := t2 ^ t14
	t16 := e & t15
	h = t4 ^ t16
	return
}

// ib4 is inverse S-box 4.
func ib4(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := c ^ d
	t2 := c | d
	t3 := b ^ t2
	t4 := a & t3
	f = t1 ^ t4
	t6 := a ^ d
	t7 := b | d
	t8 := t6 & t7
	h = t3 ^ t8
	t10 := ^a
	t11 := c ^ h
	t12 := t10 | t11
	e = t3 ^ t12
	t14 := c | t4
	t15 := t7 ^ t14
	t16 := h | t10
	g = t15 ^ t16
	return
}

// ib5 is inverse S-box 5.
func ib5(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := ^c
	t2 := b & t1
	t3 := d ^ t2
	t4 := a & t3
	t5 := b ^ t1
	h = t4 ^ t5
	t7 := b | h
	t8 := a & t7
	f = t3 ^ t8
	t10 := a | d
	t11 := t1 ^ t7
	e = t10 ^ t11
	t13 := a ^ c
	t14 := b & t10
	t15 := t4 | t13
	g = t14 ^ t15
	return
}

// ib6 is inverse S-box 6.
func ib6(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := ^a
	t2 := a ^ b
	t3 := c ^ t2
	t4 := c | t1
	t5 := d ^ t4
	t13 := d & t1
	f = t3 ^ t5
	t7 := t3 & t5
	t8 := t2 ^ t7
	t9 := b | t8
	h = t5 ^ t9
	t11 := b | h
	e = t8 ^ t11
	t14 := t3 ^ t11
	g = t13 ^ t14
	return
}

// ib7 is inverse S-box 7.
func ib7(a, b, c, d uint32) (e, f, g, h uint32) {
	t1 := a & b
	t2 := a | b
	t3 := c | t1
	t4 := d & t2
	h = t3 ^ t4
	t6 := ^d
	t7 := b ^ t4
	t8 := h ^ t6
	t11 := c ^ t7
	t9 := t7 | t8
	f = a ^ t9
	t12 := d | f
	e = t11 ^ t12
	t14 := a & h
	t15 := t3 ^ f
	t16 := e ^ t14
	g = t15 ^ t16
	return
}
