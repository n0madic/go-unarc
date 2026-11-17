package main

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"

	"github.com/n0madic/go-unarc/internal/arctest"
)

// Table returns n bytes of fixed-size records with slowly changing fields: the data the delta filter finds.
func Table(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 5))
	var b bytes.Buffer
	var a, c uint32
	for b.Len() < n {
		a += uint32(r.IntN(16))
		c += 1000 + uint32(r.IntN(3))
		binary.Write(&b, binary.LittleEndian, a)
		binary.Write(&b, binary.LittleEndian, c)
		binary.Write(&b, binary.LittleEndian, uint16(r.IntN(4)))
		binary.Write(&b, binary.LittleEndian, uint16(7))
	}
	return b.Bytes()[:n]
}

// single is an archive with one solid block.
func single(s arctest.Solid) arctest.Archive { return arctest.Archive{Solids: []arctest.Solid{s}} }

// method is a fixture of one solid block compressed with m.
func method(name, m string, files func() []arctest.File) fixture {
	return fixture{name: name, build: func() arctest.Archive { return single(packed(m, files()...)) }}
}

func fixtureList(in *inputs) []fixture {
	text := func(n int) func() []arctest.File {
		return func() []arctest.File {
			return []arctest.File{file("src/a.cpp", in.Text(0, n/3)), file("src/b.cpp", in.Text(n/3, n-n/3))}
		}
	}
	data := func(name string, b []byte) func() []arctest.File {
		return func() []arctest.File { return []arctest.File{file(name, b)} }
	}
	wav16 := data("snd/stereo16.wav", Wav(16, 2, 60000))
	mixed := func() []arctest.File {
		return []arctest.File{file("text.txt", in.Text(100000, 200000)), file("bin.dat", Repetitive(7, 200000)),
			file("x86.exe", X86(8, 100000))}
	}
	bad := uint32(0x12345678)

	fs := []fixture{
		{name: "storing", build: func() arctest.Archive {
			return arctest.Archive{
				Comment: []byte("Test archive comment"),
				Solids: []arctest.Solid{
					{Compressor: "storing", Files: []arctest.File{dir("docs"), file("docs/readme.txt", in.Text(0, 3000)),
						file("empty.txt", nil), file("docs/sub/data.bin", Random(1, 70000)), dir("docs/emptydir")}},
					{Compressor: "storing", Files: []arctest.File{file("a.txt", []byte("first\n")), file("b.txt", []byte("second\n"))}},
					{Compressor: "storing", Files: []arctest.File{file("other/a.txt", []byte("same name, other folder\n"))}},
				},
				Dirs: []int{2, 1},
			}
		}, extra: [][]string{{"l", "a.txt"}, {"v", "a.txt", "empty.txt"}, {"t", "data.bin"}, {"t", "missing"},
			{"x"}, {"e", "-o+"}, {"e", "-o-"}, {"x", "a.txt"}, {"x", "-dpout"}, {"e", "-dpout/", "-o+"}}},
		{name: "storing_sfx", build: func() arctest.Archive {
			return arctest.Archive{Prefix: Random(2, 5000), OldComment: []byte("old style comment"), Locked: true,
				Solids: []arctest.Solid{{Compressor: "storing", Files: []arctest.File{file("f.txt", []byte("sfx\n"))}}}}
		}},
		{name: "storing_nulcomment", build: func() arctest.Archive {
			return arctest.Archive{Comment: []byte("ab\x00cd"),
				Solids: []arctest.Solid{{Compressor: "storing", Files: []arctest.File{file("f.txt", []byte("x"))}}}}
		}},
		{name: "empty", build: func() arctest.Archive { return arctest.Archive{} }},
		{name: "control_lzma", build: func() arctest.Archive {
			return arctest.Archive{Comment: []byte("compressed headers"), ControlCompressor: "lzma:1mb",
				Pack:   func(c string, d []byte) []byte { return fagen(c, d) },
				Solids: []arctest.Solid{packed("lzma:1mb", file("a.txt", in.Text(0, 50000)))}}
		}},
		{name: "multi", build: func() arctest.Archive {
			return arctest.Archive{Solids: []arctest.Solid{
				packed("lzma:1mb:normal:bt4:32", file("one/a.cpp", in.Text(0, 100000)), file("one/b.cpp", in.Text(100000, 50000))),
				packed("ppmd:8:16mb", file("two/c.cpp", in.Text(150000, 120000))),
				{Compressor: "storing", Files: []arctest.File{file("three/d.txt", []byte("stored\n"))}},
				packed("tor:5", file("four/e.bin", Repetitive(3, 150000))),
			}, Dirs: []int{2, 2}}
		}, extra: [][]string{{"t", "c.cpp"}, {"t", "a.cpp", "e.bin"}}},

		method("lzma_data1", "lzma:1mb:normal:bt4:32", text(600000)),
		method("lzma_default", "lzma", text(300000)),
		method("lzma_smalldict", "lzma:64kb:fast:hc4", text(500000)),
		method("lzma_lc8", "lzma:8mb:max:lc8", text(300000)),
		method("lzma_lp4pb4", "lzma:4mb:lc0:lp4:pb4", data("t.bin", Table(1, 300000))),
		method("lzma_random", "lzma:1mb", data("r.bin", Random(3, 100000))),
		method("exe_lzma", "exe+lzma:1mb", data("prog.exe", X86(1, 400000))),
		method("delta", "delta", data("t.bin", Table(2, 300000))),
		method("delta_lzma", "delta+lzma:1mb", data("t.bin", Table(3, 300000))),
		method("rep", "rep:1mb", data("r.bin", Repetitive(4, 600000))),
		method("rep_lzma", "rep:1mb+lzma:1mb", data("r.bin", Repetitive(5, 600000))),
		method("dict", "dict", text(400000)),
		method("dict_lzp_ppmd", "dict+lzp+ppmd:8:16mb", text(400000)),
		method("lzp", "lzp", text(300000)),
		method("lzp_ppmd", "lzp+ppmd:6:8mb", text(300000)),
		method("ppmd", "ppmd:8:16mb", text(400000)),
		method("ppmd_o4", "ppmd:4:8mb", data("r.bin", Repetitive(6, 300000))),
		method("ppmd_o16", "ppmd:16:32mb", text(300000)),
		method("tor1", "tor:1", mixed),
		method("tor2", "tor:2", mixed),
		method("tor3", "tor:3", mixed),
		method("tor4", "tor:4", mixed),
		method("tor5", "tor:5", mixed),
		method("tor6", "tor:6", mixed),
		method("tor7", "tor:7", mixed),
		method("tor8", "tor:8", mixed),
		method("tor9", "tor:9", mixed),
		method("tor10", "tor:10", mixed),
		method("tor11", "tor:11", mixed),
		method("tor_11c1", "tor:11:c1", mixed),
		method("tor_9c2", "tor:9:c2", mixed),
		method("tor_7c3", "tor:7:c3", mixed),
		method("tor_u1t0", "tor:5:u1:t0", mixed),
		method("tor_smallbuf", "tor:5:b64kb", mixed),
		method("grzip", "grzip", text(400000)),
		method("grzip_m1", "grzip:m1", text(400000)),
		method("grzip_m2", "grzip:m2", text(400000)),
		method("grzip_m3", "grzip:m3", text(400000)),
		method("grzip_m4", "grzip:m4", text(400000)),
		method("grzip_blocks", "grzip:m1:b128kb", mixed),
		method("grzip_nolzp", "grzip:m2:l", text(300000)),
		method("grzip_delta", "grzip:m1:d:a", data("t.bin", Table(4, 300000))),
		method("mm", "mm:2*16:o44", data("snd/small16.wav", Wav(16, 2, 20000))),
		method("mm_d1", "mm:d1:1*8", data("snd/mono8.wav", Wav(8, 1, 30000))),
		method("mm_grzip", "mm:2*16:o44+grzip:m1", wav16),
		method("tta", "tta", wav16),
		method("tta_params", "tta:2*16:o44", wav16),
		method("tta8", "tta", data("snd/mono8.wav", Wav(8, 1, 80000))),
		method("tta24", "tta", data("snd/stereo24.wav", Wav(24, 2, 40000))),
	}

	for _, c := range []struct{ name, inner, alg string }{
		{"aes", "lzma:1mb:normal:bt4:32", "aes-256/ctr"},
		{"aes128", "lzma:1mb", "aes-128/ctr"},
		{"aes_cfb", "lzma:1mb", "aes/cfb"},
		{"blowfish", "lzma:1mb", "blowfish/ctr"},
		{"blowfish_cfb", "lzma:1mb", "blowfish-128/cfb"},
		{"twofish", "lzma:1mb", "twofish/ctr"},
		{"twofish_cfb", "lzma:1mb", "twofish-192/cfb"},
		{"serpent", "lzma:1mb", "serpent/ctr"},
		{"serpent_cfb", "lzma:1mb", "serpent-128/cfb"},
		{"chain_crypt", "lzma:1mb+aes-256/ctr", "serpent/ctr"},
	} {
		inner, alg := c.inner, c.alg
		fs = append(fs, fixture{name: c.name, password: Password, build: func() arctest.Archive {
			return single(encrypted(inner, alg, file("secret/a.txt", in.Text(0, 100000)), file("secret/b.bin", Random(9, 20000))))
		}})
	}
	// chain_crypt encrypts twice; the inner cipher must also be stored the 0.67 way.
	fs[len(fs)-1].build = func() arctest.Archive {
		c1, s1 := cipher("aes-256/ctr")
		c2, s2 := cipher("serpent/ctr")
		files := []arctest.File{file("a.txt", in.Text(0, 100000))}
		return single(arctest.Solid{Compressor: "lzma:1mb+" + s1 + "+" + s2,
			Packed: fagen("lzma:1mb+"+c1+"+"+c2, arctest.Concat(files)), Files: files})
	}
	headers := func() arctest.Archive {
		c, s := cipher("aes-256/ctr")
		return arctest.Archive{ControlCompressor: "lzma:1mb+" + s,
			Pack:    func(_ string, d []byte) []byte { return fagen("lzma:1mb+"+c, d) },
			Comment: []byte("encrypted headers"),
			Solids:  []arctest.Solid{encrypted("lzma:1mb", "aes-256/ctr", file("hidden.txt", in.Text(0, 30000)))},
		}
	}
	fs = append(fs,
		fixture{name: "aes_headers", password: Password, build: headers},
		fixture{name: "aes_wrongpw", password: "wrong", err: true, build: func() arctest.Archive {
			return single(encrypted("lzma:1mb", "aes-256/ctr", file("a.txt", in.Text(0, 10000))))
		}},
		fixture{name: "headers_wrongpw", password: "wrong", err: true, build: headers},
		fixture{name: "aes_nopassword", err: true, build: func() arctest.Archive {
			return single(encrypted("lzma:1mb", "aes-256/ctr", file("a.txt", in.Text(0, 10000))))
		}},
		fixture{name: "headers_nopassword", err: true, build: headers},

		// Archives the C unarc rejects.
		fixture{name: "bad_crc", err: true, build: func() arctest.Archive {
			f := file("b.txt", []byte("wrong crc\n"))
			f.CRC = &bad
			return single(arctest.Solid{Compressor: "storing", Files: []arctest.File{file("a.txt", []byte("good\n")), f, file("c.txt", []byte("after\n"))}})
		}, extra: [][]string{{"x"}, {"t", "a.txt"}}},
		fixture{name: "unsupported", err: true, build: func() arctest.Archive {
			return single(arctest.Solid{Compressor: "foo:1:2", Packed: []byte("whatever"), Files: []arctest.File{file("a.txt", []byte("12345678"))}})
		}},
		fixture{name: "unsupported_chain", err: true, build: func() arctest.Archive {
			return single(arctest.Solid{Compressor: "lzma:1mb+bar", Packed: []byte("whatever"), Files: []arctest.File{file("a.txt", []byte("12345678"))}})
		}},
		fixture{name: "unsupported_control", err: true, build: func() arctest.Archive {
			return arctest.Archive{ControlCompressor: "baz", Pack: func(_ string, d []byte) []byte { return d },
				Solids: []arctest.Solid{{Compressor: "storing", Files: []arctest.File{file("a.txt", []byte("x"))}}}}
		}},
		fixture{name: "lzma_corrupt", err: true, build: func() arctest.Archive {
			s := packed("lzma:1mb", file("a.txt", in.Text(0, 200000)))
			for i := 1000; i < 1100; i++ {
				s.Packed[i] ^= 0x55
			}
			return single(s)
		}},
		fixture{name: "lzma_truncated", err: true, build: func() arctest.Archive {
			s := packed("lzma:1mb", file("a.txt", in.Text(0, 200000)))
			s.Packed = s.Packed[:len(s.Packed)/2]
			return single(s)
		}},
		fixture{name: "short_data", err: true, build: func() arctest.Archive {
			// The stream ends inside the second file.
			files := []arctest.File{file("a.txt", []byte("aaaa")), file("b.txt", []byte("bbbbbbbb"))}
			return single(arctest.Solid{Compressor: "storing", Packed: []byte("aaaabbb"), Files: files})
		}},
		fixture{name: "unsafe_paths", build: func() arctest.Archive {
			return single(arctest.Solid{Compressor: "storing", Files: []arctest.File{
				file("ok.txt", []byte("fine\n")), file("../evil.txt", []byte("evil\n")), file("/tmp/abs.txt", []byte("abs\n")),
				file("a/..\\..\\win.txt", []byte("win\n"))}})
		}},
	)
	return fs
}
