package dispack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		m         string
		ok        bool
		blockSize uint32
	}{
		{"dispack070", true, 8 << 20},
		{"dispack", true, 8 << 20},
		{"dispack070:x", true, 8 << 20},
		{"dispack070:b4mb", true, 4 << 20},
		{"dispack070:2mb:x", true, 2 << 20},
		{"dispack070:20", true, 1 << 20}, // a bare number is a power of two
		{"dispack070:bx", false, 0},
		{"dispack070:y", false, 0},
		{"dispack071", false, 0},
		{"exe", false, 0},
	}
	for _, tt := range tests {
		c, ok := Parse(method.Params(tt.m), &method.Env{})
		if ok != tt.ok {
			t.Errorf("%s: accepted %v, want %v", tt.m, ok, tt.ok)
			continue
		}
		if ok && c.(*codec).blockSize != tt.blockSize {
			t.Errorf("%s: block size %d, want %d", tt.m, c.(*codec).blockSize, tt.blockSize)
		}
	}
	if got := codectest.Parse(t, "dispack070:b1mb").DecompressionMem(); got != 2<<20+1<<18+1024 {
		t.Errorf("DecompressionMem %d", got)
	}
}

// filtered builds a code record: the header of stream sizes and the streams, given as stream -> bytes.
func filtered(streams map[int][]byte) []byte {
	var b []byte
	for i := range stMax {
		b = binary.LittleEndian.AppendUint32(b, uint32(len(streams[i])))
	}
	for i := range stMax {
		b = append(b, streams[i]...)
	}
	return b
}

func be32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

const base = baseStart

// unfilterCases are code records and the code DisUnFilter rebuilds from them at address base.
var unfilterCases = []struct {
	name    string
	streams map[int][]byte
	want    []byte
}{
	{"plain opcodes", map[int][]byte{stOp: {0x90, 0xc3}}, []byte{0x90, 0xc3}},
	{
		// The call target is stored absolute and goes back to relative: target - (base + 5).
		"call by address",
		map[int][]byte{stOp: {0xe8}, stCallIdx: {0}, stCall32: be32(base + 0x100)},
		cat([]byte{0xe8}, le32(0xfb)),
	},
	{
		// The first call puts its target first in the table, ahead of the function start (base) that the
		// first instruction added; index 1 is that target, index 2 the function start.
		"calls by index",
		map[int][]byte{stOp: {0xe8, 0xe8, 0xe8}, stCallIdx: {0, 1, 2}, stCall32: be32(base + 0x100)},
		cat([]byte{0xe8}, le32(0x100-5), []byte{0xe8}, le32(0x100-10), []byte{0xe8}, le32(^uint32(14))),
	},
	{"escape", map[int][]byte{stOp: {opEscape, 0xab, 0x90}}, []byte{0xab, 0x90}},
	{
		"jump table",
		map[int][]byte{stOp: {opJumpTab, 1}, stCallIdx: {0, 0}, stCall32: cat(be32(0x11223344), be32(0x55667788))},
		cat(le32(0x11223344), le32(0x55667788)),
	},
	{
		"operand size prefix",
		map[int][]byte{stOp: {0x66, 0xb8}, stImm16: {0x12, 0x34}},
		[]byte{0x66, 0xb8, 0x34, 0x12},
	},
	{
		"ModRM, SIB and byte displacement", // mov eax, [esp+8]
		map[int][]byte{stOp: {0x8b, 0x44}, stSIB: {0x24}, stDisp8R0 + 4: {0x08}},
		[]byte{0x8b, 0x44, 0x24, 0x08},
	},
	{
		"absolute addresses", // mov eax, [addr]; mov eax, [addr] with ModRM
		map[int][]byte{stOp: {0xa1, 0x8b, 0x05}, stAddr32: cat(be32(0x00401000), be32(0x00402000))},
		cat([]byte{0xa1}, le32(0x00401000), []byte{0x8b, 0x05}, le32(0x00402000)),
	},
	{
		"conditional jump", // 0f 84: the target is absolute in the jump stream
		map[int][]byte{stOp: {0x0f, 0x84}, stJump32: be32(base + 0x40)},
		cat([]byte{0x0f, 0x84}, le32(0x40-6)),
	},
	{
		"opcode group", // f7 /0 is test r/m32, imm32
		map[int][]byte{stOp: {0xf7, 0xc0}, stImm32: be32(0x11223344)},
		cat([]byte{0xf7, 0xc0}, le32(0x11223344)),
	},
	{
		"invalid opcode", // 0f 00 is fERR: ModRM and a dword immediate, as FreeArc (no assert) decodes it
		map[int][]byte{stOp: {0x0f, 0x00, 0xc0}, stImm32: be32(0xaabbccdd)},
		cat([]byte{0x0f, 0x00, 0xc0}, le32(0xaabbccdd)),
	},
	{
		"enter", // a word and a byte operand
		map[int][]byte{stOp: {opEnter}, stImm16: {0x00, 0x10}, stImm8: {0x01}},
		[]byte{opEnter, 0x10, 0x00, 0x01},
	},
}

func TestUnfilter(t *testing.T) {
	t.Parallel()
	for _, tt := range unfilterCases {
		got := make([]byte, len(tt.want))
		if err := unfilter(filtered(tt.streams), got, base); err != nil || !bytes.Equal(got, tt.want) {
			t.Errorf("%s: % x, %v; want % x", tt.name, got, err, tt.want)
		}
	}
}

func TestUnfilterBad(t *testing.T) {
	t.Parallel()
	ok := filtered(map[int][]byte{stOp: {0x90, 0xc3}})
	tests := []struct {
		name string
		src  []byte
		size int
	}{
		{"no header", ok[:headerLen-1], 2},
		{"stream past the end", ok[:len(ok)-1], 2},
		{"bytes left over", append(bytes.Clone(ok), 0), 2},
		{"output too small", ok, 1},
		{"output too large", ok, 3},
		{"no call index", filtered(map[int][]byte{stOp: {0xe8}}), 5},
		{"no call address", filtered(map[int][]byte{stOp: {0xe8}, stCallIdx: {0}}), 5},
		{"no ModRM", filtered(map[int][]byte{stOp: {0x8b}}), 2},
		{"no immediate", filtered(map[int][]byte{stOp: {0xb8}}), 5},
		{"no jump table count", filtered(map[int][]byte{stOp: {opJumpTab}}), 4},
		{"no escaped byte", filtered(map[int][]byte{stOp: {opEscape}}), 1},
	}
	for _, tt := range tests {
		err := unfilter(tt.src, make([]byte, tt.size), base)
		if !errors.Is(err, method.ErrBadData) {
			t.Errorf("%s: %v, want corrupt data", tt.name, err)
		}
	}
}

func TestAdvance(t *testing.T) {
	t.Parallel()
	tests := []struct{ base, size, want uint32 }{
		{baseStart, 100, baseStart + 100},
		{baseWrap - 1, 0, baseWrap - 1},
		{baseWrap - 1, 1, baseStart},
		{baseWrap - 10, 8 << 20, baseStart - 10 + 8<<20},
		{0xfffffff0, 0x20, 0x10}, // the uint32 sum wraps first and stays below 3 GB
	}
	for _, tt := range tests {
		if got := advance(tt.base, tt.size); got != tt.want {
			t.Errorf("advance(%#x, %#x) = %#x, want %#x", tt.base, tt.size, got, tt.want)
		}
	}
}

// Records of the dispack stream.
func header(chunk int) []byte { return le32(uint32(chunk)) }

func dataRecord(b []byte) []byte { return cat(le32(tagData), le32(uint32(len(b))), b) }

func exeRecord(outSize int, code []byte) []byte {
	return cat(le32(tagExe), le32(uint32(outSize)), le32(uint32(len(code))), code)
}

func TestDecode(t *testing.T) {
	t.Parallel()
	raw := []byte("0123456789abcdef") // a stored chunk of the chunk size: its first word is no tag
	call := filtered(map[int][]byte{stOp: {0xe8}, stCallIdx: {0}, stCall32: be32(base + 0x100)})
	// relCall is the call to base + 0x100 made by code at base + at: the address advances by every record.
	relCall := func(at uint32) []byte { return cat([]byte{0xe8}, le32(0x100-at-5)) }
	tagged := cat(le32(tagData+5), []byte("abcd")) // starts with a reserved word, so it needs TAG_DATA
	stream := cat(header(16), raw, dataRecord([]byte("xyz")), exeRecord(5, call), dataRecord(tagged), exeRecord(5, call))
	want := cat(raw, []byte("xyz"), relCall(16+3), tagged, relCall(16+3+5+8))
	got, err := codectest.Decode(codectest.Parse(t, "dispack070"), stream)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("% x, %v\nwant % x", got, err, want)
	}
	for name, s := range map[string][]byte{"empty": nil, "header only": header(16384)} {
		if got, err := codectest.Decode(codectest.Parse(t, "dispack070"), s); err != nil || len(got) != 0 {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
}

func TestDecodeBad(t *testing.T) {
	t.Parallel()
	call := filtered(map[int][]byte{stOp: {0x90}})
	tests := []struct {
		name   string
		stream []byte
		want   string // output before the error
	}{
		{"short header", []byte{1, 2}, ""},
		{"chunk larger than the block", header(9 << 20), ""},
		{"short tag", cat(header(16), dataRecord([]byte("ab")), []byte{1, 2}), "ab"},
		{"unknown tag", cat(header(16), le32(tagData+2)), ""},
		{"no data length", cat(header(16), le32(tagData)), ""},
		{"negative data length", cat(header(16), le32(tagData), le32(^uint32(0))), ""},
		{"short data", cat(header(16), le32(tagData), le32(5), []byte("ab")), ""},
		{"short chunk", cat(header(16), []byte("0123456789")), ""},
		{"chunk size below a word", cat(header(2), []byte("0123")), ""},
		{"code larger than the block", cat(header(16), exeRecord(9<<20, call)), ""},
		{"code larger than its streams allow", cat(header(16), exeRecord(4*len(call)+1, call)), ""},
		{"bad code", cat(header(16), exeRecord(2, call)), ""},
		{"no code sizes", cat(header(16), le32(tagExe), le32(1)), ""},
	}
	for _, tt := range tests {
		got, err := codectest.Decode(codectest.Parse(t, "dispack070"), tt.stream)
		if !errors.Is(err, method.ErrBadData) || string(got) != tt.want {
			t.Errorf("%s: %q, %v; want %q and corrupt data", tt.name, got, err, tt.want)
		}
	}
}

func FuzzDecode(f *testing.F) {
	for _, tt := range unfilterCases {
		f.Add(cat(header(16), exeRecord(len(tt.want), filtered(tt.streams))))
	}
	f.Add(cat(header(16), []byte("0123456789abcdef"), dataRecord([]byte("xyz"))))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, s []byte) {
		_ = codectest.Parse(t, "dispack070:b1mb").Decode(&codectest.LimitWriter{Max: 4 << 20}, bytes.NewReader(s))
	})
}

// FuzzUnfilter runs the rebuilding on any streams and output size: it must fail cleanly, never panic.
func FuzzUnfilter(f *testing.F) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, tt := range unfilterCases {
		f.Add(filtered(tt.streams), uint16(len(tt.want)), rng.Uint32())
	}
	f.Fuzz(func(t *testing.T, src []byte, size uint16, at uint32) {
		_ = unfilter(src, make([]byte, size), at)
	})
}

// BenchmarkUnfilter rebuilds 1 MiB-ish of code made of the test instructions repeated. The output size does
// not depend on the call table, so the repeated records fill it exactly.
func BenchmarkUnfilter(b *testing.B) {
	streams := map[int][]byte{}
	size := 0
	for size < 1<<20 {
		for _, tt := range unfilterCases {
			for st, v := range tt.streams {
				streams[st] = append(streams[st], v...)
			}
			size += len(tt.want)
		}
	}
	src, dst := filtered(streams), make([]byte, size)
	b.SetBytes(int64(size))
	b.ReportAllocs()
	for b.Loop() {
		if err := unfilter(src, dst, base); err != nil {
			b.Fatal(err)
		}
	}
}
