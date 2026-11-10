package engine

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/srcdigest"
	"github.com/kyleking/slop-strata/internal/structure"
)

//go:embed *.go
var sources embed.FS

// Digest identifies this package's source, for cache keys.
var Digest = srcdigest.Of(sources) //nolint:gochecknoglobals // computed once from embedded sources

// analysisKey names the code and rule set a payload was computed by. It hashes
// every package that shapes a payload, the tree-sitter grammar modules, and
// whether experimental rules ran, so a cached answer is only ever reused under
// the same code.
func analysisKey(experimental bool) string {
	h := sha256.New()
	for _, part := range []string{
		lang.Digest, structure.Digest, rules.Digest, Digest, grammarModules(), strconv.FormatBool(experimental),
	} {
		h.Write([]byte(part + "\x00"))
	}

	return hex.EncodeToString(h.Sum(nil))
}

// grammarModules lists the pinned tree-sitter modules from the build info, or
// nothing when the binary carries none.
func grammarModules() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, dep := range info.Deps {
		if strings.HasPrefix(dep.Path, "github.com/tree-sitter/") {
			fmt.Fprintf(&b, "%s@%s\n", dep.Path, dep.Version)
		}
	}

	return b.String()
}

// pathClass names the slice of paths whose findings a blob's content shares: a
// language and whether the path is a test. Test-only rules key off it.
func pathClass(path string) string {
	if l := lang.ByPath(path); l != nil {
		if l.IsTestFile(path) {
			return l.ID + ":test"
		}

		return l.ID + ":source"
	}

	return "text:" + lang.TextKind(path)
}
