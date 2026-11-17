package cli_test

import (
	"testing"

	_ "github.com/n0madic/go-unarc/internal/codec/all"
	"github.com/n0madic/go-unarc/internal/fixturetest"
)

// TestGolden replays every recorded run of the C unarc with all codecs linked.
func TestGolden(t *testing.T) { fixturetest.Run(t) }
