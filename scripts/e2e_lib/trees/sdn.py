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
    else:
        ctx.skip("subnet list", "no vnet defined")

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
