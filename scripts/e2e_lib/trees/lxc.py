"""lxc: container inventory + per-CT read-only inspection.

The lab may have zero containers; an empty list still passes. Lifecycle is
deferred under the same isolation contract as qemu.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx
from ..model import Isolation

NAME = "lxc"
DESCRIPTION = "Manage LXC containers"


def run(ctx: Ctx) -> None:
    # config/firewall-options describe: offline schema catalogs — no API call,
    # so they run even before node discovery.
    ctx.check("config describe", "pve", "lxc", "config", "describe")
    ctx.check("firewall options describe", "pve", "lxc", "firewall", "options", "describe")
    # security caps describe: offline capability catalog + preset listing — no
    # API call, so it runs alongside the other offline schema catalogs.
    ctx.check("security caps describe", "pve", "lxc", "security", "caps", "describe")

    n = ctx.node
    if not n:
        ctx.skip("list", "no node discovered")
        return

    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    lst = ctx.check("list", "pve", "lxc", "list", node=n, validate=is_list)
    # list --cluster reads /cluster/resources instead of a node endpoint, so
    # it needs no node and each row carries the guest's own node.
    ctx.check("list cluster", "pve", "lxc", "list", "--cluster", validate=is_list)
    ctx.check("template list", "pve", "lxc", "template", "list", node=n, validate=is_list)

    ctid = None
    if lst.rc == 0:
        try:
            ctid = ctx.first(lst.json(), "vmid")
        except ValueError:
            ctid = None

    def has_status(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        missing = [k for k in ("status", "vmid") if k not in data]
        return f"status response missing keys: {missing}" if missing else None

    def has_ticket(res: CmdResult) -> str | None:
        # Validate the proxy ticket's shape only. The response carries a
        # short-lived secret; assert on key presence and never echo values.
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        missing = [k for k in ("ticket", "port") if k not in data]
        return f"console response missing keys: {missing}" if missing else None

    if ctid is None:
        ctx.skip("status", "no container on node")
        ctx.skip("config get", "no container on node")
        ctx.skip("hookscript get", "no container on node")
        ctx.skip("config pending", "no container on node")
        ctx.skip("metrics", "no container on node")
        ctx.skip("rrd", "no container on node")
        ctx.skip("feature", "no container on node")
        ctx.skip("snapshot list", "no container on node")
        ctx.skip("snapshot show", "no container on node")
        ctx.skip("migrate check", "no container on node")
        ctx.skip("firewall rules list", "no container on node")
        ctx.skip("firewall options get", "no container on node")
        ctx.skip("firewall log", "no container on node")
        ctx.skip("firewall refs", "no container on node")
        ctx.skip("console vnc ticket", "no container on node")
    else:
        cid = str(ctid)
        ctx.check("status", "pve", "lxc", "status", cid, node=n, validate=has_status)
        ctx.check("config get", "pve", "lxc", "config", "get", cid, node=n)
        # hookscript get is a read-only view over one config key; a container
        # with no hookscript configured still exits 0 with a message.
        ctx.check("hookscript get", "pve", "lxc", "hookscript", "get", cid, node=n)
        # config pending reads the diff between current and pending config; it is
        # non-mutating and returns an array on any container (even if no change is
        # staged), so it is safe against any existing container.
        ctx.check("config pending", "pve", "lxc", "config", "pending", cid,
                  node=n, validate=is_list)

        # metrics: rrd timeseries for a container; zero-row result is a valid list.
        ctx.check("metrics", "pve", "lxc", "metrics", cid, "--timeframe", "hour",
                  node=n, validate=is_list)

        # rrd: rrd PNG image reference; always returns a filename object.
        def has_filename(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            if "filename" not in data:
                return "rrd response missing 'filename' key"
            return None

        ctx.check("rrd", "pve", "lxc", "rrd", cid, "--ds", "cpu", "--timeframe", "hour",
                  node=n, validate=has_filename)

        # feature: whether the container supports a named feature (clone is always safe).
        def has_feature(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            if "hasFeature" not in data:
                return "feature response missing 'hasFeature' key"
            return None

        ctx.check("feature", "pve", "lxc", "feature", cid, "--feature", "clone",
                  node=n, validate=has_feature)

        ctx.check("snapshot list", "pve", "lxc", "snapshot", "list", cid, node=n)

        # snapshot show: discover a real snapshot name, skip when none exists.
        snap_res = ctx.run("pve", "lxc", "snapshot", "list", cid, node=n)
        snap_name = None
        if snap_res.rc == 0:
            try:
                for entry in snap_res.json():
                    if isinstance(entry, dict):
                        nm = entry.get("name") or entry.get("snapname")
                        if nm and nm != "current":
                            snap_name = str(nm)
                            break
            except (ValueError, KeyError):
                snap_name = None
        if snap_name:
            ctx.check("snapshot show", "pve", "lxc", "snapshot", "show", cid, snap_name, node=n)
        else:
            ctx.skip("snapshot show", "no snapshot found on the discovered container")

        # migrate check: pre-flight analysis (read-only). A single-node cluster
        # returns the feasibility object without an `allowed_nodes` list, so
        # assert only the object shape here.
        def is_migrate_check(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), dict) else "expected a JSON object"

        ctx.check("migrate check", "pve", "lxc", "migrate", "check", cid,
                  node=n, validate=is_migrate_check)
        # Read-only firewall inspection: rules list returns an array, and the
        # options object is always present even when the firewall is disabled.
        ctx.check("firewall rules list", "pve", "lxc", "firewall", "rules", "list", cid,
                  node=n, validate=is_list)
        ctx.check("firewall options get", "pve", "lxc", "firewall", "options", "get", cid, node=n)
        ctx.check("firewall log", "pve", "lxc", "firewall", "log", cid, node=n)
        ctx.check("firewall refs", "pve", "lxc", "firewall", "refs", cid, node=n, validate=is_list)
        # Requesting a VNC proxy ticket is non-disruptive — it spawns an
        # ephemeral proxy the same way the web GUI does and changes no CT state.
        ctx.check("console vnc ticket", "pve", "lxc", "console", cid, "--type", "vnc",
                  node=n, validate=has_ticket)

        # security posture reads: all API-only, gated on a discovered container
        # so the posture blocks and the cluster audit table render against real
        # data. `show` is the layered per-CT posture, `list` the cluster-wide
        # audit, `caps show` the capability whitelist, `features show` the
        # parsed feature flags. The mutating counterparts (caps set/add/remove/
        # reset, features set) and `caps show --effective` are deferred below.
        def has_posture(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            missing = [k for k in ("vmid", "unprivileged") if k not in data]
            return f"security posture missing keys: {missing}" if missing else None

        def has_caps_mode(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            return None if "mode" in data else "caps response missing 'mode' key"

        def has_features(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            return None if "nesting" in data else "features response missing 'nesting' key"

        ctx.check("security show", "pve", "lxc", "security", "show", cid,
                  node=n, validate=has_posture)
        # list is a cluster resources scan plus one config read per container;
        # an empty cluster still returns a valid (possibly empty) array.
        ctx.check("security list", "pve", "lxc", "security", "list", node=n, validate=is_list)
        ctx.check("security caps show", "pve", "lxc", "security", "caps", "show", cid,
                  node=n, validate=has_caps_mode)
        ctx.check("security features show", "pve", "lxc", "security", "features", "show", cid,
                  node=n, validate=has_features)

        # permissions: ACL entries scoped to the container's /vms/{vmid} path
        # (shared with `pmx qemu permissions`, since both guest kinds sit under
        # the same path grammar). `list`/`effective` are read-only ACL queries;
        # `grant`/`revoke` mutate cluster-wide ACLs and are deferred below.
        def has_permissions_effective(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), dict) else "expected a JSON object"

        ctx.check("permissions list", "pve", "lxc", "permissions", "list", cid,
                  node=n, validate=is_list)
        ctx.check("permissions effective", "pve", "lxc", "permissions", "effective", cid,
                  node=n, validate=has_permissions_effective)

    # `interfaces` reads the container's live network namespace, so it only
    # works against a running container. Discover one explicitly; when none is
    # running the read-only sweep skips it (the verb is exercised live on the
    # purpose-built Alpine container by the mutate phase).
    running_cid = None
    if lst.rc == 0:
        for it in lst.json():
            if it.get("status") == "running" and it.get("vmid") not in (None, ""):
                running_cid = str(it["vmid"])
                break
    if running_cid is None:
        ctx.skip("interfaces", "no running container on node")
    else:
        ctx.check("interfaces", "pve", "lxc", "interfaces", running_cid,
                  node=n, validate=is_list)

    # Verify clone, migrate, disk, and firewall help text parses (commands are wired).
    ctx.check("clone --help", "pve", "lxc", "clone", "--help", fmt="")
    ctx.check("migrate --help", "pve", "lxc", "migrate", "--help", fmt="")
    ctx.check("disk resize --help", "pve", "lxc", "disk", "resize", "--help", fmt="")
    ctx.check("disk move --help", "pve", "lxc", "disk", "move", "--help", fmt="")
    ctx.check("firewall rules create --help", "pve", "lxc", "firewall", "rules", "create", "--help", fmt="")
    ctx.check("firewall ipset add --help", "pve", "lxc", "firewall", "ipset", "add", "--help", fmt="")
    ctx.check("firewall alias create --help", "pve", "lxc", "firewall", "alias", "create", "--help", fmt="")
    ctx.check("firewall options set --help", "pve", "lxc", "firewall", "options", "set", "--help", fmt="")
    ctx.check("console --help", "pve", "lxc", "console", "--help", fmt="")
    ctx.check("interfaces --help", "pve", "lxc", "interfaces", "--help", fmt="")

    # Every mutating verb below — including the full power-state matrix and
    # snapshot create/rollback/delete — is exercised live on a purpose-built
    # Alpine container by the mutate phase (`scripts/e2e --mutate` /
    # `scripts/lifecycle`).
    ctx.defer(
        "create",
        "creates a container — covered live by `e2e --mutate`",
        f"pmx pve lxc create ... --pool {Isolation.POOL} --tags {Isolation.TAG}",
        isolation=True, live_covered=True,
    )
    ctx.defer("start/stop/shutdown/reboot/suspend/resume",
              "changes CT power state — covered live by `e2e --mutate`",
              "pmx pve lxc start <ctid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("delete", "destroys a container — covered live by `e2e --mutate`",
              "pmx pve lxc delete <ctid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("snapshot create/rollback/delete",
              "mutates CT snapshots — covered live by `e2e --mutate`",
              "pmx pve lxc snapshot create <ctid> <name>", isolation=True, live_covered=True)
    ctx.defer(
        "clone",
        "clones a container — covered live by `e2e --mutate`",
        f"pmx pve lxc clone <ctid> --newid <id> --pool {Isolation.POOL} --hostname {Isolation.NAME_PREFIX}ctclone",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "migrate",
        "migrates a container to another node — covered live by `e2e --mutate` on multi-node clusters",
        "pmx pve lxc migrate <ctid> --target-node <node>",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "disk resize",
        "grows a container volume — covered live by `e2e --mutate`",
        "pmx pve lxc disk resize <ctid> --disk rootfs --size +1G",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "disk move",
        "relocates a container volume — covered live by `e2e --mutate` when a second rootdir storage exists",
        "pmx pve lxc disk move <ctid> --volume rootfs --storage <other>",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "firewall rules/ipset/alias create-delete + options set",
        "mutates a CT's firewall config — covered live by `e2e --mutate` on the isolated container",
        "pmx pve lxc firewall rules create <ctid> --type in --action ACCEPT --proto tcp --dport 22",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "console connect (websocket/spice viewer)",
        "opening the proxied console session needs an interactive viewer — the "
        "CLI only returns the ticket, which the read-only sweep validates",
        "pmx pve lxc console <ctid> --type spice",
    )
    ctx.defer(
        "remote-migrate",
        "migrates a container to a different Proxmox VE cluster — requires two "
        "live clusters; no rollback without manual intervention; not exercised live",
        "pmx pve lxc remote-migrate <ctid> --yes --target-endpoint https://remote:8006 "
        "--target-storage local-lvm",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "to-template",
        "converts the discovered container into a template — irreversible for that "
        "instance and only sensible as the terminal step of a dedicated throwaway "
        "guest lifecycle; not exercised against a live container; covered by unit tests",
        "pmx pve lxc to-template <ctid> --node <node>",
        isolation=True, live_covered=False,
    )
    # `security` mutations have no read-only form and are not wired into the
    # mutate phase: the caps verbs rewrite /etc/pve/lxc/<vmid>.conf on the node
    # over root ssh, and `features set` writes the config API. Each stays
    # deferred here and is covered by unit tests (internal/lxcconf,
    # internal/nodefile, internal/cli/lxc/security*_test.go).
    ctx.defer(
        "security caps set",
        "rewrites the container capability whitelist in /etc/pve/lxc/<vmid>.conf "
        "over root ssh, so it cannot be driven head-less by the read-only sweep; "
        "not wired into the mutate phase; covered by unit tests",
        "pmx pve lxc security caps set <ctid> --preset minimal",
    )
    ctx.defer(
        "security caps add",
        "grants a capability by editing /etc/pve/lxc/<vmid>.conf over root ssh, so "
        "it cannot be driven head-less by the read-only sweep; not wired into the "
        "mutate phase; covered by unit tests",
        "pmx pve lxc security caps add <ctid> net_admin",
    )
    ctx.defer(
        "security caps remove",
        "revokes a capability by editing /etc/pve/lxc/<vmid>.conf over root ssh, so "
        "it cannot be driven head-less by the read-only sweep; not wired into the "
        "mutate phase; covered by unit tests",
        "pmx pve lxc security caps remove <ctid> net_admin",
    )
    ctx.defer(
        "security caps reset",
        "clears the capability whitelist in /etc/pve/lxc/<vmid>.conf over root ssh, "
        "so it cannot be driven head-less by the read-only sweep; not wired into "
        "the mutate phase; covered by unit tests",
        "pmx pve lxc security caps reset <ctid>",
    )
    ctx.defer(
        "security features set",
        "mutates the container features= flags via the config API; not wired into "
        "the mutate phase; covered by unit tests",
        "pmx pve lxc security features set <ctid> --nesting",
    )
    ctx.defer(
        "security caps show --effective",
        "the --effective probe reads the running container's /proc/1/status over "
        "root ssh (the configured caps read is exercised by the sweep above); it "
        "needs a running container and root ssh, so it is not driven head-less; "
        "covered by unit tests",
        "pmx pve lxc security caps show <ctid> --effective",
    )
    # `permissions grant`/`revoke` mutate cluster-wide ACLs (not scoped to the
    # isolated container's own resources), so they are not wired into the
    # mutate phase; `permissions list`/`effective` above are read-only and
    # exercised live.
    ctx.defer(
        "permissions grant",
        "grants ACL roles on the container's /vms/{vmid} path; mutates "
        "cluster-wide ACLs, not wired into the mutate phase; covered by unit tests",
        "pmx pve lxc permissions grant <ctid> --roles PVEVMAdmin --users alice@pve",
    )
    ctx.defer(
        "permissions revoke",
        "revokes ACL roles on the container's /vms/{vmid} path; mutates "
        "cluster-wide ACLs, not wired into the mutate phase; covered by unit tests",
        "pmx pve lxc permissions revoke <ctid> --roles PVEVMAdmin --users alice@pve",
    )
    # `firewall alias get` / `firewall ipset get-member` read a single
    # pre-existing entry by name. A fresh lab has none by default, and the
    # mutate phase's isolated alias/ipset create-list-update-delete lifecycle
    # (see `scripts/e2e_lib/lifecycle.py`) does not yet call these two reads,
    # so they cannot be driven head-less; covered by unit tests.
    ctx.defer(
        "firewall alias get",
        "reads a single firewall alias by name — needs a pre-existing alias; "
        "not wired into the mutate phase; covered by unit tests",
        "pmx pve lxc firewall alias get <ctid> <name>",
    )
    ctx.defer(
        "firewall ipset get-member",
        "reads a single CIDR entry of an IP set — needs a pre-existing "
        "member; not wired into the mutate phase; covered by unit tests",
        "pmx pve lxc firewall ipset get-member <ctid> <name> <cidr>",
    )
