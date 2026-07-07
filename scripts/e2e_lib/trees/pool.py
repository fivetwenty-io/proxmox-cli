"""pool: resource pools (read-only happy path).

The lab may have zero pools; an empty list still passes. Pool create/set/delete
is deferred and, when implemented, uses the `pve-cli` pool name so it never
touches a pool owned by another effort on the shared lab.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx
from ..model import Isolation

NAME = "pool"
DESCRIPTION = "Manage resource pools"


def run(ctx: Ctx) -> None:
    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    lst = ctx.check("list", "pool", "list", validate=is_list)

    pid = None
    if lst.rc == 0:
        try:
            pid = ctx.first(lst.json(), "poolid") or ctx.first(lst.json(), "id")
        except ValueError:
            pid = None

    if pid is None:
        ctx.skip("get", "no pool defined")
        ctx.skip("show", "no pool defined")
        ctx.skip("permissions list", "no pool defined")
        ctx.skip("permissions effective", "no pool defined")
    else:
        ctx.check("get", "pool", "get", str(pid))
        # show: the deprecated-but-still-live single-item endpoint
        # (GET /pools/{poolid}), distinct from `get`'s list-filtered endpoint.
        ctx.check("show", "pool", "show", str(pid))

        # permissions: ACL entries scoped to the pool's singular /pool/{poolid}
        # path (note: singular "pool", unlike the "pools" object tree noun).
        # `grant`/`revoke` mutate cluster-wide ACLs and are deferred below.
        def has_permissions_effective(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), dict) else "expected a JSON object"

        ctx.check("permissions list", "pool", "permissions", "list", str(pid),
                  validate=is_list)
        ctx.check("permissions effective", "pool", "permissions", "effective", str(pid),
                  validate=has_permissions_effective)

    # The mutate phase provisions the `pve-cli` pool and deletes it in teardown,
    # so create + delete are exercised live by it. `set` is not yet driven.
    ctx.defer(
        "create",
        "creates a resource pool — covered live by `e2e --mutate`",
        f"pve pool create {Isolation.POOL}",
        isolation=True, live_covered=True,
    )
    ctx.defer("set", "modifies pool members/comment", f"pve pool set {Isolation.POOL} ...")
    ctx.defer("delete", "deletes a resource pool — covered live by `e2e --mutate`",
              f"pve pool delete {Isolation.POOL}", isolation=True, live_covered=True)
    # `permissions grant`/`revoke` mutate cluster-wide ACLs; not wired into the
    # mutate phase. `permissions list`/`effective` above are read-only and
    # exercised live.
    ctx.defer(
        "permissions grant",
        "grants ACL roles on the pool's singular /pool/{poolid} path; mutates "
        "cluster-wide ACLs, not wired into the mutate phase; covered by unit tests",
        "pve pool permissions grant <poolid> --roles PVEPoolAdmin --users alice@pve",
    )
    ctx.defer(
        "permissions revoke",
        "revokes ACL roles on the pool's singular /pool/{poolid} path; mutates "
        "cluster-wide ACLs, not wired into the mutate phase; covered by unit tests",
        "pve pool permissions revoke <poolid> --roles PVEPoolAdmin --users alice@pve",
    )
