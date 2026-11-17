package mm_test

import (
	"testing"

	_ "github.com/n0madic/go-unarc/internal/codec/all"
	"github.com/n0madic/go-unarc/internal/fixturetest"
)

func TestFixtures(t *testing.T) { fixturetest.Run(t, "mm", "mm_d1", "mm_grzip") }
