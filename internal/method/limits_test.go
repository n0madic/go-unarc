package method

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestLimitsFromEnv(t *testing.T) {
	t.Setenv("UNARC_MAX_CONTROL_MB", "3")
	t.Setenv("UNARC_MAX_DECODER_MB", "invalid")
	t.Setenv("UNARC_MAX_TEMP_MB", "0")
	t.Setenv("UNARC_MAX_KDF_ITERATIONS", "7")
	env := NewEnv()
	if env.ControlLimit() != 3*MB || env.DecoderLimit() != defaultDecoderBytes || env.Limits.TempBytes != 0 ||
		!errors.Is(env.CheckKDFRounds(8), ErrLimit) {
		t.Fatalf("unexpected limits: %+v", env.Limits)
	}
}

func TestTempBudget(t *testing.T) {
	t.Parallel()
	env := &Env{Limits: Limits{TempBytes: 8}}
	a, b := env.TempSpace(), env.TempSpace()
	defer a.Release()
	defer b.Release()
	var out bytes.Buffer
	if _, err := io.WriteString(a.Writer(&out), "first"); err != nil {
		t.Fatal(err)
	}
	if err := b.Grow(4); !errors.Is(err, ErrLimit) {
		t.Fatalf("live total above budget: %v", err)
	}
	if n, err := io.WriteString(a.Writer(&out), "extra"); n != 0 || !errors.Is(err, ErrLimit) || out.String() != "first" {
		t.Fatalf("limited write = %d, %v; output %q", n, err, out.String())
	}
	a.Release()
	if err := b.Grow(8); err != nil {
		t.Fatalf("released budget is not reusable: %v", err)
	}
}

func TestSpillLimitCleansUp(t *testing.T) {
	t.Parallel()
	env := &Env{TempDir: t.TempDir(), Stderr: io.Discard, Limits: Limits{TempBytes: 3}}
	if err := Run("tempfile", io.Discard, strings.NewReader("example"), env); !errors.Is(err, ErrLimit) {
		t.Fatalf("Run = %v; want ErrLimit", err)
	}
	if entries, err := os.ReadDir(env.TempDir); len(entries) != 0 || err != nil {
		t.Fatalf("scratch after failed spill: %v, %v", entries, err)
	}
}

func TestControlLimit(t *testing.T) {
	t.Parallel()
	env := &Env{Limits: Limits{ControlBytes: 4}, Stderr: io.Discard}
	for _, tc := range []struct {
		in   string
		size uint64
	}{{"small", 4}, {"tiny", 5}} {
		if _, err := DecompressMem("upper", []byte(tc.in), tc.size, env); !errors.Is(err, ErrLimit) {
			t.Errorf("DecompressMem(%q, %d) = %v", tc.in, tc.size, err)
		}
	}
}

func TestControlChainUsesDecoderBudget(t *testing.T) {
	t.Parallel()
	env := &Env{TempDir: t.TempDir(), Stderr: io.Discard, Limits: Limits{DecoderBytes: 10, TempBytes: 3}}
	if _, err := DecompressMem("fakemem:6+fakemem:6", []byte("control"), 7, env); !errors.Is(err, ErrLimit) {
		t.Fatalf("control chain exceeded the budget without spilling: %v", err)
	}
}

func TestDebugRedactsCipherParameters(t *testing.T) {
	t.Parallel()
	const compressor = "lzma:64mb+aes-256/ctr:ksecret:i0011+4x4:serpent:ksecret"
	const want = "lzma:64mb+aes-256/ctr:k[redacted]:i0011+4x4:serpent:k[redacted]"
	if got := PublicCompressor(compressor); got != want {
		t.Fatalf("PublicCompressor = %q, want %q", got, want)
	}
}
