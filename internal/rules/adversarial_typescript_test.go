package rules_test

import "github.com/kyleking/slop-strata/internal/fixturetest"

func typescriptAdversarial() []advCase {
	return []advCase{
		tsSilentCatchCase(), tsAsUnknownAsCase(), tsRecordUnknownCase(), tsTypeAnyCase(), tsModuleMockCase(),
		tsTestReadsSourceCase(), tsMockOnlyAssertCase(), tsConstantEchoCase(), tsTimingHackCase(),
	}
}

func tsSilentCatchCase() advCase {
	const file = "src/config.ts"
	c := single("ts/silent-catch", "typescript: a parse whose failure is swallowed", file,
		`export async function load(text: string): Promise<Config | undefined> {
  try {
    return await parse(text)
  } catch (err) {}
  return undefined
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above the try", file,
			`export async function load(text: string): Promise<Config | undefined> {
  // a bad file is not fatal
  try {
    return await parse(text)
  } catch (err) {}
  return undefined
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("drop the binding", file, `export async function load(text: string): Promise<Config | undefined> {
  try {
    return await parse(text)
  } catch {}
  return undefined
}
`),
		one("swallow it on the promise instead", file,
			`export async function load(text: string): Promise<Config | undefined> {
  return parse(text).catch(() => {})
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("explain it in a comment inside the block", file,
			`export async function load(text: string): Promise<Config | undefined> {
  try {
    return await parse(text)
  } catch (err) {
    // ignore
  }
  return undefined
}
`),
		one("void the error", file, `export async function load(text: string): Promise<Config | undefined> {
  try {
    return await parse(text)
  } catch (err) {
    void err
  }
  return undefined
}
`),
		one("return a default from the handler", file,
			`export async function load(text: string): Promise<Config | undefined> {
  try {
    return await parse(text)
  } catch (err) {
    return undefined
  }
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("throw an error that carries the cause", file,
		`export async function load(text: string): Promise<Config> {
  try {
    return await parse(text)
  } catch (err) {
    throw new ConfigError("unreadable config", { cause: err })
  }
}
`)}

	return c
}

func tsAsUnknownAsCase() advCase {
	const file = "src/user.ts"
	c := single("ts/as-unknown-as", "typescript: a cast through unknown", file,
		`export function toUser(raw: Row): User {
  return raw as unknown as User
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment and an eslint disable above", file, `export function toUser(raw: Row): User {
  // eslint-disable-next-line @typescript-eslint/no-unsafe-type-assertion
  return raw as unknown as User
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("cast through any", file, `export function toUser(raw: Row): User {
  return raw as any as User
}
`),
		one("split the cast over two statements", file, `export function toUser(raw: Row): User {
  const loose = raw as unknown
  return loose as User
}
`),
		one("use the angle-bracket spelling", file, `export function toUser(raw: Row): User {
  return <User>(<unknown>raw)
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("widen through an annotated variable", file, `export function toUser(raw: Row): User {
  const loose: unknown = raw
  return loose as User
}
`),
		one("cast inside a generic helper", file, `function cast<T>(value: unknown): T {
  return value as T
}

export function toUser(raw: Row): User {
  return cast<User>(raw)
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("build the value field by field", file,
		`export function toUser(raw: Row): User {
  return { id: raw.id, name: raw.name }
}
`)}

	return c
}

func tsRecordUnknownCase() advCase {
	const file = "src/event.ts"
	c := single("ts/record-unknown", "typescript: a payload typed as a record of unknown", file,
		`export function track(name: string, payload: Record<string, unknown>): void {
  send(name, payload)
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above", file, `// track sends an event.
export function track(name: string, payload: Record<string, unknown>): void {
  send(name, payload)
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("hide it behind an alias", file, `type Payload = Record<string, unknown>

export function track(name: string, payload: Payload): void {
  send(name, payload)
}
`),
		one("use any for the value", file,
			`export function track(name: string, payload: Record<string, any>): void {
  send(name, payload)
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("write it as an index signature", file,
			`export function track(name: string, payload: { [key: string]: unknown }): void {
  send(name, payload)
}
`),
		one("use a Map of unknown", file, `export function track(name: string, payload: Map<string, unknown>): void {
  send(name, payload)
}
`),
		one("use the object type", file, `export function track(name: string, payload: object): void {
  send(name, payload)
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("declare the fields", file, `interface Payload {
  userId: string
  plan: "free" | "paid"
}

export function track(name: string, payload: Payload): void {
  send(name, payload)
}
`)}

	return c
}

func tsTypeAnyCase() advCase {
	const file = "src/codec.ts"
	c := single("ts/type-any", "typescript: a parameter typed any", file, `export function decode(value: any): string {
  return String(value)
}
`)
	c.Dodges = []fixturetest.Variant{
		one("add an eslint disable above", file, `// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function decode(value: any): string {
  return String(value)
}
`),
	}
	c.Persists = []fixturetest.Variant{
		one("hide it behind an alias", file, `type Loose = any

export function decode(value: Loose): string {
  return String(value)
}
`),
		one("make it an array of any", file, `export function decode(value: any[]): string {
  return String(value)
}
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("drop the annotation and leave it implicit", file, `export function decode(value): string {
  return String(value)
}
`),
		one("use the Function and object types", file, `export function decode(value: object | Function): string {
  return String(value)
}
`),
	}
	c.Fixes = []fixturetest.Variant{one("name the type", file, `export function decode(value: Uint8Array): string {
  return new TextDecoder().decode(value)
}
`)}

	return c
}

func tsModuleMockCase() advCase {
	const file = "src/sync.test.ts"
	c := single("ts/module-mock", "typescript: a test mocks the module it depends on", file,
		`import { vi, it, expect } from "vitest"
import { run } from "./sync"

vi.mock("./api")

it("runs", async () => {
  expect(await run()).toBe(0)
})
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above the mock", file, `import { vi, it, expect } from "vitest"
import { run } from "./sync"

// the network is not available here
vi.mock("./api")

it("runs", async () => {
  expect(await run()).toBe(0)
})
`),
	}
	c.Persists = []fixturetest.Variant{
		one("give the mock a factory", file, `import { vi, it, expect } from "vitest"
import { run } from "./sync"

vi.mock("./api", () => ({ fetchAll: async () => [] }))

it("runs", async () => {
  expect(await run()).toBe(0)
})
`),
		one("mock it inside the test with doMock", file, `import { vi, it, expect } from "vitest"

it("runs", async () => {
  vi.doMock("./api")
  const { run } = await import("./sync")
  expect(await run()).toBe(0)
})
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("spy on the one function and replace it", file, `import { vi, it, expect } from "vitest"
import * as api from "./api"
import { run } from "./sync"

it("runs", async () => {
  vi.spyOn(api, "fetchAll").mockResolvedValue([])
  expect(await run()).toBe(0)
})
`),
		one("import vi under another name", file, `import { vi as stub, it, expect } from "vitest"
import { run } from "./sync"

stub.mock("./api")

it("runs", async () => {
  expect(await run()).toBe(0)
})
`),
	}
	c.Fixes = []fixturetest.Variant{one("pass the dependency in", file, `import { it, expect } from "vitest"
import { run } from "./sync"

it("runs", async () => {
  expect(await run({ fetchAll: async () => [] })).toBe(0)
})
`)}

	return c
}

func tsTestReadsSourceCase() advCase {
	const file = "src/version.test.ts"
	c := single("ts/test-reads-source", "typescript: a test reads package.json", file,
		`import { readFileSync } from "node:fs"
import { it, expect } from "vitest"

it("has a version", () => {
  const pkg = JSON.parse(readFileSync("../package.json", "utf8"))
  expect(pkg.version).toBeDefined()
})
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above the read", file, `import { readFileSync } from "node:fs"
import { it, expect } from "vitest"

it("has a version", () => {
  // the manifest must carry a version
  const pkg = JSON.parse(readFileSync("../package.json", "utf8"))
  expect(pkg.version).toBeDefined()
})
`),
	}
	c.Persists = []fixturetest.Variant{
		one("hoist the path into a constant", file, `import { readFileSync } from "node:fs"
import { it, expect } from "vitest"

const manifest = "../package.json"

it("has a version", () => {
  const pkg = JSON.parse(readFileSync(manifest, "utf8"))
  expect(pkg.version).toBeDefined()
})
`),
		one("read it through the fs namespace and a joined path", file, `import * as fs from "node:fs"
import * as path from "node:path"
import { it, expect } from "vitest"

it("has a version", () => {
  const pkg = JSON.parse(fs.readFileSync(path.join(__dirname, "..", "package.json"), "utf8"))
  expect(pkg.version).toBeDefined()
})
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("import the json instead of reading it", file, `import { it, expect } from "vitest"
import pkg from "../package.json"

it("has a version", () => {
  expect(pkg.version).toBeDefined()
})
`),
		one("read it through a helper that takes the path", file, `import { readFileSync } from "node:fs"
import { it, expect } from "vitest"

function readJson(name: string): { version?: string } {
  return JSON.parse(readFileSync(name, "utf8"))
}

it("has a version", () => {
  expect(readJson("../package.json").version).toBeDefined()
})
`),
	}
	c.Fixes = []fixturetest.Variant{one("ask the code for its version", file, `import { it, expect } from "vitest"
import { version } from "./version"

it("has a version", () => {
  expect(version()).toMatch(/^\d+\./)
})
`)}

	return c
}

func tsMockOnlyAssertCase() advCase {
	const file = "src/notify.test.ts"
	c := single("ts/mock-only-assert", "typescript: a test asserts only that a spy was called", file,
		`import { vi, it, expect } from "vitest"
import { notify } from "./notify"

it("notifies", () => {
  const send = vi.fn()
  notify(send, "hi")
  expect(send).toHaveBeenCalledWith("hi")
})
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment in the body", file, `import { vi, it, expect } from "vitest"
import { notify } from "./notify"

it("notifies", () => {
  const send = vi.fn()
  notify(send, "hi")
  // the message goes out
  expect(send).toHaveBeenCalledWith("hi")
})
`),
		one("assert a constant beside it", file, `import { vi, it, expect } from "vitest"
import { notify } from "./notify"

it("notifies", () => {
  const send = vi.fn()
  notify(send, "hi")
  expect(send).toHaveBeenCalledWith("hi")
  expect(true).toBe(true)
})
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename the test", file, `import { vi, it, expect } from "vitest"
import { notify } from "./notify"

it("sends the message", () => {
  const send = vi.fn()
  notify(send, "hi")
  expect(send).toHaveBeenCalledWith("hi")
})
`),
		one("assert on the spy's call record", file, `import { vi, it, expect } from "vitest"
import { notify } from "./notify"

it("notifies", () => {
  const send = vi.fn()
  notify(send, "hi")
  expect(send.mock.calls.length).toBe(1)
})
`),
		one("mark it only", file, `import { vi, it, expect } from "vitest"
import { notify } from "./notify"

it.only("notifies", () => {
  const send = vi.fn()
  notify(send, "hi")
  expect(send).toHaveBeenCalledWith("hi")
})
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("copy the call record into a variable first", file, `import { vi, it, expect } from "vitest"
import { notify } from "./notify"

it("notifies", () => {
  const send = vi.fn()
  notify(send, "hi")
  const calls = send.mock.calls
  expect(calls).toHaveLength(1)
})
`),
	}
	c.Fixes = []fixturetest.Variant{one("assert on what the code produced", file, `import { it, expect } from "vitest"
import { notify } from "./notify"

it("notifies", () => {
  const outbox: string[] = []
  notify((message) => outbox.push(message), "hi")
  expect(outbox).toEqual(["hi"])
})
`)}

	return c
}

func tsConstantEchoCase() advCase {
	const file = "src/limits.test.ts"
	c := single("ts/constant-echo", "typescript: a test restates a constant", file,
		`import { it, expect } from "vitest"
import { MAX_RETRIES } from "./limits"

it("has the limit", () => {
  expect(MAX_RETRIES).toBe(3)
})
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above", file, `import { it, expect } from "vitest"
import { MAX_RETRIES } from "./limits"

it("has the limit", () => {
  // three is the agreed limit
  expect(MAX_RETRIES).toBe(3)
})
`),
	}
	c.Persists = []fixturetest.Variant{
		one("use toEqual", file, `import { it, expect } from "vitest"
import { MAX_RETRIES } from "./limits"

it("has the limit", () => {
  expect(MAX_RETRIES).toEqual(3)
})
`),
		one("swap the two sides", file, `import { it, expect } from "vitest"
import { MAX_RETRIES } from "./limits"

it("has the limit", () => {
  expect(3).toBe(MAX_RETRIES)
})
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("read it off the module", file, `import { it, expect } from "vitest"
import * as limits from "./limits"

it("has the limit", () => {
  expect(limits.MAX_RETRIES).toBe(3)
})
`),
		one("copy it into a lower-case local first", file, `import { it, expect } from "vitest"
import { MAX_RETRIES } from "./limits"

it("has the limit", () => {
  const limit = MAX_RETRIES
  expect(limit).toBe(3)
})
`),
		one("compare with a looser matcher", file, `import { it, expect } from "vitest"
import { MAX_RETRIES } from "./limits"

it("has the limit", () => {
  expect(MAX_RETRIES).toBeGreaterThanOrEqual(3)
})
`),
	}
	c.Fixes = []fixturetest.Variant{one("exercise the code the constant governs", file,
		`import { it, expect } from "vitest"
import { attempts } from "./retry"

it("gives up after the limit", () => {
  expect(attempts(alwaysFails)).toBe(3)
})
`)}

	return c
}

func tsTimingHackCase() advCase {
	const file = "src/worker.test.ts"
	c := single("ts/timing-hack", "typescript: a test fakes the clock", file, `import { vi, it, expect } from "vitest"
import { start } from "./worker"

it("drains", async () => {
  vi.useFakeTimers()
  const queue = start()
  expect(queue.size).toBe(0)
})
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above", file, `import { vi, it, expect } from "vitest"
import { start } from "./worker"

it("drains", async () => {
  // the worker polls on a timer
  vi.useFakeTimers()
  const queue = start()
  expect(queue.size).toBe(0)
})
`),
	}
	c.Persists = []fixturetest.Variant{
		one("move the fake clock into a hook", file, `import { vi, it, expect, beforeEach } from "vitest"
import { start } from "./worker"

beforeEach(() => {
  vi.useFakeTimers()
})

it("drains", async () => {
  const queue = start()
  expect(queue.size).toBe(0)
})
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("sleep on a promise instead", file, `import { it, expect } from "vitest"
import { start } from "./worker"

it("drains", async () => {
  const queue = start()
  await new Promise((resolve) => setTimeout(resolve, 50))
  expect(queue.size).toBe(0)
})
`),
		one("call a sleep helper", file, `import { it, expect } from "vitest"
import { sleep } from "./testing"
import { start } from "./worker"

it("drains", async () => {
  const queue = start()
  await sleep(50)
  expect(queue.size).toBe(0)
})
`),
		one("name the sleep helper something else", file, `import { it, expect } from "vitest"
import { settle } from "./testing"
import { start } from "./worker"

it("drains", async () => {
  const queue = start()
  await settle(50)
  expect(queue.size).toBe(0)
})
`),
		one("use the promise form of setTimeout", file, `import { setTimeout as delay } from "node:timers/promises"
import { it, expect } from "vitest"
import { start } from "./worker"

it("drains", async () => {
  const queue = start()
  await delay(50)
  expect(queue.size).toBe(0)
})
`),
	}
	c.Fixes = []fixturetest.Variant{one("await the queue's own signal", file, `import { it, expect } from "vitest"
import { start } from "./worker"

it("drains", async () => {
  const queue = start()
  await queue.drained()
  expect(queue.size).toBe(0)
})
`)}

	return c
}
