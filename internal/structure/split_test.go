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
	l := lang.ByPath(path)
	tree := lang.Parse(l, []byte(src))
	defer tree.Close()
	fm := structure.Analyze(l, tree, []byte(src))
	if len(fm.Funcs) == 0 {
		t.Fatalf("no function in %s", path)
	}

	return fm.Funcs[0].Split
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

func TestSplitCountsTheStatementsThatLeaveABlock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		exits      int
	}{
		{name: "a return leaves it", body: "return n", exits: 1},
		{name: "a break that ends a loop of the block does not", body: "break", exits: 0},
		{name: "a continue of a loop of the block does not", body: "continue", exits: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitOf(t, "tally.go", strings.Replace(goTwoLoops, "n++", tt.body, 1))
			if got == nil || got.StartLine != 6 || got.Exits != tt.exits {
				t.Errorf("split = %+v, want the branch on line 6 with %d exits", got, tt.exits)
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
                if y:
                    hi = y
                    lo = x
        if x < lim:
            if x:
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
			name: "that holds a defer, for the loop behind it", path: "tally.go",
			src:   strings.Replace(goTwoLoops, "\t\tif x > lim {\n", "\t\tif x > lim {\n\t\t\tdefer release(x)\n", 1),
			start: 8,
		},
		{
			name: "that holds a yield, for the loop behind it", path: "tally.py",
			src: strings.Replace(pyTwoLoops,
				"        if x > lim:\n", "        if x > lim:\n            yield x\n", 1),
			start: 6,
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
