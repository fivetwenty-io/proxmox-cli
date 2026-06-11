"""Execution context handed to every command-tree check function.

A `Ctx` wraps an immutable `Env` (binary path + context + discovered node) and
collects `Result` rows. One `Ctx` is created per tree so trees run on separate
threads without sharing mutable state.
"""

from __future__ import annotations

import json
import subprocess
import time
from dataclasses import dataclass
from typing import Any, Callable

from .model import Deferred, Result, Status


@dataclass(frozen=True)
class Env:
    binary: str
    context: str
    node: str            # a real node name discovered up front (may be "")
    timeout_s: int = 60


@dataclass
class CmdResult:
    argv: list[str]
    rc: int
    stdout: str
    stderr: str

    def json(self) -> Any:
        return json.loads(self.stdout)


class Ctx:
    def __init__(self, env: Env, tree: str):
        self.env = env
        self.tree = tree
        self.node = env.node
        self.results: list[Result] = []
        self.deferred: list[Deferred] = []

    # -- raw invocation ------------------------------------------------------

    def run(self, *args: str, node: str | None = None, fmt: str = "json",
            with_context: bool = True) -> CmdResult:
        """Invoke `pve` with context/output/no-log injected. Never raises.

        `with_context=False` omits `--context`; used by checks that operate on a
        scratch `--config` file and must not resolve the configured context.
        """
        argv = [self.env.binary, "--no-log"]
        if with_context:
            argv += ["--context", self.env.context]
        if fmt:
            argv += ["-o", fmt]
        if node:
            argv += ["--node", node]
        argv += list(args)
        try:
            proc = subprocess.run(
                argv,
                capture_output=True,
                text=True,
                timeout=self.env.timeout_s,
            )
            return CmdResult(argv, proc.returncode, proc.stdout, proc.stderr)
        except subprocess.TimeoutExpired:
            return CmdResult(argv, 124, "", f"timed out after {self.env.timeout_s}s")

    @staticmethod
    def pretty(argv: list[str]) -> str:
        # Strip the absolute binary path for readable reports.
        shown = ["pve", *argv[1:]]
        return " ".join(shown)

    # -- check recording -----------------------------------------------------

    def check(
        self,
        name: str,
        *args: str,
        node: str | None = None,
        fmt: str = "json",
        with_context: bool = True,
        validate: Callable[[CmdResult], str | None] | None = None,
    ) -> CmdResult:
        """Run a command, record PASS/FAIL.

        Default assertion: exit code 0. An optional `validate` returns an error
        string to fail the check, or None to accept. JSON output is parsed and,
        when `fmt=json`, malformed JSON fails the check.
        """
        start = time.monotonic()
        res = self.run(*args, node=node, fmt=fmt, with_context=with_context)
        dur = time.monotonic() - start
        cmd = self.pretty(res.argv)
        detail = ""
        status = Status.PASS

        if res.rc != 0:
            status = Status.FAIL
            detail = (res.stderr.strip() or res.stdout.strip() or "non-zero exit")[:200]
        elif fmt == "json" and res.stdout.strip():
            try:
                res.json()
            except json.JSONDecodeError as exc:
                status = Status.FAIL
                detail = f"invalid json: {exc}"
        if status is Status.PASS and validate is not None:
            err = validate(res)
            if err:
                status = Status.FAIL
                detail = err

        self.results.append(
            Result(self.tree, name, status, command=cmd, detail=detail, duration_s=dur)
        )
        return res

    def check_formats(self, name: str, *args: str, node: str | None = None) -> None:
        """Assert a read command renders cleanly in every `-o` format.

        Records one PASS only if `table`, `plain`, `json`, and `yaml` each exit 0
        with non-empty output; otherwise FAIL naming the offending format. This
        catches renderer regressions the json-only sweep cannot see.
        """
        start = time.monotonic()
        bad = ""
        for fmt in ("table", "plain", "json", "yaml"):
            res = self.run(*args, node=node, fmt=fmt)
            if res.rc != 0:
                bad = f"{fmt}: exit {res.rc}: {(res.stderr.strip() or res.stdout.strip())[:120]}"
                break
            if not res.stdout.strip():
                bad = f"{fmt}: empty output"
                break
        dur = time.monotonic() - start
        cmd = self.pretty([self.env.binary, *args]) + " (×4 formats)"
        status = Status.PASS if not bad else Status.FAIL
        self.results.append(
            Result(self.tree, name, status, command=cmd, detail=bad, duration_s=dur)
        )

    def expect_fail(
        self,
        name: str,
        *args: str,
        must_contain: str = "",
        node: str | None = None,
        with_context: bool = True,
    ) -> None:
        """Inverse of `check`: record PASS only when the command FAILS cleanly.

        A clean failure is a non-zero exit accompanied by a non-empty error
        message (and, when `must_contain` is given, a message that includes it).
        This guards the CLI's error surface — usage errors, missing required
        flags, and not-found lookups — which no happy-path check exercises.
        """
        start = time.monotonic()
        res = self.run(*args, node=node, fmt="", with_context=with_context)
        dur = time.monotonic() - start
        cmd = self.pretty(res.argv)
        msg = res.stderr.strip() or res.stdout.strip()
        if res.rc == 0:
            status, detail = Status.FAIL, "expected non-zero exit, got 0"
        elif not msg:
            status, detail = Status.FAIL, "non-zero exit but no error message"
        elif must_contain and must_contain.lower() not in msg.lower():
            status, detail = Status.FAIL, f"message missing {must_contain!r}: {msg[:120]}"
        else:
            status, detail = Status.PASS, ""
        self.results.append(
            Result(self.tree, name, status, command=cmd, detail=detail, duration_s=dur)
        )

    def skip(self, name: str, reason: str) -> None:
        self.results.append(Result(self.tree, name, Status.SKIP, detail=reason))

    def defer(self, name: str, reason: str, command: str = "", isolation: bool = False,
              live_covered: bool = False) -> None:
        self.deferred.append(
            Deferred(self.tree, name, reason, command, isolation, live_covered)
        )

    # -- discovery helpers (read-only) ---------------------------------------

    def first(self, items: list[dict], key: str) -> Any | None:
        for it in items:
            if key in it and it[key] not in (None, ""):
                return it[key]
        return None
