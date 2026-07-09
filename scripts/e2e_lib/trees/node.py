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

    ctx.check("list", "pve", "node", "list", validate=has_node)

    # config/firewall-options describe: offline schema catalogs — no API call,
    # so they run even before node discovery.
    ctx.check("config describe", "pve", "node", "config", "describe")
    ctx.check("firewall options describe", "pve", "node", "firewall", "options", "describe")

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

        ctx.check("status", "pve", "node", "status", n, validate=has_node_status)

        # permissions: ACL entries scoped to the node's /nodes/{node} path.
        # Both are cluster-wide ACL queries (no --node routing needed for the
        # API call itself, even though the node name is the positional id).
        # `grant`/`revoke` mutate cluster-wide ACLs and are deferred below.
        def has_permissions_effective(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), dict) else "expected a JSON object"

        def is_list_local(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), list) else "expected a JSON array"

        ctx.check("permissions list", "pve", "node", "permissions", "list", n, validate=is_list_local)
        ctx.check("permissions effective", "pve", "node", "permissions", "effective", n,
                  validate=has_permissions_effective)

        svc = ctx.check("services list", "pve", "node", "services", "list", n)
        if svc.rc == 0:
            try:
                name = ctx.first(svc.json(), "service") or ctx.first(svc.json(), "name")
            except ValueError:
                name = None
            if name:
                ctx.check("services get", "pve", "node", "services", "get", n, str(name))
            else:
                ctx.skip("services get", "no service to inspect")
        tasks = ctx.check("task list", "pve", "node", "task", "list", n)
        # `node task log` reads one task's log by UPID; conditional on the list
        # returning a task (◑). Mirrors the top-level `task log` check.
        upid = None
        if tasks.rc == 0:
            try:
                upid = ctx.first(tasks.json(), "upid") or ctx.first(tasks.json(), "id")
            except ValueError:
                upid = None
        if upid:
            ctx.check("task log", "pve", "node", "task", "log", n, str(upid))
            # task status: single-UPID runtime status, resolved against the
            # already-known node (--node/global node context).
            ctx.check("task status", "pve", "node", "task", "status", str(upid), node=n)
        else:
            ctx.skip("task log", "no task in the node task list")
            ctx.skip("task status", "no task in the node task list")
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
            ctx.check("task wait", "pve", "node", "task", "wait", str(ok_upid), "--timeout", "30")
        else:
            ctx.skip("task wait", "no successfully-finished task in the node task list")

        # Host firewall: the rule list is an array (empty when the node has no
        # host rules); options is a key/value object. Both are read-only and
        # safe to query directly. The firewall is node-scoped, so the node is
        # supplied through the --node selector rather than a positional arg.
        def is_list(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), list) else "expected a JSON array"

        ctx.check("firewall rules list", "pve", "node", "firewall", "rules", "list",
                  node=n, validate=is_list)
        ctx.check("firewall options get", "pve", "node", "firewall", "options", "get", node=n)
        ctx.check("firewall log", "pve", "node", "firewall", "log", node=n)
        ctx.check("firewall rules create --help", "pve", "node", "firewall", "rules", "create",
                  "--help", fmt="")

        # Host network: the interface list is an array (always non-empty — at
        # least the management bridge exists); a single interface is a key/value
        # object. Both are read-only and safe to query. The network commands are
        # node-scoped via the --node selector. The write verbs (create/set/
        # delete/apply/revert) edit the host networking stack and could cut the
        # node off the network, so they are never exercised live.
        net = ctx.check("network list", "pve", "node", "network", "list", node=n, validate=is_list)
        iface = None
        if net.rc == 0:
            try:
                iface = ctx.first(net.json(), "iface")
            except ValueError:
                iface = None
        if iface:
            ctx.check("network get", "pve", "node", "network", "get", str(iface), node=n)
        else:
            ctx.skip("network get", "no interface to inspect")
        ctx.check("network create --help", "pve", "node", "network", "create", "--help", fmt="")

        # APT package management: pending updates, installed versions, and the
        # standard-repository status are all local read-only queries. The
        # changelog is fetched per package — discover a package name from the
        # installed versions (PVE uses the capitalized "Package" key). The
        # apt-database refresh and any repository edit are deferred below.
        ctx.check("apt list", "pve", "node", "apt", "list", node=n, validate=is_list)
        ver = ctx.check("apt versions", "pve", "node", "apt", "versions", node=n, validate=is_list)
        pkg = None
        if ver.rc == 0:
            try:
                pkg = ctx.first(ver.json(), "Package")
            except ValueError:
                pkg = None
        if pkg:
            ctx.check("apt changelog", "pve", "node", "apt", "changelog", "--name", str(pkg), node=n, fmt="")
        else:
            ctx.skip("apt changelog", "no installed package to inspect")
        ctx.check("apt repositories list", "pve", "node", "apt", "repositories", "list", node=n)
        ctx.check("apt update --help", "pve", "node", "apt", "update", "--help", fmt="")
        # apt templates list mirrors `storage aplinfo list` (same underlying
        # catalog); it needs egress to the Proxmox template repository, so probe
        # first and skip gracefully rather than failing the sweep.
        apt_templates_probe = ctx.run("pve", "node", "apt", "templates", "list", node=n)
        if apt_templates_probe.rc == 0:
            ctx.check("apt templates list", "pve", "node", "apt", "templates", "list", node=n)
        else:
            ctx.skip("apt templates list", "appliance template catalog not reachable from this node")

        # Disks: the physical-disk inventory is an array; SMART is read per disk
        # (discover a block device from the inventory). The disk-initialization
        # verbs (create/init-gpt/wipe) format physical media and are deferred
        # below; only their --help is exercised here.
        disks = ctx.check("disks list", "pve", "node", "disks", "list", node=n, validate=is_list)
        dev = None
        if disks.rc == 0:
            try:
                dev = ctx.first(disks.json(), "devpath")
            except ValueError:
                dev = None
        if dev:
            ctx.check("disks smart", "pve", "node", "disks", "smart", "--disk", str(dev), node=n)
        else:
            ctx.skip("disks smart", "no block device to inspect")

        # Disk sub-type inventories: each returns a list (possibly empty on a lab
        # that lacks the given storage layout). Empty list is a valid pass.
        ctx.check("disks ls directory", "pve", "node", "disks", "ls", "directory",
                  node=n, validate=is_list)
        # lvm reports a volume-group tree as an object ({"children": [...]}),
        # unlike the other disk sub-types which return arrays.
        lvm_tree = ctx.check("disks ls lvm", "pve", "node", "disks", "ls", "lvm", node=n,
                             validate=lambda r: None if isinstance(r.json(), (dict, list))
                             else "expected a JSON object or array")
        ctx.check("disks ls lvmthin", "pve", "node", "disks", "ls", "lvmthin",
                  node=n, validate=is_list)
        zfs_list = ctx.check("disks ls zfs", "pve", "node", "disks", "ls", "zfs",
                             node=n, validate=is_list)
        # disks get zfs: detail for a specific pool; discover from the ls output.
        zfs_pool = None
        if zfs_list.rc == 0:
            try:
                zfs_pool = ctx.first(zfs_list.json(), "name")
            except (ValueError, KeyError):
                zfs_pool = None
        if zfs_pool:
            ctx.check("disks get zfs", "pve", "node", "disks", "get", "zfs", str(zfs_pool), node=n)
        else:
            ctx.skip("disks get zfs", "no ZFS pool on this node")

        ctx.check("disks create lvm --help", "pve", "node", "disks", "create", "lvm", "--help", fmt="")

        # Scan: the lvm and zfs probes enumerate local storage with no arguments
        # and are always safe. The remote probes (nfs/cifs/iscsi/pbs) need a
        # reachable server and credentials, so they are deferred below.
        ctx.check("scan lvm", "pve", "node", "scan", "lvm", node=n, validate=is_list)
        ctx.check("scan zfs", "pve", "node", "scan", "zfs", node=n, validate=is_list)

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
            ctx.check("scan lvmthin", "pve", "node", "scan", "lvmthin", "--vg", vg_name,
                      node=n, validate=is_list)
        else:
            ctx.skip("scan lvmthin", "no LVM volume group on this node")

        # Hardware: PCI(e) and USB inventories are read-only arrays.
        pci_list = ctx.check("hardware pci", "pve", "node", "hardware", "pci",
                             node=n, validate=is_list)
        ctx.check("hardware usb", "pve", "node", "hardware", "usb", node=n, validate=is_list)
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
            mdev_res = ctx.run("pve", "node", "hardware", "mdev", str(pci_id), node=n)
            mdev_err = (mdev_res.stderr or mdev_res.stdout).lower()
            if mdev_res.rc != 0 and any(
                m in mdev_err for m in ("mdev", "no such", "not supported", "404")
            ):
                ctx.skip("hardware pci mdev",
                         f"PCI device {pci_id} does not expose mdev types")
            else:
                ctx.check("hardware pci mdev", "pve", "node", "hardware", "mdev",
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

        # config get: node-level configuration (description, ACME, wake-on-LAN,
        # ballooning target, startall delay) — a key/value object, always safe.
        ctx.check("config get", "pve", "node", "config", "get", node=n, validate=is_object)
        ctx.check("dns get", "pve", "node", "dns", "get", node=n, validate=is_object)
        ctx.check("time get", "pve", "node", "time", "get", node=n, validate=is_object)
        ctx.check("hosts get", "pve", "node", "hosts", "get", node=n, fmt="")
        # The journal and report can be large; --lastentries bounds the journal.
        ctx.check("journal", "pve", "node", "journal", "--lastentries", "20", node=n, fmt="")
        ctx.check("syslog", "pve", "node", "syslog", "--limit", "20", node=n, validate=is_list)
        ctx.check("report", "pve", "node", "report", node=n, fmt="")
        ctx.check("subscription get", "pve", "node", "subscription", "get", node=n, validate=is_object)
        # rrddata: timeseries for node-level metrics; zero-row result is valid.
        # Node is supplied via the global --node flag, not a positional arg.
        ctx.check("rrddata", "pve", "node", "rrddata", "--timeframe", "hour",
                  node=n, validate=is_list)
        # netstat: per-interface network statistics; always returns a list.
        ctx.check("netstat", "pve", "node", "netstat", node=n, validate=is_list)
        # vzdump defaults: default vzdump settings; always a key/value object.
        ctx.check("vzdump defaults", "pve", "node", "vzdump", "defaults", node=n, validate=is_object)
        # vzdump extract-config: print the guest configuration embedded in a
        # backup archive. Read-only — it parses an existing backup file and emits
        # the config text. Discover a backup volume from cluster storage; skip
        # when the lab has no backup archive to read.
        backup_volid = None
        storages = ctx.run("pve", "storage", "list")
        if storages.rc == 0:
            try:
                names = [s.get("storage") for s in storages.json()
                         if isinstance(s, dict) and s.get("storage")]
            except (ValueError, AttributeError):
                names = []
            for sname in names:
                listing = ctx.run("pve", "storage", "content", str(sname),
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
            ctx.check("vzdump extract-config", "pve", "node", "vzdump", "extract-config",
                      "--volume", str(backup_volid), node=n, fmt="")
        else:
            ctx.skip("vzdump extract-config", "no backup archive found in cluster storage")
        ctx.check("dns set --help", "pve", "node", "dns", "set", "--help", fmt="")
        ctx.check("hosts set --help", "pve", "node", "hosts", "set", "--help", fmt="")

        # Certificates: the cert chain serving the node's API and the ACME state
        # are read-only. Every write verb (ACME order/renew/delete, custom
        # upload/delete) replaces the node's TLS certificate or contacts an ACME
        # directory, so each is deferred below rather than exercised live.
        ctx.check("cert list", "pve", "node", "cert", "list", node=n, validate=is_list)
        # cert acme list shows the pveproxy-ssl.pem info row when an ACME or
        # custom certificate is installed, and a plain message when only the
        # self-signed pve-ssl.pem exists — so no list-shape validation.
        ctx.check("cert acme list", "pve", "node", "cert", "acme", "list", node=n)
        ctx.check("cert acme order --help", "pve", "node", "cert", "acme", "order", "--help", fmt="")
        ctx.check("cert custom upload --help", "pve", "node", "cert", "custom", "upload", "--help", fmt="")

        # Storage replication: the per-node job list, plus a job's status and
        # log, are read-only. A job's id is discovered from the list; on a
        # standalone node with no replication configured the list is empty, so
        # status and log are skipped. `run` triggers an immediate sync and is
        # deferred below.
        repl = ctx.check("replication list", "pve", "node", "replication", "list", node=n, validate=is_list)
        repl_id = None
        if repl.rc == 0:
            try:
                repl_id = ctx.first(repl.json(), "id")
            except ValueError:
                repl_id = None
        if repl_id:
            ctx.check("replication get", "pve", "node", "replication", "get", str(repl_id), node=n)
            ctx.check("replication status", "pve", "node", "replication", "status", str(repl_id), node=n)
            ctx.check("replication log", "pve", "node", "replication", "log", str(repl_id), node=n, validate=is_list)
        else:
            ctx.skip("replication get", "no replication job configured on this node")
            ctx.skip("replication status", "no replication job configured on this node")
            ctx.skip("replication log", "no replication job configured on this node")
        ctx.check("replication run --help", "pve", "node", "replication", "run", "--help", fmt="")

        # Ceph: cluster status, the configuration database, and the OSD and pool
        # inventories are read-only. The lab node has no Ceph cluster
        # configured, so the API errors there — probe `ceph status` once and
        # skip the whole read-only Ceph sweep when Ceph is absent rather than
        # recording failures. Every create/delete/init and service-control verb
        # is cluster-destructive and is parsed-and-deferred below, never run
        # live.
        ceph_status = ctx.run("pve", "node", "ceph", "status", node=n)
        ceph_err = (ceph_status.stderr or ceph_status.stdout).lower()
        ceph_absent = ceph_status.rc != 0 and any(
            m in ceph_err for m in ("ceph", "not installed", "binary not installed", "rados")
        )
        if ceph_absent:
            for probe in (
                "ceph status", "ceph cfg", "ceph osd list", "ceph pool list",
                "ceph fs list", "ceph mds list", "ceph mgr list", "ceph mon list",
                "ceph osd get", "ceph pool get", "ceph pool status",
                "ceph cfg index", "ceph cfg db", "ceph cfg raw", "ceph cfg value",
                "ceph crush", "ceph log", "ceph rules",
                "ceph cmd-safety", "ceph osd lv-info", "ceph osd metadata",
            ):
                ctx.skip(probe, "Ceph is not configured on the lab node")
        else:
            ctx.check("ceph status", "pve", "node", "ceph", "status", node=n, validate=is_object)
            ctx.check("ceph cfg", "pve", "node", "ceph", "cfg", node=n, validate=is_list)
            # cfg index/db/raw are alternate views of the same configuration
            # store; each is read-only and safe once Ceph is configured.
            ctx.check("ceph cfg index", "pve", "node", "ceph", "cfg", "index", node=n, validate=is_list)
            ctx.check("ceph cfg db", "pve", "node", "ceph", "cfg", "db", node=n, validate=is_list)
            ctx.check("ceph cfg raw", "pve", "node", "ceph", "cfg", "raw", node=n, fmt="")
            # cfg value looks up specific <section>:<key> pairs; fsid is written
            # to ceph.conf's [global] section by `ceph init` and is always
            # present once Ceph is configured, so probe it before asserting.
            cfg_value_probe = ctx.run("pve", "node", "ceph", "cfg", "value", "--keys", "global:fsid", node=n)
            if cfg_value_probe.rc == 0:
                ctx.check("ceph cfg value", "pve", "node", "ceph", "cfg", "value",
                          "--keys", "global:fsid", node=n, fmt="")
            else:
                ctx.skip("ceph cfg value", "global:fsid not present in this cluster's ceph.conf")
            ctx.check("ceph crush", "pve", "node", "ceph", "crush", node=n, fmt="")
            ctx.check("ceph log", "pve", "node", "ceph", "log", "--limit", "20", node=n, validate=is_list)
            ctx.check("ceph rules", "pve", "node", "ceph", "rules", node=n, validate=is_list)
            osd_tree = ctx.check("ceph osd list", "pve", "node", "ceph", "osd", "list",
                                 node=n, validate=is_object)
            pool_list = ctx.check("ceph pool list", "pve", "node", "ceph", "pool", "list",
                                  node=n, validate=is_list)

            # The MDS, MGR, monitor, and CephFS inventories are read-only lists
            # (each empty until the matching daemon is deployed). Safe to query
            # directly on a Ceph-enabled node.
            ctx.check("ceph fs list", "pve", "node", "ceph", "fs", "list", node=n, validate=is_list)
            ctx.check("ceph mds list", "pve", "node", "ceph", "mds", "list", node=n, validate=is_list)
            ctx.check("ceph mgr list", "pve", "node", "ceph", "mgr", "list", node=n, validate=is_list)
            ctx.check("ceph mon list", "pve", "node", "ceph", "mon", "list", node=n, validate=is_list)

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
                ctx.check("ceph osd get", "pve", "node", "ceph", "osd", "get", osd_id,
                          node=n, validate=is_object)
                ctx.check("ceph osd lv-info", "pve", "node", "ceph", "osd", "lv-info", osd_id,
                          node=n, validate=is_object)
                ctx.check("ceph osd metadata", "pve", "node", "ceph", "osd", "metadata", osd_id,
                          node=n, validate=is_object)
                # cmd-safety asks Ceph's own heuristics whether stopping this OSD
                # would be safe right now — a read-only query, not a mutation.
                ctx.check("ceph cmd-safety", "pve", "node", "ceph", "cmd-safety",
                          "--action", "stop", "--id", f"osd.{osd_id}", "--service", "osd",
                          node=n, validate=is_object)
            else:
                ctx.skip("ceph osd get", "no OSD deployed on this Ceph cluster")
                ctx.skip("ceph osd lv-info", "no OSD deployed on this Ceph cluster")
                ctx.skip("ceph osd metadata", "no OSD deployed on this Ceph cluster")
                ctx.skip("ceph cmd-safety", "no OSD deployed on this Ceph cluster")

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
                ctx.check("ceph pool get", "pve", "node", "ceph", "pool", "get", str(pool_name),
                          node=n, validate=is_object)
                ctx.check("ceph pool status", "pve", "node", "ceph", "pool", "status", str(pool_name),
                          node=n, validate=is_object)
            else:
                ctx.skip("ceph pool get", "no Ceph pool configured")
                ctx.skip("ceph pool status", "no Ceph pool configured")
        ctx.check("ceph osd create --help", "pve", "node", "ceph", "osd", "create", "--help", fmt="")
        ctx.check("ceph pool create --help", "pve", "node", "ceph", "pool", "create", "--help", fmt="")

        # QEMU capability queries are read-only: the node reports the CPU
        # models and machine types it can offer guests, plus its live-migration
        # features. All three are safe to run live.
        ctx.check("capabilities qemu cpu", "pve", "node", "capabilities", "qemu", "cpu",
                  node=n, validate=is_list)
        ctx.check("capabilities qemu machines", "pve", "node", "capabilities", "qemu", "machines",
                  node=n, validate=is_list)
        ctx.check("capabilities qemu migration", "pve", "node", "capabilities", "qemu", "migration",
                  node=n, validate=is_object)
        # capabilities qemu cpu-flags: per-CPU-model flag detail; always present.
        ctx.check("capabilities qemu cpu-flags", "pve", "node", "capabilities", "qemu", "cpu-flags",
                  node=n, validate=is_list)

        # OCI image handling: `oci tags` queries a registry (network egress) and
        # is exercised live by the mutate phase against a public reference; `oci
        # pull` writes an image artifact to a storage with no CLI delete verb to
        # clean it up, so it stays deferred and is exercised by --help only.
        ctx.check("oci tags --help", "pve", "node", "oci", "tags", "--help", fmt="")
        ctx.check("oci pull --help", "pve", "node", "oci", "pull", "--help", fmt="")

        # query-url-metadata: asks the node to fetch a remote URL and report its
        # metadata (size, mime type, filename) via an HTTP HEAD. The only working
        # target is an external URL, so it is exercised live by the mutate phase
        # against a stable public URL and skips if that URL is unreachable.
        ctx.defer(
            "query-url-metadata",
            "fetches metadata from an external URL via HTTP HEAD — covered live by "
            "`e2e --mutate`, which points it at a stable public URL (skips if that "
            "URL is unreachable)",
            "pmx pve node query-url-metadata --node <node> --url https://example.com/image.iso",
            isolation=False, live_covered=True,
        )

        # services state: read the runtime state of a known service on the node.
        # pveproxy is always present on a PVE node — use it as a stable probe.
        ctx.check("services state", "pve", "node", "services", "state", n, "pveproxy", node=n)

    # `node task stop` aborts a running task; it stays deferred in this
    # read-only sweep but is exercised live by the mutate phase (which spawns a
    # deterministic server-side shutdown task and aborts it).
    ctx.defer("task stop", "aborts a running task — covered live by `e2e --mutate`",
              "pmx pve node task stop <node> <upid>", live_covered=True)

    # `permissions grant`/`revoke` mutate cluster-wide ACLs; not wired into the
    # mutate phase. `permissions list`/`effective` above are read-only and
    # exercised live.
    ctx.defer(
        "permissions grant",
        "grants ACL roles on the node's /nodes/{node} path; mutates "
        "cluster-wide ACLs, not wired into the mutate phase; covered by unit tests",
        "pmx pve node permissions grant <node> --roles PVEAuditor --users alice@pve",
    )
    ctx.defer(
        "permissions revoke",
        "revokes ACL roles on the node's /nodes/{node} path; mutates "
        "cluster-wide ACLs, not wired into the mutate phase; covered by unit tests",
        "pmx pve node permissions revoke <node> --roles PVEAuditor --users alice@pve",
    )

    # The mutate phase appends a disabled host firewall rule tagged with the
    # pmx-cli comment, finds it by comment, then deletes it — covered live
    # there. The host firewall OPTIONS are read only (enabling the host
    # firewall could cut the node off the network).
    ctx.defer(
        "firewall rule create/delete",
        "appends then removes a disabled host firewall rule — covered live by `e2e --mutate`",
        "pmx pve node firewall rules create --node <node> --type in --action ACCEPT --enable 0 --comment pmx-cli-e2e",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "firewall options set",
        "changes the host firewall policy — could cut the node off the network; not exercised live",
        "pmx pve node firewall options set --node <node> --enable 1",
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
        "pmx pve node network create <iface> --node <node> --type bridge",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "network set",
        "edits a host network interface — covered live by `e2e --mutate` on the staged throwaway bridge (never applied)",
        "pmx pve node network set <iface> --node <node> --type bridge",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "network delete",
        "removes a host network interface — covered live by `e2e --mutate` on the staged throwaway bridge (never applied)",
        "pmx pve node network delete <iface> --node <node> --yes",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "network apply",
        "reloads the staged host network configuration — could cut the node off the network; not exercised live",
        "pmx pve node network apply --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "network revert",
        "discards the staged host network configuration — covered live by `e2e --mutate`, which reverts its own staged bridge changes",
        "pmx pve node network revert --node <node> --yes",
        isolation=True, live_covered=True,
    )

    # The apt-database refresh runs apt-get update on the host — a read-like
    # refresh that touches no guest and rewrites no node config. It is exercised
    # live by `e2e --mutate`. The repository verbs (which rewrite the node's APT
    # sources) stay deferred.
    ctx.defer(
        "apt update",
        "refreshes the node's APT database — covered live by `e2e --mutate`, which runs it as a read-like refresh (skips if no mirror access)",
        "pmx pve node apt update --node <node>",
        isolation=False, live_covered=True,
    )
    # The repository verbs rewrite the node's APT sources. Each is gated behind
    # --yes and covered by a unit test (guard plus argument contract). Deferred
    # one verb at a time so the coverage matrix records every leaf.
    ctx.defer(
        "apt repositories add",
        "adds a standard APT repository to the node's sources; not exercised live",
        "pmx pve node apt repositories add --node <node> --handle no-subscription --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "apt repositories enable",
        "enables or disables a configured APT repository on the node; not exercised live",
        "pmx pve node apt repositories enable --node <node> --yes",
        isolation=False, live_covered=False,
    )
    # apt templates download pulls a real template tarball onto the node's
    # storage — bandwidth/storage-consuming; not exercised live from this tree
    # (distinct from `node oci pull`, which is mutate-covered).
    ctx.defer(
        "apt templates download",
        "downloads a real appliance template tarball to a storage — "
        "bandwidth/storage-consuming; not exercised live; covered by unit tests",
        "pmx pve node apt templates download --node <node> --storage local --template debian-12-standard",
        isolation=False, live_covered=False,
    )

    # Disk initialization formats physical media. The lifecycle runner exercises
    # the create/init-gpt and delete verbs live against a single dedicated spare
    # NVMe pinned by serial number: it resolves the device from `disks list`, hard
    # asserts the disk is unused (used in ('', None)) before touching it, then runs
    # each create -> assert -> paired delete --cleanup-disks round-trip so the disk
    # is returned to its pristine state. If the spare is absent, in use, or the
    # host is unreachable the runner skips every verb instead of touching real
    # storage. See node_disks_lifecycle in scripts/e2e_lib/lifecycle.py.
    ctx.defer(
        "disks create lvm",
        "create -> delete round-trip on a dedicated spare NVMe (pinned by serial, asserted unused)",
        "pmx pve node disks create lvm --node <node> --device /dev/sdX --name vg --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "disks create directory",
        "create -> delete round-trip on a dedicated spare NVMe (formats ext4, mounts, then unmounts and removes)",
        "pmx pve node disks create directory --node <node> --device /dev/sdX --name backups --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "disks create lvmthin",
        "create -> delete round-trip on a dedicated spare NVMe (pinned by serial, asserted unused)",
        "pmx pve node disks create lvmthin --node <node> --device /dev/sdX --name thin --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "disks create zfs",
        "create -> delete round-trip on a dedicated spare NVMe (single-vdev pool, asserted unused)",
        "pmx pve node disks create zfs --node <node> --devices /dev/sdX --name tank --raidlevel single --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "disks init-gpt",
        "writes a fresh GPT label to the dedicated spare NVMe (asserted unused) before the create round-trips",
        "pmx pve node disks init-gpt --node <node> --disk /dev/sdX --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "disks wipe",
        "BLOCKED: /nodes/{node}/disks/wipedisk is root@pam-only and rejects the API token "
        "('user != root@pam'), like storage volume copy and cluster acme account; not invokable by the suite",
        "pmx pve node disks wipe --node <node> --disk /dev/sdX --yes",
        isolation=False, live_covered=False,
    )
    # Disk sub-type delete verbs destroy the underlying VG, pool, or ZFS dataset.
    # Each is exercised live as the teardown half of its create round-trip on the
    # dedicated spare NVMe (see node_disks_lifecycle), with --cleanup-disks so the
    # spare is freed for the next type.
    ctx.defer(
        "disks delete directory",
        "delete half of the directory create round-trip on the dedicated spare NVMe (unmounts, removes mount unit and config)",
        "pmx pve node disks delete directory <path> --node <node> --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "disks delete lvm",
        "delete half of the lvm create round-trip on the dedicated spare NVMe (--cleanup-disks frees the device)",
        "pmx pve node disks delete lvm <vg> --node <node> --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "disks delete lvmthin",
        "delete half of the lvmthin create round-trip on the dedicated spare NVMe (--volume-group, --cleanup-disks)",
        "pmx pve node disks delete lvmthin <pool> --volume-group <vg> --node <node> --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "disks delete zfs",
        "delete half of the zfs create round-trip on the dedicated spare NVMe (--cleanup-disks, host-side GPT zap on teardown)",
        "pmx pve node disks delete zfs <pool> --node <node> --yes",
        isolation=False, live_covered=True,
    )

    # Pulling an OCI image downloads it from a registry into a node storage as an
    # ordinary vztmpl volume, which `storage volume delete` removes, so the mutate
    # phase pulls a small public image and deletes the artifact. The CLI gates the
    # pull behind --yes.
    ctx.defer(
        "oci pull",
        "downloads an OCI image into a storage — covered live by `e2e --mutate`, which pulls a "
        "small public image and deletes the resulting vztmpl volume (skips without registry egress)",
        "pmx pve node oci pull <storage> --node <node> --reference docker.io/library/alpine:latest --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "oci tags",
        "lists the tags of a remote OCI reference — covered live by `e2e --mutate`, which queries a public registry reference (skips if the registry is unreachable)",
        "pmx pve node oci tags <reference> --node <node>",
        isolation=False, live_covered=True,
    )

    # The dns and time write verbs are reversible (get -> set-same -> restore)
    # and are exercised by the mutate phase's node_system_lifecycle.
    ctx.defer(
        "dns/time set",
        "reconfigures node DNS or time zone — reversible; covered live by `e2e --mutate`",
        "pmx pve node dns set --node <node> --search <domain>",
        isolation=True, live_covered=True,
    )
    # node config set mutates node-level configuration (description, ACME,
    # wake-on-LAN, ballooning target, startall delay). Unlike dns/time set it is
    # not yet driven by the mutate phase, so it is parsed-and-deferred here.
    ctx.defer(
        "config set",
        "mutates node-level configuration (description, ACME, wake-on-LAN, "
        "ballooning target, startall delay); not exercised live; covered by unit tests",
        "pmx pve node config set --node <node> --description 'pmx-cli-e2e'",
        isolation=False, live_covered=False,
    )
    # /etc/hosts is covered live by `e2e --mutate`: it reads the current file
    # plus its digest and writes the identical bytes back under that digest
    # guard — a no-op replace that leaves the file exactly as found. Changing
    # the node's subscription state (set/refresh/delete) affects licensing, so
    # those stay deferred.
    ctx.defer(
        "hosts set",
        "replaces the whole /etc/hosts file — covered live by `e2e --mutate`, which writes the current content back under a digest guard (no-op)",
        "pmx pve node hosts set --node <node> --data <content> --digest <digest> --yes",
        isolation=True, live_covered=True,
    )
    # `subscription update` only re-reads the current key's status (no licensing
    # change), so it is exercised live by the mutate phase. The set/delete verbs
    # do change the node's licensing state — each is gated behind --yes, covered
    # by a unit test, and stays deferred so the matrix records every leaf.
    ctx.defer(
        "subscription set",
        "sets the node's subscription key (changes licensing state); not exercised live; covered by unit tests",
        "pmx pve node subscription set --node <node> --key <key> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "subscription update",
        "refreshes the node's subscription status against the licensing server — "
        "covered live by `e2e --mutate`; it re-reads the current key's status and "
        "does not set or clear the key (skips if the server is unreachable)",
        "pmx pve node subscription update --node <node> --yes",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "subscription delete",
        "removes the node's subscription key — covered live by `e2e --mutate`, which runs it "
        "only on a node with no active key (idempotent; never removes a real licence)",
        "pmx pve node subscription delete --node <node> --yes",
        isolation=False, live_covered=True,
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
        "pmx pve node cert acme order --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "cert acme renew",
        "renews the node's ACME certificate (contacts Let's Encrypt); not exercised live",
        "pmx pve node cert acme renew --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "cert acme delete",
        "removes the node's ACME certificate; not exercised live",
        "pmx pve node cert acme delete --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "cert custom upload",
        "replaces the node's API TLS certificate — could break TLS to the node; not exercised live",
        "pmx pve node cert custom upload --node <node> --certificates <pem> --key <pem> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "cert custom delete",
        "removes the node's custom API TLS certificate — could break TLS to the node; not exercised live",
        "pmx pve node cert custom delete --node <node> --yes",
        isolation=False, live_covered=False,
    )

    # Triggering a replication run forces an immediate sync that consumes I/O and
    # bandwidth to the target node, and needs a configured job (none exists on a
    # standalone node), so it is not exercised live.
    ctx.defer(
        "replication run",
        "triggers an immediate replication sync to the target node (needs a configured job); not exercised live",
        "pmx pve node replication run <id> --node <node> --yes",
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
        "pmx pve node scan nfs --node <node> --server <server>",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "scan cifs",
        "probes a CIFS/SMB server for its shares — covered live by `e2e --mutate`, which "
        "scans the node's own smbd on 127.0.0.1",
        "pmx pve node scan cifs --node <node> --server <server>",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "scan iscsi",
        "probes an iSCSI portal for its targets — covered live by `e2e --mutate`, which "
        "scans 127.0.0.1 on the node",
        "pmx pve node scan iscsi --node <node> --portal <portal>",
        isolation=False, live_covered=True,
    )
    ctx.defer(
        "scan pbs",
        "probes a Proxmox Backup Server for its datastores — covered live by `e2e --mutate`, "
        "which answers the scan from a host-local HTTPS stub pinned by cert fingerprint",
        "pmx pve node scan pbs --node <node> --server <server> --username <user> --password <secret>",
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
        "pmx pve node ceph init --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd create",
        "creates an OSD by wiping and consuming a block device; not exercised live",
        "pmx pve node ceph osd create --node <node> --dev /dev/sdb --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd delete",
        "destroys an OSD and optionally zaps its underlying volumes; not exercised live",
        "pmx pve node ceph osd delete <osdid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd in",
        "marks an OSD in, triggering cluster data movement; not exercised live",
        "pmx pve node ceph osd in <osdid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd out",
        "marks an OSD out, draining its data across the cluster; not exercised live",
        "pmx pve node ceph osd out <osdid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph osd scrub",
        "triggers an OSD scrub that adds cluster I/O load; not exercised live",
        "pmx pve node ceph osd scrub <osdid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph pool create",
        "creates a Ceph pool, consuming cluster capacity; not exercised live",
        "pmx pve node ceph pool create <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph pool set",
        "reconfigures an existing Ceph pool's parameters; not exercised live",
        "pmx pve node ceph pool set <name> --node <node> --size 3 --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph pool delete",
        "destroys a Ceph pool and permanently loses its data; not exercised live",
        "pmx pve node ceph pool delete <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mon create",
        "provisions a Ceph monitor daemon on the node; not exercised live",
        "pmx pve node ceph mon create <monid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mon delete",
        "destroys a Ceph monitor daemon on the node; not exercised live",
        "pmx pve node ceph mon delete <monid> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mds create",
        "provisions a Ceph metadata-server daemon on the node; not exercised live",
        "pmx pve node ceph mds create <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mds delete",
        "destroys a Ceph metadata-server daemon on the node; not exercised live",
        "pmx pve node ceph mds delete <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mgr create",
        "provisions a Ceph manager daemon on the node; not exercised live",
        "pmx pve node ceph mgr create <id> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph mgr delete",
        "destroys a Ceph manager daemon on the node; not exercised live",
        "pmx pve node ceph mgr delete <id> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph fs create",
        "creates a CephFS filesystem and its backing pools; not exercised live",
        "pmx pve node ceph fs create <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph fs delete",
        "destroys a CephFS filesystem and optionally its pools; not exercised live",
        "pmx pve node ceph fs delete <name> --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph start",
        "starts Ceph services on the node — disruptive; not exercised live",
        "pmx pve node ceph start --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph stop",
        "stops Ceph services on the node — disruptive; not exercised live",
        "pmx pve node ceph stop --node <node> --yes",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "ceph restart",
        "restarts Ceph services on the node — disruptive; not exercised live",
        "pmx pve node ceph restart --node <node> --service osd.0 --yes",
        isolation=False, live_covered=False,
    )

    # exec/ssh/rsync are exercised live by the mutate phase, SSH-gated: it
    # probes reachability and records SKIP if the host is unreachable.
    ctx.defer("exec", "runs a command on the host — covered live by `e2e --mutate` (SSH-gated)",
              "pmx pve node exec <node> -- true", isolation=True, live_covered=True)
    ctx.defer("ssh", "remote login — covered live by `e2e --mutate` (SSH-gated)",
              "pmx pve node ssh <node> -- true", isolation=True, live_covered=True)
    ctx.defer("rsync", "transfers files to/from host — covered live by `e2e --mutate` (SSH-gated)",
              "pmx pve node rsync <node> <node>:<src> <dst>", isolation=True, live_covered=True)
    # shell and its console alias open a live SSH terminal that cannot be driven
    # head-less, so they are never run live; each builds the SSH invocation the
    # same way and is covered by a unit test (the console alias resolves to the
    # shell handler, which builds the expected `root@<ip>` target). Deferred one
    # verb at a time so the matrix records each leaf.
    ctx.defer("shell", "opens a live SSH terminal on the node, so it cannot be driven head-less; not run live; covered by unit tests",
              "pmx pve node shell <node>")
    ctx.defer("console", "opens a live SSH terminal aliased to `node shell`, so it cannot be driven head-less; not run live; covered by unit tests",
              "pmx pve node console <node>")

    # `execute` runs arbitrary commands on the real host via the Proxmox API
    # (distinct from the SSH-based `exec`); security-sensitive regardless of
    # guarding, so it is out of scope for automated e2e.
    ctx.defer(
        "execute",
        "runs arbitrary commands on the real host via the PVE API — "
        "security-sensitive; out of scope for automated e2e regardless of guarding",
        "pmx pve node execute --node <node> --commands '[\"uname -a\"]'",
    )
    # reboot/shutdown take the real lab node offline — not automatable against a
    # shared lab regardless of guarding.
    ctx.defer(
        "reboot",
        "reboots the real host — would take the shared lab node offline; not automatable",
        "pmx pve node reboot --node <node> --yes",
    )
    ctx.defer(
        "shutdown",
        "shuts down the real host — would take the shared lab node offline; not automatable",
        "pmx pve node shutdown --node <node> --yes",
    )
    # spiceshell/termproxy/vncshell are websocket/interactive console-proxy
    # endpoints — the same class as the already-deferred SSH shell/console and
    # the qemu/lxc console tickets; not automatable head-less.
    ctx.defer(
        "spiceshell",
        "requests an interactive SPICE console-proxy ticket — not automatable head-less; covered by unit tests",
        "pmx pve node spiceshell --node <node>",
    )
    ctx.defer(
        "termproxy",
        "requests an interactive websocket terminal-proxy ticket — not automatable head-less; covered by unit tests",
        "pmx pve node termproxy --node <node>",
    )
    ctx.defer(
        "vncshell",
        "requests an interactive VNC console-proxy ticket — not automatable head-less; covered by unit tests",
        "pmx pve node vncshell --node <node>",
    )

    # Service control mutates running host services on the live node. Every verb
    # is built by the same factory and covered by a unit test (argument contract
    # and task handling). Deferred one verb at a time so the matrix records each
    # leaf.
    # Exercised live by the mutate phase against a benign, non-control-plane
    # service (chrony or postfix): reload, restart, stop, then start — always
    # ending in the service's original running state, never touching
    # pveproxy/pve-cluster/corosync/sshd.
    ctx.defer("services start", "starts a host service — covered live by `e2e --mutate` on a benign service (chrony/postfix), restored to running",
              "pmx pve node services start <node> <svc>", isolation=False, live_covered=True)
    ctx.defer("services stop", "stops a host service — covered live by `e2e --mutate` on a benign service (chrony/postfix), restarted immediately after",
              "pmx pve node services stop <node> <svc>", isolation=False, live_covered=True)
    ctx.defer("services restart", "restarts a host service — covered live by `e2e --mutate` on a benign service (chrony/postfix)",
              "pmx pve node services restart <node> <svc>", isolation=False, live_covered=True)
    ctx.defer("services reload", "reloads a host service — covered live by `e2e --mutate` on a benign service (chrony/postfix)",
              "pmx pve node services reload <node> <svc>", isolation=False, live_covered=True)

    # Node-wide bulk actions act on every guest on the node by default, but
    # --vmids narrows them to a subset. startall/stopall/suspendall are driven
    # live by the mutate phase scoped to ONLY the isolated pmx-cli VM, so they
    # touch no other workload. migrateall needs a second node and wakeonlan
    # powers a node on, so both stay deferred; --help exercises their parsing.
    ctx.check("startall --help", "pve", "node", "startall", "--help", fmt="")
    ctx.check("stopall --help", "pve", "node", "stopall", "--help", fmt="")
    ctx.check("suspendall --help", "pve", "node", "suspendall", "--help", fmt="")
    ctx.check("migrateall --help", "pve", "node", "migrateall", "--help", fmt="")
    ctx.check("wakeonlan --help", "pve", "node", "wakeonlan", "--help", fmt="")
    ctx.defer("startall", "starts guests on the node — covered live by `e2e --mutate` scoped to the isolated pmx-cli VM via --vmids",
              "pmx pve node startall --vmids <vmid> --yes", isolation=True, live_covered=True)
    ctx.defer("stopall", "stops guests on the node — covered live by `e2e --mutate` scoped to the isolated pmx-cli VM via --vmids",
              "pmx pve node stopall --vmids <vmid> --yes", isolation=True, live_covered=True)
    ctx.defer("suspendall", "suspends guests on the node — covered live by `e2e --mutate` scoped to the isolated pmx-cli VM via --vmids (pauses the QEMU process)",
              "pmx pve node suspendall --vmids <vmid> --yes", isolation=True, live_covered=True)
    ctx.defer("migrateall", "migrates every guest off the node to a target (needs a second node); not exercised live; covered by unit tests",
              "pmx pve node migrateall --node <node> --target <node2> --yes", isolation=False, live_covered=False)
    # wakeonlan targets another cluster node by its configured MAC; the API
    # refuses to wake the local node ("'pve' is local node, cannot wake my self!"),
    # so on a single-node cluster there is no valid target — not exercisable live.
    ctx.defer(
        "wakeonlan",
        "sends a Wake-on-LAN packet to power on another node — the API rejects waking the local "
        "node, and this is a single-node cluster, so there is no remote target; not exercised live; covered by unit tests",
        "pmx pve node wakeonlan --node <node> --yes",
        isolation=False, live_covered=False,
    )
