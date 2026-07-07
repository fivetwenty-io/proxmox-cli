"""negative: error-contract checks.

The rest of the sweep proves commands SUCCEED on the happy path. This tree
proves they FAIL cleanly on bad input — non-zero exit plus a useful message —
which guards the CLI's error surface (usage errors, missing required flags,
invalid flag values, and not-found lookups).

Every check here is non-mutating: usage/flag errors are rejected before any API
call, and the not-found lookups are reads. Names deliberately use the
`pve-cli-` isolation prefix so even a hypothetical side effect stays identifiable.
"""

from __future__ import annotations

from ..context import Ctx
from ..model import Isolation

NAME = "negative"
DESCRIPTION = "Error-contract checks: bad input must fail cleanly"

# A VMID/name guaranteed not to exist in the lab.
MISSING_VMID = "999999999"
MISSING_NAME = Isolation.NAME_PREFIX + "nonexistent-zzz"


def run(ctx: Ctx) -> None:
    # --- usage / flag validation (rejected before any API call) -------------

    # Missing required flag: `pool create` requires --poolid.
    ctx.expect_fail("pool create without --poolid",
                    "pool", "create", must_contain="poolid")

    # Missing positional arg: `qemu status` requires a <vmid>.
    ctx.expect_fail("qemu status without vmid", "qemu", "status")

    # Too few positional args: `qemu snapshot create` needs <vmid> <name>.
    ctx.expect_fail("qemu snapshot create missing name",
                    "qemu", "snapshot", "create", MISSING_VMID)

    # Invalid flag type: --max takes an integer.
    ctx.expect_fail("cluster log with non-numeric --max",
                    "cluster", "log", "--max", "not-a-number")

    # Unknown output format must be rejected by the renderer.
    ctx.expect_fail("unknown output format", "version", "-o", "bogus",
                    must_contain="format")

    # Deleting without the required confirmation flag must refuse.
    ctx.expect_fail("group delete without --yes",
                    "access", "group", "delete", MISSING_NAME)

    # --- product guard (rejected before any API call) ------------------------

    # The sweep context targets Proxmox VE, so every `pve pbs` command must be
    # refused with a pointer to `context add --product pbs`. This runs without
    # any PBS server — the guard reads only the local context config.
    ctx.expect_fail("pbs command against a PVE context",
                    "pbs", "ping", must_contain="requires a PBS context")

    # --- not-found lookups (reads; never mutate) ----------------------------

    n = ctx.node
    if n:
        ctx.expect_fail("qemu status of missing VM",
                        "qemu", "status", MISSING_VMID, node=n)
    else:
        ctx.skip("qemu status of missing VM", "no node discovered")

    ctx.expect_fail("storage get of missing storage",
                    "storage", "get", MISSING_NAME)
    ctx.expect_fail("access user get of missing user",
                    "access", "user", "get", MISSING_NAME + "@pam")
    ctx.expect_fail("pool get of missing pool",
                    "pool", "get", MISSING_NAME)
