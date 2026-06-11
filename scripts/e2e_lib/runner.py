"""Orchestration: binary discovery, context/node probing, parallel tree runs,
and reporting."""

from __future__ import annotations

import json
import os
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed

from .context import Ctx, Env
from .model import Isolation, Status, TreeReport
from .trees import TREES

# --- ANSI (suppressed when not a TTY) ---------------------------------------

_TTY = sys.stdout.isatty()


def _c(code: str, text: str) -> str:
    return f"\033[{code}m{text}\033[0m" if _TTY else text


GREEN = lambda s: _c("32", s)
RED = lambda s: _c("31", s)
YELLOW = lambda s: _c("33", s)
DIM = lambda s: _c("2", s)
BOLD = lambda s: _c("1", s)

_GLYPH = {Status.PASS: GREEN("✓"), Status.FAIL: RED("✗"), Status.SKIP: YELLOW("○")}


def die(msg: str, code: int = 2) -> None:
    print(f"e2e: error: {msg}", file=sys.stderr)
    sys.exit(code)


# --- binary + environment ---------------------------------------------------


def repo_root() -> str:
    # scripts/e2e_lib/runner.py -> repo root is two levels up from scripts/.
    here = os.path.dirname(os.path.abspath(__file__))
    return os.path.dirname(os.path.dirname(here))


def find_binary(explicit: str | None, build: bool) -> str:
    if explicit:
        if not os.path.isfile(explicit):
            die(f"binary not found: {explicit}")
        return explicit
    root = repo_root()
    cand = os.path.join(root, "dist", "pve")
    if os.path.isfile(cand):
        return cand
    if build:
        print("e2e: building ./dist/pve ...", flush=True)
        rc = subprocess.run(
            ["go", "build", "-o", cand, "./cmd/pve"], cwd=root
        ).returncode
        if rc != 0 or not os.path.isfile(cand):
            die("go build failed; build the binary or pass --binary", code=3)
        return cand
    die("no ./dist/pve; run `make build` or pass --binary (or drop --no-build)")
    raise SystemExit  # unreachable, satisfies type checkers


def _probe_json(binary: str, target: str, *args: str) -> tuple[int, object | None, str]:
    argv = [binary, "--context", target, "--no-log", "-o", "json", *args]
    proc = subprocess.run(argv, capture_output=True, text=True, timeout=60)
    if proc.returncode != 0:
        return proc.returncode, None, proc.stderr.strip() or proc.stdout.strip()
    try:
        return 0, json.loads(proc.stdout), ""
    except json.JSONDecodeError as exc:
        return 0, None, f"invalid json: {exc}"


def target_configured(binary: str, target: str) -> tuple[bool, str]:
    rc, data, err = _probe_json(binary, target, "context", "ls")
    if rc != 0 or data is None:
        return False, err
    if isinstance(data, list) and any(
        isinstance(t, dict) and t.get("name") == target for t in data
    ):
        return True, ""
    return False, f"context {target!r} not in config"


def discover_node(binary: str, target: str) -> str:
    rc, data, _ = _probe_json(binary, target, "node", "list")
    if rc == 0 and isinstance(data, list) and data:
        first = data[0]
        if isinstance(first, dict):
            return str(first.get("node") or "")
    return ""


# --- parallel execution -----------------------------------------------------


def _run_tree(env: Env, name: str) -> TreeReport:
    report = TreeReport(name)
    module = TREES[name]
    ctx = Ctx(env, name)
    try:
        module.run(ctx)
    except Exception as exc:  # a tree bug must not sink the whole sweep
        report.error = f"{type(exc).__name__}: {exc}"
    report.results = ctx.results
    report.deferred = ctx.deferred
    return report


def run_trees(env: Env, names: list[str], jobs: int) -> list[TreeReport]:
    reports: dict[str, TreeReport] = {}
    with ThreadPoolExecutor(max_workers=max(1, jobs)) as pool:
        futs = {pool.submit(_run_tree, env, n): n for n in names}
        for fut in as_completed(futs):
            r = fut.result()
            reports[r.name] = r
    # Preserve registry order for stable output.
    return [reports[n] for n in names]


# --- reporting --------------------------------------------------------------


def print_reports(reports: list[TreeReport], show_deferred: bool,
                  mutate_covers: frozenset[str] = frozenset()) -> int:
    total_pass = total_fail = total_skip = 0
    for rep in reports:
        head = BOLD(f"▸ {rep.name}")
        if rep.error:
            print(f"{head}  {RED('TREE ERROR')}: {rep.error}")
            total_fail += 1
            continue
        print(head)
        for r in rep.results:
            line = f"  {_GLYPH[r.status]} {r.name}"
            if r.command:
                line += DIM(f"  ({r.command})")
            print(line)
            if r.status is Status.FAIL and r.detail:
                print(RED(f"      {r.detail}"))
            elif r.status is Status.SKIP and r.detail:
                print(DIM(f"      skip: {r.detail}"))
        total_pass += rep.passed
        total_fail += rep.failed
        total_skip += rep.skipped

    if show_deferred:
        _print_deferred(reports, mutate_covers)

    print()
    summary = (
        f"{GREEN(str(total_pass) + ' passed')}, "
        f"{RED(str(total_fail) + ' failed') if total_fail else '0 failed'}, "
        f"{YELLOW(str(total_skip) + ' skipped')}"
    )
    print(BOLD("e2e summary: ") + summary)
    return 1 if total_fail else 0


def _print_deferred(reports: list[TreeReport],
                    mutate_covers: frozenset[str] = frozenset()) -> None:
    deferred = [d for rep in reports for d in rep.deferred]
    if not deferred:
        return

    # A verb the mutate phase actually runs this invocation is not a gap — pull
    # it out of the "not run" list and report it as exercised live instead.
    covered_live = [d for d in deferred if d.live_covered and d.tree in mutate_covers]
    not_run = [d for d in deferred if d not in covered_live]

    if covered_live:
        print()
        trees = ", ".join(sorted({d.tree for d in covered_live}))
        print(BOLD("Exercised live in the mutate phase below: ") + DIM(trees))

    if not not_run:
        return
    print()
    print(BOLD("Deferred (not run — destructive / mutating):"))
    iso_any = False
    for d in not_run:
        mark = RED(" [isolation]") if d.isolation else ""
        print(f"  {YELLOW('·')} {d.tree}: {d.name}{mark} — {d.reason}")
        if d.command:
            print(DIM(f"      e.g. {d.command}"))
        iso_any = iso_any or d.isolation
    if iso_any:
        print()
        print(BOLD("Isolation contract for [isolation] operations:"))
        print(f"  tag={Isolation.TAG}  pool={Isolation.POOL}  name-prefix={Isolation.NAME_PREFIX}")
        print(
            f"  SDN zone={Isolation.SDN_ZONE} vnet={Isolation.SDN_VNET} "
            f"subnet={Isolation.SDN_SUBNET} (off the host 172.x network)"
        )


def print_listing() -> None:
    print(BOLD("Command trees:"))
    for name, mod in TREES.items():
        print(f"  {name:10s} {DIM(mod.DESCRIPTION)}")
    print()
    print(BOLD("Lab isolation contract (for deferred resource-creating tests):"))
    print(f"  tag          {Isolation.TAG}")
    print(f"  pool         {Isolation.POOL}")
    print(f"  name-prefix  {Isolation.NAME_PREFIX}")
    print(f"  SDN zone     {Isolation.SDN_ZONE}")
    print(f"  SDN vnet     {Isolation.SDN_VNET}")
    print(f"  SDN subnet   {Isolation.SDN_SUBNET}  (off the host 172.x network)")
    print(f"  SDN gateway  {Isolation.SDN_GATEWAY}")
