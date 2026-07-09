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

    ctx.check("status", "pve", "cluster", "status", validate=nonempty_list)
    ctx.check("resources", "pve", "cluster", "resources", validate=nonempty_list)
    ctx.check("next free id", "pve", "cluster", "next-id")
    ctx.check("log (max 5)", "pve", "cluster", "log", "--max", "5")
    ctx.check("recent tasks", "pve", "cluster", "tasks")

    # Backup schedules: the job list is an array (possibly empty on a fresh
    # cluster). `backup info` was removed (its endpoint is only a directory
    # index); the coverage audit is `backup-info not-backed-up`, checked below.
    ctx.check("backup list", "pve", "cluster", "backup", "list", validate=is_list)
    # backup included-volumes: list volumes the schedule would back up per guest.
    # Requires an existing backup job id; discover one from the list and skip if
    # none exist.
    backup_job_id = None
    bl = ctx.run("pve", "cluster", "backup", "list")
    if bl.rc == 0:
        try:
            backup_job_id = ctx.first(bl.json(), "id")
        except (ValueError, KeyError):
            backup_job_id = None
    if backup_job_id:
        ctx.check("backup included-volumes", "pve", "cluster", "backup", "included-volumes",
                  str(backup_job_id), validate=is_list)
    else:
        ctx.skip("backup included-volumes", "no backup job defined")
    # backup-info not-backed-up: list guests not covered by any backup schedule.
    # Safe to run; returns empty list when all guests are covered.
    nb = ctx.run("pve", "cluster", "backup-info", "not-backed-up")
    nb_err = (nb.stderr or nb.stdout).lower()
    if nb.rc != 0 and ("root@pam" in nb_err or "permission" in nb_err):
        ctx.skip("backup-info not-backed-up",
                 "GET /cluster/backup-info/not-backed-up requires root@pam")
    else:
        ctx.check("backup-info not-backed-up", "pve", "cluster", "backup-info", "not-backed-up",
                  validate=is_list)
    ctx.check("backup create --help", "pve", "cluster", "backup", "create", "--help", fmt="")

    # The mutate phase creates a disabled, pool-scoped backup schedule with the
    # pmxcli- prefix, exercises get/set, and deletes it — covered live there.
    ctx.defer(
        "backup create/set/delete",
        "mutates cluster backup schedules — covered live by `e2e --mutate`",
        "pmx pve cluster backup create --id pmxcli-backup --schedule 'sun 03:30' --pool pmx-cli ...",
        isolation=True, live_covered=True,
    )

    # HA resources: the resource list is an array (empty when no guest is under
    # HA management). The mutate phase places the isolated guest under HA, reads
    # it back, updates it, and removes it again — covered live there.
    ctx.check("ha resource list", "pve", "cluster", "ha", "resource", "list", validate=is_list)
    ctx.check("ha resource create --help", "pve", "cluster", "ha", "resource", "create", "--help", fmt="")
    ctx.defer(
        "ha resource create/set/delete",
        "places a guest under HA management then removes it — covered live by `e2e --mutate`",
        "pmx pve cluster ha resource create vm:<id> --state started ... && ... delete vm:<id> --yes",
        isolation=True, live_covered=True,
    )
    # migrate/relocate need a second node to accept the guest; the single-node lab
    # cannot satisfy them, so each verb is parsed-and-deferred rather than run live.
    # Both require --target-node and are covered by unit tests (forwards the node;
    # refuses without it).
    ctx.defer(
        "ha resource migrate",
        "requires a second node as the migration target — not exercisable on a single-node lab",
        "pmx pve cluster ha resource migrate vm:<id> --target-node <other>",
        isolation=True, live_covered=False,
    )
    ctx.defer(
        "ha resource relocate",
        "requires a second node as the relocation target — not exercisable on a single-node lab",
        "pmx pve cluster ha resource relocate vm:<id> --target-node <other>",
        isolation=True, live_covered=False,
    )

    # HA rules: the list is an array (empty on a fresh cluster). HA groups were
    # migrated to rules in PVE 9, so GET /cluster/ha/groups returns a 500 on a 9.x
    # lab — record a skip there rather than a failure (the group CLI wiring still
    # serves older clusters and is covered by unit tests).
    grp = ctx.run("pve", "cluster", "ha", "group", "list")
    grp_err = (grp.stderr or grp.stdout).lower()
    if grp.rc != 0 and "migrated to rules" in grp_err:
        ctx.skip("ha group list", "HA groups were migrated to rules in PVE 9")
    else:
        ctx.check("ha group list", "pve", "cluster", "ha", "group", "list", validate=is_list)
    ctx.check("ha rule list", "pve", "cluster", "ha", "rule", "list", validate=is_list)
    # The mutate phase creates a pmx-cli- namespaced node-affinity rule bound to
    # the isolated guest (and an HA group where the cluster still supports them),
    # exercises get/set, and removes them — covered live there.
    ctx.defer(
        "ha group + rule create/set/delete",
        "creates a namespaced HA rule bound to the isolated guest (and a group pre-PVE-9) — covered live by `e2e --mutate`",
        "pmx pve cluster ha rule create pmx-cli-rule --type node-affinity --resources vm:<id> --nodes <node>",
        isolation=True, live_covered=True,
    )

    # HA status views are read-only and safe to query directly.
    ctx.check("ha status", "pve", "cluster", "ha", "status", "list", validate=is_list)
    ctx.check("ha status current", "pve", "cluster", "ha", "status", "current", validate=is_list)
    # ha status manager: the raw CRM/LRM manager status. Read-only; returns the
    # manager-status object (empty when HA has never been active), so it is safe
    # to query directly on any cluster.
    ctx.check("ha status manager", "pve", "cluster", "ha", "status", "manager")
    # arm/disarm flip the cluster-wide HA stack and would disrupt every HA-managed
    # resource on the lab, so each verb is parsed-and-deferred, never run live. Both
    # are gated behind --yes and covered by unit tests of that guard.
    ctx.defer(
        "ha status arm",
        "re-enables the cluster-wide HA stack — would disrupt every HA-managed resource on the lab",
        "pmx pve cluster ha status arm --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ha status disarm",
        "disables the cluster-wide HA stack — would disrupt every HA-managed resource on the lab",
        "pmx pve cluster ha status disarm --yes --resource-mode freeze",
        isolation=False, live_covered=False,
    )

    # Cluster firewall: the rule, group, ipset, and alias lists are arrays
    # (empty on a fresh datacenter); options is a key/value object. All are
    # read-only and safe to query directly.
    ctx.check("firewall rules list", "pve", "cluster", "firewall", "rules", "list", validate=is_list)
    group_list = ctx.check("firewall group list", "pve", "cluster", "firewall", "group", "list", validate=is_list)
    ipset_list = ctx.check("firewall ipset list", "pve", "cluster", "firewall", "ipset", "list", validate=is_list)
    alias_list = ctx.check("firewall alias list", "pve", "cluster", "firewall", "alias", "list", validate=is_list)
    ctx.check("firewall options get", "pve", "cluster", "firewall", "options", "get")
    # firewall options describe: offline schema catalog — no API call.
    ctx.check("firewall options describe", "pve", "cluster", "firewall", "options", "describe")
    ctx.check("firewall rules create --help", "pve", "cluster", "firewall", "rules", "create", "--help", fmt="")

    # firewall alias get: per-alias detail. Discover a real alias name from the
    # list just checked; skip when the datacenter has none defined.
    alias_name = None
    if alias_list.rc == 0:
        try:
            alias_name = ctx.first(alias_list.json(), "name")
        except ValueError:
            alias_name = None
    if alias_name:
        ctx.check("firewall alias get", "pve", "cluster", "firewall", "alias", "get", str(alias_name))
    else:
        ctx.skip("firewall alias get", "no cluster firewall alias defined")

    # firewall group get: reads a single rule within a security group by
    # position (GET /cluster/firewall/groups/{group}/{pos}), not the group
    # itself. Discover a group, then a rule position inside it; skip when
    # either is missing.
    group_name = None
    if group_list.rc == 0:
        try:
            group_name = ctx.first(group_list.json(), "group")
        except ValueError:
            group_name = None
    if group_name:
        group_rules = ctx.run("pve", "cluster", "firewall", "group", "rules", str(group_name))
        rule_pos = None
        if group_rules.rc == 0:
            try:
                rule_pos = ctx.first(group_rules.json(), "pos")
            except (ValueError, KeyError):
                rule_pos = None
        if rule_pos is not None:
            ctx.check("firewall group get", "pve", "cluster", "firewall", "group", "get",
                      str(group_name), str(rule_pos))
        else:
            ctx.skip("firewall group get", f"security group {group_name} has no rules")
    else:
        ctx.skip("firewall group get", "no cluster firewall security group defined")

    # firewall ipset get: reads a single CIDR member of an IP set
    # (GET /cluster/firewall/ipset/{name}/{cidr}). Discover an IP set, then a
    # member CIDR inside it; skip when either is missing.
    ipset_name = None
    if ipset_list.rc == 0:
        try:
            ipset_name = ctx.first(ipset_list.json(), "name")
        except ValueError:
            ipset_name = None
    if ipset_name:
        ipset_members = ctx.run("pve", "cluster", "firewall", "ipset", "list", str(ipset_name))
        member_cidr = None
        if ipset_members.rc == 0:
            try:
                member_cidr = ctx.first(ipset_members.json(), "cidr")
            except (ValueError, KeyError):
                member_cidr = None
        if member_cidr:
            ctx.check("firewall ipset get", "pve", "cluster", "firewall", "ipset", "get",
                      str(ipset_name), str(member_cidr))
        else:
            ctx.skip("firewall ipset get", f"IP set {ipset_name} has no members")
    else:
        ctx.skip("firewall ipset get", "no cluster firewall IP set defined")
    # The mutate phase creates a pmx-cli-namespaced security group (with a rule),
    # a disabled top-level rule, an IP set, and an alias on the e2e subnet, then
    # removes them all — covered live there. Datacenter firewall options are read
    # only (enabling the cluster firewall would affect every node).
    ctx.defer(
        "firewall rule/group/ipset/alias create/delete",
        "mutates the cluster firewall — covered live by `e2e --mutate`",
        "pmx pve cluster firewall group create pmxcli-grp && pmx pve cluster firewall ipset add pmxcli-clips 172.30.0.0/24",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "firewall options set",
        "datacenter firewall policy — covered live by `e2e --mutate` via an "
        "idempotent round-trip (the current `enable` value is written straight "
        "back, so the effective policy is unchanged)",
        "pmx pve cluster firewall options set --enable 0",
        isolation=True, live_covered=True,
    )

    # Datacenter options (datacenter.cfg) are a key/value object; the cluster
    # join information is an object and the member list is an array. All are
    # read-only and safe to query directly.
    ctx.check("options get", "pve", "cluster", "options", "get")
    # options describe: offline schema catalog — no API call.
    ctx.check("options describe", "pve", "cluster", "options", "describe")
    ctx.check("options set --help", "pve", "cluster", "options", "set", "--help", fmt="")
    # `config join` returns the info a new node needs to join; on a standalone
    # node (not yet part of a corosync cluster) the endpoint reports "node is not
    # in a cluster" — record a skip there rather than a failure.
    join = ctx.run("pve", "cluster", "config", "join", "list")
    join_err = (join.stderr or join.stdout).lower()
    if join.rc != 0 and "not in a cluster" in join_err:
        ctx.skip("config join list", "node is not part of a corosync cluster")
    else:
        ctx.check("config join list", "pve", "cluster", "config", "join", "list")
    ctx.check("config nodes list", "pve", "cluster", "config", "nodes", "list", validate=is_list)
    ctx.check("config create --help", "pve", "cluster", "config", "create", "--help", fmt="")
    # config create initializes brand-new corosync cluster membership on the
    # local node — a one-time, cluster-formation operation that would disrupt
    # or reformat an already-clustered target; not exercised live.
    ctx.defer(
        "config create",
        "creates/initializes a new corosync cluster on the local node — one-time "
        "and disruptive to run against an already-clustered target; not "
        "exercised live; covered by unit tests",
        "pmx pve cluster config create --clustername pmx-cli-test --yes",
        isolation=False, live_covered=False,
    )
    # The mutate phase sets a reversible marker on the datacenter description and
    # restores it — covered live there.
    ctx.defer(
        "options set",
        "changes a reversible datacenter option (description marker) — covered live by `e2e --mutate`",
        "pmx pve cluster options set --description 'pmx-cli-e2e ...'",
        isolation=True, live_covered=True,
    )
    # Membership changes (join, node add/remove) affect corosync quorum and could
    # break the cluster, so each verb is parsed-and-deferred and never run live. All
    # three are gated behind --yes and covered by unit tests of that guard.
    ctx.defer(
        "config join add",
        "joins the local node to an existing cluster — changes membership and quorum; not exercised live; covered by unit tests",
        "pmx pve cluster config join add --yes --hostname <peer> --fingerprint <fp> --password <pw>",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "config nodes add",
        "registers a new node in the cluster configuration — changes membership and quorum; not exercised live; covered by unit tests",
        "pmx pve cluster config nodes add <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "config nodes delete",
        "removes a node from the cluster configuration — changes membership and quorum; not exercised live; covered by unit tests",
        "pmx pve cluster config nodes delete <node> --yes",
        isolation=False, live_covered=False,
    )

    # Storage replication jobs: the list is an array (empty on a fresh cluster).
    ctx.check("replication list", "pve", "cluster", "replication", "list", validate=is_list)
    ctx.check("replication create --help", "pve", "cluster", "replication", "create", "--help", fmt="")
    # The mutate phase exercises replication CRUD when a second node exists; the
    # single-node lab cannot host a replication target, so it records a skip there.
    ctx.defer(
        "replication create/set/delete",
        "replicates a guest's volumes to another node — covered live by `e2e --mutate` (skipped on a single-node lab)",
        "pmx pve cluster replication create --id <guest>-0 --target-node <other> --schedule '*/15'",
        isolation=True, live_covered=True,
    )

    # Metrics servers: the list is an array (empty when no external metric server
    # is configured). The export is read-only; on some setups it requires
    # root@pam, so record a skip rather than a failure when the API-token identity
    # is denied.
    ctx.check("metrics server list", "pve", "cluster", "metrics", "server", "list", validate=is_list)
    ctx.check("metrics server create --help", "pve", "cluster", "metrics", "server", "create", "--help", fmt="")
    exp = ctx.run("pve", "cluster", "metrics", "export")
    exp_err = (exp.stderr or exp.stdout).lower()
    if exp.rc != 0 and ("root@pam" in exp_err or "permission" in exp_err):
        ctx.skip("metrics export", "GET /cluster/metrics/export requires root@pam")
    else:
        ctx.check("metrics export", "pve", "cluster", "metrics", "export")
    # The mutate phase creates a disabled Graphite server pointing at an unused
    # address on the e2e subnet, exercises get/set, and deletes it — covered live
    # there. The target is never contacted (Proxmox stores the config without
    # probing) and Graphite carries no secret.
    ctx.defer(
        "metrics server create/set/delete",
        "configures an external metric server — covered live by `e2e --mutate`",
        "pmx pve cluster metrics server create pmx-cli-graphite --type graphite --server 172.30.0.250 --port 2003 --disable",
        isolation=True, live_covered=True,
    )

    # Notification system: the targets, endpoints, per-type endpoint, and matcher
    # lists are all arrays (the targets list always includes the built-in
    # mail-to-root target). All read-only and safe to query directly.
    ctx.check("notifications targets", "pve", "cluster", "notifications", "targets", validate=is_list)
    ctx.check("notifications endpoints", "pve", "cluster", "notifications", "endpoints", validate=is_list)
    for kind in ("gotify", "sendmail", "smtp", "webhook"):
        ctx.check(f"notifications {kind} list", "pve", "cluster", "notifications", kind, "list", validate=is_list)
    ctx.check("notifications matcher list", "pve", "cluster", "notifications", "matcher", "list", validate=is_list)
    # matcher-fields and matcher-field-values are static metadata catalogs that do
    # not change with cluster state — always present and safe to query directly.
    ctx.check("notifications matcher-fields", "pve", "cluster", "notifications", "matcher-fields",
              validate=is_list)
    ctx.check("notifications matcher-field-values", "pve", "cluster", "notifications",
              "matcher-field-values", validate=is_list)
    ctx.check("notifications gotify create --help", "pve", "cluster", "notifications", "gotify", "create", "--help", fmt="")
    # The mutate phase creates a disabled Gotify endpoint pointing at an unused
    # address on the e2e subnet, exercises get/set, and deletes it — covered live
    # there. The endpoint is never tested (no `test` verb invoked), so the dummy
    # host is never contacted, and the token is a throwaway dummy value.
    ctx.defer(
        "notifications endpoint create/set/delete",
        "manages notification endpoints (gotify/sendmail/smtp/webhook) and matchers — covered live by `e2e --mutate`",
        "pmx pve cluster notifications gotify create pmx-cli-gotify --server https://172.30.0.250 --token <dummy> --disable",
        isolation=True, live_covered=True,
    )

    # Hardware/directory mappings: the per-type lists are arrays (empty on a lab
    # with no mappings defined). All read-only and safe to query directly.
    for kind in ("pci", "usb", "dir"):
        ctx.check(f"mapping {kind} list", "pve", "cluster", "mapping", kind, "list", validate=is_list)
    ctx.check("mapping dir create --help", "pve", "cluster", "mapping", "dir", "create", "--help", fmt="")
    # The mutate phase exercises full CRUD live for all three mapping kinds on
    # isolated `pmx-cli-` mappings. A directory mapping needs only a node and a
    # path; PCI and USB mappings store the device address as a drift-detection
    # hint rather than a create-time hardware gate, so an isolated mapping with a
    # host-present address (the 0000:00:00.0 PCI root, a USB bus path) creates and
    # removes cleanly. See `cluster_mapping_lifecycle` in lifecycle.py.

    # Realm-sync jobs: the list is an array (empty on a lab with no LDAP/AD realm
    # synced). Read-only and safe to query directly.
    ctx.check("jobs realm-sync list", "pve", "cluster", "jobs", "realm-sync", "list", validate=is_list)
    # schedule-analyze: validates a cron/timespec and lists next trigger times.
    # --schedule is required; a simple daily schedule always parses without any
    # configured jobs, so this is safe to run unconditionally.
    ctx.check("jobs schedule-analyze", "pve", "cluster", "jobs", "schedule-analyze",
              "--schedule", "daily", validate=is_list)
    ctx.check("jobs realm-sync create --help", "pve", "cluster", "jobs", "realm-sync", "create", "--help", fmt="")
    # A realm-sync job needs an existing LDAP/AD realm to point at; the mutate phase
    # creates one only during the access domain lifecycle, so the realm-sync CRUD is
    # covered there when a realm is present and skipped otherwise.
    ctx.defer(
        "jobs realm-sync create/set/delete",
        "needs an existing LDAP/AD realm — covered live by `e2e --mutate` when one exists",
        "pmx pve cluster jobs realm-sync create sync-ldap --schedule daily --realm pmx-cli-realm",
        isolation=True, live_covered=True,
    )

    # ACME: the account and plugin lists are arrays (empty on a lab with no ACME
    # configured); directories and challenge-schema are built-in static catalogs
    # that do not contact any CA. All read-only and safe to query directly.
    acme_accounts = ctx.check("acme account list", "pve", "cluster", "acme", "account", "list",
                              validate=is_list)
    # acme account get: show a single registered account by name. Needs an existing
    # account, so discover one from the list above; the lab has none configured, so
    # this skips there (the verb is parsed-and-deferred for live coverage below).
    acme_account_name = None
    if acme_accounts.rc == 0:
        try:
            acme_account_name = ctx.first(acme_accounts.json(), "name")
        except (ValueError, AttributeError, KeyError):
            acme_account_name = None
    if acme_account_name:
        # Reading a single account returns its private key material, so the API
        # restricts GET /cluster/acme/account/<name> to root@pam. An API-token
        # identity is denied — assert the permission error surfaces cleanly there,
        # and read the account when the identity is privileged enough.
        probe = ctx.run("pve", "cluster", "acme", "account", "get", str(acme_account_name))
        probe_err = (probe.stderr or probe.stdout).lower()
        if probe.rc != 0 and ("root@pam" in probe_err or "permission" in probe_err):
            ctx.expect_fail("acme account get", "pve", "cluster", "acme", "account", "get",
                            str(acme_account_name), must_contain="permission")
        else:
            ctx.check("acme account get", "pve", "cluster", "acme", "account", "get",
                      str(acme_account_name))
    else:
        ctx.skip("acme account get", "no ACME account registered on the lab")
    ctx.check("acme plugin list", "pve", "cluster", "acme", "plugin", "list", validate=is_list)
    ctx.check("acme directories", "pve", "cluster", "acme", "directories", validate=is_list)
    ctx.check("acme challenge-schema", "pve", "cluster", "acme", "challenge-schema", validate=is_list)
    ctx.check("acme plugin create --help", "pve", "cluster", "acme", "plugin", "create", "--help", fmt="")
    # The mutate phase creates an isolated `pmx-cli-` dns-01 plugin with throwaway
    # data (never used to issue a certificate), exercises get/set, and deletes it —
    # covered live there. The plugin's --data block is a dummy credential and is
    # never echoed.
    ctx.defer(
        "acme plugin create/set/delete",
        "manages a local dns-01 challenge plugin — covered live by `e2e --mutate`",
        "pmx pve cluster acme plugin create pmx-cli-acme --type dns --api cf --data <dummy>",
        isolation=True, live_covered=True,
    )
    # Account register/update/deregister are restricted to root@pam by the API
    # ("Permission check failed (user != root@pam)"), so the API-token-authenticated
    # e2e suite cannot invoke them even against a reachable ACME directory (a
    # host-local ACME stub satisfies the protocol, but the auth gate is on the verb,
    # not the CA). Covered by unit tests, same as `storage volume copy`.
    ctx.defer(
        "acme account create",
        "registers a new account against the ACME CA — the endpoint is restricted to root@pam and "
        "rejects API-token auth; not exercisable by the e2e suite — covered by unit tests",
        "pmx pve cluster acme account create --contact admin@example.com --directory <staging>",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "acme account set",
        "updates an account's contact at the ACME CA — the endpoint is restricted to root@pam and "
        "rejects API-token auth; not exercisable by the e2e suite — covered by unit tests",
        "pmx pve cluster acme account set <name> --contact admin@example.com",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "acme account delete",
        "deactivates and removes an account at the ACME CA — the endpoint is restricted to root@pam and "
        "rejects API-token auth; not exercisable by the e2e suite — covered by unit tests",
        "pmx pve cluster acme account delete <name> --yes",
        isolation=False, live_covered=False,
    )

    # Ceph flags/status require a configured Ceph cluster; the lab node has no
    # Ceph, so the API returns an error — record a skip there rather than a
    # failure. The flag set and set-all are cluster-disruptive and are
    # parsed-and-deferred, never run live.
    flags = ctx.run("pve", "cluster", "ceph", "flags", "list")
    flags_err = (flags.stderr or flags.stdout).lower()
    if flags.rc != 0 and "ceph" in flags_err:
        ctx.skip("ceph flags list", "Ceph is not configured on the lab node")
        ctx.skip("ceph flags get", "Ceph is not configured on the lab node")
        ctx.skip("ceph metadata", "Ceph is not configured on the lab node")
        ctx.skip("ceph status", "Ceph is not configured on the lab node")
    else:
        ctx.check("ceph flags list", "pve", "cluster", "ceph", "flags", "list", validate=is_list)
        # ceph flags get: read a single cluster-wide Ceph flag. `noout` is a
        # built-in flag that always exists once Ceph is configured, so it is safe
        # to query here inside the ceph-present branch.
        ctx.check("ceph flags get", "pve", "cluster", "ceph", "flags", "get", "noout")
        # ceph metadata: cluster-wide OSD/mon/mgr/mds daemon metadata; read-only.
        ctx.check("ceph metadata", "pve", "cluster", "ceph", "metadata")
        # ceph status: cluster-wide Ceph health/capacity summary; read-only.
        ctx.check("ceph status", "pve", "cluster", "ceph", "status")
    ctx.check("ceph flags set --help", "pve", "cluster", "ceph", "flags", "set", "--help", fmt="")
    ctx.check("ceph flags set-all --help", "pve", "cluster", "ceph", "flags", "set-all", "--help", fmt="")
    ctx.defer(
        "ceph flags set",
        "toggles a cluster-wide Ceph OSD flag (e.g. noout/pause) — cluster-disruptive, not run live",
        "pmx pve cluster ceph flags set noout true",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph flags set-all",
        "toggles several cluster-wide Ceph OSD flags atomically (e.g. noout, "
        "norebalance) in one request during maintenance — cluster-disruptive; "
        "not exercised live; covered by unit tests",
        "pmx pve cluster ceph flags set-all --noout=true --norebalance=true",
        isolation=False, live_covered=False,
    )

    # Cluster firewall: static metadata catalogs — macro list and reference list.
    # Both are always present (macros are built-in; refs reflects the current
    # firewall config), safe to query directly.
    ctx.check("firewall macros list", "pve", "cluster", "firewall", "macros", "list", validate=is_list)
    ctx.check("firewall refs list", "pve", "cluster", "firewall", "refs", "list", validate=is_list)

    # Corosync/cluster config metadata. `apiversion` is always present; `totem`
    # is always present on a cluster with corosync; `qdevice` errors when no
    # QDevice is configured — record a skip there rather than a failure.
    ctx.check("config apiversion", "pve", "cluster", "config", "apiversion")
    totem_res = ctx.run("pve", "cluster", "config", "totem")
    if totem_res.rc == 0:
        ctx.check("config totem", "pve", "cluster", "config", "totem")
    else:
        ctx.skip("config totem", "corosync totem not available on this cluster")
    qdev_res = ctx.run("pve", "cluster", "config", "qdevice")
    qdev_err = (qdev_res.stderr or qdev_res.stdout).lower()
    if qdev_res.rc != 0 and any(
        m in qdev_err for m in ("qdevice", "not configured", "no such", "404")
    ):
        ctx.skip("config qdevice", "QDevice not configured on this cluster")
    else:
        ctx.check("config qdevice", "pve", "cluster", "config", "qdevice")

    # Bulk actions act on every guest in the cluster by default, but --vmids
    # narrows them to a subset. `start`, `shutdown`, and `suspend` are driven
    # live by the mutate phase scoped to ONLY the isolated pmx-cli VM, so they
    # touch no other workload (`suspend` pauses the running QEMU process, the
    # same operation as `qemu suspend`). `migrate` needs a second node, so it
    # stays deferred; its argument parsing is still exercised via --help.
    # bulk guest was removed: GET /cluster/bulk-action/guest is only a directory
    # index of the bulk POST actions, not a guest preview.
    ctx.check("bulk start --help", "pve", "cluster", "bulk", "start", "--help", fmt="")
    ctx.check("bulk shutdown --help", "pve", "cluster", "bulk", "shutdown", "--help", fmt="")
    ctx.check("bulk suspend --help", "pve", "cluster", "bulk", "suspend", "--help", fmt="")
    ctx.check("bulk migrate --help", "pve", "cluster", "bulk", "migrate", "--help", fmt="")
    ctx.defer(
        "bulk start",
        "starts guests cluster-wide — covered live by `e2e --mutate` scoped to "
        "the isolated pmx-cli VM via --vmids",
        "pmx pve cluster bulk start --vmids <vmid> --yes",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "bulk shutdown",
        "shuts down guests cluster-wide — covered live by `e2e --mutate` scoped "
        "to the isolated pmx-cli VM via --vmids",
        "pmx pve cluster bulk shutdown --vmids <vmid> --yes",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "bulk suspend",
        "suspends guests cluster-wide — covered live by `e2e --mutate` scoped "
        "to the isolated pmx-cli VM via --vmids (pauses the QEMU process)",
        "pmx pve cluster bulk suspend --vmids <vmid> --yes",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "bulk migrate",
        "migrates guests cluster-wide — requires a second node; not exercisable "
        "on a single-node lab",
        "pmx pve cluster bulk migrate --target-node <node> --yes",
        isolation=False, live_covered=False,
    )

    # Cluster-wide QEMU CPU flags: a static capability catalog, always present
    # and safe to query directly regardless of guest inventory.
    ctx.check("qemu cpu-flags", "pve", "cluster", "qemu", "cpu-flags", validate=is_list)

    # Custom QEMU CPU models are datacenter-wide configuration. The list is
    # read-only; create/get/set/delete are reversible and infra-independent (a
    # model just pairs a reported QEMU model with extra flags), so the mutate
    # phase exercises a full isolated `pmx-cli-cpu` CRUD cycle.
    ctx.check("cpu-model list", "pve", "cluster", "cpu-model", "list", validate=is_list)
    ctx.check("cpu-model create --help", "pve", "cluster", "cpu-model", "create", "--help", fmt="")
    ctx.defer(
        "cpu-model create/get/set/delete",
        "creates and removes an isolated custom CPU model — reversible; covered live by `e2e --mutate`",
        "pmx pve cluster cpu-model create pmx-cli-cpu --reported-model qemu64",
        isolation=True, live_covered=True,
    )

    # Renderer smoke test: the tabular (Headers/Rows) shape must render in every
    # `-o` format, complementing version's key/value smoke test.
    ctx.check_formats("render formats (cluster status)", "pve", "cluster", "status")
