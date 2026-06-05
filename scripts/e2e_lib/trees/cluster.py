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

    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    ctx.check("status", "cluster", "status", validate=nonempty_list)
    ctx.check("resources", "cluster", "resources", validate=nonempty_list)
    ctx.check("next free id", "cluster", "next-id")
    ctx.check("log (max 5)", "cluster", "log", "--max", "5")
    ctx.check("recent tasks", "cluster", "tasks")

    # Backup schedules: the job list is an array (possibly empty on a fresh
    # cluster). The coverage audit (GET /cluster/backup-info) requires root@pam,
    # which an API-token identity lacks — record a skip rather than a failure when
    # the server denies it.
    ctx.check("backup list", "cluster", "backup", "list", validate=is_list)
    info = ctx.run("cluster", "backup", "info")
    info_err = (info.stderr or info.stdout).lower()
    if info.rc != 0 and ("root@pam" in info_err or "permission" in info_err):
        ctx.skip("backup info", "GET /cluster/backup-info requires root@pam")
    else:
        ctx.check("backup info", "cluster", "backup", "info", validate=is_list)
    ctx.check("backup create --help", "cluster", "backup", "create", "--help", fmt="")

    # The mutate phase creates a disabled, pool-scoped backup schedule with the
    # pvecli- prefix, exercises get/set, and deletes it — covered live there.
    ctx.defer(
        "backup create/set/delete",
        "mutates cluster backup schedules — covered live by `e2e --mutate`",
        "pve cluster backup create --id pvecli-backup --schedule 'sun 03:30' --pool pve-cli ...",
        isolation=True, live_covered=True,
    )

    # HA resources: the resource list is an array (empty when no guest is under
    # HA management). The mutate phase places the isolated guest under HA, reads
    # it back, updates it, and removes it again — covered live there.
    ctx.check("ha resource list", "cluster", "ha", "resource", "list", validate=is_list)
    ctx.check("ha resource create --help", "cluster", "ha", "resource", "create", "--help", fmt="")
    ctx.defer(
        "ha resource create/set/delete",
        "places a guest under HA management then removes it — covered live by `e2e --mutate`",
        "pve cluster ha resource create vm:<id> --state started ... && ... delete vm:<id> --yes",
        isolation=True, live_covered=True,
    )
    # migrate/relocate need a second node to accept the guest; the single-node lab
    # cannot satisfy them, so they are parsed-and-deferred rather than run live.
    ctx.defer(
        "ha resource migrate/relocate",
        "requires a second node as the migration target — not exercisable on a single-node lab",
        "pve cluster ha resource migrate vm:<id> --target-node <other>",
        isolation=True, live_covered=False,
    )

    # Renderer smoke test: the tabular (Headers/Rows) shape must render in every
    # `-o` format, complementing version's key/value smoke test.
    ctx.check_formats("render formats (cluster status)", "cluster", "status")
