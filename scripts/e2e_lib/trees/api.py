"""api: local target config + auth status.

`targets`, `target <name> show`, and `auth status` read state against the
configured target. `switch` and `target add/remove` mutate *local config only* —
they are exercised here against a throwaway scratch `--config` file in a temp
dir, so the real config and the configured `lab` target are never touched.
`auth login/logout/refresh` mutate a stored session, so they stay deferred in
this read-only sweep but are exercised live by the mutate phase.
"""

from __future__ import annotations

import os
import shutil
import tempfile

from ..context import CmdResult, Ctx

NAME = "api"
DESCRIPTION = "Manage CLI targets and authentication"


def run(ctx: Ctx) -> None:
    def has_target(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list) and any(
            t.get("name") == ctx.env.target for t in data if isinstance(t, dict)
        ):
            return None
        return f"target {ctx.env.target!r} not listed"

    ctx.check("targets", "api", "targets", validate=has_target)
    ctx.check("target show", "api", "target", ctx.env.target, "show")
    ctx.check("auth status", "api", "auth", "status")

    _scratch_config_checks(ctx)

    # login/logout/refresh mutate a stored *session*; they stay deferred in this
    # read-only sweep but are exercised live by the mutate phase (a throwaway
    # pve-realm user + scratch `--config`, so the real session is never touched).
    ctx.defer("auth login/logout/refresh",
              "mutates stored session/credentials — covered live by `e2e --mutate`",
              "pve api auth login --target <scratch>", live_covered=True)


def _scratch_config_checks(ctx: Ctx) -> None:
    """Drive `target add/show/remove` + `switch` against a temp config file.

    Uses `--config <scratch>` and omits `--target` (`with_target=False`) so the
    commands operate solely on the scratch file — never the real config or the
    configured target. The scratch dir is removed in `finally`.
    """
    probe, probe2 = "pve-cli-probe", "pve-cli-probe2"

    def has_probe(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list) and any(
            t.get("name") == probe for t in data if isinstance(t, dict)
        ):
            return None
        return f"scratch target {probe!r} not listed after add"

    scratch_dir = tempfile.mkdtemp(prefix="pve-cli-e2e-api-")
    cfg = os.path.join(scratch_dir, "config.yml")
    try:
        ctx.check("target add (scratch)", "--config", cfg, "api", "target", probe,
                  "add", "--host", "127.0.0.1", "--username", "root@pam",
                  "--token", "e2e=0", "--tls-insecure", with_target=False)
        ctx.check("target add (scratch) #2", "--config", cfg, "api", "target", probe2,
                  "add", "--host", "127.0.0.2", "--username", "root@pam",
                  "--token", "e2e=0", "--tls-insecure", with_target=False)
        ctx.check("targets (scratch)", "--config", cfg, "api", "targets",
                  with_target=False, validate=has_probe)
        ctx.check("switch (scratch)", "--config", cfg, "api", "switch", probe,
                  fmt="", with_target=False)
        ctx.check("target show (scratch)", "--config", cfg, "api", "target", probe,
                  "show", with_target=False)

        # auth set-token / set-password mutate local config only. Drive them on
        # probe2 against the scratch file, then read back via `auth status` and
        # assert the auth type/identity round-tripped. No network is touched.
        _auth_set_checks(ctx, cfg, probe2)

        ctx.check("target remove (scratch)", "--config", cfg, "api", "target", probe,
                  "remove", "--yes", with_target=False)
    finally:
        shutil.rmtree(scratch_dir, ignore_errors=True)


def _auth_set_checks(ctx: Ctx, cfg: str, target: str) -> None:
    """Drive `auth set-token` then `auth set-password` on a scratch target and
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
              "--target", target, "--token-id", token_id, "--secret", token_secret,
              "--username", "root@pam", fmt="", with_target=False)
    ctx.check("auth status after set-token (scratch)", "--config", cfg, "api", "auth",
              "status", "--target", target, with_target=False, validate=is_token)

    def is_password(res: CmdResult) -> str | None:
        data = res.json().get("data", {})
        if data.get("Auth-type") != "password":
            return f"auth-type {data.get('Auth-type')!r} != password"
        if data.get("Username") != "root@pam":
            return f"username {data.get('Username')!r} != 'root@pam'"
        return None

    ctx.check("auth set-password (scratch)", "--config", cfg, "api", "auth", "set-password",
              "--target", target, "--username", "root@pam", "--secret", "e2e-not-a-real-pw",
              fmt="", with_target=False)
    ctx.check("auth status after set-password (scratch)", "--config", cfg, "api", "auth",
              "status", "--target", target, with_target=False, validate=is_password)
