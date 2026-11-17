package tak

import (
	"testing"

	"github.com/n0madic/go-unarc/internal/codec/tak/taktest"
)

func TestBitReader(t *testing.T) {
	t.Parallel()
	r := bitReader{buf: []byte{0b1010_0101, 0xff, 0x01, 0x80}}
	if got := r.read(4); got != 0b0101 {
		t.Fatalf("first 4 bits = %b, the low nibble is read first", got)
	}
	if got := r.read(8); got != 0xFA { // the high nibble of byte 0, then the low nibble of byte 1
		t.Fatalf("next 8 bits = %#x, want 0xfa", got)
	}
	r.align()
	if r.pos != 16 {
		t.Fatalf("align: position %d, want 16", r.pos)
	}
	if got := r.readSigned(9); got != 1 { // byte 2 and the lowest bit of byte 3
		t.Fatalf("signed 9 bits = %d, want 1", got)
	}
	if got := r.readSigned(7); got != -64 { // the other 7 bits of byte 3: 1000000
		t.Fatalf("signed 7 bits = %d, want -64", got)
	}
}

func TestBitReaderFields(t *testing.T) {
	t.Parallel()
	var w taktest.BitWriter
	w.Put(0x5, 3)
	w.PutSigned(-3, 5)
	w.Put(0x123456789, 35)
	w.PutSigned(-1, 32)
	w.Put(1, 1) // escape4 flag
	w.Put(9, 4)
	w.Put(0b1000, 4) // unary: three zeros, then a one
	r := bitReader{buf: w.Bytes()}
	if got := r.read(3); got != 5 {
		t.Errorf("read(3) = %d", got)
	}
	if got := r.readSigned(5); got != -3 {
		t.Errorf("readSigned(5) = %d", got)
	}
	if got := r.read64(35); got != 0x123456789 {
		t.Errorf("read64(35) = %#x", got)
	}
	if got := r.readSigned(32); got != -1 {
		t.Errorf("readSigned(32) = %d", got)
	}
	if got := r.escape4(); got != 10 {
		t.Errorf("escape4 = %d, want 10", got)
	}
	if got := r.unary(1, 9); got != 3 {
		t.Errorf("unary = %d, want 3", got)
	}
	if got := r.unary(1, 2); got != 2 {
		t.Errorf("unary with a limit = %d, want 2", got)
	}
	if r.overrun() {
		t.Error("overrun before the end")
	}
	r.read(32)
	r.read(32)
	if !r.overrun() || r.bitsLeft() >= 0 {
		t.Errorf("no overrun after reading past the end (left %d)", r.bitsLeft())
	}
	if got := r.read(8); got != 0 {
		t.Errorf("read past the end = %d, want 0", got)
	}
}

func TestReadSignedZeroBits(t *testing.T) {
	t.Parallel()
	r := bitReader{buf: []byte{0xff}}
	if got := r.readSigned(0); got != 0 || r.pos != 0 {
		t.Fatalf("readSigned(0) = %d at %d", got, r.pos)
	}
}

// A CRC taken from a real stream (the stream info block of a game's audio file) and the independent
// implementation of the test helper agree with the decoder's.
func TestCRC24(t *testing.T) {
	t.Parallel()
	payload := []byte{0x02, 0x0d, 0x00, 0x3a, 0x00, 0x00, 0x20, 0xeb, 0x03, 0x02}
	stored := []byte{0xf9, 0x20, 0x6b}
	if got := taktest.CRC24(payload); string(got) != string(stored) {
		t.Fatalf("taktest.CRC24 = %x, want %x", got, stored)
	}
	if !checkCRC(append(append([]byte(nil), payload...), stored...)) {
		t.Fatal("checkCRC rejects the real block")
	}
	bad := append(append([]byte(nil), payload...), stored...)
	bad[3] ^= 1
	if checkCRC(bad) {
		t.Fatal("checkCRC accepts a damaged block")
	}
	if checkCRC([]byte{1, 2, 3}) {
		t.Fatal("checkCRC accepts a block shorter than a CRC and a byte")
	}
}
