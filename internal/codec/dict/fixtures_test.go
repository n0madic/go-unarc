package dict_test

import (
	"testing"

	_ "github.com/n0madic/go-unarc/internal/codec/all"
	"github.com/n0madic/go-unarc/internal/fixturetest"
)

func TestFixtures(t *testing.T) { fixturetest.Run(t, "dict", "dict_lzp_ppmd") }
