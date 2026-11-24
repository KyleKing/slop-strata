// Package depth measures how much of a module its callers see. A module is
// the group a caller hands in, a directory in this tool, and a name of it is
// shown when a file of another group uses it and hidden otherwise. What is
// shown is read from use alone: a visibility keyword changes nothing here,
// so marking a name public or private without changing who uses it cannot
// move a number.
package depth

import (
	"math"
	"slices"
	"strings"

	"github.com/kyleking/slop-strata/internal/depgraph"
)

// Unit is one declared name of a file: a function, type, class, constant, or
// variable at file scope, or a method written Type.method. The unit with no
// name carries mass that belongs to no declaration.
type Unit struct {
	Name string
	// Forwards is set on a function that only passes its own parameters on:
	// the name of the function it calls, ".name" for a method of its own
	// type, or "*" for a callee nothing here can place. A forwarder counts
	// as neither shown nor hidden, and a use of it from outside shows the
	// name it forwards to when the same module declares it.
	Forwards string
	Mass     float64
	// Sealed marks a method its language keeps other directories from
	// calling, such as an unexported Go method.
	Sealed bool
}

// File is one production file: what it declares, and the names it reads off
// a value, the m of x.m, each once. Test files are left out by the caller, so
// a name only tests use from outside stays hidden.
type File struct {
	Path    string
	Members []string
	Units   []Unit
}

// Module is the depth of one group of files. A shown name is attributed when
// a resolved import ties its use to the declaration, and matched by name when
// it is a method some other group reads a member of that name off: a guess,
// since nothing here knows a receiver's type.
type Module struct {
	// Depth is hidden over shown plus hidden, by mass when ByMass is set and
	// by count otherwise. It is nil when no other group uses a name of this
	// one, which is an entry point or a leaf and not a module with nothing
	// hidden. Names matched by name count as shown, so it is a lower bound.
	Depth *float64 `json:"depth"`
	// DepthAttributed is the same ratio with the names matched by name
	// counted as hidden: the upper bound. It is nil when no use is attributed.
	DepthAttributed *float64 `json:"depth_attributed"`
	// WidestFile is the file with the most shown names, and WidestShown is
	// how many it has.
	WidestFile string `json:"widest_file,omitempty"`
	// ShownNames lists the attributed shown names and NameMatched the ones
	// matched by name, both sorted and file-independent, for comparing two
	// trees.
	ShownNames  []string `json:"-"`
	NameMatched []string `json:"-"`
	Shown       int      `json:"shown"`
	// ShownByName is how many of Shown rest on a name match alone, and
	// ShownMassByName how much of ShownMass.
	ShownByName     int     `json:"shown_by_name"`
	Hidden          int     `json:"hidden"`
	Forwarders      int     `json:"forwarders"`
	WidestShown     int     `json:"widest_shown"`
	ShownMass       float64 `json:"shown_mass"`
	ShownMassByName float64 `json:"shown_mass_by_name"`
	HiddenMass      float64 `json:"hidden_mass"`
	ByMass          bool    `json:"depth_by_mass"`
	// Provisional marks a depth read off fewer than ProvisionalNames names.
	Provisional bool `json:"depth_provisional,omitempty"`
}

// Attributed is how many shown names a resolved import attributes.
func (m *Module) Attributed() int { return m.Shown - m.ShownByName }

// ProvisionalNames is the count of shown and hidden names under which a
// module's depth is marked provisional: one constant used from outside makes
// a two-name directory read as half shown.
const ProvisionalNames = 5

// decl identifies one declaration: a name in a file.
type decl struct {
	file string
	name string
}

// calc holds one Compute run.
type calc struct {
	group func(string) string
	files map[string]*File
	// shown holds the attributed names and byName the methods matched by
	// name alone.
	shown  map[decl]bool
	byName map[decl]bool
	// reach lists, per group, the files of other groups that import it.
	reach map[string][]string
}

// Compute answers the depth of every group that has a file. Uses are the
// names each file uses of each other, and edges are the imports between
// files. A use or an edge that touches a file not listed is ignored. A use
// from a file of another group shows the name, attributed. Nothing here knows
// the type of a receiver, so a method is shown by name alone, when any file
// of another group reads a member of that name off any value, provided some
// file of another group imports the method's group at all.
func Compute(
	files []File, uses []depgraph.Use, edges []depgraph.Edge, group func(file string) string,
) map[string]*Module {
	c := &calc{
		group: group, files: map[string]*File{}, shown: map[decl]bool{}, byName: map[decl]bool{},
		reach: map[string][]string{},
	}
	for i := range files {
		c.files[files[i].Path] = &files[i]
	}
	for _, u := range uses {
		if c.crosses(u.From, u.To) {
			c.shown[decl{u.To, u.Name}] = true
			c.reach[group(u.To)] = append(c.reach[group(u.To)], u.From)
		}
	}
	for _, e := range edges {
		if c.crosses(e.From, e.To) {
			c.reach[group(e.To)] = append(c.reach[group(e.To)], e.From)
		}
	}
	for g, from := range c.reach {
		slices.Sort(from)
		c.reach[g] = slices.Compact(from)
	}
	c.showMethods()
	c.followForwarders()

	out := map[string]*Module{}
	for _, path := range sortedPaths(c.files) {
		g := group(path)
		if out[g] == nil {
			out[g] = &Module{}
		}
		c.tally(out[g], c.files[path])
	}
	for _, m := range out {
		m.finish()
	}

	return out
}

func sortedPaths(files map[string]*File) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)

	return paths
}

// crosses reports whether both files are known and sit in different groups.
func (c *calc) crosses(from, to string) bool {
	return c.files[from] != nil && c.files[to] != nil && c.group(from) != c.group(to)
}

// baseName is a unit's own name, without the type that holds a method.
func baseName(name string) string {
	return name[strings.LastIndexByte(name, '.')+1:]
}

// showMethods marks, as matched by name, each method whose name a file of
// another group reads off some value, in every group that a file of another
// group imports.
func (c *calc) showMethods() {
	everywhere := map[string]int{} // member name -> files that read it
	within := map[decl]int{}       // group and member name -> files of the group that read it
	for path, f := range c.files {
		g := c.group(path)
		for _, m := range f.Members {
			everywhere[m]++
			within[decl{g, m}]++
		}
	}
	for path, f := range c.files {
		g := c.group(path)
		if len(c.reach[g]) == 0 {
			continue
		}
		for i := range f.Units {
			u := &f.Units[i]
			if u.Sealed || !strings.Contains(u.Name, ".") {
				continue
			}
			member := baseName(u.Name)
			if everywhere[member] > within[decl{g, member}] {
				c.byName[decl{path, u.Name}] = true
			}
		}
	}
}

// followForwarders shows the name a shown forwarder passes its call to, when
// the same group declares it, and repeats down a chain of forwarders. What
// an attributed forwarder reaches is attributed, and what one matched by
// name reaches is matched by name.
func (c *calc) followForwarders() {
	byName, forwards := c.forwardIndex()
	follow := func(set map[decl]bool) {
		queue := make([]decl, 0, len(set))
		for d := range set {
			queue = append(queue, d)
		}
		for len(queue) > 0 {
			d := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			target := forwardTarget(d.name, forwards[d])
			for _, next := range byName[decl{c.group(d.file), target}] {
				if target != "" && next != d && !set[next] {
					set[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
	follow(c.shown)
	follow(c.byName)
}

// forwardIndex answers the declarations of each name per group, keyed by
// group and name, and what each forwarder forwards to.
func (c *calc) forwardIndex() (map[decl][]decl, map[decl]string) {
	byName := map[decl][]decl{}
	forwards := map[decl]string{}
	for file, f := range c.files {
		for i := range f.Units {
			u := &f.Units[i]
			if u.Name == "" {
				continue
			}
			key := decl{c.group(file), u.Name}
			byName[key] = append(byName[key], decl{file, u.Name})
			if u.Forwards != "" {
				forwards[decl{file, u.Name}] = u.Forwards
			}
		}
	}

	return byName, forwards
}

// forwardTarget answers the full name a forwarder's callee has in its own
// group: the function's name, or Type.method for a method of the
// forwarder's type. It is empty for a callee that cannot be placed.
func forwardTarget(forwarder, forwards string) string {
	switch {
	case forwards == "" || forwards == "*":
		return ""
	case !strings.HasPrefix(forwards, "."):
		return forwards
	}
	owner, _, isMethod := strings.Cut(forwarder, ".")
	if !isMethod {
		return ""
	}

	return owner + forwards
}

// name is one file's declarations of one name, merged: overloads and a
// property's getter and setter are one name.
type name struct {
	mass      float64
	forwarder bool
}

// tally adds one file's names to its module.
func (c *calc) tally(m *Module, f *File) {
	names := map[string]*name{}
	var order []string
	for i := range f.Units {
		u := &f.Units[i]
		if u.Name == "" {
			m.HiddenMass += u.Mass

			continue
		}
		n := names[u.Name]
		if n == nil {
			n = &name{forwarder: true}
			names[u.Name] = n
			order = append(order, u.Name)
		}
		n.mass += u.Mass
		n.forwarder = n.forwarder && u.Forwards != ""
	}
	shownHere := 0
	for _, id := range order {
		n := names[id]
		switch {
		case n.forwarder:
			m.Forwarders++
		case c.shown[decl{f.Path, id}]:
			m.Shown++
			m.ShownMass += n.mass
			m.ShownNames = append(m.ShownNames, id)
			shownHere++
		case c.byName[decl{f.Path, id}]:
			m.Shown++
			m.ShownByName++
			m.ShownMass += n.mass
			m.ShownMassByName += n.mass
			m.NameMatched = append(m.NameMatched, id)
			shownHere++
		default:
			m.Hidden++
			m.HiddenMass += n.mass
		}
	}
	if shownHere > m.WidestShown {
		m.WidestShown, m.WidestFile = shownHere, f.Path
	}
}

// finish sets the ratios once every file is counted.
func (m *Module) finish() {
	slices.Sort(m.ShownNames)
	slices.Sort(m.NameMatched)
	m.ShownMass = math.Round(m.ShownMass*massScale) / massScale
	m.ShownMassByName = math.Round(m.ShownMassByName*massScale) / massScale
	m.HiddenMass = math.Round(m.HiddenMass*massScale) / massScale
	if m.Shown == 0 {
		return
	}
	m.ByMass = m.ShownMass+m.HiddenMass > 0
	m.Depth = m.ratio(m.Hidden, m.HiddenMass)
	if m.Attributed() > 0 {
		m.DepthAttributed = m.ratio(m.Hidden+m.ShownByName, m.HiddenMass+m.ShownMassByName)
	}
	m.Provisional = m.Shown+m.Hidden < ProvisionalNames
}

// ratio answers the hidden share, by mass when the module has any and by
// name count otherwise.
func (m *Module) ratio(hidden int, hiddenMass float64) *float64 {
	share := float64(hidden) / float64(m.Shown+m.Hidden)
	if m.ByMass {
		share = hiddenMass / (m.ShownMass + m.HiddenMass)
	}
	share = math.Round(share*depthScale) / depthScale

	return &share
}

// massScale and depthScale round what a module reports, so the order files
// are summed in never shows in the last digits.
const (
	massScale  = 100
	depthScale = 10000
)
