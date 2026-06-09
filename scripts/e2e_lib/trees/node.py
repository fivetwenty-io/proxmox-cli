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

        # Disk sub-type inventories: each returns a list (possibly empty on a lab
        # that lacks the given storage layout). Empty list is a valid pass.
        ctx.check("disks ls directory", "node", "disks", "ls", "directory",
                  node=n, validate=is_list)
        # lvm reports a volume-group tree as an object ({"children": [...]}),
        # unlike the other disk sub-types which return arrays.
        lvm_tree = ctx.check("disks ls lvm", "node", "disks", "ls", "lvm", node=n,
                             validate=lambda r: None if isinstance(r.json(), (dict, list))
                             else "expected a JSON object or array")
        ctx.check("disks ls lvmthin", "node", "disks", "ls", "lvmthin",
                  node=n, validate=is_list)
        zfs_list = ctx.check("disks ls zfs", "node", "disks", "ls", "zfs",
                             node=n, validate=is_list)
        # disks get zfs: detail for a specific pool; discover from the ls output.
        zfs_pool = None
        if zfs_list.rc == 0:
            try:
                zfs_pool = ctx.first(zfs_list.json(), "name")
            except (ValueError, KeyError):
                zfs_pool = None
        if zfs_pool:
            ctx.check("disks get zfs", "node", "disks", "get", "zfs", str(zfs_pool), node=n)
        else:
            ctx.skip("disks get zfs", "no ZFS pool on this node")

        ctx.check("disks create lvm --help", "node", "disks", "create", "lvm", "--help", fmt="")

        # Scan: the lvm and zfs probes enumerate local storage with no arguments
        # and are always safe. The remote probes (nfs/cifs/iscsi/pbs) need a
        # reachable server and credentials, so they are deferred below.
        ctx.check("scan lvm", "node", "scan", "lvm", node=n, validate=is_list)
        ctx.check("scan zfs", "node", "scan", "zfs", node=n, validate=is_list)

        # scan lvmthin: list the LVM-thin pools inside a volume group. It needs a
        # real VG (`--vg`), so discover one from the local LVM tree (`disks ls
        # lvm`, an object whose top-level children are the node's volume groups).
        # An empty thin-pool list is a valid pass; skip when the node has no VG.
        vg_name = None
        if lvm_tree.rc == 0:
            try:
                children = lvm_tree.json().get("children")
                if isinstance(children, list):
                    for entry in children:
                        if isinstance(entry, dict) and entry.get("name"):
                            vg_name = str(entry["name"])
                            break
            except (ValueError, AttributeError, KeyError):
                vg_name = None
        if vg_name:
            ctx.check("scan lvmthin", "node", "scan", "lvmthin", "--vg", vg_name,
                      node=n, validate=is_list)
        else:
            ctx.skip("scan lvmthin", "no LVM volume group on this node")

        # Hardware: PCI(e) and USB inventories are read-only arrays.
        pci_list = ctx.check("hardware pci", "node", "hardware", "pci",
                             node=n, validate=is_list)
        ctx.check("hardware usb", "node", "hardware", "usb", node=n, validate=is_list)
        # hardware pci mdev: list mdev types on a specific PCI device; discover
        # a PCI id from the inventory. Skip when no PCI device found or the
        # device does not expose mdev types (not all GPUs/cards do).
        pci_id = None
        if pci_list.rc == 0:
            try:
                pci_id = ctx.first(pci_list.json(), "id")
            except (ValueError, KeyError):
                pci_id = None
        if pci_id:
            mdev_res = ctx.run("node", "hardware", "mdev", str(pci_id), node=n)
            mdev_err = (mdev_res.stderr or mdev_res.stdout).lower()
            if mdev_res.rc != 0 and any(
                m in mdev_err for m in ("mdev", "no such", "not supported", "404")
            ):
                ctx.skip("hardware pci mdev",
                         f"PCI device {pci_id} does not expose mdev types")
            else:
                ctx.check("hardware pci mdev", "node", "hardware", "mdev",
                          str(pci_id), node=n, validate=is_list)
        else:
            ctx.skip("hardware pci mdev", "no PCI device found on this node")

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
        # rrddata: timeseries for node-level metrics; zero-row result is valid.
        # Node is supplied via the global --node flag, not a positional arg.
        ctx.check("rrddata", "node", "rrddata", "--timeframe", "hour",
                  node=n, validate=is_list)
        # netstat: per-interface network statistics; always returns a list.
        ctx.check("netstat", "node", "netstat", node=n, validate=is_list)
        # vzdump defaults: default vzdump settings; always a key/value object.
        ctx.check("vzdump defaults", "node", "vzdump", "defaults", node=n, validate=is_object)
        # vzdump extract-config: print the guest configuration embedded in a
        # backup archive. Read-only — it parses an existing backup file and emits
        # the config text. Discover a backup volume from cluster storage; skip
        # when the lab has no backup archive to read.
        backup_volid = None
        storages = ctx.run("storage", "list")
        if storages.rc == 0:
            try:
                names = [s.get("storage") for s in storages.json()
                         if isinstance(s, dict) and s.get("storage")]
            except (ValueError, AttributeError):
                names = []
            for sname in names:
                listing = ctx.run("storage", "content", str(sname),
                                  "--content", "backup", node=n)
                if listing.rc != 0:
                    continue
                try:
                    backup_volid = ctx.first(listing.json(), "volid")
                except (ValueError, AttributeError, KeyError):
                    backup_volid = None
                if backup_volid:
                    break
        if backup_volid:
            ctx.check("vzdump extract-config", "node", "vzdump", "extract-config",
                      "--volume", str(backup_volid), node=n, fmt="")
        else:
            ctx.skip("vzdump extract-config", "no backup archive found in cluster storage")
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
            for probe in (
                "ceph status", "ceph cfg", "ceph osd list", "ceph pool list",
                "ceph fs list", "ceph mds list", "ceph mgr list", "ceph mon list",
                "ceph osd get", "ceph pool get", "ceph pool status",
            ):
                ctx.skip(probe, "Ceph is not configured on the lab node")
        else:
            ctx.check("ceph status", "node", "ceph", "status", node=n, validate=is_object)
            ctx.check("ceph cfg", "node", "ceph", "cfg", node=n, validate=is_list)
            osd_tree = ctx.check("ceph osd list", "node", "ceph", "osd", "list",
                                 node=n, validate=is_object)
            pool_list = ctx.check("ceph pool list", "node", "ceph", "pool", "list",
                                  node=n, validate=is_list)

            # The MDS, MGR, monitor, and CephFS inventories are read-only lists
            # (each empty until the matching daemon is deployed). Safe to query
            # directly on a Ceph-enabled node.
            ctx.check("ceph fs list", "node", "ceph", "fs", "list", node=n, validate=is_list)
            ctx.check("ceph mds list", "node", "ceph", "mds", "list", node=n, validate=is_list)
            ctx.check("ceph mgr list", "node", "ceph", "mgr", "list", node=n, validate=is_list)
            ctx.check("ceph mon list", "node", "ceph", "mon", "list", node=n, validate=is_list)

            # osd get: per-OSD detail. The OSD list is a CRUSH tree object; walk
            # its `nodes` for the first entry of type "osd" to find a real id.
            osd_id = None
            if osd_tree.rc == 0:
                try:
                    nodes = osd_tree.json().get("nodes")
                    if isinstance(nodes, list):
                        for entry in nodes:
                            if isinstance(entry, dict) and entry.get("type") == "osd":
                                oid = entry.get("id")
                                if oid is not None:
                                    osd_id = str(oid)
                                    break
                except (ValueError, AttributeError, KeyError):
                    osd_id = None
            if osd_id is not None:
                ctx.check("ceph osd get", "node", "ceph", "osd", "get", osd_id,
                          node=n, validate=is_object)
            else:
                ctx.skip("ceph osd get", "no OSD deployed on this Ceph cluster")

            # pool get / pool status: per-pool parameters and runtime status.
            # Discover a pool name from the pool list; skip when no pool exists.
            pool_name = None
            if pool_list.rc == 0:
                try:
                    pool_name = ctx.first(pool_list.json(), "pool_name") or \
                        ctx.first(pool_list.json(), "name")
                except (ValueError, KeyError):
                    pool_name = None
            if pool_name:
                ctx.check("ceph pool get", "node", "ceph", "pool", "get", str(pool_name),
                          node=n, validate=is_object)
                ctx.check("ceph pool status", "node", "ceph", "pool", "status", str(pool_name),
                          node=n, validate=is_object)
            else:
                ctx.skip("ceph pool get", "no Ceph pool configured")
                ctx.skip("ceph pool status", "no Ceph pool configured")
        ctx.check("ceph osd create --help", "node", "ceph", "osd", "create", "--help", fmt="")
        ctx.check("ceph pool create --help", "node", "ceph", "pool", "create", "--help", fmt="")

        # QEMU capability queries are read-only: the node reports the CPU
        # models and machine types it can offer guests, plus its live-migration
        # features. All three are safe to run live.
        ctx.check("capabilities qemu cpu", "node", "capabilities", "qemu", "cpu",
                  node=n, validate=is_list)
        ctx.check("capabilities qemu machines", "node", "capabilities", "qemu", "machines",
                  node=n, validate=is_list)
        ctx.check("capabilities qemu migration", "node", "capabilities", "qemu", "migration",
                  node=n, validate=is_object)
        # capabilities qemu cpu-flags: per-CPU-model flag detail; always present.
        ctx.check("capabilities qemu cpu-flags", "node", "capabilities", "qemu", "cpu-flags",
                  node=n, validate=is_list)

        # OCI image handling: `oci tags` queries a registry (network egress) and
        # is exercised live by the mutate phase against a public reference; `oci
        # pull` writes an image artifact to a storage with no CLI delete verb to
        # clean it up, so it stays deferred and is exercised by --help only.
        ctx.check("oci tags --help", "node", "oci", "tags", "--help", fmt="")
        ctx.check("oci pull --help", "node", "oci", "pull", "--help", fmt="")

        # query-url-metadata: asks the node to fetch a remote URL and report its
        # metadata (size, mime type, filename) via an HTTP HEAD. The only working
        # target is an external URL, so it is exercised live by the mutate phase
        # against a stable public URL and skips if that URL is unreachable.
        ctx.defer(
            "query-url-metadata",
            "fetches metadata from an external URL via HTTP HEAD — covered live by "
            "`e2e --mutate`, which points it at a stable public URL (skips if that "
            "URL is unreachable)",
            "pve node query-url-metadata --node <node> --url https://example.com/image.iso",
            isolation=False, live_covered=True,
        )

        # services state: read the runtime state of a known service on the node.
        # pveproxy is always present on a PVE node — use it as a stable probe.
        ctx.check("services state", "node", "services", "state", n, "pveproxy", node=n)

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

    # Host network interface edits stage changes to /etc/network/interfaces.new.
    # create/set/delete/revert are covered live by `e2e --mutate`: it stages a
    # throwaway bridge (vmbr987), edits it, deletes it, and reverts the staged
    # file — all entirely in interfaces.new, so the live config is never
    # touched. Only `network apply` reloads the host networking stack (and could
    # cut the node off the network), so apply alone stays deferred.
    ctx.defer(
        "network create",
        "creates a host network interface — covered live by `e2e --mutate`, which stages a throwaway bridge and reverts it (never applied)",
        "pve node network create <iface> --node <node> --type bridge",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "network set",
        "edits a host network interface — covered live by `e2e --mutate` on the staged throwaway bridge (never applied)",
        "pve node network set <iface> --node <node> --type bridge",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "network delete",
        "removes a host network interface — covered live by `e2e --mutate` on the staged throwaway bridge (never applied)",
        "pve node network delete <iface> --node <node> --yes",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "network apply",
        "reloads the staged host network configuration — could cut the node off the network; not exercised live",
        "pve node network apply --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "network revert",
        "discards the staged host network configuration — covered live by `e2e --mutate`, which reverts its own staged bridge changes",
        "pve node network revert --node <node> --yes",
        isolation=True, live_covered=True,
    )

    # The apt-database refresh runs apt-get update on the host — a read-like
    # refresh that touches no guest and rewrites no node config. It is exercised
    # live by `e2e --mutate`. The repository verbs (which rewrite the node's APT
    # sources) stay deferred.
    ctx.defer(
        "apt update",
        "refreshes the node's APT database — covered live by `e2e --mutate`, which runs it as a read-like refresh (skips if no mirror access)",
        "pve node apt update --node <node>",
        isolation=False, live_covered=True,
    )
    # The repository verbs rewrite the node's APT sources. Each is gated behind
    # --yes and covered by a unit test (guard plus argument contract). Deferred
    # one verb at a time so the coverage matrix records every leaf.
    ctx.defer(
        "apt repositories add",
        "adds a standard APT repository to the node's sources; not exercised live",
        "pve node apt repositories add --node <node> --handle no-subscription --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "apt repositories enable",
        "enables or disables a configured APT repository on the node; not exercised live",
        "pve node apt repositories enable --node <node> --yes",
        isolation=False, live_covered=False,
    )

    # Disk initialization formats physical media and is irreversible; it is
    # never exercised live on the shared lab (it would destroy the node's
    # storage). The CLI gates each verb behind --yes, and each is covered by a
    # unit test (--yes guard plus argument contract). Deferred one verb at a time
    # so the coverage matrix records every leaf (create lvm is reachable via its
    # --help check above).
    ctx.defer(
        "disks create lvm",
        "formats a disk into an LVM volume group — irreversible; not exercised live; covered by unit tests",
        "pve node disks create lvm --node <node> --device /dev/sdX --name vg --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "disks create directory",
        "formats a disk and mounts it as a directory storage — irreversible; not exercised live",
        "pve node disks create directory --node <node> --device /dev/sdX --name backups --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "disks create lvmthin",
        "formats a disk into an LVM-thin pool — irreversible; not exercised live",
        "pve node disks create lvmthin --node <node> --device /dev/sdX --name thin --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "disks create zfs",
        "formats one or more disks into a ZFS pool — irreversible; not exercised live",
        "pve node disks create zfs --node <node> --devices /dev/sdX --name tank --raidlevel single --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "disks init-gpt",
        "writes a fresh GPT partition table to a disk — irreversible; not exercised live",
        "pve node disks init-gpt --node <node> --disk /dev/sdX --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "disks wipe",
        "wipes all data and partition tables from a disk — irreversible; not exercised live",
        "pve node disks wipe --node <node> --disk /dev/sdX --yes",
        isolation=False, live_covered=False,
    )
    # Disk sub-type delete verbs destroy the underlying VG, pool, or ZFS dataset
    # and cannot be reversed without reinitializing storage from scratch.
    ctx.defer(
        "disks delete directory",
        "removes a mounted directory storage from the host — irreversible; not exercised live",
        "pve node disks delete directory <path> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "disks delete lvm",
        "removes an LVM volume group from the host — irreversible; not exercised live",
        "pve node disks delete lvm <vg> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "disks delete lvmthin",
        "removes an LVM thin pool from a VG — irreversible; not exercised live",
        "pve node disks delete lvmthin <pool> --volume-group <vg> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "disks delete zfs",
        "destroys a ZFS pool — irreversible, destroys all data on the pool; not exercised live",
        "pve node disks delete zfs <pool> --node <node> --yes",
        isolation=False, live_covered=False,
    )

    # Pulling an OCI image downloads it from a registry into a node storage and
    # leaves an image artifact that has no CLI delete verb to clean it up on the
    # shared lab (same reasoning as `storage upload`), so it is never exercised
    # live. The CLI gates the pull behind --yes.
    ctx.defer(
        "oci pull",
        "downloads an OCI image into a storage — leaves an uncleanable artifact on lab storage; not exercised live; covered by unit tests",
        "pve node oci pull <storage> --node <node> --reference docker.io/library/alpine:latest --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "oci tags",
        "lists the tags of a remote OCI reference — covered live by `e2e --mutate`, which queries a public registry reference (skips if the registry is unreachable)",
        "pve node oci tags <reference> --node <node>",
        isolation=False, live_covered=True,
    )

    # The dns and time write verbs are reversible (get -> set-same -> restore)
    # and are exercised by the mutate phase's node_system_lifecycle.
    ctx.defer(
        "dns/time set",
        "reconfigures node DNS or time zone — reversible; covered live by `e2e --mutate`",
        "pve node dns set --node <node> --search <domain>",
        isolation=True, live_covered=True,
    )
    # /etc/hosts is covered live by `e2e --mutate`: it reads the current file
    # plus its digest and writes the identical bytes back under that digest
    # guard — a no-op replace that leaves the file exactly as found. Changing
    # the node's subscription state (set/refresh/delete) affects licensing, so
    # those stay deferred.
    ctx.defer(
        "hosts set",
        "replaces the whole /etc/hosts file — covered live by `e2e --mutate`, which writes the current content back under a digest guard (no-op)",
        "pve node hosts set --node <node> --data <content> --digest <digest> --yes",
        isolation=True, live_covered=True,
    )
    # `subscription update` only re-reads the current key's status (no licensing
    # change), so it is exercised live by the mutate phase. The set/delete verbs
    # do change the node's licensing state — each is gated behind --yes, covered
    # by a unit test, and stays deferred so the matrix records every leaf.
    ctx.defer(
        "subscription set",
        "sets the node's subscription key (changes licensing state); not exercised live; covered by unit tests",
        "pve node subscription set --node <node> --key <key> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "subscription update",
        "refreshes the node's subscription status against the licensing server — "
        "covered live by `e2e --mutate`; it re-reads the current key's status and "
        "does not set or clear the key (skips if the server is unreachable)",
        "pve node subscription update --node <node> --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "subscription delete",
        "removes the node's subscription key (changes licensing state); not exercised live; covered by unit tests",
        "pve node subscription delete --node <node> --yes",
        isolation=False, live_covered=False,
    )

    # Certificate writes all replace the node's API TLS certificate: an ACME
    # order/renew contacts Let's Encrypt (real account + DNS challenge), and a
    # custom upload/delete could break TLS to the node. None is exercised live.
    # Each cert write verb is gated behind --yes and covered by a unit test
    # (guard plus argument contract). Deferred one verb at a time so the
    # coverage matrix records every leaf.
    ctx.defer(
        "cert acme order",
        "orders the node's ACME certificate (contacts Let's Encrypt); not exercised live",
        "pve node cert acme order --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "cert acme renew",
        "renews the node's ACME certificate (contacts Let's Encrypt); not exercised live",
        "pve node cert acme renew --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "cert acme delete",
        "removes the node's ACME certificate; not exercised live",
        "pve node cert acme delete --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "cert custom upload",
        "replaces the node's API TLS certificate — could break TLS to the node; not exercised live",
        "pve node cert custom upload --node <node> --certificates <pem> --key <pem> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "cert custom delete",
        "removes the node's custom API TLS certificate — could break TLS to the node; not exercised live",
        "pve node cert custom delete --node <node> --yes",
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

    # The remote storage scans each probe a storage server for its
    # shares/targets/exports/datastores. Rather than an external server, the mutate
    # phase points them at the node itself: cifs/iscsi hit services the node already
    # exposes, pbs is answered by a host-local HTTPS stub pinned by cert fingerprint,
    # and nfs-kernel-server is installed for the nfs probe and purged afterward.
    ctx.defer(
        "scan nfs",
        "probes an NFS server for its exports — covered live by `e2e --mutate`, which "
        "installs nfs-kernel-server, exports a throwaway dir to localhost, scans it, "
        "then purges the package",
        "pve node scan nfs --node <node> --server <server>",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "scan cifs",
        "probes a CIFS/SMB server for its shares — covered live by `e2e --mutate`, which "
        "scans the node's own smbd on 127.0.0.1",
        "pve node scan cifs --node <node> --server <server>",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "scan iscsi",
        "probes an iSCSI portal for its targets — covered live by `e2e --mutate`, which "
        "scans 127.0.0.1 on the node",
        "pve node scan iscsi --node <node> --portal <portal>",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "scan pbs",
        "probes a Proxmox Backup Server for its datastores — covered live by `e2e --mutate`, "
        "which answers the scan from a host-local HTTPS stub pinned by cert fingerprint",
        "pve node scan pbs --node <node> --server <server> --username <user> --password <secret>",
        isolation=False, live_covered=True,
    )

    # Every Ceph write verb is cluster-destructive: init lays down a new Ceph
    # cluster, OSD/pool/mon/mds/mgr/fs create and delete provision or destroy
    # daemons and data, and start/stop/restart control running Ceph services.
    # None is exercised live on the shared lab; the CLI gates each behind --yes
    # and each verb is covered by a unit test (guard + argument contract).
    # Deferred one verb at a time so the coverage matrix records every leaf.
    ctx.defer(
        "ceph init",
        "initializes a Ceph cluster configuration on the node — cluster-wide and destructive; not exercised live",
        "pve node ceph init --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd create",
        "creates an OSD by wiping and consuming a block device; not exercised live",
        "pve node ceph osd create --node <node> --dev /dev/sdb --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd delete",
        "destroys an OSD and optionally zaps its underlying volumes; not exercised live",
        "pve node ceph osd delete <osdid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd in",
        "marks an OSD in, triggering cluster data movement; not exercised live",
        "pve node ceph osd in <osdid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd out",
        "marks an OSD out, draining its data across the cluster; not exercised live",
        "pve node ceph osd out <osdid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd scrub",
        "triggers an OSD scrub that adds cluster I/O load; not exercised live",
        "pve node ceph osd scrub <osdid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph pool create",
        "creates a Ceph pool, consuming cluster capacity; not exercised live",
        "pve node ceph pool create <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph pool set",
        "reconfigures an existing Ceph pool's parameters; not exercised live",
        "pve node ceph pool set <name> --node <node> --size 3 --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph pool delete",
        "destroys a Ceph pool and permanently loses its data; not exercised live",
        "pve node ceph pool delete <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mon create",
        "provisions a Ceph monitor daemon on the node; not exercised live",
        "pve node ceph mon create <monid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mon delete",
        "destroys a Ceph monitor daemon on the node; not exercised live",
        "pve node ceph mon delete <monid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mds create",
        "provisions a Ceph metadata-server daemon on the node; not exercised live",
        "pve node ceph mds create <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mds delete",
        "destroys a Ceph metadata-server daemon on the node; not exercised live",
        "pve node ceph mds delete <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mgr create",
        "provisions a Ceph manager daemon on the node; not exercised live",
        "pve node ceph mgr create <id> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mgr delete",
        "destroys a Ceph manager daemon on the node; not exercised live",
        "pve node ceph mgr delete <id> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph fs create",
        "creates a CephFS filesystem and its backing pools; not exercised live",
        "pve node ceph fs create <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph fs delete",
        "destroys a CephFS filesystem and optionally its pools; not exercised live",
        "pve node ceph fs delete <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph start",
        "starts Ceph services on the node — disruptive; not exercised live",
        "pve node ceph start --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph stop",
        "stops Ceph services on the node — disruptive; not exercised live",
        "pve node ceph stop --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph restart",
        "restarts Ceph services on the node — disruptive; not exercised live",
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
    # shell and its console alias open a live SSH terminal that cannot be driven
    # head-less, so they are never run live; each builds the SSH invocation the
    # same way and is covered by a unit test (the console alias resolves to the
    # shell handler, which builds the expected `root@<ip>` target). Deferred one
    # verb at a time so the matrix records each leaf.
    ctx.defer("shell", "opens a live SSH terminal on the node, so it cannot be driven head-less; not run live; covered by unit tests",
              "pve node shell <node>")
    ctx.defer("console", "opens a live SSH terminal aliased to `node shell`, so it cannot be driven head-less; not run live; covered by unit tests",
              "pve node console <node>")

    # Service control mutates running host services on the live node. Every verb
    # is built by the same factory and covered by a unit test (argument contract
    # and task handling). Deferred one verb at a time so the matrix records each
    # leaf.
    ctx.defer("services start", "starts a running host service on the node; not exercised live; covered by unit tests",
              "pve node services start <node> <svc>")
    ctx.defer("services stop", "stops a running host service on the node; not exercised live; covered by unit tests",
              "pve node services stop <node> <svc>")
    ctx.defer("services restart", "restarts a running host service on the node; not exercised live; covered by unit tests",
              "pve node services restart <node> <svc>")
    ctx.defer("services reload", "reloads a running host service on the node; not exercised live; covered by unit tests",
              "pve node services reload <node> <svc>")

    # Node-wide bulk actions act on every guest on the node by default, but
    # --vmids narrows them to a subset. startall/stopall/suspendall are driven
    # live by the mutate phase scoped to ONLY the isolated pve-cli VM, so they
    # touch no other workload. migrateall needs a second node and wakeonlan
    # powers a node on, so both stay deferred; --help exercises their parsing.
    ctx.check("startall --help", "node", "startall", "--help", fmt="")
    ctx.check("stopall --help", "node", "stopall", "--help", fmt="")
    ctx.check("suspendall --help", "node", "suspendall", "--help", fmt="")
    ctx.check("migrateall --help", "node", "migrateall", "--help", fmt="")
    ctx.check("wakeonlan --help", "node", "wakeonlan", "--help", fmt="")
    ctx.defer("startall", "starts guests on the node — covered live by `e2e --mutate` scoped to the isolated pve-cli VM via --vmids",
              "pve node startall --vmids <vmid> --yes", isolation=True, live_covered=True)
    ctx.defer("stopall", "stops guests on the node — covered live by `e2e --mutate` scoped to the isolated pve-cli VM via --vmids",
              "pve node stopall --vmids <vmid> --yes", isolation=True, live_covered=True)
    ctx.defer("suspendall", "suspends guests on the node — covered live by `e2e --mutate` scoped to the isolated pve-cli VM via --vmids (pauses the QEMU process)",
              "pve node suspendall --vmids <vmid> --yes", isolation=True, live_covered=True)
    ctx.defer("migrateall", "migrates every guest off the node to a target (needs a second node); not exercised live; covered by unit tests",
              "pve node migrateall --node <node> --target <node2> --yes", isolation=False, live_covered=False)
    ctx.defer(
        "wakeonlan",
        "sends a Wake-on-LAN packet to power on a node — affects node power state; not exercised live; covered by unit tests",
        "pve node wakeonlan --node <node> --yes",
        isolation=False, live_covered=False,
    )
