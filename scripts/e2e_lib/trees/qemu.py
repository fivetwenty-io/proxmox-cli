"""qemu: VM inventory + per-VM read-only inspection.

Lifecycle (start/stop/reboot/reset/suspend/resume/delete/snapshot create) is
deferred. When implemented it must create VMs under the isolation contract:
pool `pve-cli`, tag `pve-cli`, NIC on the `pvecli` SDN — never the host bridge.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx
from ..model import Isolation

NAME = "qemu"
DESCRIPTION = "Manage QEMU virtual machines"


def run(ctx: Ctx) -> None:
    n = ctx.node
    if not n:
        ctx.skip("list", "no node discovered")
        return

    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    lst = ctx.check("list", "qemu", "list", node=n, validate=is_list)

    vmid = None
    if lst.rc == 0:
        try:
            vmid = ctx.first(lst.json(), "vmid")
        except ValueError:
            vmid = None

    def has_status(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        missing = [k for k in ("status", "vmid") if k not in data]
        return f"status response missing keys: {missing}" if missing else None

    if vmid is None:
        ctx.skip("status", "no VM on node")
        ctx.skip("config get", "no VM on node")
        ctx.skip("snapshot list", "no VM on node")
        ctx.skip("firewall rules list", "no VM on node")
        ctx.skip("firewall options get", "no VM on node")
    else:
        vid = str(vmid)
        ctx.check("status", "qemu", "status", vid, node=n, validate=has_status)
        ctx.check("config get", "qemu", "config", "get", vid, node=n)
        ctx.check("snapshot list", "qemu", "snapshot", "list", vid, node=n)
        # Firewall reads are non-mutating: safe against any existing VM.
        ctx.check("firewall rules list", "qemu", "firewall", "rules", "list", vid,
                  node=n, validate=is_list)
        ctx.check("firewall options get", "qemu", "firewall", "options", "get", vid, node=n)

    # Verify clone, migrate, disk, and firewall help text parses (commands are wired).
    ctx.check("clone --help", "qemu", "clone", "--help", fmt="")
    ctx.check("migrate --help", "qemu", "migrate", "--help", fmt="")
    ctx.check("disk resize --help", "qemu", "disk", "resize", "--help", fmt="")
    ctx.check("disk move --help", "qemu", "disk", "move", "--help", fmt="")
    ctx.check("disk unlink --help", "qemu", "disk", "unlink", "--help", fmt="")
    ctx.check("firewall rules create --help", "qemu", "firewall", "rules", "create", "--help", fmt="")
    ctx.check("firewall ipset add --help", "qemu", "firewall", "ipset", "add", "--help", fmt="")
    ctx.check("firewall alias create --help", "qemu", "firewall", "alias", "create", "--help", fmt="")
    ctx.check("firewall options set --help", "qemu", "firewall", "options", "set", "--help", fmt="")

    # The mutating verbs below are not run by the read-only sweep, but are all
    # exercised live on a purpose-built isolated VM by the mutate phase
    # (`scripts/e2e --mutate` / `scripts/lifecycle`). `reboot` is the sole
    # exception: a diskless VM has no guest OS to ACPI-reboot, so it is covered
    # on the LXC side instead (qemu `reset` covers the in-place restart path).
    ctx.defer(
        "create",
        "creates a VM — covered live by `e2e --mutate`",
        f"pve qemu create ... --pool {Isolation.POOL} --tags {Isolation.TAG}",
        isolation=True, live_covered=True,
    )
    ctx.defer("start/stop/shutdown/reset/suspend/resume",
              "changes VM power state — covered live by `e2e --mutate`",
              "pve qemu start <vmid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("reboot", "graceful reboot needs a guest OS — covered on the lxc container",
              "pve qemu reboot <vmid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("delete", "destroys a VM — covered live by `e2e --mutate`",
              "pve qemu delete <vmid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("snapshot create/rollback/delete",
              "mutates VM snapshots — covered live by `e2e --mutate`",
              "pve qemu snapshot create <vmid> <name>", isolation=True, live_covered=True)
    ctx.defer(
        "clone",
        "clones a VM — covered live by `e2e --mutate`",
        f"pve qemu clone <vmid> --newid <id> --pool {Isolation.POOL} --name {Isolation.NAME_PREFIX}clone",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "migrate",
        "migrates a VM to another node — covered live by `e2e --mutate` on multi-node clusters",
        "pve qemu migrate <vmid> --target <node>",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "disk resize/move/unlink",
        "grows, relocates, and detaches VM disks — covered live by `e2e --mutate`",
        "pve qemu disk resize <vmid> --disk scsi0 --size +1G",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "firewall rules/ipset/alias create-delete + options set",
        "mutates a VM's firewall config — covered live by `e2e --mutate` on the isolated VM",
        "pve qemu firewall rules create <vmid> --type in --action ACCEPT --proto tcp --dport 22",
        isolation=True, live_covered=True,
    )
