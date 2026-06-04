"""cluster: cluster-wide read-only state."""

from __future__ import annotations

from ..context import CmdResult, Ctx

NAME = "cluster"
DESCRIPTION = "Inspect Proxmox VE cluster state"


def run(ctx: Ctx) -> None:
    def nonempty_list(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list):
            return None
        return "expected a JSON array"

    ctx.check("status", "cluster", "status", validate=nonempty_list)
    ctx.check("resources", "cluster", "resources", validate=nonempty_list)
    ctx.check("next free id", "cluster", "next-id")
    ctx.check("log (max 5)", "cluster", "log", "--max", "5")
    ctx.check("recent tasks", "cluster", "tasks")
    # Renderer smoke test: the tabular (Headers/Rows) shape must render in every
    # `-o` format, complementing version's key/value smoke test.
    ctx.check_formats("render formats (cluster status)", "cluster", "status")
