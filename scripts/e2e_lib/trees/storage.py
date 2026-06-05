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
        ctx.skip("content", "no storage defined")
    else:
        ctx.check("get", "storage", "get", str(sid), validate=has_storage_keys)
        if ctx.node:
            ctx.check("content", "storage", "content", str(sid), node=ctx.node)
        else:
            ctx.skip("content", "no node discovered")

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
    # download-url has the node pull a URL). Both create a real volume and there
    # is no CLI verb yet to delete a single volume, so exercising them live would
    # leave a namespaced artifact behind on shared lab storage. They are checked
    # read-only via --help here and deferred live until a `storage volume` delete
    # verb exists to clean up after them.
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

    # Transfer verbs are not exercised live: there is no CLI verb yet to delete a
    # single storage volume, so a live upload/download would leave a namespaced
    # file on shared lab storage with no way to clean it up through the CLI.
    ctx.defer(
        "upload",
        "pushes a local file onto a storage — no CLI volume-delete verb yet to remove the artifact; not exercised live",
        f"pve storage upload local --file ./{Isolation.NAME_PREFIX}test.iso --content iso",
        isolation=True, live_covered=False,
    )
    ctx.defer(
        "download-url",
        "downloads a URL onto a storage — no CLI volume-delete verb yet to remove the artifact; not exercised live",
        f"pve storage download-url local --url <U> --filename {Isolation.NAME_PREFIX}test.iso --content iso",
        isolation=True, live_covered=False,
    )
