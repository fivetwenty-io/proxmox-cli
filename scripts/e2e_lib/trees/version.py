"""version: cluster API version + CLI build info. All read-only."""

from __future__ import annotations

from ..context import CmdResult, Ctx

NAME = "version"
DESCRIPTION = "Cluster API version and CLI build info"


def run(ctx: Ctx) -> None:
    def has_version(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, dict) and (data.get("version") or data.get("release")):
            return None
        return "response missing version/release"

    ctx.check("cluster api version", "version", validate=has_version)
    ctx.check("cli build info", "version", "client")
    # Renderer smoke test: the key/value (Single) shape must render in every
    # `-o` format, not just the json the rest of the sweep uses.
    ctx.check_formats("render formats (version)", "version")
