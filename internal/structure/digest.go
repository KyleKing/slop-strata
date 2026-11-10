package structure

import (
	"embed"

	"github.com/kyleking/slop-strata/internal/srcdigest"
)

//go:embed *.go
var sources embed.FS

// Digest identifies this package's source, for cache keys.
var Digest = srcdigest.Of(sources) //nolint:gochecknoglobals // computed once from embedded sources
