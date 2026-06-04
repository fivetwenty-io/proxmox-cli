"""node: node inventory + per-node read-only state.

SSH/rsync/shell/exec/console and service control are deferred: they need a
remote login or mutate the host, so they are not part of the happy-path sweep.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx

NAME = "node"
DESCRIPTION = "Manage Proxmox VE nodes"


def run(ctx: Ctx) -> None:
    def has_node(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list) and data:
            return None
        return "no nodes returned"

    ctx.check("list", "node", "list", validate=has_node)

    # These subcommands take the node as a positional argument.
    n = ctx.node
    if not n:
        ctx.skip("status", "no node discovered")
        ctx.skip("services list", "no node discovered")
        ctx.skip("task list", "no node discovered")
        ctx.skip("task log", "no node discovered")
        ctx.skip("task wait", "no node discovered")
    else:
        def has_node_status(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            missing = [k for k in ("memory", "pveversion") if k not in data]
            return f"node status missing keys: {missing}" if missing else None

        ctx.check("status", "node", "status", n, validate=has_node_status)
        svc = ctx.check("services list", "node", "services", "list", n)
        if svc.rc == 0:
            try:
                name = ctx.first(svc.json(), "service") or ctx.first(svc.json(), "name")
            except ValueError:
                name = None
            if name:
                ctx.check("services get", "node", "services", "get", n, str(name))
            else:
                ctx.skip("services get", "no service to inspect")
        tasks = ctx.check("task list", "node", "task", "list", n)
        # `node task log` reads one task's log by UPID; conditional on the list
        # returning a task (◑). Mirrors the top-level `task log` check.
        upid = None
        if tasks.rc == 0:
            try:
                upid = ctx.first(tasks.json(), "upid") or ctx.first(tasks.json(), "id")
            except ValueError:
                upid = None
        if upid:
            ctx.check("task log", "node", "task", "log", n, str(upid))
            # `node task wait` against the same (already-finished) UPID returns
            # immediately — WaitForUPID sees a `stopped` task, so no hang (◑).
            # The verb takes only <upid> (node is parsed from the UPID).
            ctx.check("task wait", "node", "task", "wait", str(upid), "--timeout", "30")
        else:
            ctx.skip("task log", "no task in the node task list")
            ctx.skip("task wait", "no task in the node task list")

    # `node task stop` aborts a running task; it stays deferred in this
    # read-only sweep but is exercised live by the mutate phase (which spawns a
    # deterministic server-side shutdown task and aborts it).
    ctx.defer("task stop", "aborts a running task — covered live by `e2e --mutate`",
              "pve node task stop <node> <upid>", live_covered=True)

    # exec/ssh/rsync are exercised live by the mutate phase, SSH-gated: it
    # probes reachability and records SKIP if the host is unreachable.
    ctx.defer("exec", "runs a command on the host — covered live by `e2e --mutate` (SSH-gated)",
              "pve node exec <node> -- true", isolation=True, live_covered=True)
    ctx.defer("ssh", "remote login — covered live by `e2e --mutate` (SSH-gated)",
              "pve node ssh <node> -- true", isolation=True, live_covered=True)
    ctx.defer("rsync", "transfers files to/from host — covered live by `e2e --mutate` (SSH-gated)",
              "pve node rsync <node> <node>:<src> <dst>", isolation=True, live_covered=True)
    # Genuinely out of scope: interactive PTYs and real host-daemon control.
    ctx.defer("shell / console", "interactive session; not automatable", "pve node shell <node>")
    ctx.defer(
        "services start/stop/restart/reload",
        "mutates real host daemons on a shared lab",
        "pve node services restart <svc> --node <node>",
    )
