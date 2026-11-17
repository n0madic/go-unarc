package dispack

import "encoding/binary"

// Opcodes the filter treats specially (enum Opcodes).
const (
	op2Byte = 0x0f // start of a two-byte opcode
	opOSize = 0x66 // operand size prefix
	opCallF = 0x9a
	opRetNI = 0xc2 // ret near + immediate
	opRetN  = 0xc3
	opEnter = 0xc8
	opInt3  = 0xcc
	opCallN = 0xe8
	opJmpF  = 0xea

	opEscape  = 0xf1 // ICEBP: the next op byte is stored as is
	opJumpTab = 0xce // INTO: a jump table follows
)

// Instruction formats of the tables (enum InstructionFormat).
const (
	fNM     = 0x0 // no ModRM
	fAM     = 0x1 // no ModRM, "address mode" (jumps or direct addresses)
	fMR     = 0x2 // ModRM present
	fMEXTRA = 0x3 // ModRM present, includes extra bits for opcode
	fMODE   = 0x3 // bitmask for mode

	fNI   = 0x0 // no immediate
	fBI   = 0x4 // byte immediate
	fWI   = 0x8 // word immediate
	fDI   = 0xc // dword immediate
	fTYPE = 0xc // type mask

	fAD = 0x0 // absolute address
	fDA = 0x4 // dword absolute jump target
	fBR = 0x8 // byte relative jump target
	fDR = 0xc // dword relative jump target

	fERR = 0xf // invalid opcode: FreeArc is built without the assert, so it decodes as ModRM + dword immediate
)

// Streams of the filtered code (enum Streams), stored one after another behind a header of their sizes.
const (
	stOp      = iota // prefixes, first byte of opcode, ModRM, second opcode byte, jump table counts
	stSIB            // SIB byte
	stCallIdx        // call table index
	stDisp8R0        // byte displacement on ModRM, one stream per base register
	stJump8   = stDisp8R0 + 8
	stImm8    = stJump8 + 1
	stImm16   = stImm8 + 1
	stImm32   = stImm16 + 1
	stDisp32  = stImm32 + 1
	stAddr32  = stDisp32 + 1
	stCall32  = stAddr32 + 1 // 32-bit call target
	stJump32  = stCall32 + 1 // 32-bit jump target, absolute or relative
	stMax     = stJump32 + 1

	headerLen = stMax * 4
)

// unfilterer holds what DisUnFilter keeps in local variables: the unread part of each stream, the output and
// the move-to-front table of call targets.
type unfilterer struct {
	st    [stMax][]byte
	out   []byte
	n     int // bytes written to out
	funcs [256]uint32
}

// unfilter mirrors DisUnFilter: it rebuilds the code of src into dst, which it must fill exactly, as if dst
// were loaded at address base. Multi-byte values are big-endian in the streams and little-endian in the code;
// relative jump and call targets are stored as absolute addresses.
func unfilter(src, dst []byte, base uint32) error {
	if len(src) < headerLen {
		return badData("code record of %d bytes has no stream header", len(src))
	}
	u := unfilterer{out: dst}
	rest := src[headerLen:]
	for i := range stMax {
		n := uint64(binary.LittleEndian.Uint32(src[4*i:]))
		if n > uint64(len(rest)) {
			return badData("stream %d of %d bytes runs past the record", i, n)
		}
		u.st[i], rest = rest[:n], rest[n:]
	}
	if len(rest) != 0 {
		return badData("streams leave %d bytes of the record unused", len(rest))
	}
	if !u.run(base) {
		return badData("filtered code is corrupt")
	}
	if u.n != len(dst) {
		return badData("code unfilters to %d bytes instead of %d", u.n, len(dst))
	}
	return nil
}

// run is the decoding loop of DisUnFilter; it returns false where the C code does (a stream or the output
// would be overrun).
func (u *unfilterer) run(base uint32) bool {
	nextIsFunc := true
	for len(u.st[stOp]) > 0 {
		start := u.n
		memory := base + uint32(start)

		code := u.st[stOp][0]
		u.st[stOp] = u.st[stOp][1:]
		if code == opJumpTab {
			count, ok := u.fetch8(stOp)
			if !ok {
				return false
			}
			for range int(count) + 1 {
				target, ok := u.callTarget()
				if !ok || !u.put32(target) {
					return false
				}
			}
			continue
		}

		if nextIsFunc && code != opInt3 {
			u.addMTF(memory)
			nextIsFunc = false
		}

		if code == opEscape {
			if !u.copy8(stOp) {
				return false
			}
			continue
		}
		if !u.put8(code) {
			return false
		}

		o16 := false
		if code == opOSize {
			o16 = true
			c, ok := u.fetch8(stOp)
			if !ok || !u.put8(c) {
				return false
			}
			code = c
		}
		if code == opRetNI || code == opRetN || code == opInt3 {
			nextIsFunc = true // a new function is likely to start next
		}

		var flags uint8
		if code == op2Byte {
			c, ok := u.fetch8(stOp)
			if !ok || !u.put8(c) {
				return false
			}
			flags = table2[c]
		} else {
			flags = table1[code]
		}

		// Far call and jump have a 48-bit address: the segment goes here, the rest as a 32-bit address.
		// Enter has a word and a byte operand: the word goes here, the byte with the immediates below.
		if (code == opCallF || code == opJmpF || code == opEnter) && !u.copy16(stImm16) {
			return false
		}

		if flags&fMR != 0 {
			modrm, ok := u.fetch8(stOp)
			if !ok || !u.put8(modrm) {
				return false
			}
			if flags == fMEXTRA {
				flags = tableX[(modrm>>3)&7|(code&0x01)<<3|(code&0x08)<<1]
			}
			var sib uint8
			if modrm&0x07 == 4 && modrm < 0xc0 {
				if sib, ok = u.fetch8(stSIB); !ok || !u.put8(sib) {
					return false
				}
			}
			if modrm&0xc0 == 0x40 && !u.copy8(stDisp8R0+int(modrm&0x07)) { // register + byte displacement
				return false
			}
			if modrm&0xc0 == 0x80 || modrm&0xc7 == 0x05 || (modrm < 0x40 && sib&0x07 == 0x05) {
				st := stDisp32
				if modrm&0xc7 == 0x05 {
					st = stAddr32
				}
				if !u.copy32(st) {
					return false
				}
			}
		}

		if flags&fMODE == fAM {
			var ok bool
			switch flags & fTYPE {
			case fAD:
				ok = u.copy32(stAddr32)
			case fDA:
				ok = u.copy32(stJump32)
			case fBR:
				ok = u.copy8(stJump8)
			case fDR:
				var target uint32
				if code == opCallN {
					target, ok = u.callTarget()
				} else {
					target, ok = u.fetch32(stJump32)
				}
				ok = ok && u.put32(target-(uint32(u.n-start)+4+memory))
			}
			if !ok {
				return false
			}
			continue
		}
		var ok = true
		switch flags & fTYPE {
		case fBI:
			ok = u.copy8(stImm8)
		case fWI:
			ok = u.copy16(stImm16)
		case fDI:
			if o16 {
				ok = u.copy16(stImm16)
			} else {
				ok = u.copy32(stImm32)
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// callTarget reads a call target: an index into the move-to-front table, or 0 and the address itself.
func (u *unfilterer) callTarget() (uint32, bool) {
	ind, ok := u.fetch8(stCallIdx)
	if !ok {
		return 0, false
	}
	if ind != 0 {
		return u.moveToFront(int(ind)-1, u.funcs[ind-1]), true
	}
	target, ok := u.fetch32(stCall32)
	if ok {
		u.addMTF(target)
	}
	return target, ok
}

// moveToFront mirrors MoveToFront: it shifts funcs[0:pos] up by one and puts val first.
func (u *unfilterer) moveToFront(pos int, val uint32) uint32 {
	copy(u.funcs[1:pos+1], u.funcs[:pos])
	u.funcs[0] = val
	return val
}

// addMTF mirrors AddMTF: val goes first and the last of the 255 entries in use drops out.
func (u *unfilterer) addMTF(val uint32) { u.moveToFront(255, val) }

func (u *unfilterer) fetch8(st int) (uint8, bool) {
	s := u.st[st]
	if len(s) < 1 {
		return 0, false
	}
	u.st[st] = s[1:]
	return s[0], true
}

func (u *unfilterer) fetch32(st int) (uint32, bool) {
	s := u.st[st]
	if len(s) < 4 {
		return 0, false
	}
	u.st[st] = s[4:]
	return binary.BigEndian.Uint32(s), true
}

func (u *unfilterer) put8(v uint8) bool {
	if u.n >= len(u.out) {
		return false
	}
	u.out[u.n] = v
	u.n++
	return true
}

func (u *unfilterer) put32(v uint32) bool {
	if len(u.out)-u.n < 4 {
		return false
	}
	binary.LittleEndian.PutUint32(u.out[u.n:], v)
	u.n += 4
	return true
}

// copy8, copy16 and copy32 move a value from a stream to the output, big-endian to little-endian.
func (u *unfilterer) copy8(st int) bool {
	v, ok := u.fetch8(st)
	return ok && u.put8(v)
}

func (u *unfilterer) copy16(st int) bool {
	s := u.st[st]
	if len(s) < 2 || len(u.out)-u.n < 2 {
		return false
	}
	u.st[st] = s[2:]
	binary.LittleEndian.PutUint16(u.out[u.n:], binary.BigEndian.Uint16(s))
	u.n += 2
	return true
}

func (u *unfilterer) copy32(st int) bool {
	s := u.st[st]
	if len(s) < 4 || len(u.out)-u.n < 4 {
		return false
	}
	u.st[st] = s[4:]
	binary.LittleEndian.PutUint32(u.out[u.n:], binary.BigEndian.Uint32(s))
	u.n += 4
	return true
}
