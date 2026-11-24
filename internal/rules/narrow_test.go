package rules_test

// narrowHitCases pin where the rules widened for a dodge stop: each shape
// here sat next to one a rule now catches and must stay quiet, or is the
// shape itself.
func narrowHitCases() []hitCase {
	return []hitCase{
		{
			name: "go timeout arm of a select is not a sleep", path: "a_test.go", rule: "go/timing-hack",
			src: "package p\n\nfunc TestA(t *testing.T) {\n\tselect {\n\tcase <-done:\n" +
				"\tcase <-time.After(time.Second):\n\t\tt.Fatal(\"timeout\")\n\t}\n}\n",
		},
		{
			name: "go value received from time.After is not a bare sleep", path: "a_test.go", rule: "go/timing-hack",
			src: "package p\n\nfunc TestA(t *testing.T) {\n\tnow := <-time.After(time.Second)\n\t_ = now\n}\n",
		},
		{
			name: "go blank assignment of err is a dropped error", path: "a.go", rule: "go/silent-error", want: 1,
			src: "package p\n\nfunc f() {\n\terr := g()\n\t_ = err\n}\n", match: "err",
		},
		{
			name: "go blank assignment of any other name stays quiet", path: "a.go", rule: "go/silent-error",
			src: "package p\n\nfunc f() {\n\tn := g()\n\t_ = n\n}\n",
		},
		{
			name: "go path bound twice is not read through", path: "a_test.go", rule: "go/test-reads-source",
			src: "package p\n\nfunc TestA(t *testing.T) {\n\tname := \"../README.md\"\n\tname = t.TempDir()\n" +
				"\t_, _ = os.ReadFile(name)\n}\n",
		},
		{
			name: "go path bound to a temp directory stays quiet", path: "a_test.go", rule: "go/test-reads-source",
			src: "package p\n\nfunc TestA(t *testing.T) {\n\tname := filepath.Join(t.TempDir(), \"out.json\")\n" +
				"\t_, _ = os.ReadFile(name)\n}\n",
		},
		{
			name: "python ast.parse of a name bound to a literal stays quiet", path: "test_a.py",
			rule: "py/test-reads-source",
			src:  "import ast\n\n\ndef test_a():\n    source = \"x = 1\"\n    assert ast.parse(source)\n",
		},
		{
			name: "python flag named called is no mock record", path: "test_a.py", rule: "py/mock-only-assert",
			src: "def test_a(app):\n    class NS:\n        called = False\n\n    run(NS)\n    assert NS.called\n",
		},
		{
			name: "python or with a non-empty default stays quiet", path: "a.py", rule: "py/or-default",
			src: "def f(rows=None):\n    rows = rows if rows else [0]\n    return rows or list(DEFAULTS)\n",
		},
		{
			name: "python handler that only comments beside a call stays quiet", path: "a.py", rule: "py/silent-except",
			src: "def f():\n    try:\n        g()\n    except OSError:\n        log()  # recorded\n",
		},
	}
}

// narrowScriptCases are the Python and TypeScript half of narrowHitCases.
func narrowScriptCases() []hitCase {
	return []hitCase{
		{
			name: "python handler with a reason beside pass stays quiet", path: "a.py", rule: "py/silent-except",
			src: "def f():\n    try:\n        g()\n    except OSError:\n        pass  # the file is optional\n",
		},
		{
			name: "typescript catch block that says why stays quiet", path: "a.ts", rule: "ts/silent-catch",
			src: "try {\n  run()\n} catch {\n  // storage is blocked: read the cookie\n}\n",
		},
		{
			name: "typescript index signature of unknown stays quiet", path: "a.ts", rule: "ts/record-unknown",
			src: "export interface Extra { name: string; [key: string]: unknown }\n",
		},
		{
			name: "typescript promise on a timer stays quiet", path: "a.test.ts", rule: "ts/timing-hack",
			src: "it(\"x\", async () => {\n  await new Promise((resolve) => setTimeout(resolve, 10))\n})\n",
		},
		{
			name: "python path built from the test's location and bound to a name stays quiet", path: "test_a.py",
			rule: "py/test-reads-source",
			src: "from pathlib import Path\n\nDATA = Path(__file__).parent / \"data.json\"\n\n\n" +
				"def test_a():\n    assert DATA.read_bytes()\n",
		},
		{
			name: "python counter named call_count is no mock record", path: "test_a.py", rule: "py/mock-only-assert",
			src: "def test_a(stats):\n    run(stats)\n    assert stats.call_count == 1\n",
		},
		{
			name: "padded and block comments are counted as before", path: "a.go", rule: "x/comment-density",
			src: "package p\n\nfunc f() int {\n\t/* one\n\t   two\n\t   three */\n\tn := 1\n\n\n\n" +
				"\treturn n\n}\n",
		},
		{
			name: "typescript catch callback that records the error stays quiet", path: "a.ts", rule: "ts/silent-catch",
			src: "run().catch((err) => {\n  log(err)\n})\n",
		},
		{
			name: "typescript bare sleep helper stays quiet", path: "a.test.ts", rule: "ts/timing-hack",
			src: "it(\"x\", async () => {\n" +
				"  const q = { queryFn: () => sleep(10).then(() => 1) }\n" +
				"  expect(q).toBeDefined()\n" +
				"})\n",
		},
		{
			name: "typescript constant read off an object is not an echo", path: "a.test.ts", rule: "ts/constant-echo",
			src: "it(\"x\", () => {\n  expect(process.env.NODE_ENV).toBe(\"test\")\n})\n",
		},
		{
			name: "typescript test.describe block is not one test", path: "a.spec.ts", rule: "ts/mock-only-assert",
			src: "test.describe(\"group\", () => {\n  test(\"a\", () => {\n    expect(run()).toBe(1)\n  })\n" +
				"  test(\"b\", () => {\n    expect(run()).toBe(2)\n  })\n})\n",
		},
		{
			name: "typescript double angle-bracket cast goes through unknown", path: "a.ts", rule: "ts/as-unknown-as",
			src: "export const u = <User>(<unknown>raw)\n", want: 2,
		},
		{
			name: "typescript single angle-bracket cast stays quiet", path: "a.ts", rule: "ts/as-unknown-as",
			src: "export const u = <User>raw\n",
		},
	}
}
