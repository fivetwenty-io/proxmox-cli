"""sdn: software-defined networking (read-only happy path).

Zone/vnet/subnet creation, apply, and deletion are deferred — they mutate
cluster networking. The full provision→teardown cycle is exercised by the
lifecycle suite (`scripts/lifecycle`) on an isolated `pvecli` zone.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx
from ..model import Isolation

NAME = "sdn"
DESCRIPTION = "Manage software-defined networking (zones, vnets, subnets)"


def run(ctx: Ctx) -> None:
    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    ctx.check("zone list", "sdn", "zone", "list", validate=is_list)
    vnets = ctx.check("vnet list", "sdn", "vnet", "list", validate=is_list)

    vnet = None
    if vnets.rc == 0:
        try:
            vnet = ctx.first(vnets.json(), "vnet")
        except ValueError:
            vnet = None
    if vnet:
        ctx.check("subnet list", "sdn", "subnet", "list", str(vnet), validate=is_list)
        ctx.check("vnet firewall rules list", "sdn", "vnet", "firewall", "rules", "list",
                  str(vnet), validate=is_list)
        ctx.check("vnet firewall options get", "sdn", "vnet", "firewall", "options", "get",
                  str(vnet))
    else:
        ctx.skip("subnet list", "no vnet defined")
        ctx.skip("vnet firewall rules list", "no vnet defined")
        ctx.skip("vnet firewall options get", "no vnet defined")

    ctx.check("vnet firewall rules create --help", "sdn", "vnet", "firewall", "rules",
              "create", "--help", fmt="")

    # Routing controllers, IPAM backends, and DNS providers are cluster-global.
    ctx.check("controller list", "sdn", "controller", "list", validate=is_list)
    ipams = ctx.check("ipam list", "sdn", "ipam", "list", validate=is_list)
    ctx.check("dns list", "sdn", "dns", "list", validate=is_list)

    # IPAM status reports recorded allocations; probe a discovered backend (the
    # built-in `pve` IPAM is always present on a default install).
    ipam = None
    if ipams.rc == 0:
        try:
            ipam = ctx.first(ipams.json(), "ipam")
        except ValueError:
            ipam = None
    if ipam:
        ctx.check("ipam status", "sdn", "ipam", "status", str(ipam), validate=is_list)
    else:
        ctx.skip("ipam status", "no IPAM backend defined")

    # Preview pending SDN changes (read-only diff) and the rollback help. The
    # dry-run is safe: it computes the running-vs-pending diff for a node without
    # changing anything, and on a clean cluster the diff is empty.
    if ctx.node:
        ctx.check("dry-run", "sdn", "dry-run", node=ctx.node)
    else:
        ctx.skip("dry-run", "no node discovered")
    ctx.check("rollback --help", "sdn", "rollback", "--help", fmt="")
    ctx.defer("rollback",
              "discards ALL pending SDN changes cluster-wide — never run on shared lab",
              "pve sdn rollback --yes", isolation=False, live_covered=False)

    # The mutate phase provisions and tears down this exact isolated SDN, so
    # zone/vnet/subnet create+delete and apply are all exercised live by it.
    ctx.defer("zone create/delete", "mutates cluster networking — covered live by `e2e --mutate`",
              f"pve sdn zone create {Isolation.SDN_ZONE} --type simple",
              isolation=True, live_covered=True)
    ctx.defer("vnet create/delete", "mutates cluster networking — covered live by `e2e --mutate`",
              f"pve sdn vnet create {Isolation.SDN_VNET} --zone {Isolation.SDN_ZONE}",
              isolation=True, live_covered=True)
    ctx.defer("subnet create/delete", "mutates cluster networking — covered live by `e2e --mutate`",
              f"pve sdn subnet create {Isolation.SDN_VNET} {Isolation.SDN_SUBNET}",
              isolation=True, live_covered=True)
    ctx.defer("apply", "reloads network config on all nodes — covered live by `e2e --mutate`",
              "pve sdn apply", isolation=True, live_covered=True)

    # Controller/IPAM/DNS write verbs are staged config edits; the help surface
    # is checked read-only and the full CRUD cycle runs live in the mutate phase.
    ctx.check("controller create --help", "sdn", "controller", "create", "--help", fmt="")
    ctx.check("ipam create --help", "sdn", "ipam", "create", "--help", fmt="")
    ctx.check("dns create --help", "sdn", "dns", "create", "--help", fmt="")
    ctx.defer("controller create/get/set/delete",
              "needs an FRR routing backend — covered by unit tests",
              "pve sdn controller create pvecli-bgp --type bgp",
              isolation=True, live_covered=False)
    ctx.defer("dns create/get/set/delete",
              "validates connectivity to an external DNS backend — covered by unit tests",
              "pve sdn dns create pveclidns --type powerdns --url URL --key KEY",
              isolation=True, live_covered=False)
    ctx.defer("ipam create/get/delete",
              "pve-type IPAM CRUD — covered live by `e2e --mutate`",
              "pve sdn ipam create pvecliipam --type pve",
              isolation=True, live_covered=True)
    ctx.defer("vnet set",
              "stages a vnet edit — covered live by `e2e --mutate`",
              f"pve sdn vnet set {Isolation.SDN_VNET} --alias pve-cli-e2e",
              isolation=True, live_covered=True)
    ctx.defer("vnet firewall rules create/get/set/delete",
              "stages a vnet firewall rule — covered live by `e2e --mutate`",
              f"pve sdn vnet firewall rules create {Isolation.SDN_VNET} --type forward --action ACCEPT",
              isolation=True, live_covered=True)
    ctx.defer("vnet firewall options set",
              "enabling a vnet firewall affects guest traffic — not exercised live",
              f"pve sdn vnet firewall options set {Isolation.SDN_VNET} --enable",
              isolation=True, live_covered=False)
