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

    # Datacenter options (datacenter.cfg) are a key/value object; the cluster
    # join information is an object and the member list is an array. All are
    # read-only and safe to query directly.
    ctx.check("options get", "cluster", "options", "get")
    ctx.check("options set --help", "cluster", "options", "set", "--help", fmt="")
    # `config join` returns the info a new node needs to join; on a standalone
    # node (not yet part of a corosync cluster) the endpoint reports "node is not
    # in a cluster" — record a skip there rather than a failure.
    join = ctx.run("cluster", "config", "join", "list")
    join_err = (join.stderr or join.stdout).lower()
    if join.rc != 0 and "not in a cluster" in join_err:
        ctx.skip("config join list", "node is not part of a corosync cluster")
    else:
        ctx.check("config join list", "cluster", "config", "join", "list")
    ctx.check("config nodes list", "cluster", "config", "nodes", "list", validate=is_list)
    # The mutate phase sets a reversible marker on the datacenter description and
    # restores it — covered live there.
    ctx.defer(
        "options set",
        "changes a reversible datacenter option (description marker) — covered live by `e2e --mutate`",
        "pve cluster options set --description 'pve-cli-e2e ...'",
        isolation=True, live_covered=True,
    )
    # Membership changes (join, node add/remove) affect corosync quorum and could
    # break the cluster, so they are parsed-and-deferred and never run live.
    ctx.defer(
        "config join add / nodes add / nodes delete",
        "changes cluster membership and quorum — too dangerous to exercise on a shared lab",
        "pve cluster config nodes add <node> --yes",
        isolation=False, live_covered=False,
    )

    # Storage replication jobs: the list is an array (empty on a fresh cluster).
    ctx.check("replication list", "cluster", "replication", "list", validate=is_list)
    ctx.check("replication create --help", "cluster", "replication", "create", "--help", fmt="")
    # The mutate phase exercises replication CRUD when a second node exists; the
    # single-node lab cannot host a replication target, so it records a skip there.
    ctx.defer(
        "replication create/set/delete",
        "replicates a guest's volumes to another node — covered live by `e2e --mutate` (skipped on a single-node lab)",
        "pve cluster replication create --id <guest>-0 --target-node <other> --schedule '*/15'",
        isolation=True, live_covered=True,
    )

    # Metrics servers: the list is an array (empty when no external metric server
    # is configured). The export is read-only; on some setups it requires
    # root@pam, so record a skip rather than a failure when the API-token identity
    # is denied.
    ctx.check("metrics server list", "cluster", "metrics", "server", "list", validate=is_list)
    ctx.check("metrics server create --help", "cluster", "metrics", "server", "create", "--help", fmt="")
    exp = ctx.run("cluster", "metrics", "export")
    exp_err = (exp.stderr or exp.stdout).lower()
    if exp.rc != 0 and ("root@pam" in exp_err or "permission" in exp_err):
        ctx.skip("metrics export", "GET /cluster/metrics/export requires root@pam")
    else:
        ctx.check("metrics export", "cluster", "metrics", "export")
    # The mutate phase creates a disabled Graphite server pointing at an unused
    # address on the e2e subnet, exercises get/set, and deletes it — covered live
    # there. The target is never contacted (Proxmox stores the config without
    # probing) and Graphite carries no secret.
    ctx.defer(
        "metrics server create/set/delete",
        "configures an external metric server — covered live by `e2e --mutate`",
        "pve cluster metrics server create pve-cli-graphite --type graphite --server 172.30.0.250 --port 2003 --disable",
        isolation=True, live_covered=True,
    )

    # Notification system: the targets, endpoints, per-type endpoint, and matcher
    # lists are all arrays (the targets list always includes the built-in
    # mail-to-root target). All read-only and safe to query directly.
    ctx.check("notifications targets", "cluster", "notifications", "targets", validate=is_list)
    ctx.check("notifications endpoints", "cluster", "notifications", "endpoints", validate=is_list)
    for kind in ("gotify", "sendmail", "smtp", "webhook"):
        ctx.check(f"notifications {kind} list", "cluster", "notifications", kind, "list", validate=is_list)
    ctx.check("notifications matcher list", "cluster", "notifications", "matcher", "list", validate=is_list)
    ctx.check("notifications gotify create --help", "cluster", "notifications", "gotify", "create", "--help", fmt="")
    # The mutate phase creates a disabled Gotify endpoint pointing at an unused
    # address on the e2e subnet, exercises get/set, and deletes it — covered live
    # there. The endpoint is never tested (no `test` verb invoked), so the dummy
    # host is never contacted, and the token is a throwaway dummy value.
    ctx.defer(
        "notifications endpoint create/set/delete",
        "manages notification endpoints (gotify/sendmail/smtp/webhook) and matchers — covered live by `e2e --mutate`",
        "pve cluster notifications gotify create pve-cli-gotify --server https://172.30.0.250 --token <dummy> --disable",
        isolation=True, live_covered=True,
    )

    # Hardware/directory mappings: the per-type lists are arrays (empty on a lab
    # with no mappings defined). All read-only and safe to query directly.
    for kind in ("pci", "usb", "dir"):
        ctx.check(f"mapping {kind} list", "cluster", "mapping", kind, "list", validate=is_list)
    ctx.check("mapping dir create --help", "cluster", "mapping", "dir", "create", "--help", fmt="")
    # The mutate phase creates an isolated `pve-cli-` directory mapping (which needs
    # only a node and a path — no real hardware) and exercises full CRUD live. PCI
    # and USB mappings require real device IDs, so their writes are deferred.
    ctx.defer(
        "mapping pci/usb create/set/delete",
        "PCI/USB mappings need real device IDs — dir mapping CRUD is covered live by `e2e --mutate`",
        "pve cluster mapping pci create gpu --map node=pve,path=0000:01:00.0,id=10de:1b80",
        isolation=True, live_covered=False,
    )

    # Realm-sync jobs: the list is an array (empty on a lab with no LDAP/AD realm
    # synced). Read-only and safe to query directly.
    ctx.check("jobs realm-sync list", "cluster", "jobs", "realm-sync", "list", validate=is_list)
    ctx.check("jobs realm-sync create --help", "cluster", "jobs", "realm-sync", "create", "--help", fmt="")
    # A realm-sync job needs an existing LDAP/AD realm to point at; the mutate phase
    # creates one only during the access domain lifecycle, so the realm-sync CRUD is
    # covered there when a realm is present and skipped otherwise.
    ctx.defer(
        "jobs realm-sync create/set/delete",
        "needs an existing LDAP/AD realm — covered live by `e2e --mutate` when one exists",
        "pve cluster jobs realm-sync create sync-ldap --schedule daily --realm pve-cli-realm",
        isolation=True, live_covered=True,
    )

    # Renderer smoke test: the tabular (Headers/Rows) shape must render in every
    # `-o` format, complementing version's key/value smoke test.
    ctx.check_formats("render formats (cluster status)", "cluster", "status")
