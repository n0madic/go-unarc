package storing_test

import (
	"testing"

	_ "github.com/n0madic/go-unarc/internal/codec/all"
	"github.com/n0madic/go-unarc/internal/fixturetest"
)

func TestFixtures(t *testing.T) {
	fixturetest.Run(t, "storing*", "empty", "bad_crc", "short_data", "unsupported*")
}
