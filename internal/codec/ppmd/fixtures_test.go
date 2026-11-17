package ppmd_test

import (
	"testing"

	_ "github.com/n0madic/go-unarc/internal/codec/all"
	"github.com/n0madic/go-unarc/internal/fixturetest"
)

func TestFixtures(t *testing.T) {
	fixturetest.Run(t, "ppmd", "ppmd_o4", "ppmd_o16", "multi", "dict_lzp_ppmd", "lzp_ppmd")
}
