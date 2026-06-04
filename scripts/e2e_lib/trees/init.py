"""init: config scaffold. Exercised against a throwaway path so the user's
real ~/.config/pve/config.yml is never touched."""

from __future__ import annotations

import os
import tempfile

from ..context import Ctx
from ..model import Status

NAME = "init"
DESCRIPTION = "Scaffold local CLI configuration"


def run(ctx: Ctx) -> None:
    with tempfile.TemporaryDirectory(prefix="pve-e2e-init-") as tmp:
        cfg = os.path.join(tmp, "config.yml")
        # --config (global) selects the destination; -o suppressed (human output).
        res = ctx.check(
            "config (temp path)",
            "--config", cfg, "init", "config",
            fmt="",
        )
        if res.rc == 0 and not os.path.isfile(cfg):
            ctx.results[-1].status = Status.FAIL
            ctx.results[-1].detail = "init config reported success but wrote no file"
