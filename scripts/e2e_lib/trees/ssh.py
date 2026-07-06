"""ssh: top-level SSH passthrough wrapper (`pve ssh <node> [ssh-option...] [command...]`).

Delegates to the same `remote.RunSSH` code path as `pve node ssh` (see
`node.py`), so every invocation execs the real `ssh` binary and opens a live
session on the resolved node. `pve node ssh` already exercises that shared
code path live and SSH-gated in `scripts/lifecycle.py`; this top-level alias
is not yet wired into the mutate phase itself, so it stays deferred here and
is covered by unit tests (`internal/cli/remote/ssh_test.go`).
"""

from __future__ import annotations

from ..context import Ctx

NAME = "ssh"
DESCRIPTION = "SSH passthrough to a resolved node address"


def run(ctx: Ctx) -> None:
    ctx.defer(
        "ssh",
        "opens a live SSH session on the resolved node, so it cannot be driven "
        "head-less by the read-only sweep; shares the `pve node ssh` code path "
        "(SSH-gated live coverage there) but this top-level alias is not yet "
        "wired into the mutate phase; covered by unit tests",
        "pve ssh <node> -- true",
    )
