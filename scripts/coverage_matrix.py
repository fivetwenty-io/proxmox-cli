#!/usr/bin/env python3
"""Generate docs/test-coverage-matrix.md from the live sources of truth.

The matrix maps every invocable leaf command in the built CLI to its automated
test coverage across the two suites (read-only `e2e` sweep and the destructive
`lifecycle` / `--mutate` phase). Hand-maintaining ~550 rows drifts immediately,
so this script derives the matrix mechanically:

  1. the leaf set, from a walk of `./dist/pve ... --help` (the actual command
     tree), plus the handful of runnable parent commands that carry a `RunE`
     alongside their sub-commands;
  2. the read-only coverage, by statically parsing every
     `ctx.check`/`check_formats`/`expect_fail`/`skip`/`defer` call in
     `scripts/e2e_lib/trees/*.py` (a check at module scope is unconditional `✓`;
     one nested in an `if`/`for` is inventory-gated `◑`);
  3. the mutate coverage, by statically parsing every `Step(...)` record and
     `step`/`soft_step`/`cover_skip`/`del_step` call in
     `scripts/e2e_lib/lifecycle.py`.

Run from the repo root after building the binary:

    go build -o ./dist/pve ./cmd/pve && python3 scripts/coverage_matrix.py

It rewrites docs/test-coverage-matrix.md in place. No live target is required;
the classification is purely static so the result is reproducible in CI.
"""
from __future__ import annotations

import ast
import os
import re
import subprocess
import sys
from collections import Counter, defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BIN = os.path.join(ROOT, "dist", "pve")
TREES_DIR = os.path.join(ROOT, "scripts", "e2e_lib", "trees")
LIFECYCLE = os.path.join(ROOT, "scripts", "e2e_lib", "lifecycle.py")
OUT = os.path.join(ROOT, "docs", "test-coverage-matrix.md")

# Runnable parent commands: they have their own RunE *and* sub-commands, so the
# childless-leaf walk misses them. Determined by probing each has-children
# parent against a live target (a pure group prints "Available Commands:" at
# exit 0; a runnable parent errors on the missing required argument). Stable.
RUNNABLE_PARENTS = {"version", "lxc migrate", "node vzdump", "qemu agent", "qemu migrate"}

# Leaves with no automated coverage by design (interactive PTY or shared-host
# daemon mutation). Detected from the deferred reason text, listed here too so
# the intent is explicit and greppable.
NA_REASON_MARKERS = ("not automatable", "interactive", "out of scope",
                     "host daemon", "real host", "shared-host", "shared lab")


# --------------------------------------------------------------------------- #
# 1. leaf set                                                                  #
# --------------------------------------------------------------------------- #
def _help(path: list[str]) -> str:
    r = subprocess.run([BIN, *path, "--help"], capture_output=True, text=True)
    return r.stdout + r.stderr


def _subcommands(path: list[str]) -> list[str]:
    subs, in_cmds = [], False
    for line in _help(path).splitlines():
        if re.match(r"^\s*Available Commands:", line):
            in_cmds = True
            continue
        if in_cmds:
            if re.match(r"^[A-Z].*:$", line.strip()):
                break
            m = re.match(r"^\s{2,}([a-z][a-z0-9-]*)\s+\S", line)
            if m and m.group(1) not in ("help", "completion"):
                subs.append(m.group(1))
    return subs


def discover_leaves() -> set[str]:
    if not os.path.isfile(BIN):
        sys.exit(f"coverage_matrix: binary not found at {BIN} "
                 f"(run: go build -o ./dist/pve ./cmd/pve)")
    leaves: set[str] = set()

    def walk(path: list[str]) -> None:
        subs = _subcommands(path)
        if not subs:
            leaves.add(" ".join(path))
            return
        for s in subs:
            walk(path + [s])

    for top in _subcommands([]):
        walk([top])
    leaves |= RUNNABLE_PARENTS
    return leaves


# --------------------------------------------------------------------------- #
# leaf-mapping helpers                                                          #
# --------------------------------------------------------------------------- #
def make_mapper(leaves: set[str]):
    trees = {l.split()[0] for l in leaves}
    val_flags = {"--context", "-c", "-o", "--output", "--node", "--config", "--timeout"}

    def map_leaf(tokens: list[str]) -> str | None:
        # skip leading global flags + their values to find the tree token
        start, i = None, 0
        while i < len(tokens):
            t = tokens[i]
            if t in trees:
                start = i
                break
            if t in val_flags:
                i += 2
                continue
            i += 1
        if start is None:
            return None
        toks: list[str] = []
        for t in tokens[start:]:
            if t.startswith("-"):
                break
            toks.append(t)
        for n in range(len(toks), 0, -1):
            cand = " ".join(toks[:n])
            if cand in leaves:
                return cand
        return None

    return map_leaf


# --------------------------------------------------------------------------- #
# 2. read-only (e2e) intent                                                    #
# --------------------------------------------------------------------------- #
def parse_e2e(leaves, map_leaf):
    e2e = defaultdict(lambda: {"uncond": False, "cond": False, "help": False})
    errpath = set()  # leaves whose error contract is checked by `expect_fail`
    defers = {}  # leaf -> {"live_covered": bool, "reason": str, "na": bool}

    # Non-literal args (variables like `cfg`, `str(uid)`) become a placeholder so
    # a value-taking flag still consumes its (dynamic) value during mapping; a
    # bare drop would misalign `--config <var> init config` into `init` loss.
    PLACEHOLDER = "\x00"

    def literal_seq(node):
        """If node is a tuple/list of string literals, return the values, else None."""
        if isinstance(node, (ast.Tuple, ast.List)):
            vals = [e.value for e in node.elts
                    if isinstance(e, ast.Constant) and isinstance(e.value, str)]
            if vals and len(vals) == len(node.elts):
                return vals
        return None

    class V(ast.NodeVisitor):
        def __init__(self):
            self.depth = 0          # nesting under If / inventory-For (=> ◑)
            self.loopvars = {}      # name -> [literal values] for literal-tuple Fors

        def visit_If(self, n):
            self.depth += 1
            self.generic_visit(n)
            self.depth -= 1

        def visit_For(self, n):
            seq = literal_seq(n.iter) if isinstance(n.target, ast.Name) else None
            if seq is not None:
                # for x in ("a","b",...) : a fixed fan-out, still unconditional.
                self.loopvars[n.target.id] = seq
                self.generic_visit(n)
                del self.loopvars[n.target.id]
            else:
                # for x in <discovered inventory> : conditional (=> ◑).
                self.depth += 1
                self.generic_visit(n)
                self.depth -= 1

        def _arg_values(self, node):
            """Return the list of possible string values for one positional arg."""
            if isinstance(node, ast.Constant) and isinstance(node.value, str):
                return [node.value]
            if isinstance(node, ast.Name) and node.id in self.loopvars:
                return self.loopvars[node.id]
            return [PLACEHOLDER]

        def _expand(self, args):
            """Cartesian product of per-arg value lists -> list of token lists."""
            combos = [[]]
            for a in args:
                vals = self._arg_values(a)
                combos = [c + [v] for c in combos for v in vals]
            return combos

        def visit_Call(self, n):
            if isinstance(n.func, ast.Attribute):
                meth = n.func.attr
                if meth == "expect_fail":
                    # error-contract check: exercises the failure path, NOT a
                    # successful happy-path run, so it never sets ✓/◑.
                    for toks in self._expand(n.args[1:]):
                        leaf = map_leaf(toks)
                        if leaf:
                            errpath.add(leaf)
                elif meth in ("check", "check_formats"):
                    for toks in self._expand(n.args[1:]):
                        leaf = map_leaf(toks)
                        if not leaf:
                            continue
                        if any(t == "--help" for t in toks):
                            e2e[leaf]["help"] = True
                        elif self.depth == 0:
                            e2e[leaf]["uncond"] = True
                        else:
                            e2e[leaf]["cond"] = True
                elif meth == "defer":
                    def _cs(node):
                        return (node.value if isinstance(node, ast.Constant)
                                and isinstance(node.value, str) else None)
                    cmd = _cs(n.args[2]) if len(n.args) > 2 else None
                    reason = _cs(n.args[1]) if len(n.args) > 1 else ""
                    live = False
                    for k in n.keywords:
                        if k.arg == "live_covered" and isinstance(k.value, ast.Constant):
                            live = bool(k.value.value)
                    leaf = map_leaf(cmd.split()[1:]) if cmd and cmd.startswith("pve ") else None
                    if leaf:
                        na = any(m in (reason or "").lower() for m in NA_REASON_MARKERS)
                        # keep the strongest signal if a leaf is deferred twice
                        prev = defers.get(leaf)
                        if prev is None or (live and not prev["live_covered"]):
                            defers[leaf] = {"live_covered": live, "reason": reason or "", "na": na}
            self.generic_visit(n)

    for fn in sorted(os.listdir(TREES_DIR)):
        if fn.endswith(".py") and fn != "__init__.py":
            V().visit(ast.parse(open(os.path.join(TREES_DIR, fn)).read()))
    return e2e, errpath, defers


# --------------------------------------------------------------------------- #
# 3. mutate intent                                                             #
# --------------------------------------------------------------------------- #
def parse_mutate(leaves, map_leaf):
    mut = defaultdict(set)  # leaf -> {"PASS","SKIP"}
    step_fns = {"step", "soft_step", "cover_skip", "del_step", "step_raw"}
    tree = ast.parse(open(LIFECYCLE).read())

    def cs(node):
        return node.value if isinstance(node, ast.Constant) and isinstance(node.value, str) else None

    for n in ast.walk(tree):
        if not isinstance(n, ast.Call):
            continue
        guest = verb = None
        argv: list[str] = []
        status = "PASS"
        if isinstance(n.func, ast.Name) and n.func.id == "Step":
            a = n.args
            guest = cs(a[0]) if a else None
            verb = cs(a[1]) if len(a) > 1 else None
            if len(a) > 2 and isinstance(a[2], ast.Name):
                status = a[2].id
        elif isinstance(n.func, ast.Attribute) and n.func.attr in step_fns:
            a = n.args
            guest = cs(a[0]) if a else None
            verb = cs(a[1]) if len(a) > 1 else None
            argv = [cs(x) for x in a[3:] if cs(x) is not None]
            if n.func.attr == "cover_skip":
                status = "SKIP"
        if not verb:
            continue
        leaf = map_leaf(argv) if argv else None
        if not leaf and guest:
            leaf = map_leaf([guest, *verb.split()])
        if not leaf:
            leaf = map_leaf(verb.split())
        if not leaf:
            cands = [l for l in leaves if l == verb or l.endswith(" " + verb)]
            if len(cands) == 1:
                leaf = cands[0]
        if leaf:
            mut[leaf].add("PASS" if status == "PASS" else "SKIP")
    return mut


# --------------------------------------------------------------------------- #
# classification + rendering                                                   #
# --------------------------------------------------------------------------- #
def classify(leaf, e2e, mut, defers, errpath):
    ec = e2e.get(leaf, {})
    if ec.get("uncond"):
        e_cell = "✓"
    elif ec.get("cond"):
        e_cell = "◑"
    else:
        e_cell = "—"
    m = mut.get(leaf, set())
    if "PASS" in m:
        m_cell = "✓"
    elif "SKIP" in m:
        m_cell = "·"
    else:
        m_cell = "—"

    note = ""
    covered = e_cell in ("✓", "◑") or m_cell in ("✓", "·")
    if not covered:
        d = defers.get(leaf)
        if d and d["na"]:
            note = "n/a — " + _short(d["reason"])
        elif d and d["live_covered"]:
            # deferred in the sweep but driven by the mutate phase — mark covered
            m_cell = "✓"
            note = "live via mutate phase"
        elif d:
            note = "deferred — " + _short(d["reason"])
        elif ec.get("help"):
            note = "help-only (parse smoke test)"
        elif leaf in errpath:
            note = "error-contract checked (happy path not run)"
        else:
            note = "**uncovered**"
    elif leaf in errpath and e_cell == "—":
        # happy path covered by mutate; error path also guarded by the sweep
        note = "error-contract checked"
    return e_cell, m_cell, note


def _short(reason: str) -> str:
    # Strip the "— covered live ..." suffix (redundant with the live-via-mutate
    # note) but keep the full deferral/n-a rationale — truncating it mid-word
    # dropped the very reason the leaf is deferred.
    return reason.split(" — covered live")[0].strip()


def bucket(leaf, e_cell, m_cell, note):
    if e_cell in ("✓", "◑") and m_cell == "✓":
        return "both"
    if e_cell in ("✓", "◑"):
        return "e2e"
    if m_cell in ("✓", "·"):
        return "mutate"
    if note.startswith("n/a"):
        return "na"
    if note.startswith("deferred") or note.startswith("help-only"):
        return "deferred"
    if note.startswith("error-contract"):
        return "erroronly"
    return "uncovered"


def render(leaves, e2e, mut, defers, errpath) -> str:
    rows = {}
    for leaf in leaves:
        e_cell, m_cell, note = classify(leaf, e2e, mut, defers, errpath)
        rows[leaf] = (e_cell, m_cell, note, bucket(leaf, e_cell, m_cell, note))

    by_tree = defaultdict(list)
    for leaf in sorted(leaves):
        by_tree[leaf.split()[0]].append(leaf)

    # summary counts
    def tcount(tree):
        c = Counter()
        for leaf in by_tree[tree]:
            e_cell, m_cell, _note, _b = rows[leaf]
            if e_cell == "✓":
                c["e2e_full"] += 1
            elif e_cell == "◑":
                c["e2e_cond"] += 1
            if m_cell == "✓":
                c["mut_full"] += 1
            elif m_cell == "·":
                c["mut_skip"] += 1
            c[_b] += 1
        c["total"] = len(by_tree[tree])
        return c

    out = [HEADER]
    # ---- summary table ----
    out.append("## Coverage summary\n")
    out.append("| Tree | Leaves | e2e ✓ | e2e ◑ | mutate ✓ | mutate · | deferred | n/a | uncovered |")
    out.append("|------|-------:|------:|------:|---------:|---------:|---------:|----:|----------:|")
    tot = Counter()
    for tree in sorted(by_tree):
        c = tcount(tree)
        out.append(f"| `{tree}` | {c['total']} | {c['e2e_full']} | {c['e2e_cond']} | "
                   f"{c['mut_full']} | {c['mut_skip']} | {c['deferred']} | {c['na']} | "
                   f"{c['uncovered']} |")
        for k, v in c.items():
            tot[k] += v
    out.append(f"| **Total** | **{tot['total']}** | **{tot['e2e_full']}** | **{tot['e2e_cond']}** | "
               f"**{tot['mut_full']}** | **{tot['mut_skip']}** | **{tot['deferred']}** | "
               f"**{tot['na']}** | **{tot['uncovered']}** |")
    covered = tot['both'] + tot['e2e'] + tot['mutate']
    out.append("")
    out.append(f"Leaf commands are counted from a walk of the built command tree "
               f"(`pve <tree> … --help`); each `create`/`delete` and `get`/`set` verb is its "
               f"own leaf. Of **{tot['total']}** leaves, **{covered}** are exercised by at least "
               f"one live suite, **{tot['deferred']}** are deferred from the live suites "
               f"(irreversible, interactive, or environment-bound — covered by unit tests), "
               f"**{tot['na']}** are n/a by design, and **{tot['uncovered']}** are not yet "
               f"exercised by either suite — see [Uncovered leaves](#uncovered-leaves).")
    out.append("")

    # ---- per-tree detail ----
    for tree in sorted(by_tree):
        out.append(f"## `{tree}`\n")
        out.append("| Leaf | e2e | mutate | Notes |")
        out.append("|------|-----|--------|-------|")
        for leaf in by_tree[tree]:
            e_cell, m_cell, note, _b = rows[leaf]
            out.append(f"| `{leaf}` | {e_cell} | {m_cell} | {note} |")
        out.append("")

    # ---- uncovered appendix ----
    out.append("## Uncovered leaves\n")
    out.append("Leaves exercised by neither suite. These are genuine coverage gaps — "
               "candidates for read-only sweep checks (the `get`/`list`/`show` verbs) or "
               "isolated mutate-phase coverage (the `create`/`set`/`delete` verbs). Each is "
               "listed inline per tree for a compact gap view.\n")
    any_unc = False
    for tree in sorted(by_tree):
        unc = [l for l in by_tree[tree] if rows[l][3] == "uncovered"]
        if not unc:
            continue
        any_unc = True
        spans = ", ".join(f"`{l}`" for l in unc)
        out.append(f"**`{tree}`** ({len(unc)}) — {spans}")
        out.append("")
    if not any_unc:
        out.append("_None — every leaf is exercised or explicitly deferred._\n")

    out.append(FOOTER)
    return "\n".join(out) + "\n"


HEADER = """# Test Coverage Matrix

> **Generated file — do not edit by hand.** Regenerate with
> `go build -o ./dist/pve ./cmd/pve && python3 scripts/coverage_matrix.py`.
> The classification is derived statically from the built command tree, the
> read-only sweep definitions in `scripts/e2e_lib/trees/*.py`, and the mutate
> phase in `scripts/e2e_lib/lifecycle.py`, so it stays correct as commands and
> tests change.

This document maps every invocable leaf command to its automated test coverage
across the two live suites:

- **e2e** (`scripts/e2e`, `make test-e2e`) — a read-only, parallel happy-path
  sweep against a configured context. Mutating operations are never executed;
  they are recorded as deferred. The `pbs` tree is opt-in: it runs only when
  `--pbs-context` (or `make test-e2e PBS_CONTEXT=…`) names a configured
  `product: pbs` context whose server is reachable, so all of its leaves are
  prerequisite-gated (◑).

- **lifecycle / mutate** (`scripts/lifecycle`, `make test-lifecycle`, or
  `scripts/e2e --mutate`) — the destructive counterpart. It provisions an
  isolated SDN zone and resource pool, drives the mutating sub-commands on
  purpose-built throwaway resources, records each verb, and tears everything
  down.

A third tree, **negative** (`scripts/e2e_lib/trees/negative.py`), asserts the
CLI's error contract: bad input must fail cleanly (non-zero exit plus a useful
message). It never mutates, so it does not set a happy-path ✓; leaves whose
failure path it guards are tagged `error-contract checked` in the Notes column.

## Legend

- **e2e ✓** — exercised unconditionally by the read-only sweep on every run.

- **e2e ◑** — exercised by the sweep only when prerequisite inventory exists
  (a VM, user, vnet, …); otherwise skipped (a skip still passes, exit 0).

- **mutate ✓** — driven live by the mutate phase on a purpose-built resource.

- **mutate ·** — driven by the mutate phase but recorded as SKIP because the
  host/guest cannot complete it (the reason is recorded); not a CLI gap.

- **—** — not exercised by that suite (a mutating verb is `—` for e2e because
  the read sweep never mutates; a read verb is `—` for mutate).

- **Notes** — `live via mutate phase` (deferred in the sweep, driven by
  `--mutate`), `deferred — …` (intentionally not run live, with reason),
  `n/a — …` (interactive or host-daemon, no automated coverage by design),
  `help-only` (only the `--help` parse is checked), `error-contract checked`
  (the failure path is guarded by the negative tree), or **uncovered** (a
  genuine gap, listed in the appendix).

## Isolation contract

Every resource the lifecycle suite creates is shielded from other lab efforts
(see `scripts/e2e_lib/model.py`, the single source of truth):

- named or hostnamed with the `pve-cli-` prefix,

- placed in the `pve-cli` resource pool and tagged `pve-cli`,

- attached to a dedicated `pvecli` simple SDN zone and `pvecli0` vnet on the
  `172.30.0.0/24` subnet, deliberately off the host management network.

Teardown runs in a `finally` block and is idempotent: a crashed prior run is
swept clean before the next provisions.
"""

FOOTER = """## Running the suites

```bash
make test-e2e                  # all trees, read-only, against the `lab` context
make test-e2e TREES=qemu       # a subset
make test-e2e CONTEXT=prod     # a different configured context
make test-e2e PBS_CONTEXT=pbs-lab  # opt into the pbs tree (needs a `product: pbs` context)
scripts/e2e --list             # list trees and the isolation contract

make test-e2e-mutate           # read-only sweep + the destructive verb matrix
make test-lifecycle            # the destructive verb matrix only, against `lab`
scripts/e2e --mutate --vm-only # sweep + VM verb matrix (skip the container)
scripts/lifecycle --vm-only    # VM verb matrix only
scripts/lifecycle --ct-only    # container verb matrix only
```

Both suites skip gracefully (exit 0) when no context is configured; pass
`--strict` to fail instead. The mutate phase prints a per-guest coverage table
listing every verb it drove and its result.
"""


def main():
    leaves = discover_leaves()
    map_leaf = make_mapper(leaves)
    e2e, errpath, defers = parse_e2e(leaves, map_leaf)
    mut = parse_mutate(leaves, map_leaf)
    md = render(leaves, e2e, mut, defers, errpath)
    open(OUT, "w").write(md)
    print(f"coverage_matrix: wrote {OUT} ({len(leaves)} leaves)")


if __name__ == "__main__":
    main()
