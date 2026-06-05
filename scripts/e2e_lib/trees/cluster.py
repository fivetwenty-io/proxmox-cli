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

    # HA rules: the list is an array (empty on a fresh cluster). HA groups were
    # migrated to rules in PVE 9, so GET /cluster/ha/groups returns a 500 on a 9.x
    # lab — record a skip there rather than a failure (the group CLI wiring still
    # serves older clusters and is covered by unit tests).
    grp = ctx.run("cluster", "ha", "group", "list")
    grp_err = (grp.stderr or grp.stdout).lower()
    if grp.rc != 0 and "migrated to rules" in grp_err:
        ctx.skip("ha group list", "HA groups were migrated to rules in PVE 9")
    else:
        ctx.check("ha group list", "cluster", "ha", "group", "list", validate=is_list)
    ctx.check("ha rule list", "cluster", "ha", "rule", "list", validate=is_list)
    # The mutate phase creates a pve-cli- namespaced node-affinity rule bound to
    # the isolated guest (and an HA group where the cluster still supports them),
    # exercises get/set, and removes them — covered live there.
    ctx.defer(
        "ha group + rule create/set/delete",
        "creates a namespaced HA rule bound to the isolated guest (and a group pre-PVE-9) — covered live by `e2e --mutate`",
        "pve cluster ha rule create pve-cli-rule --type node-affinity --resources vm:<id> --nodes <node>",
        isolation=True, live_covered=True,
    )

    # HA status views are read-only and safe to query directly.
    ctx.check("ha status", "cluster", "ha", "status", "list", validate=is_list)
    ctx.check("ha status current", "cluster", "ha", "status", "current", validate=is_list)
    # arm/disarm flip the cluster-wide HA stack and would disrupt every HA-managed
    # resource on the lab, so they are parsed-and-deferred, never run live.
    ctx.defer(
        "ha status arm/disarm",
        "toggles the cluster-wide HA stack — would disrupt every HA-managed resource on the lab",
        "pve cluster ha status disarm --yes --resource-mode freeze",
        isolation=False, live_covered=False,
    )

    # Cluster firewall: the rule, group, ipset, and alias lists are arrays
    # (empty on a fresh datacenter); options is a key/value object. All are
    # read-only and safe to query directly.
    ctx.check("firewall rules list", "cluster", "firewall", "rules", "list", validate=is_list)
    ctx.check("firewall group list", "cluster", "firewall", "group", "list", validate=is_list)
    ctx.check("firewall ipset list", "cluster", "firewall", "ipset", "list", validate=is_list)
    ctx.check("firewall alias list", "cluster", "firewall", "alias", "list", validate=is_list)
    ctx.check("firewall options get", "cluster", "firewall", "options", "get")
    ctx.check("firewall rules create --help", "cluster", "firewall", "rules", "create", "--help", fmt="")
    # The mutate phase creates a pve-cli-namespaced security group (with a rule),
    # a disabled top-level rule, an IP set, and an alias on the e2e subnet, then
    # removes them all — covered live there. Datacenter firewall options are read
    # only (enabling the cluster firewall would affect every node).
    ctx.defer(
        "firewall rule/group/ipset/alias create/delete",
        "mutates the cluster firewall — covered live by `e2e --mutate`",
        "pve cluster firewall group create pvecli-grp && pve cluster firewall ipset add pvecli-clips 172.30.0.0/24",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "firewall options set",
        "enables/changes the datacenter firewall policy cluster-wide — not exercised live",
        "pve cluster firewall options set --enable 1 --policy-in DROP",
        isolation=False, live_covered=False,
    )

    # Renderer smoke test: the tabular (Headers/Rows) shape must render in every
    # `-o` format, complementing version's key/value smoke test.
    ctx.check_formats("render formats (cluster status)", "cluster", "status")
