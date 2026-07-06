"""rsync: top-level rsync wrapper (`pve rsync [flags] <rsync-arg>...`).

Delegates to the same SSH-transport plumbing as `pve node rsync` (resolves a
`node:path` operand to the cluster address and injects `-e "ssh ..."`; see
`node.py`), so every invocation execs the real `rsync`/`ssh` binaries and
transfers files against a live node. `pve node rsync` already exercises that
shared code path live and SSH-gated in `scripts/lifecycle.py`; this top-level
alias is not yet wired into the mutate phase itself, so it stays deferred here
and is covered by unit tests (`internal/cli/remote/rsync_test.go`,
`internal/cli/remote/rsyncargs_test.go`).
"""

from __future__ import annotations

from ..context import Ctx

NAME = "rsync"
DESCRIPTION = "Transfer files to/from a resolved node via rsync over SSH"


def run(ctx: Ctx) -> None:
    ctx.defer(
        "rsync",
        "transfers files to/from a live node over SSH, so it cannot be driven "
        "head-less by the read-only sweep; shares the `pve node rsync` code "
        "path (SSH-gated live coverage there) but this top-level alias is not "
        "yet wired into the mutate phase; covered by unit tests",
        "pve rsync <node>:/tmp/pve-cli-e2e-rsync /tmp/pve-cli-e2e-rsync",
    )
