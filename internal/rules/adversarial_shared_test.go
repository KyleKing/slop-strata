package rules_test

import (
	"fmt"
	"strings"

	"github.com/kyleking/slop-strata/internal/fixturetest"
)

func sharedAdversarial() []advCase {
	return []advCase{
		goCaseTableCase(), pyCaseTableCase(), tsCaseTableCase(),
		goRegistryCase(), pyRegistryCase(), tsRegistryCase(),
		goNarratingCase(), pyNarratingCase(), tsNarratingCase(),
		goDensityCase(), pyDensityCase(), tsDensityCase(),
		goIgnoreCase(), pyIgnoreCase(), tsIgnoreCase(),
	}
}

// rows renders one line per number in [from, to), each from a format that
// takes the number twice.
func rows(from, to int, format string) string {
	var b strings.Builder
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, format, i, i)
	}

	return b.String()
}

const (
	ruleCaseTable = "x/case-table"
	tableEntries  = 16
	tableHalf     = 8
)

// tableShape is how one language writes a table of records and the two
// shapes that hold the same records without being one map.
type tableShape struct {
	name, file    string
	head, foot    string // around the entries of the table
	row           string
	renamedHead   string
	splitHead     string // opens the first half
	splitMiddle   string // closes it and opens the second
	splitFoot     string
	otherHead     string
	otherRow      string
	otherFoot     string
	computed      string
	lead, comment string // what precedes every variant, and a comment line
}

func (s tableShape) build() advCase {
	c := single(ruleCaseTable, s.name, s.file, s.lead+s.head+rows(0, tableEntries, s.row)+s.foot)
	c.Dodges = []fixturetest.Variant{
		one("reorder the entries and add a comment", s.file,
			s.lead+s.comment+s.head+rows(tableHalf, tableEntries, s.row)+rows(0, tableHalf, s.row)+s.foot),
	}
	c.Persists = []fixturetest.Variant{
		one("rename the table", s.file, s.lead+s.renamedHead+rows(0, tableEntries, s.row)+s.foot),
	}
	c.Gaps = []fixturetest.Variant{
		one("split it into two tables", s.file,
			s.lead+s.splitHead+rows(0, tableHalf, s.row)+
				s.splitMiddle+rows(tableHalf, tableEntries, s.row)+s.splitFoot),
		one("hold the records in a list instead of a map", s.file,
			s.otherHead+rows(0, tableEntries, s.otherRow)+s.otherFoot),
	}
	c.Fixes = []fixturetest.Variant{one("compute the record from the kind", s.file, s.computed)}

	return c
}

func goCaseTableCase() advCase {
	return tableShape{
		name: "go: a map with one record per case", file: "shape/table.go",
		lead: "package shape\n\ntype spec struct{ sides int }\n\n", comment: "// specs lists every shape.\n",
		head: "var specs = map[string]spec{\n", row: "\t\"kind%d\": {sides: %d},\n", foot: "}\n",
		renamedHead: "var shapes = map[string]spec{\n",
		splitHead:   "var specsA = map[string]spec{\n", splitMiddle: "}\n\nvar specsB = map[string]spec{\n",
		splitFoot: "}\n",
		otherHead: "package shape\n\ntype spec struct {\n\tkind  string\n\tsides int\n}\n\nvar specs = []spec{\n",
		otherRow:  "\t{kind: \"kind%d\", sides: %d},\n", otherFoot: "}\n",
		computed: "package shape\n\ntype spec struct{ sides int }\n\n" +
			"func specOf(sides int) spec {\n\treturn spec{sides: sides}\n}\n",
	}.build()
}

func pyCaseTableCase() advCase {
	return tableShape{
		name: "python: a dict with one record per case", file: "shape/table.py", comment: "# every shape\n",
		head: "SPECS = {\n", row: "    \"kind%d\": (%d, \"flat\"),\n", foot: "}\n",
		renamedHead: "SHAPES = {\n",
		splitHead:   "_A = {\n", splitMiddle: "}\n_B = {\n", splitFoot: "}\nSPECS = {**_A, **_B}\n",
		otherHead: "SPECS = dict([\n", otherRow: "    (\"kind%d\", (%d, \"flat\")),\n", otherFoot: "])\n",
		computed: "def spec_of(sides):\n    return (sides, \"flat\")\n",
	}.build()
}

func tsCaseTableCase() advCase {
	return tableShape{
		name: "typescript: an object with one record per case", file: "src/table.ts", comment: "// every shape\n",
		head: "export const specs = {\n", row: "  kind%d: { sides: %d },\n", foot: "}\n",
		renamedHead: "export const shapes = {\n",
		splitHead:   "const a = {\n", splitMiddle: "}\nconst b = {\n",
		splitFoot: "}\nexport const specs = { ...a, ...b }\n",
		otherHead: "export const specs = new Map([\n", otherRow: "  [\"kind%d\", { sides: %d }],\n", otherFoot: "])\n",
		computed: "export function specOf(sides: number) {\n  return { sides }\n}\n",
	}.build()
}

const ruleRegistry = "x/single-entry-registry"

func goRegistryCase() advCase {
	const file = "codec/registry.go"
	c := single(ruleRegistry, "go: a registry with one entry", file, `package codec

var handlers = map[string]func([]byte) error{
	"json": decodeJSON,
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above", file, `package codec

// handlers maps a format to its decoder.
var handlers = map[string]func([]byte) error{
	"json": decodeJSON,
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it to another registry word", file, `package codec

var decoderPlugins = map[string]func([]byte) error{
	"json": decodeJSON,
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("rename it to a word the rule does not know", file, `package codec

var decoders = map[string]func([]byte) error{
	"json": decodeJSON,
}
`),
		one("fill it in an init function", file, `package codec

var handlers = map[string]func([]byte) error{}

func init() {
	handlers["json"] = decodeJSON
}
`),
		one("add a second entry for the same function", file, `package codec

var handlers = map[string]func([]byte) error{
	"json":  decodeJSON,
	"json5": decodeJSON,
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("call the one decoder", file, `package codec

func decode(data []byte) error {
	return decodeJSON(data)
}
`)}

	return c
}

func pyRegistryCase() advCase {
	const file = "codec/registry.py"
	c := single(ruleRegistry, "python: a registry with one entry", file, `HANDLERS = {
    "json": decode_json,
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above", file, `# a format to its decoder
HANDLERS = {
    "json": decode_json,
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it to another registry word", file, `DECODER_PLUGINS = {
    "json": decode_json,
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("rename it to a word the rule does not know", file, `DECODERS = {
    "json": decode_json,
}
`),
		one("fill it after creating it empty", file, `HANDLERS = {}
HANDLERS["json"] = decode_json
`),
	}
	c.Fixes = []fixturetest.Variant{one("call the one decoder", file, `def decode(data):
    return decode_json(data)
`)}

	return c
}

func tsRegistryCase() advCase {
	const file = "src/registry.ts"
	c := single(ruleRegistry, "typescript: a registry with one entry", file, `export const handlers = {
  json: decodeJson,
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above", file, `// a format to its decoder
export const handlers = {
  json: decodeJson,
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename it to another registry word", file, `export const decoderPlugins = {
  json: decodeJson,
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("rename it to a word the rule does not know", file, `export const decoders = {
  json: decodeJson,
}
`),
		one("build it as a Map", file, `export const handlers = new Map([["json", decodeJson]])
`),
	}
	c.Fixes = []fixturetest.Variant{one("call the one decoder", file, `export function decode(data: Uint8Array) {
  return decodeJson(data)
}
`)}

	return c
}

const ruleNarrating = "x/narrating-comment"

func goNarratingCase() advCase {
	const file = "store/cache.go"
	c := single(ruleNarrating, "go: a comment tells the diff's story", file, `package store

// get previously read from disk on every call.
func get(key string) string {
	return cache[key]
}
`)
	c.Dodges = []fixturetest.Variant{
		one("move the function down the file", file, `package store

var cache = map[string]string{}

// get previously read from disk on every call.
func get(key string) string {
	return cache[key]
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("reword it with another narrating phrase", file, `package store

// get no longer reads from disk on every call.
func get(key string) string {
	return cache[key]
}
`),
		one("write it as a block comment", file, `package store

/* get previously read from disk on every call. */
func get(key string) string {
	return cache[key]
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("reword it with a phrase the rule does not know", file, `package store

// get formerly read from disk on every call.
func get(key string) string {
	return cache[key]
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("state what holds", file, `package store

// get answers from memory and never touches the disk.
func get(key string) string {
	return cache[key]
}
`)}

	return c
}

func pyNarratingCase() advCase {
	const file = "store/cache.py"
	c := single(ruleNarrating, "python: a comment tells the diff's story", file, `def get(key):
    # previously this read from disk on every call
    return CACHE[key]
`)
	c.Dodges = []fixturetest.Variant{
		one("add code above the comment", file, `def get(key):
    key = key.strip()
    # previously this read from disk on every call
    return CACHE[key]
`),
	}
	c.Persists = []fixturetest.Variant{
		one("move it to the end of the line", file, `def get(key):
    return CACHE[key]  # previously this read from disk on every call
`),
		one("cite the ticket instead", file, `def get(key):
    # see PROJ-4821
    return CACHE[key]
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("move it into the docstring", file, `def get(key):
    """Answer from memory. Previously this read from disk on every call."""
    return CACHE[key]
`),
		one("reword it with a phrase the rule does not know", file, `def get(key):
    # earlier versions read from disk on every call
    return CACHE[key]
`),
	}
	c.Fixes = []fixturetest.Variant{one("state what holds", file, `def get(key):
    # answers from memory and never touches the disk
    return CACHE[key]
`)}

	return c
}

func tsNarratingCase() advCase {
	const file = "src/cache.ts"
	c := single(ruleNarrating, "typescript: a comment tells the diff's story", file,
		`export function get(key: string): string {
  // this used to read from disk on every call
  return cache[key]
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add code above the comment", file, `export function get(key: string): string {
  const k = key.trim()
  // this used to read from disk on every call
  return cache[k]
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("move it into the JSDoc block", file, `/**
 * Answers from memory. This used to read from disk on every call.
 */
export function get(key: string): string {
  return cache[key]
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("reword it with a phrase the rule does not know", file, `export function get(key: string): string {
  // we switched this away from reading the disk on every call
  return cache[key]
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("state what holds", file, `export function get(key: string): string {
  // answers from memory and never touches the disk
  return cache[key]
}
`)}

	return c
}

const ruleDensity = "x/comment-density"

func goDensityCase() advCase {
	const file = "bill/total.go"
	c := single(ruleDensity, "go: comments restate each line", file, `package bill

func total(items []item) int {
	// start at zero
	sum := 0
	// loop over the items
	for _, it := range items {
		// add the price
		sum += it.price
	}
	// return the sum
	return sum
}
`)
	c.Persists = []fixturetest.Variant{
		one("move the comments to the ends of the lines", file, `package bill

func total(items []item) int {
	sum := 0                   // start at zero
	for _, it := range items { // loop over the items
		sum += it.price // add the price
	}
	return sum // return the sum
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("pad the body with blank lines", file, `package bill

func total(items []item) int {
	// start at zero

	sum := 0

	// loop over the items

	for _, it := range items {

		// add the price

		sum += it.price

	}

	// return the sum

	return sum
}
`),
		one("fold the comments into one block", file, `package bill

func total(items []item) int {
	/* start at zero
	   loop over the items
	   add the price
	   return the sum */
	sum := 0
	for _, it := range items {
		sum += it.price
	}
	return sum
}
`),
		one("move the comments above the function", file, `package bill

// total starts at zero,
// loops over the items,
// and adds the price.
func total(items []item) int {
	sum := 0
	for _, it := range items {
		sum += it.price
	}
	return sum
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("delete the comments that restate the code", file, `package bill

func total(items []item) int {
	sum := 0
	for _, it := range items {
		sum += it.price
	}
	return sum
}
`)}

	return c
}

func pyDensityCase() advCase {
	const file = "bill/total.py"
	c := single(ruleDensity, "python: comments restate each line", file, `def total(items):
    # start at zero
    result = 0
    # loop over the items
    for item in items:
        # add the price
        result += item.price
    return result
`)
	c.Persists = []fixturetest.Variant{
		one("move the comments to the ends of the lines", file, `def total(items):
    result = 0  # start at zero
    for item in items:  # loop over the items
        result += item.price  # add the price
    return result
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("pad the body with blank lines", file, `def total(items):
    # start at zero

    result = 0

    # loop over the items

    for item in items:

        # add the price

        result += item.price

    return result
`),
		one("move the comments into the docstring", file, `def total(items):
    """Start at zero.

    Loop over the items.
    Add the price.
    """
    result = 0
    for item in items:
        result += item.price
    return result
`),
	}
	c.Fixes = []fixturetest.Variant{one("delete the comments that restate the code", file, `def total(items):
    result = 0
    for item in items:
        result += item.price
    return result
`)}

	return c
}

func tsDensityCase() advCase {
	const file = "src/total.ts"
	c := single(ruleDensity, "typescript: comments restate each line", file,
		`export function total(items: Item[]): number {
  // start at zero
  let sum = 0
  // loop over the items
  for (const item of items) {
    // add the price
    sum += item.price
  }
  // return the sum
  return sum
}
`)
	c.Gaps = []fixturetest.Variant{
		one("pad the body with blank lines", file, `export function total(items: Item[]): number {
  // start at zero

  let sum = 0

  // loop over the items

  for (const item of items) {

    // add the price

    sum += item.price

  }

  // return the sum

  return sum
}
`),
		one("fold the comments into one block", file, `export function total(items: Item[]): number {
  /*
   * start at zero
   * loop over the items
   * add the price
   * return the sum
   */
  let sum = 0
  for (const item of items) {
    sum += item.price
  }
  return sum
}
`),
		one("move the comments above the function", file, `// start at zero
// loop over the items
// add the price
export function total(items: Item[]): number {
  let sum = 0
  for (const item of items) {
    sum += item.price
  }
  return sum
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("delete the comments that restate the code", file,
		`export function total(items: Item[]): number {
  let sum = 0
  for (const item of items) {
    sum += item.price
  }
  return sum
}
`)}

	return c
}

const ruleIgnore = "x/ignore-without-reason"

func goIgnoreCase() advCase {
	const file = "store/save.go"
	c := single(ruleIgnore, "go: an ignore with no reason", file, `package store

func cleanup(path string) {
	// slop-strata:ignore go/silent-error
	_ = remove(path)
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above it", file, `package store

func cleanup(path string) {
	// best effort
	// slop-strata:ignore go/silent-error
	_ = remove(path)
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("move it to the end of the line", file, `package store

func cleanup(path string) {
	_ = remove(path) // slop-strata:ignore go/silent-error
}
`),
		one("write it as a block comment", file, `package store

func cleanup(path string) {
	/* slop-strata:ignore go/silent-error */
	_ = remove(path)
}
`),
		one("drop the rule id too", file, `package store

func cleanup(path string) {
	// slop-strata:ignore
	_ = remove(path)
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("give a reason that says nothing", file, `package store

func cleanup(path string) {
	// slop-strata:ignore go/silent-error x
	_ = remove(path)
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("say why", file, `package store

func cleanup(path string) {
	// slop-strata:ignore go/silent-error the file may already be gone, and that is the goal
	_ = remove(path)
}
`)}

	return c
}

func pyIgnoreCase() advCase {
	const file = "app/config.py"
	c := single(ruleIgnore, "python: an ignore with no reason", file, `def load(path):
    try:
        return open(path).read()
    # slop-strata:ignore py/silent-except
    except OSError:
        pass
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above it", file, `def load(path):
    try:
        return open(path).read()
    # optional file
    # slop-strata:ignore py/silent-except
    except OSError:
        pass
`),
	}
	c.Persists = []fixturetest.Variant{
		one("move it to the end of the line", file, `def load(path):
    try:
        return open(path).read()
    except OSError:  # slop-strata:ignore py/silent-except
        pass
`),
		one("close it up against the hash", file, `def load(path):
    try:
        return open(path).read()
    #slop-strata:ignore py/silent-except
    except OSError:
        pass
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("give a reason that says nothing", file, `def load(path):
    try:
        return open(path).read()
    # slop-strata:ignore py/silent-except ok
    except OSError:
        pass
`),
	}
	c.Fixes = []fixturetest.Variant{one("say why", file, `def load(path):
    try:
        return open(path).read()
    # slop-strata:ignore py/silent-except the file is optional and its absence is the default
    except OSError:
        pass
`)}

	return c
}

func tsIgnoreCase() advCase {
	const file = "src/config.ts"
	c := single(ruleIgnore, "typescript: an ignore with no reason", file, `export function load(text: string) {
  try {
    return parse(text)
    // slop-strata:ignore ts/silent-catch
  } catch (err) {}
  return undefined
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above it", file, `export function load(text: string) {
  try {
    return parse(text)
    // a bad file is not fatal
    // slop-strata:ignore ts/silent-catch
  } catch (err) {}
  return undefined
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("move it to the end of the line", file, `export function load(text: string) {
  try {
    return parse(text)
  } catch (err) {} // slop-strata:ignore ts/silent-catch
  return undefined
}
`),
		one("write it as a block comment", file, `export function load(text: string) {
  try {
    return parse(text)
    /* slop-strata:ignore ts/silent-catch */
  } catch (err) {}
  return undefined
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("give a reason that says nothing", file, `export function load(text: string) {
  try {
    return parse(text)
    // slop-strata:ignore ts/silent-catch -
  } catch (err) {}
  return undefined
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("say why", file, `export function load(text: string) {
  try {
    return parse(text)
    // slop-strata:ignore ts/silent-catch a config that fails to parse falls back to the defaults
  } catch (err) {}
  return undefined
}
`)}

	return c
}
