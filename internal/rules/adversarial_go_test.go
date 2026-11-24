package rules_test

import (
	"strings"

	"github.com/kyleking/slop-strata/internal/fixturetest"
)

// single builds a case over one file.
func single(rule, name, file, base string) advCase {
	return advCase{rules: []string{rule}, Case: fixturetest.Case{Name: name, Base: fixturetest.Tree{file: base}}}
}

func goAdversarial() []advCase {
	return []advCase{goSilentErrorCase(), goTypeAnyCase(), goTestReadsSourceCase(), goTimingHackCase()}
}

func goSilentErrorCase() advCase {
	const file = "store/save.go"
	c := single("go/silent-error", "go: a removal whose error is dropped", file, `package store

import "os"

func cleanup(path string) {
	_ = os.Remove(path)
}
`)
	c.Dodges = []fixturetest.Variant{
		one("explain it in a comment above", file, `package store

import "os"

// cleanup removes the file.
func cleanup(path string) {
	// best effort, the error does not matter

	_ = os.Remove(path)
}
`),
		one("add a nolint with no reason", file, `package store

import "os"

func cleanup(path string) {
	//nolint:errcheck
	_ = os.Remove(path)
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename the function", file, `package store

import "os"

func tidy(path string) {
	_ = os.Remove(path)
}
`),
		{Name: "move it to another file", Remove: []string{file}, Files: fixturetest.Tree{"store/b.go": `package store

import "os"

func cleanup(path string) {
	_ = os.Remove(path)
}
`}},
		one("bind the error, then drop the binding", file, `package store

import "os"

func cleanup(path string) {
	err := os.Remove(path)
	_ = err
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("drop the assignment and call it bare", file, `package store

import "os"

func cleanup(path string) {
	os.Remove(path)
}
`),
		one("pass the error to a function that ignores it", file, `package store

import "os"

func ignore(error) {}

func cleanup(path string) {
	ignore(os.Remove(path))
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("return the error", file, `package store

import "os"

func cleanup(path string) error {
	return os.Remove(path)
}
`)}

	return c
}

func goTypeAnyCase() advCase {
	const file = "codec/decode.go"
	c := single("go/type-any", "go: a parameter typed any", file, `package codec

func Decode(v any) error {
	return check(v)
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment and a second function", file, `package codec

func other() {}

// Decode takes anything.
func Decode(v any) error {
	return check(v)
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("spell it interface{}", file, `package codec

func Decode(v interface{}) error {
	return check(v)
}
`),
		one("hide it behind an alias", file, `package codec

type value = any

func Decode(v value) error {
	return check(v)
}
`),
		one("rename the parameter", file, `package codec

func Decode(input any) error {
	return check(input)
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("make it a type parameter constrained by any", file, `package codec

func Decode[T any](v T) error {
	return check(v)
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("name the type", file, `package codec

func Decode(v *Config) error {
	return check(v)
}
`)}

	return c
}

// goDocsTest wraps a test body in the docs package's test file, importing os
// and testing plus any others.
func goDocsTest(body string, imports ...string) string {
	list := make([]string, 0, len(imports))
	for _, name := range append([]string{"os"}, append(imports, "testing")...) {
		list = append(list, "\t\""+name+"\"\n")
	}

	return "package docs\n\nimport (\n" + strings.Join(list, "") + ")\n\n" + body
}

// goReadme is a test that reads one path and checks the result.
func goReadme(read string) string {
	return "func TestReadme(t *testing.T) {\n" + read +
		"\tif err != nil || len(data) == 0 {\n\t\tt.Fatal(err)\n\t}\n}\n"
}

func goTestReadsSourceCase() advCase {
	const file = "docs/readme_test.go"
	c := single("go/test-reads-source", "go: a test reads the README", file,
		goDocsTest(goReadme("\tdata, err := os.ReadFile(\"../README.md\")\n")))
	c.Dodges = []fixturetest.Variant{
		one("add a comment above the read", file,
			goDocsTest(goReadme("\t// the README must exist\n\tdata, err := os.ReadFile(\"../README.md\")\n"))),
	}
	c.Persists = []fixturetest.Variant{
		one("hoist the path into a variable", file,
			goDocsTest(goReadme("\tname := \"../README.md\"\n\tdata, err := os.ReadFile(name)\n"))),
		one("hoist the path into a package constant", file,
			goDocsTest("const readme = \"../README.md\"\n\n"+goReadme("\tdata, err := os.ReadFile(readme)\n"))),
		one("join the path from parts", file,
			goDocsTest(goReadme("\tdata, err := os.ReadFile(filepath.Join(\"..\", \"README.md\"))\n"),
				"path/filepath")),
		one("open the file instead of reading it", file, goDocsTest(`func TestReadme(t *testing.T) {
	f, err := os.Open("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
}
`)),
	}
	c.Gaps = []fixturetest.Variant{
		one("read it through a helper that takes the path", file, goDocsTest(
			`func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestReadme(t *testing.T) {
	if len(read(t, "../README.md")) == 0 {
		t.Fatal("empty")
	}
}
`)),
	}
	c.Fixes = []fixturetest.Variant{
		one("read a fixture the test owns", file,
			goDocsTest(goReadme("\tdata, err := os.ReadFile(\"testdata/sample.md\")\n"))),
	}

	return c
}

// goWorkerTest is the queue package's test file around one way of waiting.
func goWorkerTest(imports, wait string) string {
	return "package queue\n\nimport (\n\t\"testing\"\n" + imports + ")\n\n" +
		"func TestWorker(t *testing.T) {\n\tgo work()\n" + wait + "\tcheck(t)\n}\n"
}

func goTimingHackCase() advCase {
	const file = "queue/worker_test.go"
	const clock = "\t\"time\"\n"
	c := single("go/timing-hack", "go: a test sleeps to wait for a worker", file,
		goWorkerTest(clock, "\ttime.Sleep(50 * time.Millisecond)\n"))
	c.Dodges = []fixturetest.Variant{
		one("add a comment above the sleep", file,
			goWorkerTest(clock, "\t// give the worker time to start\n\ttime.Sleep(50 * time.Millisecond)\n")),
	}
	c.Persists = []fixturetest.Variant{
		one("alias the import", file,
			goWorkerTest("\tclock \"time\"\n", "\tclock.Sleep(50 * clock.Millisecond)\n")),
		one("wrap the sleep in a helper of the test file", file,
			goWorkerTest(clock, "\tsettle()\n")+"\nfunc settle() { time.Sleep(50 * time.Millisecond) }\n"),
		one("receive from time.After instead", file,
			goWorkerTest(clock, "\t<-time.After(50 * time.Millisecond)\n")),
	}
	c.Gaps = []fixturetest.Variant{
		{Name: "move the sleep into a helper outside the test files", Files: fixturetest.Tree{
			file: "package queue\n\nimport \"testing\"\n\n" +
				"func TestWorker(t *testing.T) {\n\tgo work()\n\tsettle()\n\tcheck(t)\n}\n",
			"queue/settle.go": "package queue\n" +
				"\n" +
				"import \"time\"\n" +
				"\n" +
				"func settle() { time.Sleep(50 * time.Millisecond) }\n",
		}},
	}
	c.Fixes = []fixturetest.Variant{one("wait on a channel the worker closes", file, `package queue

import "testing"

func TestWorker(t *testing.T) {
	done := make(chan struct{})
	go func() { work(); close(done) }()
	<-done
	check(t)
}
`)}

	return c
}
