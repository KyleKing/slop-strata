package rules_test

import "github.com/kyleking/slop-strata/internal/fixturetest"

func pythonAdversarial() []advCase {
	return []advCase{
		pySilentExceptCase(), pyOrDefaultCase(), pyTypeAnyCase(), pyMockPatchCase(), pyTestReadsSourceCase(),
		pyMockOnlyAssertCase(), pyConstantEchoCase(), pyTimingHackCase(),
	}
}

func pySilentExceptCase() advCase {
	const file = "app/config.py"
	c := single("py/silent-except", "python: a read whose failure is swallowed", file, `def load(path):
    try:
        return open(path).read()
    except OSError:
        pass
    return ""
`)
	c.Dodges = []fixturetest.Variant{
		one("write an ellipsis for pass", file, `def load(path):
    try:
        return open(path).read()
    except OSError:
        ...
    return ""
`),
		one("explain it in a comment inside the handler", file, `def load(path):
    try:
        return open(path).read()
    except OSError:
        # the file is optional
        pass
    return ""
`),
		one("add a noqa on the pass", file, `def load(path):
    try:
        return open(path).read()
    except OSError:
        pass  # noqa: S110
    return ""
`),
	}
	c.Persists = []fixturetest.Variant{
		one("catch everything instead", file, `def load(path):
    try:
        return open(path).read()
    except Exception:
        pass
    return ""
`),
		one("put the handler on one line", file, `def load(path):
    try:
        return open(path).read()
    except OSError: pass
    return ""
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("suppress it with contextlib", file, `import contextlib


def load(path):
    with contextlib.suppress(OSError):
        return open(path).read()
    return ""
`),
		one("return a default from the handler", file, `def load(path):
    try:
        return open(path).read()
    except OSError:
        return ""
`),
	}
	c.Fixes = []fixturetest.Variant{one("raise an error that names the file", file, `def load(path):
    try:
        return open(path).read()
    except OSError as err:
        raise ConfigError(path) from err
`)}

	return c
}

func pyOrDefaultCase() advCase {
	const file = "app/report.py"
	c := single("py/or-default", "python: a None parameter replaced by an empty list", file, `def render(rows=None):
    rows = rows or []
    return [str(r) for r in rows]
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above", file, `def render(rows=None):
    # rows may be missing
    rows = rows or []
    return [str(r) for r in rows]
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename the parameter", file, `def render(items=None):
    items = items or []
    return [str(r) for r in items]
`),
		one("call list() for the literal", file, `def render(rows=None):
    rows = rows or list()
    return [str(r) for r in rows]
`),
		one("write it as a conditional expression", file, `def render(rows=None):
    rows = rows if rows else []
    return [str(r) for r in rows]
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("write it as an if statement on truthiness", file, `def render(rows=None):
    if not rows:
        rows = []
    return [str(r) for r in rows]
`),
	}
	c.Fixes = []fixturetest.Variant{one("test for None by name", file, `def render(rows=None):
    if rows is None:
        rows = []
    return [str(r) for r in rows]
`)}

	return c
}

func pyTypeAnyCase() advCase {
	const file = "app/codec.py"
	c := single("py/type-any", "python: a parameter typed Any", file, `from typing import Any


def decode(value: Any) -> str:
    return str(value)
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment and a type ignore", file, `from typing import Any

# decode takes anything


def decode(value: Any) -> str:
    return str(value)  # type: ignore
`),
	}
	c.Persists = []fixturetest.Variant{
		one("read it off the module", file, `import typing


def decode(value: typing.Any) -> str:
    return str(value)
`),
		one("read it off an aliased module", file, `import typing as t


def decode(value: t.Any) -> str:
    return str(value)
`),
		one("nest it in a container type", file, `from typing import Any


def decode(value: dict[str, Any]) -> str:
    return str(value)
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("import it under another name", file, `from typing import Any as Value


def decode(value: Value) -> str:
    return str(value)
`),
		one("assign it to a module-level alias", file, `from typing import Any

Value = Any


def decode(value: Value) -> str:
    return str(value)
`),
		one("quote the annotation", file, `from typing import Any


def decode(value: "Any") -> str:
    return str(value)
`),
	}
	c.Fixes = []fixturetest.Variant{one("name the type", file, `def decode(value: bytes) -> str:
    return value.decode()
`)}

	return c
}

func pyMockPatchCase() advCase {
	const file = "tests/test_sync.py"
	c := single("py/mock-patch", "python: a test patches the function it depends on", file,
		`from unittest.mock import patch

from app.sync import run


def test_run():
    with patch("app.sync.fetch") as fetch:
        fetch.return_value = []
        assert run() == 0
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above the patch", file, `from unittest.mock import patch

from app.sync import run


def test_run():
    # the network is not available here
    with patch("app.sync.fetch") as fetch:
        fetch.return_value = []
        assert run() == 0
`),
	}
	c.Persists = []fixturetest.Variant{
		one("patch as a decorator", file, `from unittest.mock import patch

from app.sync import run


@patch("app.sync.fetch")
def test_run(fetch):
    fetch.return_value = []
    assert run() == 0
`),
		one("use monkeypatch", file, `from app import sync


def test_run(monkeypatch):
    monkeypatch.setattr(sync, "fetch", lambda: [])
    assert sync.run() == 0
`),
		one("spell out the module", file, `import unittest.mock

from app.sync import run


def test_run():
    with unittest.mock.patch("app.sync.fetch") as fetch:
        fetch.return_value = []
        assert run() == 0
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("import patch under another name", file, `from unittest.mock import patch as swap

from app.sync import run


def test_run():
    with swap("app.sync.fetch") as fetch:
        fetch.return_value = []
        assert run() == 0
`),
		one("assign over the attribute by hand", file, `from app import sync


def test_run():
    sync.fetch = lambda: []
    assert sync.run() == 0
`),
	}
	c.Fixes = []fixturetest.Variant{one("pass the dependency in", file, `from app.sync import run


def test_run():
    assert run(fetch=lambda: []) == 0
`)}

	return c
}

func pyTestReadsSourceCase() advCase {
	const file = "tests/test_docs.py"
	c := single("py/test-reads-source", "python: a test reads the README", file, `def test_readme_mentions_install():
    text = open("../README.md").read()
    assert "install" in text
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above the read", file, `def test_readme_mentions_install():
    # the docs must stay in step
    text = open("../README.md").read()
    assert "install" in text
`),
	}
	c.Persists = []fixturetest.Variant{
		one("hoist the path into a module constant", file, `README = "../README.md"


def test_readme_mentions_install():
    text = open(README).read()
    assert "install" in text
`),
		one("read it through pathlib from the test's own location", file, `from pathlib import Path


def test_readme_mentions_install():
    text = (Path(__file__).parent.parent / "README.md").read_text()
    assert "install" in text
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("hoist a pathlib path into a variable", file, `from pathlib import Path


def test_readme_mentions_install():
    readme = Path(__file__).parent.parent / "README.md"
    text = readme.read_text()
    assert "install" in text
`),
		one("read it through a fixture that takes the name", file, `import pytest


@pytest.fixture
def read():
    def _read(name):
        return open(name).read()

    return _read


def test_readme_mentions_install(read):
    assert "install" in read("../README.md")
`),
	}
	c.Fixes = []fixturetest.Variant{one("call the code instead of reading the docs", file, `from app.cli import usage


def test_usage_mentions_install():
    assert "install" in usage()
`)}

	return c
}

func pyMockOnlyAssertCase() advCase {
	const file = "tests/test_notify.py"
	c := single("py/mock-only-assert", "python: a test asserts only that a mock was called", file,
		`from app.notify import notify


def test_notify(sender):
    notify(sender, "hi")
    sender.send.assert_called_once_with("hi")
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment and a blank line", file, `from app.notify import notify


def test_notify(sender):
    notify(sender, "hi")

    # the message goes out
    sender.send.assert_called_once_with("hi")
`),
		one("assert a constant beside it", file, `from app.notify import notify


def test_notify(sender):
    notify(sender, "hi")
    sender.send.assert_called_once_with("hi")
    assert True
`),
	}
	c.Persists = []fixturetest.Variant{
		one("rename the test", file, `from app.notify import notify


def test_notify_sends(sender):
    notify(sender, "hi")
    sender.send.assert_called_once_with("hi")
`),
		one("assert on the mock's call record", file, `from app.notify import notify


def test_notify(sender):
    notify(sender, "hi")
    assert sender.send.call_args_list == [call("hi")]
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("assert the mock was called", file, `from app.notify import notify


def test_notify(sender):
    notify(sender, "hi")
    assert sender.send.called
`),
		one("copy the call record into a variable first", file, `from app.notify import notify


def test_notify(sender):
    notify(sender, "hi")
    sent = sender.send.call_args_list
    assert len(sent) == 1
`),
	}
	c.Fixes = []fixturetest.Variant{one("assert on what the code produced", file, `from app.notify import notify


def test_notify(outbox):
    notify(outbox, "hi")
    assert outbox.messages == ["hi"]
`)}

	return c
}

func pyConstantEchoCase() advCase {
	const file = "tests/test_limits.py"
	c := single("py/constant-echo", "python: a test restates a constant", file, `from app.limits import MAX_RETRIES


def test_max_retries():
    assert MAX_RETRIES == 3
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment and a message", file, `from app.limits import MAX_RETRIES


def test_max_retries():
    # three is the agreed limit
    assert MAX_RETRIES == 3, "limit changed"
`),
		one("swap the operands", file, `from app.limits import MAX_RETRIES


def test_max_retries():
    assert 3 == MAX_RETRIES
`),
	}
	c.Persists = []fixturetest.Variant{
		one("read it off the module", file, `from app import limits


def test_max_retries():
    assert limits.MAX_RETRIES == 3
`),
		one("copy it into a local first", file, `from app.limits import MAX_RETRIES


def test_max_retries():
    limit = MAX_RETRIES
    assert limit == 3
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("wrap the constant in a call that does nothing", file, `from app.limits import MAX_RETRIES


def test_max_retries():
    assert int(MAX_RETRIES) == 3
`),
		one("use a unittest assertion method", file, `import unittest

from app.limits import MAX_RETRIES


class TestLimits(unittest.TestCase):
    def test_max_retries(self):
        self.assertEqual(MAX_RETRIES, 3)
`),
	}
	c.Fixes = []fixturetest.Variant{one("exercise the code the constant governs", file, `from app.retry import attempts


def test_gives_up_after_the_limit():
    assert attempts(always_fails) == 3
`)}

	return c
}

func pyTimingHackCase() advCase {
	const file = "tests/test_worker.py"
	c := single("py/timing-hack", "python: a test sleeps to wait for a worker", file, `import time

from app.worker import start


def test_worker_drains():
    queue = start()
    time.sleep(0.1)
    assert queue.empty()
`)
	c.Dodges = []fixturetest.Variant{
		one("add a comment above the sleep", file, `import time

from app.worker import start


def test_worker_drains():
    queue = start()
    # let the worker catch up
    time.sleep(0.1)
    assert queue.empty()
`),
	}
	c.Persists = []fixturetest.Variant{
		one("import sleep by name", file, `from time import sleep

from app.worker import start


def test_worker_drains():
    queue = start()
    sleep(0.1)
    assert queue.empty()
`),
		one("alias the module", file, `import time as clock

from app.worker import start


def test_worker_drains():
    queue = start()
    clock.sleep(0.1)
    assert queue.empty()
`),
		one("wrap the sleep in a helper of the test file", file, `import time

from app.worker import start


def settle():
    time.sleep(0.1)


def test_worker_drains():
    queue = start()
    settle()
    assert queue.empty()
`),
	}
	c.Gaps = []fixturetest.Variant{
		one("import sleep under another name", file, `from time import sleep as pause

from app.worker import start


def test_worker_drains():
    queue = start()
    pause(0.1)
    assert queue.empty()
`),
		one("wait on an event nobody sets", file, `import threading

from app.worker import start


def test_worker_drains():
    queue = start()
    threading.Event().wait(0.1)
    assert queue.empty()
`),
	}
	c.Fixes = []fixturetest.Variant{one("join the queue", file, `from app.worker import start


def test_worker_drains():
    queue = start()
    queue.join()
    assert queue.empty()
`)}

	return c
}
