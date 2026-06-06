"""node: node inventory + per-node read-only state.

SSH/rsync/shell/exec/console and service control are deferred: they need a
remote login or mutate the host, so they are not part of the happy-path sweep.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx

NAME = "node"
DESCRIPTION = "Manage Proxmox VE nodes"


def run(ctx: Ctx) -> None:
    def has_node(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, list) and data:
            return None
        return "no nodes returned"

    ctx.check("list", "node", "list", validate=has_node)

    # These subcommands take the node as a positional argument.
    n = ctx.node
    if not n:
        ctx.skip("status", "no node discovered")
        ctx.skip("services list", "no node discovered")
        ctx.skip("task list", "no node discovered")
        ctx.skip("task log", "no node discovered")
        ctx.skip("task wait", "no node discovered")
    else:
        def has_node_status(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            missing = [k for k in ("memory", "pveversion") if k not in data]
            return f"node status missing keys: {missing}" if missing else None

        ctx.check("status", "node", "status", n, validate=has_node_status)
        svc = ctx.check("services list", "node", "services", "list", n)
        if svc.rc == 0:
            try:
                name = ctx.first(svc.json(), "service") or ctx.first(svc.json(), "name")
            except ValueError:
                name = None
            if name:
                ctx.check("services get", "node", "services", "get", n, str(name))
            else:
                ctx.skip("services get", "no service to inspect")
        tasks = ctx.check("task list", "node", "task", "list", n)
        # `node task log` reads one task's log by UPID; conditional on the list
        # returning a task (◑). Mirrors the top-level `task log` check.
        upid = None
        if tasks.rc == 0:
            try:
                upid = ctx.first(tasks.json(), "upid") or ctx.first(tasks.json(), "id")
            except ValueError:
                upid = None
        if upid:
            ctx.check("task log", "node", "task", "log", n, str(upid))
        else:
            ctx.skip("task log", "no task in the node task list")
        # `node task wait` against an already-finished UPID returns immediately —
        # WaitForUPID sees a `stopped` task, so no hang (◑). It must target a task
        # that finished SUCCESSFULLY: `wait` reports a non-zero exit when the task
        # itself failed (for example a realm-sync probe that could not reach its
        # server), which would be a spurious probe failure. Pick the first task
        # whose status is "OK".
        ok_upid = None
        if tasks.rc == 0:
            try:
                for entry in tasks.json():
                    if isinstance(entry, dict) and entry.get("status") == "OK" and entry.get("upid"):
                        ok_upid = entry["upid"]
                        break
            except ValueError:
                ok_upid = None
        if ok_upid:
            # The verb takes only <upid> (node is parsed from the UPID).
            ctx.check("task wait", "node", "task", "wait", str(ok_upid), "--timeout", "30")
        else:
            ctx.skip("task wait", "no successfully-finished task in the node task list")

        # Host firewall: the rule list is an array (empty when the node has no
        # host rules); options is a key/value object. Both are read-only and
        # safe to query directly. The firewall is node-scoped, so the node is
        # supplied through the --node selector rather than a positional arg.
        def is_list(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), list) else "expected a JSON array"

        ctx.check("firewall rules list", "node", "firewall", "rules", "list",
                  node=n, validate=is_list)
        ctx.check("firewall options get", "node", "firewall", "options", "get", node=n)
        ctx.check("firewall rules create --help", "node", "firewall", "rules", "create",
                  "--help", fmt="")

        # Host network: the interface list is an array (always non-empty — at
        # least the management bridge exists); a single interface is a key/value
        # object. Both are read-only and safe to query. The network commands are
        # node-scoped via the --node selector. The write verbs (create/set/
        # delete/apply/revert) edit the host networking stack and could cut the
        # node off the network, so they are never exercised live.
        net = ctx.check("network list", "node", "network", "list", node=n, validate=is_list)
        iface = None
        if net.rc == 0:
            try:
                iface = ctx.first(net.json(), "iface")
            except ValueError:
                iface = None
        if iface:
            ctx.check("network get", "node", "network", "get", str(iface), node=n)
        else:
            ctx.skip("network get", "no interface to inspect")
        ctx.check("network create --help", "node", "network", "create", "--help", fmt="")

        # APT package management: pending updates, installed versions, and the
        # standard-repository status are all local read-only queries. The
        # changelog is fetched per package — discover a package name from the
        # installed versions (PVE uses the capitalized "Package" key). The
        # apt-database refresh and any repository edit are deferred below.
        ctx.check("apt list", "node", "apt", "list", node=n, validate=is_list)
        ver = ctx.check("apt versions", "node", "apt", "versions", node=n, validate=is_list)
        pkg = None
        if ver.rc == 0:
            try:
                pkg = ctx.first(ver.json(), "Package")
            except ValueError:
                pkg = None
        if pkg:
            ctx.check("apt changelog", "node", "apt", "changelog", "--name", str(pkg), node=n, fmt="")
        else:
            ctx.skip("apt changelog", "no installed package to inspect")
        ctx.check("apt repositories list", "node", "apt", "repositories", "list", node=n)
        ctx.check("apt update --help", "node", "apt", "update", "--help", fmt="")

        # Disks: the physical-disk inventory is an array; SMART is read per disk
        # (discover a block device from the inventory). The disk-initialization
        # verbs (create/init-gpt/wipe) format physical media and are deferred
        # below; only their --help is exercised here.
        disks = ctx.check("disks list", "node", "disks", "list", node=n, validate=is_list)
        dev = None
        if disks.rc == 0:
            try:
                dev = ctx.first(disks.json(), "devpath")
            except ValueError:
                dev = None
        if dev:
            ctx.check("disks smart", "node", "disks", "smart", "--disk", str(dev), node=n)
        else:
            ctx.skip("disks smart", "no block device to inspect")
        ctx.check("disks create lvm --help", "node", "disks", "create", "lvm", "--help", fmt="")

        # Scan: the lvm and zfs probes enumerate local storage with no arguments
        # and are always safe. The remote probes (nfs/cifs/iscsi/pbs) need a
        # reachable server and credentials, so they are deferred below.
        ctx.check("scan lvm", "node", "scan", "lvm", node=n, validate=is_list)
        ctx.check("scan zfs", "node", "scan", "zfs", node=n, validate=is_list)

        # Hardware: PCI(e) and USB inventories are read-only arrays.
        ctx.check("hardware pci", "node", "hardware", "pci", node=n, validate=is_list)
        ctx.check("hardware usb", "node", "hardware", "usb", node=n, validate=is_list)

        # System config: dns/time are key/value objects; the system log, journal,
        # and report are read-only diagnostics; subscription is the node's
        # licensing status. /etc/hosts is read here but never replaced live (the
        # set verb rewrites the whole file and is deferred below). The dns/time/
        # subscription write verbs are reversible/idempotent — covered by the
        # mutate lifecycle, not this read-only sweep.
        def is_object(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), dict) else "expected a JSON object"

        ctx.check("dns get", "node", "dns", "get", node=n, validate=is_object)
        ctx.check("time get", "node", "time", "get", node=n, validate=is_object)
        ctx.check("hosts get", "node", "hosts", "get", node=n, fmt="")
        # The journal and report can be large; --lastentries bounds the journal.
        ctx.check("journal", "node", "journal", "--lastentries", "20", node=n, fmt="")
        ctx.check("syslog", "node", "syslog", "--limit", "20", node=n, validate=is_list)
        ctx.check("report", "node", "report", node=n, fmt="")
        ctx.check("subscription get", "node", "subscription", "get", node=n, validate=is_object)
        ctx.check("dns set --help", "node", "dns", "set", "--help", fmt="")
        ctx.check("hosts set --help", "node", "hosts", "set", "--help", fmt="")

        # Certificates: the cert chain serving the node's API and the ACME state
        # are read-only. Every write verb (ACME order/renew/delete, custom
        # upload/delete) replaces the node's TLS certificate or contacts an ACME
        # directory, so each is deferred below rather than exercised live.
        ctx.check("cert list", "node", "cert", "list", node=n, validate=is_list)
        ctx.check("cert acme list", "node", "cert", "acme", "list", node=n, validate=is_list)
        ctx.check("cert acme order --help", "node", "cert", "acme", "order", "--help", fmt="")
        ctx.check("cert custom upload --help", "node", "cert", "custom", "upload", "--help", fmt="")

        # Storage replication: the per-node job list, plus a job's status and
        # log, are read-only. A job's id is discovered from the list; on a
        # standalone node with no replication configured the list is empty, so
        # status and log are skipped. `run` triggers an immediate sync and is
        # deferred below.
        repl = ctx.check("replication list", "node", "replication", "list", node=n, validate=is_list)
        repl_id = None
        if repl.rc == 0:
            try:
                repl_id = ctx.first(repl.json(), "id")
            except ValueError:
                repl_id = None
        if repl_id:
            ctx.check("replication status", "node", "replication", "status", str(repl_id), node=n)
            ctx.check("replication log", "node", "replication", "log", str(repl_id), node=n, validate=is_list)
        else:
            ctx.skip("replication status", "no replication job configured on this node")
            ctx.skip("replication log", "no replication job configured on this node")
        ctx.check("replication run --help", "node", "replication", "run", "--help", fmt="")

        # Ceph: cluster status, the configuration database, and the OSD and pool
        # inventories are read-only. The lab node has no Ceph cluster
        # configured, so the API errors there — probe `ceph status` once and
        # skip the whole read-only Ceph sweep when Ceph is absent rather than
        # recording failures. Every create/delete/init and service-control verb
        # is cluster-destructive and is parsed-and-deferred below, never run
        # live.
        ceph_status = ctx.run("node", "ceph", "status", node=n)
        ceph_err = (ceph_status.stderr or ceph_status.stdout).lower()
        ceph_absent = ceph_status.rc != 0 and any(
            m in ceph_err for m in ("ceph", "not installed", "binary not installed", "rados")
        )
        if ceph_absent:
            for probe in ("ceph status", "ceph cfg", "ceph osd list", "ceph pool list"):
                ctx.skip(probe, "Ceph is not configured on the lab node")
        else:
            ctx.check("ceph status", "node", "ceph", "status", node=n, validate=is_object)
            ctx.check("ceph cfg", "node", "ceph", "cfg", node=n, validate=is_list)
            ctx.check("ceph osd list", "node", "ceph", "osd", "list", node=n, validate=is_object)
            ctx.check("ceph pool list", "node", "ceph", "pool", "list", node=n, validate=is_list)
        ctx.check("ceph osd create --help", "node", "ceph", "osd", "create", "--help", fmt="")
        ctx.check("ceph pool create --help", "node", "ceph", "pool", "create", "--help", fmt="")

    # `node task stop` aborts a running task; it stays deferred in this
    # read-only sweep but is exercised live by the mutate phase (which spawns a
    # deterministic server-side shutdown task and aborts it).
    ctx.defer("task stop", "aborts a running task — covered live by `e2e --mutate`",
              "pve node task stop <node> <upid>", live_covered=True)

    # The mutate phase appends a disabled host firewall rule tagged with the
    # pve-cli comment, finds it by comment, then deletes it — covered live
    # there. The host firewall OPTIONS are read only (enabling the host
    # firewall could cut the node off the network).
    ctx.defer(
        "firewall rule create/delete",
        "appends then removes a disabled host firewall rule — covered live by `e2e --mutate`",
        "pve node firewall rules create --node <node> --type in --action ACCEPT --enable 0 --comment pve-cli-e2e",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "firewall options set",
        "changes the host firewall policy — could cut the node off the network; not exercised live",
        "pve node firewall options set --node <node> --enable 1",
        isolation=False, live_covered=False,
    )

    # Host network interface edits stage changes to /etc/network/interfaces.new;
    # applying them reloads the host networking stack and could cut the node off
    # the network on a shared lab, so create/set/delete/apply/revert are never
    # exercised live.
    ctx.defer(
        "network create/set/delete",
        "edits a host network interface — could cut the node off the network; not exercised live",
        "pve node network create --node <node> --iface vmbr9 --type bridge --cidr 172.30.9.1/24",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "network apply/revert",
        "reloads or discards the staged host network configuration — could cut the node off the network; not exercised live",
        "pve node network apply --node <node> --yes",
        isolation=False, live_covered=False,
    )

    # The apt-database refresh runs apt-get update on the host (network I/O and
    # churns the node's package state on a shared lab), and the repository
    # verbs rewrite the node's APT sources — neither is exercised live.
    ctx.defer(
        "apt update",
        "refreshes the node's APT database (network I/O, apt state churn); not exercised live",
        "pve node apt update --node <node>",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "apt repositories add/enable",
        "rewrites the node's APT repository configuration; not exercised live",
        "pve node apt repositories add --node <node> --handle no-subscription --yes",
        isolation=False, live_covered=False,
    )

    # Disk initialization formats physical media and is irreversible; it is
    # never exercised live on the shared lab (it would destroy the node's
    # storage). The CLI gates each verb behind --yes.
    ctx.defer(
        "disks create/init-gpt/wipe",
        "formats or wipes a physical disk — irreversible; not exercised live",
        "pve node disks wipe --node <node> --disk /dev/sdX --yes",
        isolation=False, live_covered=False,
    )

    # The dns and time write verbs are reversible (get -> set-same -> restore)
    # and are exercised by the mutate phase's node_system_lifecycle.
    ctx.defer(
        "dns/time set",
        "reconfigures node DNS or time zone — reversible; covered live by `e2e --mutate`",
        "pve node dns set --node <node> --search <domain>",
        isolation=True, live_covered=True,
    )
    # Replacing /etc/hosts wholesale could break host name resolution on the
    # shared lab, and changing the node's subscription state (set/refresh/delete)
    # affects licensing — neither is exercised live.
    ctx.defer(
        "hosts set",
        "replaces the whole /etc/hosts file — could break host name resolution; not exercised live",
        "pve node hosts set --node <node> --data <content> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "subscription set/update/delete",
        "changes the node's subscription/licensing state on a shared lab; not exercised live",
        "pve node subscription set --node <node> --key <key> --yes",
        isolation=False, live_covered=False,
    )

    # Certificate writes all replace the node's API TLS certificate: an ACME
    # order/renew contacts Let's Encrypt (real account + DNS challenge), and a
    # custom upload/delete could break TLS to the node. None is exercised live.
    ctx.defer(
        "cert acme order/renew/delete",
        "orders, renews, or removes the node's ACME certificate (contacts Let's Encrypt); not exercised live",
        "pve node cert acme order --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "cert custom upload/delete",
        "replaces or removes the node's API TLS certificate — could break TLS to the node; not exercised live",
        "pve node cert custom upload --node <node> --certificates <pem> --key <pem> --yes",
        isolation=False, live_covered=False,
    )

    # Triggering a replication run forces an immediate sync that consumes I/O and
    # bandwidth to the target node, and needs a configured job (none exists on a
    # standalone node), so it is not exercised live.
    ctx.defer(
        "replication run",
        "triggers an immediate replication sync to the target node (needs a configured job); not exercised live",
        "pve node replication run <id> --node <node> --yes",
        isolation=False, live_covered=False,
    )

    # The remote storage scans need a reachable server and (for cifs/pbs)
    # credentials, so they are not part of the local read-only sweep.
    ctx.defer(
        "scan nfs/cifs/iscsi/pbs",
        "probes a remote storage server (needs a server address and credentials); not exercised live",
        "pve node scan nfs --node <node> --server <server>",
        isolation=False, live_covered=False,
    )

    # Every Ceph write verb is cluster-destructive: init lays down a new Ceph
    # cluster, OSD/pool/mon/mds/mgr/fs create and delete provision or destroy
    # daemons and data, and start/stop/restart control running Ceph services.
    # None is exercised live on the shared lab; the CLI gates each behind --yes.
    ctx.defer(
        "ceph init",
        "initializes a Ceph cluster configuration on the node — cluster-wide and destructive; not exercised live",
        "pve node ceph init --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd create/delete/in/out/scrub",
        "creates or destroys OSDs (wipes block devices) and moves cluster data; not exercised live",
        "pve node ceph osd create --node <node> --dev /dev/sdb --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph pool create/set/delete",
        "creates, reconfigures, or destroys a Ceph pool (data loss on delete); not exercised live",
        "pve node ceph pool create <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mon/mds/mgr/fs create/delete",
        "provisions or destroys Ceph monitor/MDS/MGR/filesystem daemons; not exercised live",
        "pve node ceph mon create <monid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph start/stop/restart",
        "controls running Ceph services on the node — disruptive; not exercised live",
        "pve node ceph restart --node <node> --service osd.0 --yes",
        isolation=False, live_covered=False,
    )

    # exec/ssh/rsync are exercised live by the mutate phase, SSH-gated: it
    # probes reachability and records SKIP if the host is unreachable.
    ctx.defer("exec", "runs a command on the host — covered live by `e2e --mutate` (SSH-gated)",
              "pve node exec <node> -- true", isolation=True, live_covered=True)
    ctx.defer("ssh", "remote login — covered live by `e2e --mutate` (SSH-gated)",
              "pve node ssh <node> -- true", isolation=True, live_covered=True)
    ctx.defer("rsync", "transfers files to/from host — covered live by `e2e --mutate` (SSH-gated)",
              "pve node rsync <node> <node>:<src> <dst>", isolation=True, live_covered=True)
    # Genuinely out of scope: interactive PTYs and real host-daemon control.
    ctx.defer("shell / console", "interactive session; not automatable", "pve node shell <node>")
    ctx.defer(
        "services start/stop/restart/reload",
        "mutates real host daemons on a shared lab",
        "pve node services restart <svc> --node <node>",
    )

    # Node-wide bulk actions act on every guest on the node (or a --vmids subset)
    # and would start, stop, suspend, or migrate non-isolated workloads; wakeonlan
    # powers a node on. Only argument parsing is exercised here via --help; every
    # verb is parsed-and-deferred, never run live on the shared lab.
    ctx.check("startall --help", "node", "startall", "--help", fmt="")
    ctx.check("stopall --help", "node", "stopall", "--help", fmt="")
    ctx.check("suspendall --help", "node", "suspendall", "--help", fmt="")
    ctx.check("migrateall --help", "node", "migrateall", "--help", fmt="")
    ctx.check("wakeonlan --help", "node", "wakeonlan", "--help", fmt="")
    ctx.defer(
        "startall / stopall / suspendall / migrateall",
        "node-wide guest power and migration actions — affect every guest on the node, not run live",
        "pve node stopall --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "wakeonlan",
        "sends a Wake-on-LAN packet to power on a node — affects real host power state, not run live",
        "pve node wakeonlan --node <node> --yes",
        isolation=False, live_covered=False,
    )
