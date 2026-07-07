"""task: task inspection (read-only happy path).

`task list` is read-only; `task log` is exercised against a real UPID drawn
from the list. `wait`/`stop` are deferred (stop is destructive; wait blocks).
"""

from __future__ import annotations

from ..context import CmdResult, Ctx

NAME = "task"
DESCRIPTION = "Inspect and control Proxmox VE tasks"


def run(ctx: Ctx) -> None:
    n = ctx.node
    if not n:
        ctx.skip("list", "no node discovered")
        return

    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    lst = ctx.check("list", "task", "list", node=n, validate=is_list)
    # cluster-list: the cluster-wide equivalent of `list` (GET /cluster/tasks),
    # unconditionally safe on any cluster (empty array on one with no tasks).
    ctx.check("cluster-list", "task", "cluster-list", validate=is_list)

    upid = None
    if lst.rc == 0:
        try:
            upid = ctx.first(lst.json(), "upid") or ctx.first(lst.json(), "id")
        except ValueError:
            upid = None

    if upid is None:
        ctx.skip("log", "no task in list")
        ctx.skip("status", "no task in list")
    else:
        ctx.check("log", "task", "log", str(upid), node=n)
        # status: the node is parsed from the UPID itself, so no --node is needed.
        ctx.check("status", "task", "status", str(upid))

    ctx.defer("wait", "blocks until a task finishes", "pmx task wait <upid> --node <node>")
    # `stop` aborts a running task; it stays deferred in this read-only sweep but
    # is exercised live by the mutate phase against a deterministic server-side
    # shutdown task.
    ctx.defer("stop", "aborts a running task — covered live by `e2e --mutate`",
              "pmx task stop <upid> --node <node>", live_covered=True)
