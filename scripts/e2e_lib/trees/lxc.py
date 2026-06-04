"""lxc: container inventory + per-CT read-only inspection.

The lab may have zero containers; an empty list still passes. Lifecycle is
deferred under the same isolation contract as qemu.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx
from ..model import Isolation

NAME = "lxc"
DESCRIPTION = "Manage LXC containers"


def run(ctx: Ctx) -> None:
    n = ctx.node
    if not n:
        ctx.skip("list", "no node discovered")
        return

    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    lst = ctx.check("list", "lxc", "list", node=n, validate=is_list)
    ctx.check("template list", "lxc", "template", "list", node=n, validate=is_list)

    ctid = None
    if lst.rc == 0:
        try:
            ctid = ctx.first(lst.json(), "vmid")
        except ValueError:
            ctid = None

    def has_status(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        missing = [k for k in ("status", "vmid") if k not in data]
        return f"status response missing keys: {missing}" if missing else None

    if ctid is None:
        ctx.skip("status", "no container on node")
        ctx.skip("config get", "no container on node")
        ctx.skip("snapshot list", "no container on node")
    else:
        cid = str(ctid)
        ctx.check("status", "lxc", "status", cid, node=n, validate=has_status)
        ctx.check("config get", "lxc", "config", "get", cid, node=n)
        ctx.check("snapshot list", "lxc", "snapshot", "list", cid, node=n)

    # Every mutating verb below — including the full power-state matrix and
    # snapshot create/rollback/delete — is exercised live on a purpose-built
    # Alpine container by the mutate phase (`scripts/e2e --mutate` /
    # `scripts/lifecycle`).
    ctx.defer(
        "create",
        "creates a container — covered live by `e2e --mutate`",
        f"pve lxc create ... --pool {Isolation.POOL} --tags {Isolation.TAG}",
        isolation=True, live_covered=True,
    )
    ctx.defer("start/stop/shutdown/reboot/suspend/resume",
              "changes CT power state — covered live by `e2e --mutate`",
              "pve lxc start <ctid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("delete", "destroys a container — covered live by `e2e --mutate`",
              "pve lxc delete <ctid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("snapshot create/rollback/delete",
              "mutates CT snapshots — covered live by `e2e --mutate`",
              "pve lxc snapshot create <ctid> <name>", isolation=True, live_covered=True)
