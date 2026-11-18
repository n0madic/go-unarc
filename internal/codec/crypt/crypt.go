// Package crypt implements FreeArc's encryption methods (C_Encryption.cpp): <cipher>[-<bits>][/<mode>] with
// the ciphers aes, blowfish, serpent and twofish and the modes ctr (default; little-endian counter) and cfb.
//
// Parameters: k<hex key>, i<hex iv>, s<hex salt>, c<hex check code>, n<PBKDF2 iterations> (default 1000),
// r<rounds>. Archives store s, c and i: the key is PBKDF2-HMAC-SHA512(password, salt) and two more bytes of the
// derived data are the check code. FreeArc 0.67 read k and i with a hex decoder that maps a..f to 0..5, and
// passed the derived key through it as well; archives were encrypted with keys and IVs mangled that way.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/blowfish"
	"golang.org/x/crypto/twofish"

	"github.com/n0madic/go-unarc/internal/codec/crypt/serpent"
	"github.com/n0madic/go-unarc/internal/method"
)

func init() { method.Register(Parse) }

const (
	// maxKeySize is MAXKEYSIZE: the longest key, IV, salt or check code in bytes.
	maxKeySize = 64
	// checkSize is the length of the password check code.
	checkSize         = 2
	defaultIterations = 1000
	bufferSize        = 256 * method.KB // LARGE_BUFFER_SIZE
)

// cipherInfo describes a block cipher as LibTomCrypt registers it.
type cipherInfo struct {
	name      string
	maxKey    int
	blockSize int
	// newBlock checks the key length and the number of rounds (0 is the default) like the LibTomCrypt setup.
	newBlock func(key []byte, rounds int) (cipher.Block, error)
}

var errKey = errors.New("invalid key size or number of rounds")

// ciphers in the order C registers them: their indexes appear in UNARC_DEBUG output.
var ciphers = []cipherInfo{
	{"aes", 32, aes.BlockSize, func(key []byte, rounds int) (cipher.Block, error) {
		if n := len(key); n != 16 && n != 24 && n != 32 || rounds != 0 && rounds != 10+(n/8-2)*2 {
			return nil, errKey
		}
		return aes.NewCipher(key)
	}},
	{"blowfish", 56, blowfish.BlockSize, func(key []byte, rounds int) (cipher.Block, error) {
		if len(key) < 8 || len(key) > 56 || rounds != 0 && rounds != 16 {
			return nil, errKey
		}
		return blowfish.NewCipher(key)
	}},
	{"serpent", serpent.MaxKeySize, serpent.BlockSize, func(key []byte, _ int) (cipher.Block, error) {
		return serpent.NewCipher(key) // serpent.c ignores the number of rounds
	}},
	{"twofish", 32, twofish.BlockSize, func(key []byte, rounds int) (cipher.Block, error) {
		if n := len(key); n != 16 && n != 24 && n != 32 || rounds != 0 && rounds != 16 {
			return nil, errKey
		}
		return twofish.NewCipher(key)
	}},
}

func findCipher(name string) int {
	for i, c := range ciphers {
		if c.name == name {
			return i
		}
	}
	return -1
}

// Modes: their numbers appear in UNARC_DEBUG output.
const (
	modeCTR = 0
	modeCFB = 1
)

func findMode(name string) int {
	switch name {
	case "ctr":
		return modeCTR
	case "cfb":
		return modeCFB
	}
	return -1
}

type codec struct {
	cipher             int
	mode               int
	key, iv, salt, chk string // hex, as written in the method
	iterations         uint32
	rounds             uint32
	keySize            int // bytes
	env                *method.Env
}

// Parse mirrors parse_ENCRYPTION. It succeeds without a password: the password is needed only to decode.
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	name, modeName, hasMode := strings.Cut(params[0], "/")
	mode := modeCTR
	if hasMode {
		mode = findMode(modeName)
	}
	cipherName, bitsStr, hasBits := strings.Cut(name, "-")
	cipherIdx := findCipher(cipherName)
	var keyBits uint32
	bad := false
	if hasBits {
		var ok bool
		keyBits, ok = method.ParseInt(bitsStr)
		bad = !ok
	}
	if env != nil && env.Debug {
		fmt.Fprintf(env.Stderr, "parse_ENCRYPTION p0=%s mode=%d cipher=%d error=%d\n", params[0], mode, cipherIdx, b2i(bad))
	}
	if mode < 0 || cipherIdx < 0 || bad {
		return nil, false
	}
	c := &codec{cipher: cipherIdx, mode: mode, iterations: defaultIterations, keySize: int(keyBits / 8), env: env}
	if c.keySize == 0 {
		c.keySize = ciphers[cipherIdx].maxKey
	}
	for _, p := range params[1:] {
		if p == "" {
			return nil, false
		}
		v := p[1:]
		var ok = true
		switch p[0] {
		case 'k':
			c.key = v
		case 'i':
			c.iv = v
		case 's':
			c.salt = v
		case 'c':
			c.chk = v
		case 'n':
			c.iterations, ok = method.ParseInt(v)
		case 'r':
			c.rounds, ok = method.ParseInt(v)
		default:
			ok = false
		}
		if !ok {
			return nil, false
		}
	}
	return c, true
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DecompressionMem is 0, as in C.
func (*codec) DecompressionMem() uint64 { return 0 }

// hexval is C hexval: digits and letters, anything else gives garbage the same way.
func hexval(ch byte) int {
	if ch >= '0' && ch <= '9' {
		return int(ch - '0')
	}
	return int(lower(ch)) - 'a' + 10
}

// hexval067 is FreeArc 0.67's char2int: like hexval, but a..f give 0..5.
func hexval067(ch byte) int {
	if ch >= '0' && ch <= '9' {
		return int(ch - '0')
	}
	return int(lower(ch)) - 'a'
}

func lower(ch byte) byte {
	if ch >= 'A' && ch <= 'Z' {
		return ch + 'a' - 'A'
	}
	return ch
}

// decode16 decodes pairs of hex digits with val; a trailing odd digit is ignored, as in C.
func decode16(s string, val func(byte) int) []byte {
	out := make([]byte, len(s)/2)
	for i := range out {
		out[i] = byte(val(s[2*i])*16 + val(s[2*i+1]))
	}
	return out
}

// mangle067 is what happened to a derived key in 0.67: written as hex and read back with hexval067.
func mangle067(key []byte) []byte {
	out := make([]byte, len(key))
	for i, b := range key {
		hi, lo := b>>4, b&15
		if hi >= 10 {
			hi -= 10
		}
		if lo >= 10 {
			lo -= 10
		}
		out[i] = hi<<4 | lo
	}
	return out
}

func badData(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrBadData, fmt.Sprintf(format, args...))
}

// deriveKey returns the key: the k parameter or, without one, the key derived from the password.
func (c *codec) deriveKey() ([]byte, error) {
	if len(c.key)/2 > 0 {
		return decode16(c.key, hexval067), nil
	}
	if c.env.Password == "" {
		fmt.Fprint(c.env.Stderr, "\nERROR: the archive is encrypted: give its password with -p{password} or UNARC_PASSWORD\n")
		return nil, method.ErrPasswordRequired
	}
	if c.keySize <= 0 || c.keySize > maxKeySize-checkSize {
		return nil, badData("crypt: key size %d", c.keySize)
	}
	if c.chk != "" && len(c.chk) != 2*checkSize {
		return nil, badData("crypt: invalid check code length %d", len(c.chk))
	}
	// PBKDF2 runs U1 alone for any count below 2, like pkcs_5_alg2 (whose count is a C int).
	iter := max(int(int32(c.iterations)), 1)
	if err := c.env.CheckKDFRounds(uint64(iter)); err != nil {
		return nil, err
	}
	derived, err := pbkdf2.Key(sha512.New, c.env.Password, decode16(c.salt, hexval), iter, c.keySize+checkSize)
	if err != nil {
		return nil, fmt.Errorf("crypt: %w", err)
	}
	if c.chk != "" {
		// C compares strlen(code)/2 bytes, reading past the derived data for longer codes: only the two check
		// bytes are meaningful.
		if string(derived[c.keySize:]) != string(decode16(c.chk, hexval)) {
			fmt.Fprint(c.env.Stderr, "\nERROR: wrong password\n")
			return nil, method.ErrWrongPassword
		}
	}
	return mangle067(derived[:c.keySize]), nil
}

// Decode decrypts src in 256 KiB blocks, like docrypt.
func (c *codec) Decode(dst io.Writer, src io.Reader) error {
	// C keeps every hex parameter in a buffer of 2*MAXKEYSIZE characters: longer ones are corrupt data
	// (C truncated them silently and overran its buffers comparing the check code).
	for _, h := range []string{c.key, c.iv, c.salt, c.chk} {
		if len(h) > 2*maxKeySize {
			return badData("crypt: hex parameter of %d characters", len(h))
		}
	}
	info := &ciphers[c.cipher]
	if c.env.Debug {
		fmt.Fprintf(c.env.Stderr, "METHOD cipher=%s n=%d ks=%d\n", info.name, int32(c.iterations), c.keySize)
	}
	key, err := c.deriveKey()
	if err != nil {
		return err
	}
	iv := decode16(c.iv, hexval067)
	if len(iv) < info.blockSize {
		return badData("crypt: %d-byte IV for a %d-byte block", len(iv), info.blockSize)
	}
	var stream cipher.Stream
	block, err := info.newBlock(key, int(int32(c.rounds)))
	switch {
	case err != nil && c.mode == modeCTR:
		// C ignores the setup error and its ctr_decrypt then fails on the uninitialised state without touching
		// the data, so the data passes through as it is. FreeArc 0.67 encrypted that way too: an archive made
		// with -ae=aes-128:r20 holds its data unencrypted (testdata/genuine, genuine_aes_r20_n2000).
		stream = plain{}
	case err != nil:
		// In CFB mode C goes on with a half-initialised state: it crashes or produces garbage.
		return badData("crypt: %s with a %d-byte key, %d rounds: %v", info.name, len(key), int32(c.rounds), err)
	case c.mode == modeCTR:
		stream = newCTR(block, iv)
	default:
		stream = newCFB(block, iv)
	}
	buf := make([]byte, bufferSize)
	for {
		n, err := io.ReadFull(src, buf)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		if n == 0 {
			return nil
		}
		stream.XORKeyStream(buf[:n], buf[:n])
		if _, err := dst.Write(buf[:n]); err != nil {
			return err
		}
	}
}

// plain is the "encryption" of a cipher whose setup failed: none.
type plain struct{}

func (plain) XORKeyStream(dst, src []byte) { copy(dst, src) }
