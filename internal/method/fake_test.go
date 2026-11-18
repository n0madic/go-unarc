package method

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Test methods, registered for the tests of this package:
//
//	fakemem:<n>     needs n bytes of memory, copies its input
//	prefix:<text>   writes text, then its input
//	fail            reads a little, then fails with ErrBadData
//	panic           panics
//	upper           upper-cases its input chunk by chunk
func init() {
	Register(func(params []string, _ *Env) (Codec, bool) {
		switch params[0] {
		case "fakemem":
			if len(params) != 2 {
				return nil, false
			}
			n, err := strconv.ParseUint(params[1], 10, 64)
			return fakeCodec{mem: n}, err == nil
		case "prefix":
			return fakeCodec{prefix: strings.Join(params[1:], ":")}, true
		case "fail", "panic", "upper", "take-one", "fail-at-end":
			return fakeCodec{kind: params[0]}, true
		}
		return nil, false
	})
}

type fakeCodec struct {
	kind   string
	mem    uint64
	prefix string
}

func (c fakeCodec) DecompressionMem() uint64 { return c.mem }

func (c fakeCodec) Decode(dst io.Writer, src io.Reader) error {
	switch c.kind {
	case "fail":
		_, _ = io.ReadFull(src, make([]byte, 10))
		return errors.Join(ErrBadData, errors.New("fake failure"))
	case "take-one":
		_, err := io.CopyN(dst, src, 1)
		return err
	case "fail-at-end":
		if _, err := io.Copy(dst, src); err != nil {
			return err
		}
		return fmt.Errorf("%w: final check failed", ErrBadData)
	case "panic":
		panic("fake panic")
	case "upper":
		buf := make([]byte, 7)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				if _, werr := dst.Write(bytes.ToUpper(buf[:n])); werr != nil {
					return werr
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	if _, err := io.WriteString(dst, c.prefix); err != nil {
		return err
	}
	_, err := io.Copy(dst, src)
	return err
}
