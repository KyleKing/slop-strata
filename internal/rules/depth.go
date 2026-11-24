package rules

import (
	"strings"

	"github.com/kyleking/slop-strata/internal/depth"
	"github.com/kyleking/slop-strata/internal/lang"
)

// Depth measures every group of production files, such as a directory: how
// many of its declared names files of other groups use, and how many stay
// inside. Test files neither declare a counted name nor show one by using it.
func (im *Imports) Depth(group func(file string) string) map[string]*depth.Module {
	var files []depth.File
	for _, b := range im.blobs {
		l := lang.ByPath(b.Path)
		if l == nil || lang.IsTestPath(b.Path) {
			continue
		}
		f := depth.File{Path: b.Path, Members: b.Print.Members, Units: make([]depth.Unit, 0, len(b.Print.Units))}
		for i := range b.Print.Units {
			u := &b.Print.Units[i]
			f.Units = append(f.Units, depth.Unit{
				Name: u.Name, Forwards: u.Forwards, Mass: u.Mass,
				Sealed: l == lang.Go && !u.Public && strings.Contains(u.Name, "."),
			})
		}
		files = append(files, f)
	}

	return depth.Compute(files, im.Graph.Uses(), im.Graph.Edges(), group)
}

// WidenedSurfaces lists the groups, such as directories, whose surface the
// change from base to rev widened: names of the group that files of other
// groups use at rev and did not at base.
func WidenedSurfaces(base, rev map[string]*Fingerprint, group func(file string) string) []depth.Widening {
	return depth.Widened(depthOf(base, group), depthOf(rev, group))
}

func depthOf(prints map[string]*Fingerprint, group func(file string) string) map[string]*depth.Module {
	blobs := make([]BlobPrint, 0, len(prints))
	paths := make([]string, 0, len(prints))
	for p, fp := range prints {
		blobs = append(blobs, BlobPrint{Path: p, Print: fp})
		paths = append(paths, p)
	}

	return ResolveImports(blobs, paths).Depth(group)
}
