package rules_test

import (
	"fmt"
	"path"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/depth"
	"github.com/kyleking/slop-strata/internal/fixturetest"
	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/rules"
	"github.com/kyleking/slop-strata/internal/structure"
)

// weighedPrint fingerprints one file as the engine does: collected, then
// weighed with the mass of its functions.
func weighedPrint(t *testing.T, file, src string) *rules.Fingerprint {
	t.Helper()
	l := lang.ByPath(file)
	if l == nil {
		return rules.CollectText(file, []byte(src))
	}
	content := []byte(src)
	tree := lang.Parse(l, content)
	defer tree.Close()
	fp := rules.Collect(&rules.Ctx{
		Lang: l, Root: tree.RootNode(), Src: content, Path: file, Test: l.IsTestFile(file),
	})
	var weights []rules.Weight
	for _, fn := range structure.Analyze(l, tree, content).Funcs {
		weights = append(weights, rules.Weight{Start: fn.StartLine, End: fn.EndLine, Mass: structure.Mass(fn)})
	}
	fp.Weigh(weights)

	return fp
}

// depthOf measures every directory of a tree.
func depthOf(t *testing.T, tree fixturetest.Tree) map[string]*depth.Module {
	t.Helper()
	blobs := make([]rules.BlobPrint, 0, len(tree))
	paths := make([]string, 0, len(tree))
	for file, src := range tree {
		paths = append(paths, file)
		blobs = append(blobs, rules.BlobPrint{Path: file, Print: weighedPrint(t, file, src)})
	}

	return rules.ResolveImports(blobs, paths).Depth(path.Dir)
}

// storeDepth reads the numbers a cosmetic edit must not move off the
// directory named store: shown, hidden, their masses, and depth. The
// forwarder count is left out, since adding a wrapper is meant to raise it.
func storeDepth(t *testing.T, dir string) fixturetest.Metric {
	t.Helper()

	return func(tree fixturetest.Tree) string {
		m := depthOf(t, tree)[dir]
		if m == nil {
			return "no module"
		}
		ratio := "none"
		if m.Depth != nil {
			ratio = fmt.Sprintf("%.4f", *m.Depth)
		}

		return fmt.Sprintf("shown=%d hidden=%d shown_mass=%.2f hidden_mass=%.2f depth=%s",
			m.Shown, m.Hidden, m.ShownMass, m.HiddenMass, ratio)
	}
}

// one is a variant that replaces or adds a single file.
func one(name, file, body string) fixturetest.Variant {
	return fixturetest.Variant{Name: name, Files: fixturetest.Tree{file: body}}
}

// many is a variant that replaces or adds the files of an archive.
func many(name, archive string) fixturetest.Variant {
	return fixturetest.Variant{Name: name, Files: fixturetest.Files(archive)}
}

const pyParse = `def parse(path):
    out = []
    for line in open(path):
        if line.strip():
            out.append(_clean(line))
    return out
`

const pyClean = `def _clean(line):
    if line.startswith("#"):
        return ""
    return line.strip()
`

const pyEngine = pyParse + "\n\n" + pyClean

const pyRun = `

def run(path):
    if path:
        return load(path)
    return []
`

func pythonDepthCase() fixturetest.MetricCase {
	return fixturetest.MetricCase{
		Name: "python: one shown function over a hidden engine",
		Base: fixturetest.Files(`
-- app/__init__.py --
-- app/main.py --
from store.api import load
` + pyRun + `
-- store/__init__.py --
-- store/api.py --
from store.engine import parse


def load(path):
    if not path:
        return None
    return parse(path)
-- store/engine.py --
` + pyEngine),
		Same: []fixturetest.Variant{
			one("list the hidden names in __all__", "store/engine.py",
				"__all__ = [\"parse\", \"_clean\"]\n\n\n"+pyEngine),
			one("add pass-through wrappers", "store/engine.py", pyEngine+`

def parse_again(path):
    """Parse."""
    return parse(path)


def clean_too(line, *args, **kwargs):
    return _clean(line, *args, **kwargs)
`),
			many("move a function to a sibling file", `
-- store/engine.py --
from store.text import _clean


`+pyParse+`
-- store/text.py --
`+pyClean),
			one("re-export from the package nobody consumes", "store/__init__.py",
				"from store.engine import _clean, parse\n\n__all__ = [\"_clean\", \"parse\"]\n"),
			one("re-export from another package nobody consumes", "app/__init__.py",
				"from store.engine import parse\n\n__all__ = [\"parse\"]\n"),
			one("rename the import alias", "app/main.py", `from store.api import load as fetch


def run(path):
    if path:
        return fetch(path)
    return []
`),
			one("reach the same name through its module", "app/main.py", `from store import api as store_api


def run(path):
    if path:
        return store_api.load(path)
    return []
`),
			one("a test starts using an internal name", "tests/test_engine.py", `from store.engine import _clean


def test_clean():
    assert _clean("#") == ""
`),
		},
		Moved: pythonRealChanges(),
	}
}

// pythonRealChanges are the edits that must move the Python case's depth.
func pythonRealChanges() []fixturetest.Variant {
	return []fixturetest.Variant{
		one("another directory starts using an internal name", "app/main.py", `from store.api import load
from store.engine import _clean


def run(path):
    if path:
        return load(_clean(path))
    return []
`),
		many("another directory uses an internal name through a re-export", `
-- store/__init__.py --
from store.engine import parse as read
-- app/main.py --
from store import read
from store.api import load


def run(path):
    if path:
        return load(path)
    return read(path)
`),
		one("another directory uses an internal name through its module", "app/main.py", `import store.engine
from store.api import load


def run(path):
    if path:
        return load(path)
    return store.engine.parse(path)
`),
		one("delete hidden implementation", "store/engine.py", "def parse(path):\n    return list(open(path))\n"),
	}
}

const tsParse = `export function parse(path: string): string[] {
  const out: string[] = []
  for (const line of path.split(',')) {
    if (line.trim()) {
      out.push(clean(line))
    }
  }
  return out
}
`

const tsClean = `function clean(line: string): string {
  if (line.startsWith('#')) {
    return ''
  }
  return line.trim()
}
`

const tsEngine = tsParse + "\n" + tsClean

const tsMain = `import { load } from '../store/api'

export function run(path: string): string[] {
  if (path) {
    return load(path)
  }
  return []
}
`

const tsImportParse = "import { parse } from '../store/engine'\n"

func typescriptDepthCase() fixturetest.MetricCase {
	const mainPath, enginePath = "src/app/main.ts", "src/store/engine.ts"

	return fixturetest.MetricCase{
		Name: "typescript: one shown function over a hidden engine",
		Base: fixturetest.Files(`
-- src/app/main.ts --
` + tsMain + `
-- src/store/api.ts --
import { parse } from './engine'

export function load(path: string): string[] {
  if (!path) {
    return []
  }
  return parse(path)
}
-- src/store/engine.ts --
` + tsEngine),
		Same: []fixturetest.Variant{
			one("export a name nobody outside uses", enginePath, tsEngine+"\nexport { clean, clean as tidy }\n"),
			one("add pass-through wrappers", enginePath, tsEngine+`
export function parseAgain(path: string): string[] {
  return parse(path)
}

export const cleanToo = (line: string): string => clean(line)
`),
			many("move a function to a sibling file", `
-- src/store/engine.ts --
import { clean } from './text'

`+tsParse+`
-- src/store/text.ts --
export `+tsClean),
			many("add barrels nobody consumes", `
-- src/store/index.ts --
export * from './engine'
export { load } from './api'
-- src/index.ts --
export * from './store/engine'
export { load as read } from './store/api'
`),
			one("rename the import alias", mainPath, `import { load as fetch } from '../store/api'

export function run(path: string): string[] {
  if (path) {
    return fetch(path)
  }
  return []
}
`),
			one("import the same name through a namespace", mainPath, `import * as api from '../store/api'

export function run(path: string): string[] {
  if (path) {
    return api.load(path)
  }
  return []
}
`),
			one("import an internal name and never use it", mainPath, tsImportParse+tsMain),
		},
		Moved: []fixturetest.Variant{
			one("another directory starts using an internal name", mainPath, tsImportParse+
				tsMain+"\nexport const again = (path: string): number => parse(path).length + 1\n"),
			many("another directory uses an internal name through a barrel", `
-- src/store/index.ts --
export * from './engine'
-- src/app/main.ts --
import { parse as read } from '../store'
`+tsMain+`
export const again = (path: string): number => read(path).length + 1
`),
			one("delete hidden implementation", enginePath,
				"export function parse(path: string): string[] {\n  return path.split(',')\n}\n"),
		},
	}
}

const goParse = `package store

func parse(path string) []string {
	var out []string
	for _, line := range []string{path} {
		if line != "" {
			out = append(out, clean(line))
		}
	}

	return out
}
`

const goClean = `
func clean(line string) string {
	if line == "#" {
		return ""
	}

	return line
}
`

// goExported is the engine with clean renamed Clean, and nothing else changed.
const goExported = `package store

func parse(path string) []string {
	var out []string
	for _, line := range []string{path} {
		if line != "" {
			out = append(out, Clean(line))
		}
	}

	return out
}

func Clean(line string) string {
	if line == "#" {
		return ""
	}

	return line
}
`

const goMain = `package main

import "example.com/app/store"

func main() {
	if len(store.Load("x")) > 0 {
		panic("x")
	}
}
`

func goDepthCase() fixturetest.MetricCase {
	const mainPath, enginePath = "cmd/app/main.go", "store/engine.go"

	return fixturetest.MetricCase{
		Name: "go: one shown function over a hidden engine",
		Base: fixturetest.Files(`
-- go.mod --
module example.com/app
-- cmd/app/main.go --
` + goMain + `
-- store/api.go --
package store

// Load loads.
func Load(path string) []string {
	if path == "" {
		return nil
	}

	return parse(path)
}
-- store/engine.go --
` + goParse + goClean),
		Same: []fixturetest.Variant{
			one("export a name nobody outside uses", enginePath, goExported),
			one("add pass-through wrappers", enginePath, goParse+goClean+`
func ParseAgain(path string) []string { return parse(path) }

func CleanToo(line string) string {
	// Clean it.
	return clean(line)
}
`),
			many("move a function to a sibling file", `
-- store/engine.go --
`+goParse+`
-- store/text.go --
package store
`+goClean),
			one("rename the import alias", mainPath, `package main

import st "example.com/app/store"

func main() {
	if len(st.Load("x")) > 0 {
		panic("x")
	}
}
`),
			one("another directory declares the same name", "cmd/app/clean.go", "package main\n"+goClean),
		},
		Moved: []fixturetest.Variant{
			many("another directory starts using an internal name", `
-- store/engine.go --
`+goExported+`
-- cmd/app/main.go --
package main

import "example.com/app/store"

func main() {
	if len(store.Load(store.Clean("x"))) > 0 {
		panic("x")
	}
}
`),
			one("delete hidden implementation", enginePath,
				"package store\n\nfunc parse(path string) []string { return []string{path} }\n"),
		},
	}
}

func TestModuleDepthHoldsThroughCosmeticEditsAndMovesOnRealOnes(t *testing.T) {
	t.Parallel()
	fixturetest.RunMetric(t, []fixturetest.MetricCase{pythonDepthCase(), goDepthCase()}, storeDepth(t, "store"))
	fixturetest.RunMetric(t, []fixturetest.MetricCase{typescriptDepthCase()}, storeDepth(t, "src/store"))
}

func TestModuleDepthOfTheBaseFixtures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tree fixturetest.Tree
		name string
		dir  string
	}{
		{name: "python", tree: pythonDepthCase().Base, dir: "store"},
		{name: "typescript", tree: typescriptDepthCase().Base, dir: "src/store"},
		{name: "go", tree: goDepthCase().Base, dir: "store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := depthOf(t, tc.tree)[tc.dir]
			if m == nil || m.Depth == nil {
				t.Fatalf("store = %+v, want a depth", m)
			}
			counts := [5]int{m.Shown, m.Hidden, m.Forwarders, m.WidestShown, len(m.ShownNames)}
			if counts != [5]int{1, 2, 0, 1, 1} || !m.ByMass || !strings.HasPrefix(path.Base(m.WidestFile), "api.") {
				t.Errorf("store = %+v, want load shown from the api file, parse and clean hidden, by mass", m)
			}
			if *m.Depth <= 0.5 || m.HiddenMass <= m.ShownMass {
				t.Errorf("store = %+v, want more mass hidden than shown", m)
			}
		})
	}
}

func TestModuleDepthLeavesAnEntryPointWithoutARatio(t *testing.T) {
	t.Parallel()
	modules := depthOf(t, pythonDepthCase().Base)
	app := modules["app"]
	if app == nil || app.Shown != 0 || app.Hidden != 1 || app.Depth != nil {
		t.Errorf("app = %+v, want one hidden name and no depth: nothing outside uses it", app)
	}
	if _, ok := modules["tests"]; ok {
		t.Errorf("a directory of tests was measured: %+v", modules["tests"])
	}
}

func TestModuleDepthShowsWhatAUsedForwarderCalls(t *testing.T) {
	t.Parallel()
	tree := fixturetest.Files(`
-- app/main.py --
from store.api import load, Store


def run(path):
    return load(path) or Store().get(path)
-- store/api.py --
def load(path):
    return _load(path)


def _load(path):
    if path:
        return [path]
    return []


class Store:
    def get(self, key):
        return self._get(key)

    def _get(self, key):
        if key:
            return key
        return None

    def _unused(self):
        if self:
            return 1
        return 2
`)
	m := depthOf(t, tree)["store"]
	if m == nil || m.Forwarders != 2 || m.Shown != 3 || m.Hidden != 1 {
		t.Fatalf("store = %+v, want 2 forwarders, 3 shown through them, 1 hidden", m)
	}
	if got := fmt.Sprint(m.ShownNames, m.NameMatched); got != "[Store _load] [Store._get]" {
		t.Errorf("attributed and name-matched = %s, want Store and _load attributed, Store._get by name", got)
	}
}

// printsOf fingerprints every file of a tree.
func printsOf(t *testing.T, tree fixturetest.Tree) map[string]*rules.Fingerprint {
	t.Helper()
	out := map[string]*rules.Fingerprint{}
	for file, src := range tree {
		out[file] = weighedPrint(t, file, src)
	}

	return out
}

func TestWidenedSurfacesNamesWhatAChangeExposed(t *testing.T) {
	t.Parallel()
	c := pythonDepthCase()
	base := printsOf(t, c.Base)
	wrapped := printsOf(t, c.Same[1].Apply(c.Base))
	if got := rules.WidenedSurfaces(base, wrapped, path.Dir); len(got) != 0 {
		t.Errorf("adding wrappers widened a surface: %+v", got)
	}
	got := rules.WidenedSurfaces(base, printsOf(t, c.Moved[0].Apply(c.Base)), path.Dir)
	if len(got) != 1 {
		t.Fatalf("widened = %+v, want store alone", got)
	}
	w := got[0]
	summary := fmt.Sprintf("%s %v %d %d %d", w.Dir, w.NewlyShown, w.NewlyShownCount, w.ShownBefore, w.ShownAfter)
	if summary != "store [_clean] 1 1 2" {
		t.Errorf("widened = %+v, want store newly showing _clean, shown 1 to 2", w)
	}
	if w.DepthBefore == nil || w.DepthAfter == nil || *w.DepthAfter >= *w.DepthBefore {
		t.Errorf("widened = %+v, want a lower depth", w)
	}
}

// attributedDepth reads the numbers that rest on attributed uses alone off
// one directory: the names, their mass, and the attributed depth.
func attributedDepth(t *testing.T, dir string) fixturetest.Metric {
	t.Helper()

	return func(tree fixturetest.Tree) string {
		m := depthOf(t, tree)[dir]
		if m == nil {
			return "no module"
		}
		ratio := "none"
		if m.DepthAttributed != nil {
			ratio = fmt.Sprintf("%.4f", *m.DepthAttributed)
		}

		return fmt.Sprintf("attributed=%v mass=%.2f depth=%s", m.ShownNames, m.ShownMass-m.ShownMassByName, ratio)
	}
}

// collisionCase is a store whose class another directory uses, with one
// method called from outside and one only from inside. Collide adds, in the
// other directory, an unrelated type with a method named like the inside one
// and a call of it. Reach makes the other directory use a second top-level
// name of the store.
type collisionCase struct {
	base    fixturetest.Tree
	name    string
	dir     string
	collide fixturetest.Variant
	reach   fixturetest.Variant
}

func collisionCases() []collisionCase {
	return []collisionCase{pythonCollision(), typescriptCollision(), goCollision()}
}

func pythonCollision() collisionCase {
	return collisionCase{
		name: "python", dir: "store",
		base: fixturetest.Files(`
-- app/main.py --
from store.api import Store


def run(key):
    return Store().get(key)
-- store/api.py --
class Store:
    def get(self, key):
        if key:
            return self.scan(key)
        return None

    def scan(self, key):
        for item in [key]:
            if item:
                return item
        return None


def open_store(path):
    if path:
        return Store()
    return None
`),
		collide: one("", "app/cache.py", `class Cache:
    def scan(self, key):
        return [key]


def warm(key):
    return Cache().scan(key)
`),
		reach: one("", "app/main.py", `from store.api import Store, open_store


def run(key):
    return open_store(key) or Store().get(key)
`),
	}
}

func typescriptCollision() collisionCase {
	return collisionCase{
		name: "typescript", dir: "src/store",
		base: fixturetest.Files(`
-- src/app/main.ts --
import { Store } from '../store/api'

export const run = (key: string) => new Store().get(key)
-- src/store/api.ts --
export class Store {
  get(key: string): string | null {
    if (key) {
      return this.scan(key)
    }
    return null
  }

  scan(key: string): string | null {
    for (const item of [key]) {
      if (item) {
        return item
      }
    }
    return null
  }
}

export function openStore(path: string): Store | null {
  if (path) {
    return new Store()
  }
  return null
}
`),
		collide: one("", "src/app/cache.ts", `class Cache {
  scan(key: string): string[] {
    return [key]
  }
}

export const warm = (key: string) => new Cache().scan(key)
`),
		reach: one("", "src/app/main.ts", `import { Store, openStore } from '../store/api'

export const run = (key: string) => openStore(key) ?? new Store().get(key)
`),
	}
}

func goCollision() collisionCase {
	return collisionCase{
		name: "go", dir: "store",
		base: fixturetest.Files(`
-- go.mod --
module example.com/app
-- cmd/app/main.go --
package main

import "example.com/app/store"

func main() {
	s := store.Store{}
	s.Get("x")
}
-- store/api.go --
package store

type Store struct{}

func (s Store) Get(key string) string {
	if key != "" {
		return s.Scan(key)
	}

	return ""
}

func (s Store) Scan(key string) string {
	for _, item := range []string{key} {
		if item != "" {
			return item
		}
	}

	return ""
}

func Open(path string) *Store {
	if path != "" {
		return &Store{}
	}

	return nil
}
`),
		collide: one("", "cmd/app/cache.go", `package main

type cache struct{}

func (cache) Scan(key string) []string { return []string{key} }

func warm(key string) []string { return cache{}.Scan(key) }
`),
		reach: one("", "cmd/app/main.go", `package main

import "example.com/app/store"

func main() {
	s := store.Open("x")
	if s == nil {
		s = &store.Store{}
	}
	s.Get("x")
}
`),
	}
}

func TestASameNamedMethodElsewhereMovesNoAttributedNumber(t *testing.T) {
	t.Parallel()
	for _, tc := range collisionCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.collide.Name, tc.reach.Name = "an unrelated type's same-named method is called", "a second name is used"
			fixturetest.CheckMetric(t, fixturetest.MetricCase{
				Name: tc.name, Base: tc.base,
				Same: []fixturetest.Variant{tc.collide}, Moved: []fixturetest.Variant{tc.reach},
			}, attributedDepth(t, tc.dir))

			base, collided := depthOf(t, tc.base)[tc.dir], depthOf(t, tc.collide.Apply(tc.base))[tc.dir]
			if base.ShownByName != 1 || collided.ShownByName != 2 || collided.Shown != base.Shown+1 {
				t.Errorf("by name %d then %d of %d then %d shown, want the collision to add one guess",
					base.ShownByName, collided.ShownByName, base.Shown, collided.Shown)
			}
			prints := printsOf(t, tc.base)
			if got := rules.WidenedSurfaces(prints, printsOf(t, tc.collide.Apply(tc.base)), path.Dir); len(got) != 0 {
				t.Errorf("a same-named method elsewhere widened a surface: %+v", got)
			}
			got := rules.WidenedSurfaces(prints, printsOf(t, tc.reach.Apply(tc.base)), path.Dir)
			if len(got) != 1 || len(got[0].NewlyShown) != 1 || strings.Contains(got[0].NewlyShown[0], ".") {
				t.Errorf("widened = %+v, want one row naming the one top-level name newly used", got)
			}
		})
	}
}
