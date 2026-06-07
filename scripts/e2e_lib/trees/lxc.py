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
    n = ctx.node
    if not n:
        ctx.skip("list", "no node discovered")
        return

    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    lst = ctx.check("list", "lxc", "list", node=n, validate=is_list)
    ctx.check("template list", "lxc", "template", "list", node=n, validate=is_list)

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
        ctx.skip("config pending", "no container on node")
        ctx.skip("snapshot list", "no container on node")
        ctx.skip("firewall rules list", "no container on node")
        ctx.skip("firewall options get", "no container on node")
        ctx.skip("console vnc ticket", "no container on node")
    else:
        cid = str(ctid)
        ctx.check("status", "lxc", "status", cid, node=n, validate=has_status)
        ctx.check("config get", "lxc", "config", "get", cid, node=n)
        # config pending reads the diff between current and pending config; it is
        # non-mutating and returns an array on any container (even if no change is
        # staged), so it is safe against any existing container.
        ctx.check("config pending", "lxc", "config", "pending", cid,
                  node=n, validate=is_list)
        ctx.check("snapshot list", "lxc", "snapshot", "list", cid, node=n)
        # Read-only firewall inspection: rules list returns an array, and the
        # options object is always present even when the firewall is disabled.
        ctx.check("firewall rules list", "lxc", "firewall", "rules", "list", cid,
                  node=n, validate=is_list)
        ctx.check("firewall options get", "lxc", "firewall", "options", "get", cid, node=n)
        # Requesting a VNC proxy ticket is non-disruptive — it spawns an
        # ephemeral proxy the same way the web GUI does and changes no CT state.
        ctx.check("console vnc ticket", "lxc", "console", cid, "--type", "vnc",
                  node=n, validate=has_ticket)

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
        ctx.check("interfaces", "lxc", "interfaces", running_cid,
                  node=n, validate=is_list)

    # Verify clone, migrate, disk, and firewall help text parses (commands are wired).
    ctx.check("clone --help", "lxc", "clone", "--help", fmt="")
    ctx.check("migrate --help", "lxc", "migrate", "--help", fmt="")
    ctx.check("disk resize --help", "lxc", "disk", "resize", "--help", fmt="")
    ctx.check("disk move --help", "lxc", "disk", "move", "--help", fmt="")
    ctx.check("firewall rules create --help", "lxc", "firewall", "rules", "create", "--help", fmt="")
    ctx.check("firewall ipset add --help", "lxc", "firewall", "ipset", "add", "--help", fmt="")
    ctx.check("firewall alias create --help", "lxc", "firewall", "alias", "create", "--help", fmt="")
    ctx.check("firewall options set --help", "lxc", "firewall", "options", "set", "--help", fmt="")
    ctx.check("console --help", "lxc", "console", "--help", fmt="")
    ctx.check("interfaces --help", "lxc", "interfaces", "--help", fmt="")

    # Every mutating verb below — including the full power-state matrix and
    # snapshot create/rollback/delete — is exercised live on a purpose-built
    # Alpine container by the mutate phase (`scripts/e2e --mutate` /
    # `scripts/lifecycle`).
    ctx.defer(
        "create",
        "creates a container — covered live by `e2e --mutate`",
        f"pve lxc create ... --pool {Isolation.POOL} --tags {Isolation.TAG}",
        isolation=True, live_covered=True,
    )
    ctx.defer("start/stop/shutdown/reboot/suspend/resume",
              "changes CT power state — covered live by `e2e --mutate`",
              "pve lxc start <ctid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("delete", "destroys a container — covered live by `e2e --mutate`",
              "pve lxc delete <ctid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("snapshot create/rollback/delete",
              "mutates CT snapshots — covered live by `e2e --mutate`",
              "pve lxc snapshot create <ctid> <name>", isolation=True, live_covered=True)
    ctx.defer(
        "clone",
        "clones a container — covered live by `e2e --mutate`",
        f"pve lxc clone <ctid> --newid <id> --pool {Isolation.POOL} --hostname {Isolation.NAME_PREFIX}ctclone",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "migrate",
        "migrates a container to another node — covered live by `e2e --mutate` on multi-node clusters",
        "pve lxc migrate <ctid> --target-node <node>",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "disk resize",
        "grows a container volume — covered live by `e2e --mutate`",
        "pve lxc disk resize <ctid> --disk rootfs --size +1G",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "disk move",
        "relocates a container volume — covered live by `e2e --mutate` when a second rootdir storage exists",
        "pve lxc disk move <ctid> --volume rootfs --storage <other>",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "firewall rules/ipset/alias create-delete + options set",
        "mutates a CT's firewall config — covered live by `e2e --mutate` on the isolated container",
        "pve lxc firewall rules create <ctid> --type in --action ACCEPT --proto tcp --dport 22",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "console connect (websocket/spice viewer)",
        "opening the proxied console session needs an interactive viewer — the "
        "CLI only returns the ticket, which the read-only sweep validates",
        "pve lxc console <ctid> --type spice",
    )
    ctx.defer(
        "remote-migrate",
        "migrates a container to a different Proxmox VE cluster — requires two "
        "live clusters; no rollback without manual intervention; not exercised live",
        "pve lxc remote-migrate <ctid> --yes --target-endpoint https://remote:8006 "
        "--target-storage local-lvm",
        isolation=False, live_covered=False,
    )
