"""logs: local JSONL command-log maintenance.

`prune` is the only leaf. It deletes files under ~/.pmx/logs, so the sweep
drives it exclusively through `--dry-run`, which reports what it would remove
and touches nothing. The two refusal paths — a negative age and no cutoff at
all — round out the contract.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx

NAME = "logs"
DESCRIPTION = "Local command-log maintenance"


def run(ctx: Ctx) -> None:
    def is_dry_run_report(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        if data.get("dry_run") is not True:
            return "report does not record dry_run"
        for key in ("files", "dirs", "bytes"):
            if not isinstance(data.get(key), int):
                return f"report has no integer {key!r} count"
        return None

    # An age no log can exceed, so the dry run reports a plan over every file
    # present without ever selecting one for deletion had --dry-run been
    # dropped. `--no-log` is already injected, so this run adds no file itself.
    ctx.check(
        "prune (dry run)",
        "logs", "prune", "--older-than", "36500", "--dry-run",
        with_context=False, validate=is_dry_run_report,
    )
    ctx.check_formats(
        "prune (dry run, formats)",
        "logs", "prune", "--older-than", "36500", "--dry-run",
    )

    ctx.expect_fail(
        "prune (negative age)", "logs", "prune", "--older-than", "-1",
        must_contain="positive", with_context=False,
    )
    # With no --older-than, no --empty, and no log.retention configured there
    # is no cutoff to prune against; the command must say so rather than
    # default to deleting something.
    ctx.expect_fail(
        "prune (no cutoff)", "--config", "/dev/null", "logs", "prune",
        must_contain="nothing to prune", with_context=False,
    )
