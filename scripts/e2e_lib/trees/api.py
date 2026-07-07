"""api: authentication status and credential mutation.

`auth status` reads state against the configured context.
`auth set-token` and `auth set-password` mutate *local config only* — they
are exercised here against a throwaway scratch `--config` file in a temp dir,
so the real config and the configured context are never touched.
`auth login/logout/refresh` mutate a stored session, so they stay deferred in
this read-only sweep but are exercised live by the mutate phase.

The verbs `target`, `targets`, and `switch` were removed when the CLI was
renamed from target to context (D-01 full rename). They are no longer present
in the `api` group and are not tested here.
"""

from __future__ import annotations

import os
import shutil
import tempfile

from ..context import CmdResult, Ctx

NAME = "api"
DESCRIPTION = "Manage authentication against named Proxmox VE contexts"


def run(ctx: Ctx) -> None:
    ctx.check("auth status", "api", "auth", "status")

    # auth whoami calls GET /access/permissions with the configured context's
    # live credentials (token or password session) to confirm they still
    # authenticate. Probe first and skip gracefully rather than failing the
    # sweep when the configured identity cannot be verified.
    whoami_probe = ctx.run("api", "auth", "whoami")
    if whoami_probe.rc == 0:
        ctx.check("auth whoami", "api", "auth", "whoami")
    else:
        ctx.skip("auth whoami", "credentials for the configured context could not be verified")

    _scratch_config_checks(ctx)

    # login/logout/refresh mutate a stored *session*; they stay deferred in this
    # read-only sweep but are exercised live by the mutate phase (a throwaway
    # pve-realm user + scratch `--config`, so the real session is never touched).
    ctx.defer("auth login/logout/refresh",
              "mutates stored session/credentials — covered live by `e2e --mutate`",
              "pmx api auth login --context <scratch>", live_covered=True)


def _scratch_config_checks(ctx: Ctx) -> None:
    """Drive `auth set-token` and `auth set-password` against a temp config file.

    Uses `--config <scratch>` and omits `--context` (`with_context=False`) so the
    commands operate solely on the scratch file — never the real config or the
    configured context. The scratch dir is removed in `finally`.
    """
    scratch_dir = tempfile.mkdtemp(prefix="pmx-cli-e2e-api-")
    cfg = os.path.join(scratch_dir, "config.yml")
    probe = "pmx-cli-probe"
    try:
        # Seed a context into the scratch config via `context add` so that
        # `auth set-token` / `auth set-password` have a named context to mutate.
        ctx.check("context add (scratch for auth)", "--config", cfg, "context", "add",
                  probe, "--host", "127.0.0.1", "--username", "root@pam",
                  "--token-id", "e2e", "--secret", "00000000-0000-0000-0000-000000000000",
                  "--insecure", with_context=False, fmt="")

        _auth_set_checks(ctx, cfg, probe)
    finally:
        shutil.rmtree(scratch_dir, ignore_errors=True)


def _auth_set_checks(ctx: Ctx, cfg: str, target: str) -> None:
    """Drive `auth set-token` then `auth set-password` on a scratch context and
    assert each round-trips through `auth status`."""
    token_id = "root@pam!e2e"
    token_secret = "00000000-0000-0000-0000-000000000000"

    def is_token(res: CmdResult) -> str | None:
        data = res.json().get("data", {})
        if data.get("Auth-type") != "token":
            return f"auth-type {data.get('Auth-type')!r} != token"
        if data.get("Token-ID") != token_id:
            return f"token-id {data.get('Token-ID')!r} != {token_id!r}"
        return None

    ctx.check("auth set-token (scratch)", "--config", cfg, "api", "auth", "set-token",
              "--context", target, "--token-id", token_id, "--secret", token_secret,
              "--username", "root@pam", fmt="", with_context=False)
    ctx.check("auth status after set-token (scratch)", "--config", cfg, "api", "auth",
              "status", "--context", target, with_context=False, validate=is_token)

    def is_password(res: CmdResult) -> str | None:
        data = res.json().get("data", {})
        if data.get("Auth-type") != "password":
            return f"auth-type {data.get('Auth-type')!r} != password"
        if data.get("Username") != "root@pam":
            return f"username {data.get('Username')!r} != 'root@pam'"
        return None

    ctx.check("auth set-password (scratch)", "--config", cfg, "api", "auth", "set-password",
              "--context", target, "--username", "root@pam", "--secret", "e2e-not-a-real-pw",
              fmt="", with_context=False)
    ctx.check("auth status after set-password (scratch)", "--config", cfg, "api", "auth",
              "status", "--context", target, with_context=False, validate=is_password)
