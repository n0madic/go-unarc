package dispack

// The opcode tables of DisPack.cpp, copied as they are: the format bits of every one-byte opcode (Table1),
// two-byte opcode 0x0f xx (Table2) and the ModRM reg field of 0xf6, 0xf7, 0xfe, 0xff (TableX).

var table1 = [256]uint8{
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fBI, fNM | fDI, fNM | fNI, fNM | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fBI, fNM | fDI, fNM | fNI, fNM | fNI, // 0
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fBI, fNM | fDI, fNM | fNI, fNM | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fBI, fNM | fDI, fNM | fNI, fNM | fNI, // 1
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fBI, fNM | fDI, fNM | fNI, fNM | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fBI, fNM | fDI, fNM | fNI, fNM | fNI, // 2
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fBI, fNM | fDI, fNM | fNI, fNM | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fBI, fNM | fDI, fNM | fNI, fNM | fNI, // 3
	fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, // 4
	fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, // 5
	fNM | fNI, fNM | fNI, fMR | fNI, fMR | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fDI, fMR | fDI, fNM | fBI, fMR | fBI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, // 6
	fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, // 7
	fMR | fBI, fMR | fDI, fMR | fBI, fMR | fBI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // 8
	fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fAM | fDA, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, // 9
	fAM | fAD, fAM | fAD, fAM | fAD, fAM | fAD, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fBI, fNM | fDI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, // a
	fNM | fBI, fNM | fBI, fNM | fBI, fNM | fBI, fNM | fBI, fNM | fBI, fNM | fBI, fNM | fBI, fNM | fDI, fNM | fDI, fNM | fDI, fNM | fDI, fNM | fDI, fNM | fDI, fNM | fDI, fNM | fDI, // b
	fMR | fBI, fMR | fBI, fNM | fWI, fNM | fNI, fMR | fNI, fMR | fNI, fMR | fBI, fMR | fDI, fNM | fBI, fNM | fNI, fNM | fWI, fNM | fNI, fNM | fNI, fNM | fBI, fERR, fNM | fNI, // c
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fBI, fNM | fBI, fNM | fNI, fNM | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // d
	fAM | fBR, fAM | fBR, fAM | fBR, fAM | fBR, fNM | fBI, fNM | fBI, fNM | fBI, fNM | fBI, fAM | fDR, fAM | fDR, fAM | fAD, fAM | fBR, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, // e
	fNM | fNI, fERR, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fMEXTRA, fMEXTRA, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fMEXTRA, fMEXTRA, // f
}

var table2 = [256]uint8{
	fERR, fERR, fERR, fERR, fERR, fERR, fNM | fNI, fERR, fNM | fNI, fNM | fNI, fERR, fERR, fERR, fERR, fERR, fERR, // 0
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fERR, fERR, fERR, fERR, fERR, fERR, fERR, // 1
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fERR, fERR, fERR, fERR, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // 2
	fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fERR, fNM | fNI, fERR, fERR, fERR, fERR, fERR, fERR, fERR, fERR, // 3
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // 4
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // 5
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // 6
	fMR | fBI, fMR | fBI, fMR | fBI, fMR | fBI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fNI, fERR, fERR, fERR, fERR, fERR, fERR, fMR | fNI, fMR | fNI, // 7
	fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, fAM | fDR, // 8
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // 9
	fNM | fNI, fNM | fNI, fNM | fNI, fMR | fNI, fMR | fBI, fMR | fNI, fMR | fNI, fMR | fNI, fERR, fERR, fERR, fMR | fNI, fMR | fBI, fMR | fNI, fERR, fMR | fNI, // a
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fERR, fERR, fERR, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // b
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, fNM | fNI, // c
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // d
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // e
	fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fERR, // f
}

var tableX = [32]uint8{
	fMR | fBI, fERR, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // escapes for 0xf6
	fMR | fDI, fERR, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, fMR | fNI, // escapes for 0xf7
	fMR | fNI, fMR | fNI, fERR, fERR, fERR, fERR, fERR, fERR, // escapes for 0xfe
	fMR | fNI, fMR | fNI, fMR | fNI, fERR, fMR | fNI, fERR, fMR | fNI, fERR, // escapes for 0xff
}
