"""context: named connection context management (config-only).

All checks in this tree operate against a throwaway scratch `--config` file in
a temp dir. No live Proxmox API is contacted. `with_context=False` is used
throughout so the real config and configured context are never touched.

Verbs covered: add, ls, show, select (by name), select '-' (previous alias),
previous, copy, update, validate, rm (non-active), rm-active guard.

Deferred: edit (requires $EDITOR / TTY interaction).
"""

from __future__ import annotations

import os
import shutil
import tempfile

from ..context import CmdResult, Ctx

NAME = "context"
DESCRIPTION = "Manage named connection contexts (config-only)"


def run(ctx: Ctx) -> None:
    _scratch_context_checks(ctx)

    ctx.defer(
        "context edit",
        "requires $EDITOR / interactive TTY — not safe to drive in headless e2e; "
        "covered in unit tests via EDITOR=true trick (test-strategy §4.2)",
        "pmx context edit <name>",
    )


def _scratch_context_checks(ctx: Ctx) -> None:
    """Drive all context verbs against a throwaway config file.

    Uses `--config <scratch>` and `with_context=False` so the configured lab
    context is never involved. Scratch dir removed in `finally`.
    """
    probe = "pmx-cli-ctx-probe"
    probe2 = "pmx-cli-ctx-probe2"

    def has_probe(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list) and any(
            isinstance(t, dict) and t.get("name") == probe for t in data
        ):
            return None
        return f"scratch context {probe!r} not listed after add"

    def probe_active(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list) and any(
            isinstance(t, dict) and t.get("name") == probe and t.get("active")
            for t in data
        ):
            return None
        return f"context {probe!r} not marked active after select"

    def probe2_active(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list) and any(
            isinstance(t, dict) and t.get("name") == probe2 and t.get("active")
            for t in data
        ):
            return None
        return f"context {probe2!r} not marked active after select"

    def secret_redacted(res: CmdResult) -> str | None:
        data = res.json()
        secret = data.get("secret", "")
        if secret and secret != "***":
            return f"secret not redacted in show output: {secret!r}"
        return None

    def updated_node(res: CmdResult) -> str | None:
        data = res.json()
        node = data.get("default_node", "")
        if node == "e2e-node":
            return None
        return f"default-node not updated, got {node!r}"

    def validate_ok(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list) and any(
            isinstance(v, dict) and v.get("name") == probe and v.get("status") == "OK"
            for v in data
        ):
            return None
        return f"validate did not report status OK for {probe!r}"

    def copy_present(res: CmdResult) -> str | None:
        data = res.json()
        copy_name = probe + "-copy"
        if isinstance(data, list) and any(
            isinstance(t, dict) and t.get("name") == copy_name for t in data
        ):
            return None
        return f"copied context {copy_name!r} not listed after copy"

    def probe_absent(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list) and not any(
            isinstance(t, dict) and t.get("name") == probe for t in data
        ):
            return None
        return f"context {probe!r} still listed after rm"

    scratch_dir = tempfile.mkdtemp(prefix="pmx-cli-e2e-ctx-")
    cfg = os.path.join(scratch_dir, "config.yml")
    try:
        # -- add two contexts --------------------------------------------------
        ctx.check(
            "context add", "--config", cfg, "context", "add", probe,
            "--host", "127.0.0.1", "--username", "root@pam",
            "--token-id", "e2e", "--secret", "00000000-0000-0000-0000-000000000000",
            "--insecure",
            with_context=False, fmt="",
        )
        ctx.check(
            "context add #2", "--config", cfg, "context", "add", probe2,
            "--host", "127.0.0.2", "--username", "root@pam",
            "--token-id", "e2e", "--secret", "00000000-0000-0000-0000-000000000000",
            "--insecure",
            with_context=False, fmt="",
        )

        # -- ls ----------------------------------------------------------------
        ctx.check(
            "context ls", "--config", cfg, "context", "ls",
            with_context=False, validate=has_probe,
        )

        # -- show (secret redaction visible) -----------------------------------
        ctx.check(
            "context show", "--config", cfg, "context", "show", probe,
            with_context=False, validate=secret_redacted,
        )

        # -- select by name (first select so probe becomes current) ------------
        ctx.check(
            "context select probe", "--config", cfg, "context", "select", probe,
            with_context=False, fmt="",
        )
        ctx.check(
            "context ls (probe active)", "--config", cfg, "context", "ls",
            with_context=False, validate=probe_active,
        )

        # -- select by name (second select so previous is set) -----------------
        ctx.check(
            "context select probe2", "--config", cfg, "context", "select", probe2,
            with_context=False, fmt="",
        )
        ctx.check(
            "context ls (probe2 active)", "--config", cfg, "context", "ls",
            with_context=False, validate=probe2_active,
        )

        # -- select '-' (previous alias: back to probe) ------------------------
        ctx.check(
            "context select '-'", "--config", cfg, "context", "select", "-",
            with_context=False, fmt="",
        )
        ctx.check(
            "context ls (probe active again)", "--config", cfg, "context", "ls",
            with_context=False, validate=probe_active,
        )

        # -- previous (back to probe2) -----------------------------------------
        ctx.check(
            "context previous", "--config", cfg, "context", "previous",
            with_context=False, fmt="",
        )
        ctx.check(
            "context ls (probe2 active after previous)", "--config", cfg, "context", "ls",
            with_context=False, validate=probe2_active,
        )

        # -- copy --------------------------------------------------------------
        ctx.check(
            "context copy", "--config", cfg, "context", "copy", probe, probe + "-copy",
            with_context=False, fmt="",
        )
        ctx.check(
            "context ls (copy present)", "--config", cfg, "context", "ls",
            with_context=False, validate=copy_present,
        )

        # -- update (single-field, everything else preserved) -------------------
        ctx.check(
            "context update", "--config", cfg, "context", "update", probe,
            "--default-node", "e2e-node",
            with_context=False, fmt="",
        )
        ctx.check(
            "context show (updated field)", "--config", cfg, "context", "show", probe,
            with_context=False, validate=updated_node,
        )

        # -- validate ----------------------------------------------------------
        ctx.check(
            "context validate", "--config", cfg, "context", "validate", probe,
            with_context=False, validate=validate_ok,
        )

        # -- rm non-active context (probe; probe2 is current) ------------------
        ctx.check(
            "context rm (non-active)", "--config", cfg, "context", "rm", probe,
            "--yes", with_context=False, fmt="",
        )
        ctx.check(
            "context ls (probe absent after rm)", "--config", cfg, "context", "ls",
            with_context=False, validate=probe_absent,
        )

        # -- rm active context must fail with helpful error --------------------
        ctx.expect_fail(
            "context rm (active guard)", "--config", cfg, "context", "rm", probe2,
            "--yes", must_contain="active context", with_context=False,
        )
    finally:
        shutil.rmtree(scratch_dir, ignore_errors=True)
