// Package all registers every codec; import it for its side effects.
package all

import (
	_ "github.com/n0madic/go-unarc/internal/codec/bcj"
	_ "github.com/n0madic/go-unarc/internal/codec/crypt"
	_ "github.com/n0madic/go-unarc/internal/codec/delta"
	_ "github.com/n0madic/go-unarc/internal/codec/dict"
	_ "github.com/n0madic/go-unarc/internal/codec/dispack"
	_ "github.com/n0madic/go-unarc/internal/codec/external"
	_ "github.com/n0madic/go-unarc/internal/codec/fourxfour"
	_ "github.com/n0madic/go-unarc/internal/codec/grzip"
	_ "github.com/n0madic/go-unarc/internal/codec/lzma"
	_ "github.com/n0madic/go-unarc/internal/codec/lzp"
	_ "github.com/n0madic/go-unarc/internal/codec/mm"
	_ "github.com/n0madic/go-unarc/internal/codec/plugin"
	_ "github.com/n0madic/go-unarc/internal/codec/ppmd"
	_ "github.com/n0madic/go-unarc/internal/codec/rep"
	_ "github.com/n0madic/go-unarc/internal/codec/storing"
	_ "github.com/n0madic/go-unarc/internal/codec/tornado"
	_ "github.com/n0madic/go-unarc/internal/codec/tta"
)
