package crypt

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/n0madic/go-unarc/internal/codectest"
	"github.com/n0madic/go-unarc/internal/method"
)

const password = "test-password"

func random(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, 7))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// decode decodes in with method m and the password; it returns the output, the error and what went to stderr.
func decode(t testing.TB, m, pwd string, in []byte) ([]byte, error, string) {
	t.Helper()
	var stderr bytes.Buffer
	env := &method.Env{Password: pwd, Stderr: &stderr}
	c, ok := Parse(method.Params(m), env)
	if !ok {
		t.Fatalf("Parse(%q) failed", m)
	}
	var out bytes.Buffer
	err := c.Decode(&out, bytes.NewReader(in))
	return out.Bytes(), err, stderr.String()
}

func TestRoundTripFagen(t *testing.T) {
	codectest.FagenPath(t)
	algs := []string{"aes", "aes-128", "aes-192/ctr", "aes-256/cfb", "aes-128/cfb", "blowfish", "blowfish-64",
		"blowfish-128/cfb", "twofish", "twofish-128", "twofish-192/cfb", "serpent", "serpent-128", "serpent-192/cfb"}
	// 2 MiB + 3: the little-endian counter carries into its third byte; the odd tail ends inside a block.
	data := random(2<<20+3, 1)
	for _, alg := range algs {
		t.Run(alg, func(t *testing.T) {
			t.Parallel()
			lines := strings.Fields(string(codectest.Fagen(t, nil, "crypt", alg, password)))
			if len(lines) != 2 {
				t.Fatalf("fagen crypt: %q", lines)
			}
			compress, stored := lines[0], lines[1]
			packed := codectest.Fagen(t, data, "c", compress)

			// The archive's method: key from the password.
			got, err, _ := decode(t, stored, password, packed)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("stored method %s: err %v, output equal %v", stored, err, bytes.Equal(got, data))
			}
			// An explicit key, read the 0.67 way by both decoders.
			want := codectest.Fagen(t, packed, "d", compress)
			got, err, _ = decode(t, compress, "", packed)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("method %s: err %v, output equal to C %v", compress, err, bytes.Equal(got, want))
			}
			// Short inputs, ending inside the first blocks.
			for _, n := range []int{0, 1, 7, 8, 9, 16, 17} {
				p := codectest.Fagen(t, data[:n], "c", compress)
				got, err, _ := decode(t, stored, password, p)
				if err != nil || !bytes.Equal(got, data[:n]) {
					t.Fatalf("%d bytes: err %v", n, err)
				}
			}
		})
	}
}

func TestPasswordErrors(t *testing.T) {
	t.Parallel()
	codectest.FagenPath(t)
	lines := strings.Fields(string(codectest.Fagen(t, nil, "crypt", "aes", password)))
	stored := lines[1]
	tests := []struct {
		name, method, pwd string
		want              error
		stderr            string
	}{
		{"no password", stored, "", method.ErrPasswordRequired,
			"\nERROR: the archive is encrypted: give its password with -p{password} or UNARC_PASSWORD\n"},
		{"wrong password", stored, "wrong", method.ErrWrongPassword, "\nERROR: wrong password\n"},
		{"three-byte check code", strings.Replace(stored, ":c", ":c00", 1), password, method.ErrBadData, ""},
		{"one-byte check code", withCode(stored, "c00"), password, method.ErrBadData, ""},
		{"short iv", "aes:s00:i00", password, method.ErrBadData, ""},
		{"long salt", "aes:s" + strings.Repeat("0", 130) + ":i" + strings.Repeat("0", 32), password, method.ErrBadData, ""},
		{"key too long for PBKDF2 check", "aes-512:s00:i" + strings.Repeat("0", 32), password, method.ErrBadData, ""},
		// A key or a number of rounds the cipher rejects: in CFB mode C crashes or produces garbage.
		{"bad aes key, cfb", "aes/cfb:k0102:i" + strings.Repeat("0", 32), "", method.ErrBadData, ""},
		{"bad aes rounds, cfb", "aes/cfb:r9:k" + strings.Repeat("1", 32) + ":i" + strings.Repeat("0", 32), "", method.ErrBadData, ""},
		{"short blowfish key, cfb", "blowfish/cfb:k01020304:i" + strings.Repeat("0", 16), "", method.ErrBadData, ""},
	}
	for _, tt := range tests {
		_, err, stderr := decode(t, tt.method, tt.pwd, []byte("data"))
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
		if stderr != tt.stderr {
			t.Errorf("%s: stderr = %q, want %q", tt.name, stderr, tt.stderr)
		}
	}
}

// TestRejectedKeyCTR: when the cipher rejects the key or the number of rounds, CTR mode passes the data through
// unchanged, as the C decoder does (FreeArc 0.67 wrote such archives unencrypted).
func TestRejectedKeyCTR(t *testing.T) {
	t.Parallel()
	data := []byte("hello world, plain data")
	for _, m := range []string{
		"aes:k0102:i" + strings.Repeat("0", 32),
		"aes:r9:k" + strings.Repeat("1", 32) + ":i" + strings.Repeat("0", 32),
		"blowfish:k01020304:i" + strings.Repeat("0", 16),
		"twofish:r3:k" + strings.Repeat("1", 32) + ":i" + strings.Repeat("0", 32),
	} {
		got, err, _ := decode(t, m, "", data)
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: %q, %v; want the input unchanged", m, got, err)
		}
		if codectest.HaveFagen(t) {
			if want := codectest.Fagen(t, data, "d", m); !bytes.Equal(got, want) {
				t.Errorf("%s: Go %q, C %q", m, got, want)
			}
		}
	}
}

// withCode replaces the check code parameter of m.
func withCode(m, code string) string {
	params := strings.Split(m, ":")
	for i, p := range params {
		if strings.HasPrefix(p, "c") {
			params[i] = code
		}
	}
	return strings.Join(params, ":")
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method          string
		ok              bool
		cipher, mode, n int
		keySize         int
	}{
		{"aes", true, 0, modeCTR, 1000, 32},
		{"aes-128/cfb", true, 0, modeCFB, 1000, 16},
		{"aes/ctr:n10:r14:k:i:s:c", true, 0, modeCTR, 10, 32},
		{"aes-0", true, 0, modeCTR, 1000, 32},
		{"aes-7", true, 0, modeCTR, 1000, 32},
		{"blowfish", true, 1, modeCTR, 1000, 56},
		{"serpent-192", true, 2, modeCTR, 1000, 24},
		{"twofish/cfb", true, 3, modeCFB, 1000, 32},
		{"aes/ecb", false, 0, 0, 0, 0},
		{"aes/ctr/x", false, 0, 0, 0, 0},
		{"aes-x", false, 0, 0, 0, 0},
		{"aes-", false, 0, 0, 0, 0},
		{"AES", false, 0, 0, 0, 0},
		{"rijndael", false, 0, 0, 0, 0},
		{"aes:", false, 0, 0, 0, 0},
		{"aes:n", false, 0, 0, 0, 0},
		{"aes:q1", false, 0, 0, 0, 0},
		{"aes:f", false, 0, 0, 0, 0},
		{"lzma", false, 0, 0, 0, 0},
	}
	for _, tt := range tests {
		c, ok := Parse(method.Params(tt.method), nil)
		if ok != tt.ok {
			t.Errorf("Parse(%q) ok = %v, want %v", tt.method, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		got := c.(*codec)
		if got.cipher != tt.cipher || got.mode != tt.mode || int(got.iterations) != tt.n || got.keySize != tt.keySize {
			t.Errorf("Parse(%q) = %+v", tt.method, *got)
		}
	}
}

func TestHex(t *testing.T) {
	t.Parallel()
	if got := decode16("0aFf7", hexval); !bytes.Equal(got, []byte{0x0a, 0xff}) {
		t.Errorf("decode16 = %x", got)
	}
	if got := decode16("0aFf", hexval067); !bytes.Equal(got, []byte{0x00, 0x55}) {
		t.Errorf("decode16 0.67 = %x", got)
	}
	// Non-hex characters give what C computes: 'g' is 16 (or 6 for 0.67), '!' is negative.
	junk := ('!' - 'a' + 10) * 16
	if got := decode16("g0!0", hexval); !bytes.Equal(got, []byte{0x00, byte(junk)}) {
		t.Errorf("decode16 of junk = %x", got)
	}
	for b := range 256 {
		x := []byte{byte(b)}
		if got, want := mangle067(x), decode16(hex.EncodeToString(x), hexval067); !bytes.Equal(got, want) {
			t.Errorf("mangle067(%02x) = %x, want %x", b, got, want)
		}
	}
}

// stopWriter asks for no more data.
type stopWriter struct{}

func (stopWriter) Write([]byte) (int, error) { return 0, method.ErrStop }

func TestStop(t *testing.T) {
	t.Parallel()
	c, _ := Parse(method.Params("aes:k"+strings.Repeat("1", 64)+":i"+strings.Repeat("2", 32)), &method.Env{})
	if err := c.Decode(stopWriter{}, bytes.NewReader(random(1000, 2))); !errors.Is(err, method.ErrStop) {
		t.Fatalf("err = %v, want ErrStop", err)
	}
}

func FuzzParseDecode(f *testing.F) {
	f.Add("aes-128/cfb:s00:c0000:i00000000000000000000000000000000:n1", []byte("abc"))
	f.Add("blowfish:k1111111111111111:i0000000000000000", []byte("abc"))
	f.Add("serpent-8/ctr:k:i"+strings.Repeat("9", 40), []byte{})
	f.Fuzz(func(t *testing.T, m string, data []byte) {
		if len(m) > 2048 {
			return
		}
		env := &method.Env{Password: "x", Stderr: io.Discard}
		params := method.Params(m)
		if len(params) == 0 {
			return
		}
		c, ok := Parse(params, env)
		if !ok || c.(*codec).iterations > 10 {
			return
		}
		_ = c.Decode(io.Discard, bytes.NewReader(data))
	})
}

func BenchmarkDecode(b *testing.B) {
	for _, m := range []string{"aes/ctr", "aes/cfb", "blowfish", "twofish", "serpent"} {
		b.Run(m, func(b *testing.B) {
			info := ciphers[findCipher(strings.Split(m, "/")[0])]
			c, _ := Parse(method.Params(m+":k"+strings.Repeat("1", 2*info.maxKey)+":i"+strings.Repeat("2", 32)), &method.Env{})
			data := random(16<<20, 3)
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				if err := c.Decode(io.Discard, bytes.NewReader(data)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
