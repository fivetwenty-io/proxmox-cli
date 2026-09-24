"""Child-process environment for every e2e-spawned `pmx` invocation.

A developer's shell often carries exported `PMX_API_*` overrides (host, port,
proxy, timeouts, ...) for day-to-day use against a real cluster. Left in the
inherited environment, one of those can silently steer a sweep — pointing the
up-front node discovery, or a whole destructive suite, at a connection the
operator never asked the harness to use. `child_env()` strips every
`PMX_API_*` name before any spawned `pmx` process ever sees it.

Leaf module: stdlib only, and it must never import `runner` or `context`.
`runner.py` already imports `context.py` at module scope, so `context.py`
importing this module back would form a cycle that breaks every suite with an
`ImportError`. Sitting one level below both keeps the import graph a DAG.
"""

from __future__ import annotations

import os

# The terminal width pmx prefers over a real tty size, so pinning it makes
# every table rendering reproducible off a tty and lets the render audit
# assert against a known budget. Mirrors e2e_lib.render.BUDGET, which this
# leaf module cannot import (stdlib-only); keep the two in sync by hand.
COLUMNS = 120


def child_env() -> dict[str, str]:
    """`os.environ`, with every `PMX_API_*` name dropped and `$COLUMNS` pinned.

    Every e2e-spawned `pmx` process — the read-only sweep's checks, the
    PVE/PBS/PDM lifecycle suites, and the up-front context/node discovery —
    takes its environment from here, so none of them can inherit an operator's
    exported connection override.
    """
    env = {name: value for name, value in os.environ.items()
            if not name.startswith("PMX_API_")}
    env["COLUMNS"] = str(COLUMNS)
    return env
