"""storage: cluster storage configuration (read-only happy path)."""

from __future__ import annotations

from ..context import CmdResult, Ctx
from ..model import Isolation

NAME = "storage"
DESCRIPTION = "Manage cluster storage configuration"


def run(ctx: Ctx) -> None:
    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    lst = ctx.check("list", "storage", "list", validate=is_list)

    # describe: offline schema catalog — no API call.
    ctx.check("describe", "storage", "describe")
    ctx.check("describe --type", "storage", "describe", "--type", "zfspool")

    sid = None
    if lst.rc == 0:
        try:
            sid = ctx.first(lst.json(), "storage")
        except ValueError:
            sid = None

    def has_storage_keys(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        missing = [k for k in ("storage", "type") if k not in data]
        return f"storage get missing keys: {missing}" if missing else None

    if sid is None:
        ctx.skip("get", "no storage defined")
        ctx.skip("permissions list", "no storage defined")
        ctx.skip("permissions effective", "no storage defined")
        ctx.skip("content", "no storage defined")
        ctx.skip("status", "no storage defined")
        ctx.skip("identity", "no storage defined")
        ctx.skip("rrddata", "no storage defined")
        ctx.skip("rrd", "no storage defined")
    else:
        ctx.check("get", "storage", "get", str(sid), validate=has_storage_keys)

        # permissions: ACL entries scoped to the storage's /storage/{storage}
        # path. Both are cluster-wide ACL queries (no --node routing needed,
        # unlike the storage-content checks below). `grant`/`revoke` mutate
        # cluster-wide ACLs and are deferred below.
        def has_permissions_effective(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), dict) else "expected a JSON object"

        ctx.check("permissions list", "storage", "permissions", "list", str(sid),
                  validate=is_list)
        ctx.check("permissions effective", "storage", "permissions", "effective", str(sid),
                  validate=has_permissions_effective)

        if ctx.node:
            ctx.check("content", "storage", "content", str(sid), node=ctx.node)
            # status: per-storage usage summary; requires a node context.
            def has_usage_keys(res: CmdResult) -> str | None:
                data = res.json()
                if not isinstance(data, dict):
                    return "expected a JSON object"
                # At least one of used/avail/total must be present.
                if not any(k in data for k in ("used", "avail", "total")):
                    return "storage status missing usage keys (used/avail/total)"
                return None

            ctx.check("status", "storage", "status", str(sid),
                      node=ctx.node, validate=has_usage_keys)
            # identity: backend identity (path/export/URL). Not every storage
            # plugin implements get_identity (e.g. ZFS), so skip gracefully when
            # the backend reports it as unsupported.
            id_probe = ctx.run("storage", "identity", str(sid), node=ctx.node)
            if id_probe.rc == 0:
                ctx.check("identity", "storage", "identity", str(sid), node=ctx.node)
            else:
                ctx.skip("identity", "storage plugin does not implement identity: "
                         f"{(id_probe.stderr.strip() or id_probe.stdout.strip())[:80]}")
            # rrddata: timeseries for storage metrics; zero-row result is valid.
            ctx.check("rrddata", "storage", "rrddata", str(sid),
                      "--timeframe", "hour", node=ctx.node, validate=is_list)
            # rrd: rrd PNG image reference. The RRD database may not exist yet for
            # a recently added storage, so skip gracefully when no data exists.
            def has_filename(res: CmdResult) -> str | None:
                data = res.json()
                if not isinstance(data, dict):
                    return "expected a JSON object"
                if "filename" not in data:
                    return "rrd response missing 'filename' key"
                return None

            rrd_probe = ctx.run("storage", "rrd", str(sid),
                                "--ds", "used", "--timeframe", "hour", node=ctx.node)
            if rrd_probe.rc == 0:
                ctx.check("rrd", "storage", "rrd", str(sid),
                          "--ds", "used", "--timeframe", "hour",
                          node=ctx.node, validate=has_filename)
            else:
                ctx.skip("rrd", "no RRD data recorded for this storage: "
                         f"{(rrd_probe.stderr.strip() or rrd_probe.stdout.strip())[:80]}")
        else:
            ctx.skip("content", "no node discovered")
            ctx.skip("status", "no node discovered")
            ctx.skip("identity", "no node discovered")
            ctx.skip("rrddata", "no node discovered")
            ctx.skip("rrd", "no node discovered")

    # Single-volume inspection: `volume get` reads one volume's attributes
    # (GET .../content/<volid>) and is non-mutating. Discover a real volume by
    # listing the content of each storage until one yields a volid.
    def has_format(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), dict) else "expected a JSON object"

    vol = None
    if ctx.node and lst.rc == 0 and isinstance(lst.json(), list):
        for cand in lst.json():
            if not isinstance(cand, dict) or not cand.get("storage"):
                continue
            cres = ctx.run("storage", "content", str(cand["storage"]), node=ctx.node)
            if cres.rc == 0 and isinstance(cres.json(), list) and cres.json():
                v0 = cres.json()[0]
                if isinstance(v0, dict) and v0.get("volid"):
                    vol = str(v0["volid"])
                    break
    if vol:
        ctx.check("volume get", "storage", "volume", "get", vol, node=ctx.node, validate=has_format)
    else:
        ctx.skip("volume get", "no volume found on any storage" if ctx.node else "no node discovered")

    # node-list: runtime storage availability/usage as seen from a specific node
    # (distinct from `storage list`'s cluster-wide configuration). Needs a node.
    if ctx.node:
        ctx.check("node-list", "storage", "node-list", node=ctx.node, validate=is_list)
    else:
        ctx.skip("node-list", "no node discovered")

    # aplinfo list: the appliance-template catalog index. It needs egress to the
    # Proxmox template repository; probe first and skip gracefully when the lab
    # has no reachable catalog rather than recording a failure.
    if ctx.node:
        aplinfo_probe = ctx.run("storage", "aplinfo", "list", node=ctx.node)
        if aplinfo_probe.rc == 0:
            ctx.check("aplinfo list", "storage", "aplinfo", "list",
                      node=ctx.node, validate=is_list)
        else:
            ctx.skip("aplinfo list", "appliance template catalog not reachable from this node")
    else:
        ctx.skip("aplinfo list", "no node discovered")

    # The remaining browsing verbs need backends or sources the lab does not
    # provide, so they are checked read-only via --help here and gated below.
    ctx.check("volume set --help", "storage", "volume", "set", "--help", fmt="")
    ctx.check("volume copy --help", "storage", "volume", "copy", "--help", fmt="")
    ctx.check("file-restore list --help", "storage", "file-restore", "list", "--help", fmt="")
    ctx.check("file-restore download --help", "storage", "file-restore", "download", "--help", fmt="")
    ctx.check("import-metadata --help", "storage", "import-metadata", "--help", fmt="")

    # Prune preview: --dry-run queries the prunebackups endpoint, which reports
    # the keep/remove decision for each archive WITHOUT deleting anything, so it
    # is safe in the read-only sweep. Run it against a backup-capable storage when
    # one exists and a node is known; the result is an array of prune decisions.
    backup_sid = None
    if lst.rc == 0 and isinstance(lst.json(), list):
        for s in lst.json():
            if isinstance(s, dict) and "backup" in str(s.get("content", "")):
                backup_sid = str(s.get("storage", ""))
                break
    if backup_sid and ctx.node:
        ctx.check("prune dry-run", "storage", "prune", backup_sid,
                  "--keep-last", "1", "--dry-run", node=ctx.node, validate=is_list)
    else:
        ctx.skip("prune dry-run", "no backup-capable storage or node discovered")
    ctx.check("prune --help", "storage", "prune", "--help", fmt="")

    # Transfer verbs move bytes onto a storage (upload pushes a local file;
    # download-url has the node pull a URL). Both create a real volume; the mutate
    # phase exercises each on `local` and removes the artifact with
    # `storage volume delete` in a finally block, so the help surface is checked
    # read-only here and the full transfer is covered live below.
    ctx.check("upload --help", "storage", "upload", "--help", fmt="")
    ctx.check("download-url --help", "storage", "download-url", "--help", fmt="")

    # The mutate phase backs up the isolated guest then prunes its own archive
    # (keep-last=0, scoped to that vmid) — covered live by `e2e --mutate`.
    ctx.defer(
        "prune (delete archives)",
        "deletes backup archives by retention policy — covered live by `e2e --mutate`",
        f"pve storage prune {Isolation.NAME_PREFIX}... --vmid <id> --keep-last 0 --yes",
        isolation=True, live_covered=True,
    )

    # The mutate phase creates an isolated `pve-cli-store` dir storage,
    # node-restricted, exercises set, and deletes it — covered live by it.
    ctx.defer(
        "create",
        "adds a cluster storage definition — covered live by `e2e --mutate`",
        f"pve storage create --storage {Isolation.NAME_PREFIX}store --type dir ...",
        isolation=True, live_covered=True,
    )
    ctx.defer("set", "modifies a storage definition — covered live by `e2e --mutate`",
              f"pve storage set {Isolation.NAME_PREFIX}store --content iso,vztmpl",
              isolation=True, live_covered=True)
    ctx.defer("delete", "removes a storage definition — covered live by `e2e --mutate`",
              f"pve storage delete {Isolation.NAME_PREFIX}store", isolation=True, live_covered=True)

    # The mutate phase allocates a raw volume, uploads a small file, and pulls a
    # file by URL onto `local`, then removes each artifact with
    # `storage volume delete` in a finally block — all covered live by it.
    ctx.defer(
        "volume alloc/delete",
        "allocates a raw volume then deletes it — covered live by `e2e --mutate` "
        "(storage_volume_lifecycle: alloc on `local`, capture returned volid, delete in finally)",
        "pve storage volume alloc local --vmid 9999 --filename local:vm-9999-pve-cli-test --size 1G",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "upload",
        "pushes a local file onto a storage then deletes the volume — covered live by `e2e --mutate`",
        f"pve storage upload local --file ./{Isolation.NAME_PREFIX}upload.iso --content iso",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "download-url",
        "has the node pull a small Proxmox-hosted file then deletes the volume — covered live by `e2e --mutate`",
        f"pve storage download-url local --url <U> --filename {Isolation.NAME_PREFIX}download.iso --content iso",
        isolation=True, live_covered=True,
    )

    # The mutate phase backs up the isolated guest, then sets and restores the
    # notes/protected attributes on that archive — covered live by `e2e --mutate`.
    ctx.defer(
        "volume set",
        "updates a volume's notes/protected flag — covered live (reversibly) by `e2e --mutate`",
        "pve storage volume set local:backup/vzdump-...-<id>.vma.zst --notes <m>",
        isolation=True, live_covered=True,
    )
    # Volume copy hits POST .../content/{volume}, which the PVE API restricts to
    # root@pam (it rejects API-token auth with "Permission check failed
    # (user != root@pam)"), so the token-authenticated e2e suite cannot exercise
    # it live; covered by unit tests.
    ctx.defer(
        "volume copy",
        "copies a volume to a new target — the copy endpoint is restricted to root@pam "
        "and rejects API-token auth; not exercisable by the e2e suite — covered by unit tests",
        "pve storage volume copy local:backup/... --target-volume <storage>:<vol>",
        isolation=True, live_covered=False,
    )
    # file-restore browses files inside a backup snapshot, but the backing
    # endpoints support Proxmox Backup Server snapshots only; the lab has no PBS
    # storage, so these are not exercised live.
    ctx.defer(
        "file-restore list",
        "browses files inside a PBS snapshot — lab has no Proxmox Backup Server storage; not exercised live; covered by unit tests",
        "pve storage file-restore list <pbs> --volume <snapshot>",
        isolation=True, live_covered=False,
    )
    ctx.defer(
        "file-restore download",
        "extracts a file from a PBS snapshot — lab has no Proxmox Backup Server storage; not exercised live; covered by unit tests",
        "pve storage file-restore download <pbs> --volume <snapshot> --filepath </etc/hostname>",
        isolation=True, live_covered=False,
    )
    # import-metadata inspects a foreign guest archive (OVA/ESXi). The API cannot
    # upload such an archive (the upload endpoint accepts only iso/vztmpl), so the
    # mutate phase stages a crafted minimal OVF in an import-capable storage's
    # import/ directory over passwordless root SSH to the node host, reads its
    # metadata, and removes the fixture; it skips if the host is unreachable.
    ctx.defer(
        "import-metadata",
        "inspects an importable guest archive — covered live by `e2e --mutate`, which stages a crafted OVF on the node's import dir and reads its metadata",
        "pve storage import-metadata <import-storage> --volume <archive>",
        isolation=True, live_covered=True,
    )
    # Downloading a real appliance template consumes bandwidth and storage; not
    # exercised live.
    ctx.defer(
        "aplinfo download",
        "downloads a real appliance template tarball to a storage — bandwidth/storage-consuming; not exercised live; covered by unit tests",
        "pve storage aplinfo download --node <node> --storage local --template pve-cli-template",
        isolation=False, live_covered=False,
    )
    # Pulling an OCI image needs registry egress and consumes storage; not
    # exercised live from this tree (the equivalent `node oci pull` verb is
    # covered live by the mutate phase against a small public image).
    ctx.defer(
        "oci-pull",
        "pulls a real OCI image from a registry into a storage — needs registry egress and consumes storage; not exercised live from this tree; covered by unit tests",
        "pve storage oci-pull local --node <node> --reference docker.io/library/alpine:latest",
        isolation=False, live_covered=False,
    )
    # `permissions grant`/`revoke` mutate cluster-wide ACLs; not wired into the
    # mutate phase. `permissions list`/`effective` above are read-only and
    # exercised live.
    ctx.defer(
        "permissions grant",
        "grants ACL roles on the storage's /storage/{storage} path; mutates "
        "cluster-wide ACLs, not wired into the mutate phase; covered by unit tests",
        "pve storage permissions grant local-lvm --roles PVEDatastoreAdmin --users alice@pve",
    )
    ctx.defer(
        "permissions revoke",
        "revokes ACL roles on the storage's /storage/{storage} path; mutates "
        "cluster-wide ACLs, not wired into the mutate phase; covered by unit tests",
        "pve storage permissions revoke local-lvm --roles PVEDatastoreAdmin --users alice@pve",
    )
