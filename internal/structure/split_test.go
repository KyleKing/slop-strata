package structure_test

import (
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/lang"
	"github.com/kyleking/slop-strata/internal/structure"
)

// Two loops over one counter: the first nests four deep and scores 10, the
// second three deep and scores 6, which makes 16. The branch inside the first
// loop scores 9 where it sits and 6 on its own, and leaves 7 behind.
const (
	goTwoLoops = `package p

func tally(a, b []int, lim int) int {
	n := 0
	for _, x := range a {
		if x > lim {
			for _, y := range b {
				if y == x {
					n++
				}
			}
		}
	}
	for _, y := range b {
		if y > lim {
			if y%2 == 0 {
				n--
			}
		}
	}

	return n
}
`
	pyTwoLoops = `def tally(a, b, lim):
    n = 0
    for x in a:
        if x > lim:
            for y in b:
                if y == x:
                    n += 1
    for y in b:
        if y > lim:
            if y % 2 == 0:
                n -= 1
    return n
`
	tsTwoLoops = `export function tally(a: number[], b: number[], lim: number): number {
  let n = 0
  for (const x of a) {
    if (x > lim) {
      for (const y of b) {
        if (y === x) {
          n++
        }
      }
    }
  }
  for (const y of b) {
    if (y > lim) {
      if (y % 2 === 0) {
        n--
      }
    }
  }
  return n
}
`
)

// splitOf analyzes a snippet and answers the split of its first function.
func splitOf(t *testing.T, path, src string) *structure.Split {
	t.Helper()

	return splitOfFunc(t, path, src, 0)
}

// splitOfFunc answers the split of one function of a snippet, counted in the
// order the functions start.
func splitOfFunc(t *testing.T, path, src string, fn int) *structure.Split {
	t.Helper()
	l := lang.ByPath(path)
	tree := lang.Parse(l, []byte(src))
	defer tree.Close()
	fm := structure.Analyze(l, tree, []byte(src))
	if len(fm.Funcs) <= fn {
		t.Fatalf("%d functions in %s, want more than %d", len(fm.Funcs), path, fn)
	}

	return fm.Funcs[fn].Split
}

func TestSplitNamesTheBlockThatTakesMostOffAFunction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, src string
		start, end      int
	}{
		{name: "go", path: "tally.go", src: goTwoLoops, start: 6, end: 12},
		{name: "python", path: "tally.py", src: pyTwoLoops, start: 4, end: 7},
		{name: "typescript", path: "tally.ts", src: tsTwoLoops, start: 4, end: 10},
		{name: "tsx", path: "tally.tsx", src: tsTwoLoops, start: 4, end: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitOf(t, tt.path, tt.src)
			// x, lim, b, and n are given a value before the branch and read in
			// it, and n is read again after it.
			want := structure.Split{
				StartLine: tt.start, EndLine: tt.end, Statements: 1, Drop: 9, Cognitive: 6, CC: 4,
				Remainder: 7, RemainderCC: 5, Inputs: 4, Outputs: 1,
			}
			if got == nil || *got != want {
				t.Errorf("split = %+v, want %+v", got, want)
			}
		})
	}
}

// goErrLoops is goTwoLoops with an error for its last result, so that a
// return in the first branch can hand one back.
var goErrLoops = strings.NewReplacer(
	"lim int) int {", "lim int) (int, error) {", "return n\n}", "return n, nil\n}",
).Replace(goTwoLoops)

func TestSplitCountsTheStatementsThatLeaveABlock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, src, body string
		start, exits    int // the line the split starts on, or 0 for none
	}{
		{name: "a break that ends a loop of the block does not", src: goTwoLoops, body: "break", start: 6},
		{name: "a continue of a loop of the block does not", src: goTwoLoops, body: "continue", start: 6},
		{
			name: "a return of an error leaves it, and the caller hands the error on", src: goErrLoops,
			body: `return 0, errors.New("twice")`, start: 6, exits: 1,
		},
		{
			name: "a return of a value from a block that can also go on needs a flag, for the end of the function",
			src:  goTwoLoops, body: "return n", start: 14, exits: 1,
		},
		{
			name: "so does a return of no error", src: goErrLoops, body: "return n, nil", start: 14, exits: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitOf(t, "tally.go", strings.Replace(tt.src, "n++", tt.body, 1))
			if got == nil || got.StartLine != tt.start || got.Exits != tt.exits {
				t.Errorf("split = %+v, want one that starts on line %d with %d exits", got, tt.start, tt.exits)
			}
		})
	}
}

// Every block of spread that would take enough off it reads six or more of
// the variables around it.
const pySpread = `def spread(a, b, c, d, e, g, h):
    n = 0
    for x in a:
        if x > b:
            for y in c:
                if y > d and x > e and y > h:
                    n += x
                if y > g:
                    n -= x
    return n
`

// The first branch of pair hands two variables on, and the second none.
const pyPair = `def pair(a, lim):
    lo = 0
    hi = 0
    for x in a:
        if x > lim:
            for y in x:
                if y and x:
                    hi = y
                    lo = x
        if x < lim:
            for y in x:
                if y:
                    print(hi, lo)
    return lo + hi
`

// One loop whose body is one condition: the body alone is the function again.
const goOneCondition = `package p

func scan(rows []Row) int {
	n := 0
	for _, r := range rows {
		if r.A && r.B || r.C && r.D || r.E && r.F || r.G && r.H || r.I && r.J || r.K && r.L || r.M && r.N {
			n++
		}
	}

	return n
}
`

func TestSplitPassesOverABlock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, src string
		start           int // the line the split starts on, or 0 for none
	}{
		{
			name: "in a function under the complexity floor", path: "tally.go",
			src: strings.Replace(goTwoLoops, "\t\t\tif y%2 == 0 {\n\t\t\t\tn--\n\t\t\t}\n", "\t\t\tn--\n", 1),
		},
		{
			name: "that holds a defer, for the end of the function", path: "tally.go",
			src:   strings.Replace(goTwoLoops, "\t\tif x > lim {\n", "\t\tif x > lim {\n\t\t\tdefer release(x)\n", 1),
			start: 15,
		},
		{
			name: "that holds a yield, for the end of the function", path: "tally.py",
			src: strings.Replace(pyTwoLoops,
				"        if x > lim:\n", "        if x > lim:\n            yield x\n", 1),
			start: 9,
		},
		{
			name: "that a break leaves, for the end of the function", path: "tally.go",
			src:   strings.Replace(goTwoLoops, "\t\t\t}\n\t\t}\n\t}\n", "\t\t\t}\n\t\t\tbreak\n\t\t}\n\t}\n", 1),
			start: 15,
		},
		{name: "that reads six variables", path: "spread.py", src: pySpread},
		{name: "that hands two variables on, for one that hands on none", path: "pair.py", src: pyPair, start: 10},
		{name: "that is the function again", path: "scan.go", src: goOneCondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitOf(t, tt.path, tt.src)
			switch {
			case tt.start == 0 && got != nil:
				t.Errorf("split = %+v, want none", got)
			case tt.start != 0 && (got == nil || got.StartLine != tt.start):
				t.Errorf("split = %+v, want one that starts on line %d", got, tt.start)
			}
		})
	}
}

func TestSplitReadsAWriteThroughAFieldAsARead(t *testing.T) {
	t.Parallel()
	// The branch fills seen through an index and never names it on the left of
	// an assignment, so it hands nothing on and takes seen in.
	src := strings.NewReplacer("n++", "seen[y] = x", "n--", "seen[y] = 0", "n := 0", "seen := map[int]int{}",
		"return n", "return len(seen)").Replace(goTwoLoops)
	got := splitOf(t, "tally.go", src)
	if got == nil || got.Inputs != 4 || got.Outputs != 0 {
		t.Errorf("split = %+v, want 4 inputs (x, lim, b, seen) and no output", got)
	}
}

// Each of these is the two loops again with the names moved: the branch of
// the first loop is still the split, and what crosses its ends is what the
// scopes say.
func TestSplitCountsVariablesByTheScopeThatDeclaresThem(t *testing.T) {
	t.Parallel()
	goMethod := strings.NewReplacer("func tally(a, b []int, lim int) int {", "func (t *T) tally(a, b []int) int {",
		"> lim", "> t.lim").Replace(goTwoLoops)
	goClosure := strings.NewReplacer("\tn := 0\n", "\tn := 0\n\twalk := func() {\n",
		"\n\treturn n\n", "\t}\n\twalk()\n\n\treturn n\n").Replace(goTwoLoops)
	pyClosure := strings.NewReplacer("    n = 0", "    n = 0\n    def walk():\n        nonlocal n",
		"\n    for ", "\n        for ", "\n        ", "\n            ",
		"    return n\n", "    walk()\n    return n\n").Replace(pyTwoLoops)
	tests := []struct {
		name, path, src string
		fn              int // the function of the file that is read
		inputs, outputs int
	}{
		{name: "a method's receiver is not an input", path: "tally.go", src: goMethod, inputs: 3, outputs: 1},
		{
			name: "nor is self", path: "tally.py", inputs: 3, outputs: 1,
			src: strings.NewReplacer("(a, b, lim)", "(self, a, b)", "> lim", "> self.lim").Replace(pyTwoLoops),
		},
		{
			name: "nor is this", path: "tally.ts", inputs: 3, outputs: 1,
			src: strings.NewReplacer(", lim: number)", ")", "> lim", "> this.lim").Replace(tsTwoLoops),
		},
		{
			name: "a named result that a bare return reads is an output", path: "tally.go", inputs: 4, outputs: 1,
			src: strings.NewReplacer(
				"lim int) int {", "lim int) (n int) {", "\tn := 0\n", "", "return n", "return").Replace(goTwoLoops),
		},
		{
			name: "a closure reads and writes the variables of the function around it", path: "tally.go",
			src: goClosure, fn: 1, inputs: 4, outputs: 1,
		},
		{
			name: "an arrow function does too", path: "tally.ts", fn: 1, inputs: 4, outputs: 1,
			src: strings.NewReplacer("  let n = 0\n", "  let n = 0\n  const walk = () => {\n",
				"  return n\n", "  }\n  walk()\n  return n\n").Replace(tsTwoLoops),
		},
		{
			name: "and so does a nested function that says nonlocal", path: "tally.py", src: pyClosure,
			fn: 1, inputs: 4, outputs: 1,
		},
		{
			name: "a global is no variable of the function", path: "tally.py", inputs: 3, outputs: 0,
			src: strings.Replace(pyTwoLoops, "    n = 0\n", "    global n\n", 1),
		},
		{
			name: "a name declared again in the block is another variable", path: "tally.ts", inputs: 3, outputs: 0,
			src: strings.Replace(tsTwoLoops, "    if (x > lim) {\n", "    if (x > lim) {\n      let n = 0\n", 1),
		},
		{
			name: "a value the block makes and nothing later reads is not an output", path: "tally.go",
			inputs: 3, outputs: 0,
			src: strings.NewReplacer("n++", "n = y", "n--", "n = 0").Replace(goTwoLoops),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitOfFunc(t, tt.path, tt.src, tt.fn)
			if got == nil || got.Statements != 1 || got.Inputs != tt.inputs || got.Outputs != tt.outputs {
				t.Errorf("split = %+v, want the branch with %d inputs and %d outputs", got, tt.inputs, tt.outputs)
			}
		})
	}
}

// The loop of five, the branch in it, and the last line each read five of
// the variables around them: a or x, b, c, d, and n.
const pyFive = `def five(a, b, c, d, n):
    for x in a:
        if x > b:
            for y in c:
                if y > d and x > b:
                    n += x
    return n if a and b or c and d or a and n else 0
`

func TestSplitBoundsTheVariablesABlockReads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, src string
		inputs    int // of the split, or 0 for none
	}{
		{name: "five are one too many", src: pyFive},
		{
			// The branch reads x, b, c, and n once d is gone.
			name: "four are not", inputs: 4,
			src: strings.Replace(pyFive, "y > d and x > b", "y > 1 and x > b", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitOf(t, "five.py", tt.src)
			switch {
			case tt.inputs == 0 && got != nil:
				t.Errorf("split = %+v, want none", got)
			case tt.inputs != 0 && (got == nil || got.Inputs != tt.inputs):
				t.Errorf("split = %+v, want one with %d inputs", got, tt.inputs)
			}
		})
	}
}

// Three sections of one list: each tests a field of v for nil and writes what
// it holds. Every section scores 6 where it sits, and the function 18.
const goSections = `package p

func restore(v *View, w Writer) {
	if v.Fg != nil {
		for _, c := range v.Fg {
			if c.Set {
				w.Write(c.Code)
			}
		}
	}
	if v.Bg != nil {
		for _, c := range v.Bg {
			if c.Set {
				w.Write(c.Code)
			}
		}
	}
	if v.Ul != nil {
		for _, c := range v.Ul {
			if c.Set {
				w.Write(c.Code)
			}
		}
	}
}
`

// goSectionsAfterALoop is a loop of its own, which reads one variable more,
// and then two of the sections, so that the two are all of their list.
const goSectionsAfterALoop = `package p

func restore(v *View, w Writer, lim int) {
	for _, c := range v.Fg {
		if c.Set {
			if c.Wide > lim {
				w.Write(c.Code)
			}
		}
	}
	if v.Bg != nil {
		for _, c := range v.Bg {
			if c.Set {
				w.Write(c.Code)
			}
		}
	}
	if v.Ul != nil {
		for _, c := range v.Ul {
			if c.Set {
				w.Write(c.Code)
			}
		}
	}
}
`

// goDeepSection is two sections of one list. The arm of the first is a call
// and a loop, and scores 14 of the function's 25 where it sits: with or
// without the call, the loop is the most a cut can take off.
const goDeepSection = `package p

func restore(v *View, w Writer) {
	if v.Fg != nil {
		w.Begin()
		for _, c := range v.Fg {
			for _, d := range c.Parts {
				if d.Set {
					if d.Wide {
						w.Write(d.Code)
					}
				}
			}
		}
	}
	if v.Bg != nil {
		for _, c := range v.Bg {
			if c.Set {
				if c.Wide {
					w.Write(c.Code)
				}
			}
		}
	}
}
`

func TestSplitPassesOverSomeOfAListOfLikeSections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, src string
		start, end      int // the lines of the split, or 0 for none
	}{
		{name: "one section, or two of three", path: "restore.go", src: goSections},
		{
			name: "in Python", path: "restore.py",
			src: strings.Repeat("    if v.fg is not None:\n        for c in v.fg:\n            if c.set:\n"+
				"                w.write(c.code)\n", 3),
		},
		{
			name: "in TypeScript", path: "restore.ts",
			src: strings.Repeat("  if (v.fg !== null) {\n    for (const c of v.fg) {\n      if (c.set) {\n"+
				"        w.write(c.code)\n      }\n    }\n  }\n", 3),
		},
		{
			// A header is its shape and not its names: != and == are two shapes.
			name: "but a section among branches that test something else", path: "restore.go",
			src:   strings.NewReplacer("v.Bg != nil", "v.Bg == nil").Replace(goSections),
			start: 4, end: 10,
		},
		{
			name: "and a section among loops, which all share a header", path: "restore.go",
			src:   strings.ReplaceAll(goSections, "\tif v.", "\tfor v."),
			start: 4, end: 10,
		},
		{
			name: "and every section of the list together", path: "restore.go", src: goSectionsAfterALoop,
			start: 11, end: 24,
		},
		{
			// Every statement of a block goes before a part of it, so the arm
			// would be named with its call. It is all there is in a section.
			name: "the whole arm of a section is passed over for the loop in it", path: "restore.go",
			src: goDeepSection, start: 6, end: 14,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := tt.src
			switch {
			case strings.HasSuffix(tt.path, ".py"):
				src = "def restore(v, w):\n" + src
			case strings.HasSuffix(tt.path, ".ts"):
				src = "function restore(v: View, w: Writer) {\n" + src + "}\n"
			}
			got := splitOf(t, tt.path, src)
			switch {
			case tt.start == 0 && got != nil:
				t.Errorf("split = %+v, want none", got)
			case tt.start != 0 && (got == nil || got.StartLine != tt.start || got.EndLine != tt.end):
				t.Errorf("split = %+v, want lines %d to %d", got, tt.start, tt.end)
			}
		})
	}
}

// The second loop of gather fills a list that the line ahead of it makes from
// base. Cut after that line, the loop reads rows, seen, and lim. With it, the
// two read base, rows, and lim: as many, and the shorter cut would win. The
// break keeps the inside of the loop where it is, and the first loop with
// the line ahead of it takes one variable more across.
const pyGather = `def gather(rows, base, lim):
    total = 0
    for r in rows:
        if r.a > lim and r.a in base:
            if r.b:
                total += 1
    seen = list(base)
    for r in rows:
        if r.ok:
            for c in r.cells:
                if c:
                    seen.append(c)
            if len(seen) > lim:
                break
    return total
`

func TestSplitTakesADeclarationWithTheBlockThatAloneReadsIt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, src string
		start     int
	}{
		{name: "the line that makes the list goes with the loop that fills it", src: pyGather, start: 7},
		{
			name: "a list that is read again afterwards stays where it is", start: 8,
			src: strings.Replace(pyGather, "return total", "return total + len(seen)", 1),
		},
		{
			name: "and so does one that an earlier line names", start: 9,
			src: strings.NewReplacer("    total = 0\n", "    total = 0\n    seen = None\n").Replace(pyGather),
		},
		{
			// The branch ahead of the loop is no declaration to take along.
			name: "a branch ahead of the block is not taken with it", start: 9,
			src: strings.Replace(pyGather, "    seen = list(base)\n",
				"    if base:\n        seen = list(base)\n", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitOf(t, "gather.py", tt.src)
			if got == nil || got.StartLine != tt.start {
				t.Errorf("split = %+v, want one that starts on line %d", got, tt.start)
			}
		})
	}
}

// BenchmarkAnalyze measures one file of forty functions that each hold a
// block to move out, which is the most the split search adds to a parse.
func BenchmarkAnalyze(b *testing.B) {
	_, body, _ := strings.Cut(goTwoLoops, "\n\n")
	src := []byte("package p\n\n" + strings.Repeat(body+"\n", 40))
	tree := lang.Parse(lang.Go, src)
	defer tree.Close()
	b.ReportAllocs()
	for b.Loop() {
		if fm := structure.Analyze(lang.Go, tree, src); fm.Funcs[0].Split == nil {
			b.Fatal("no split in a function that has one")
		}
	}
}
