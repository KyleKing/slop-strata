// Package srcdigest hashes a package's own Go sources, so a value computed
// from them, such as a cache key, changes whenever the code producing it does.
package srcdigest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"strings"
)

// Of hashes the non-test Go files at the root of fsys in name order, framing
// each with its name so a rename changes the digest. It panics on a read
// failure: the callers hand it an embedded filesystem, which cannot fail.
func Of(fsys fs.FS) string {
	names, err := fs.Glob(fsys, "*.go")
	if err != nil {
		panic(fmt.Sprintf("srcdigest: listing sources: %v", err))
	}
	h := sha256.New()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			panic(fmt.Sprintf("srcdigest: reading %s: %v", name, err))
		}
		if _, err := fmt.Fprintf(h, "%s\x00%d\x00", name, len(body)); err != nil {
			panic(fmt.Sprintf("srcdigest: hashing %s: %v", name, err))
		}
		h.Write(body)
	}

	return hex.EncodeToString(h.Sum(nil))
}
