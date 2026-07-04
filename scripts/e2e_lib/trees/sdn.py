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

    zones = ctx.check("zone list", "sdn", "zone", "list", validate=is_list)
    vnets = ctx.check("vnet list", "sdn", "vnet", "list", validate=is_list)

    # zone show: per-zone configuration detail. Discover a real zone from the
    # list above; a fresh cluster always has at least the built-in `localnetwork`
    # zone, but skip gracefully if somehow none is reported.
    zone_id = None
    if zones.rc == 0:
        try:
            zone_id = ctx.first(zones.json(), "zone")
        except ValueError:
            zone_id = None
    if zone_id:
        ctx.check("zone show", "sdn", "zone", "show", str(zone_id))
    else:
        ctx.skip("zone show", "no zone defined")

    vnet = None
    if vnets.rc == 0:
        try:
            vnet = ctx.first(vnets.json(), "vnet")
        except ValueError:
            vnet = None
    if vnet:
        ctx.check("vnet show", "sdn", "vnet", "show", str(vnet))
        subnets = ctx.check("subnet list", "sdn", "subnet", "list", str(vnet), validate=is_list)
        # subnet show: per-subnet configuration detail. Discover a real subnet id
        # from the list just checked; skip when the vnet has no subnet defined.
        subnet_id = None
        if subnets.rc == 0:
            try:
                subnet_id = ctx.first(subnets.json(), "subnet")
            except ValueError:
                subnet_id = None
        if subnet_id:
            ctx.check("subnet show", "sdn", "subnet", "show", str(vnet), str(subnet_id))
        else:
            ctx.skip("subnet show", "no subnet defined on the discovered vnet")
        ctx.check("vnet firewall rules list", "sdn", "vnet", "firewall", "rules", "list",
                  str(vnet), validate=is_list)
        ctx.check("vnet firewall options get", "sdn", "vnet", "firewall", "options", "get",
                  str(vnet))
    else:
        ctx.skip("vnet show", "no vnet defined")
        ctx.skip("subnet list", "no vnet defined")
        ctx.skip("subnet show", "no vnet defined")
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
              "discards ALL pending SDN changes cluster-wide; not exercised live; covered by unit tests",
              "pve sdn rollback --yes", isolation=False, live_covered=False)

    # SDN fabrics, prefix lists, and route maps are PVE 9.2 net-new (BGP/OSPF/
    # OpenFabric routing underlays and route policy). The list endpoints are
    # cluster-global and read-only; on a cluster without the FRR routing stack
    # configured they may report an error, so guard with a skip rather than fail.
    fab = ctx.run("sdn", "fabric", "list")
    if fab.rc != 0:
        ctx.skip("fabric list", "SDN fabric routing not configured on this cluster")
        ctx.skip("fabric node list", "SDN fabric routing not configured on this cluster")
        ctx.skip("fabric list-all", "SDN fabric routing not configured on this cluster")
    else:
        ctx.check("fabric list", "sdn", "fabric", "list", validate=is_list)
        ctx.check("fabric node list", "sdn", "fabric", "node", "list", validate=is_list)
        # fabric list-all: returns all fabrics across nodes; same guard as list.
        ctx.check("fabric list-all", "sdn", "fabric", "list-all", validate=is_list)

    pl = ctx.run("sdn", "prefix-list", "list")
    if pl.rc != 0:
        ctx.skip("prefix-list list", "SDN prefix lists not available on this cluster")
    else:
        ctx.check("prefix-list list", "sdn", "prefix-list", "list", validate=is_list)

    rm = ctx.run("sdn", "route-map", "list")
    if rm.rc != 0:
        ctx.skip("route-map list", "SDN route maps not available on this cluster")
        ctx.skip("route-map entry list", "SDN route maps not available on this cluster")
    else:
        ctx.check("route-map list", "sdn", "route-map", "list", validate=is_list)
        ctx.check("route-map entry list", "sdn", "route-map", "entry", "list", validate=is_list)

    ctx.check("fabric create --help", "sdn", "fabric", "create", "--help", fmt="")
    ctx.check("prefix-list create --help", "sdn", "prefix-list", "create", "--help", fmt="")
    ctx.check("route-map entry add --help", "sdn", "route-map", "entry", "add", "--help", fmt="")

    # sdn status fabrics *: live FRR routing-daemon state for a fabric. The
    # mutate phase's fabric create/get/set/delete cycle deliberately never
    # applies (see the fabric defer below), so no fabric is ever actually
    # running FRR in this lab — these read-only verbs have no live target to
    # query and are not exercised.
    ctx.defer(
        "status fabrics get",
        "requires applied FRR fabric backend not present in lab",
        "pve sdn status fabrics get <fabric> --node <node>",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "status fabrics interfaces",
        "requires applied FRR fabric backend not present in lab",
        "pve sdn status fabrics interfaces <fabric> --node <node>",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "status fabrics neighbors",
        "requires applied FRR fabric backend not present in lab",
        "pve sdn status fabrics neighbors <fabric> --node <node>",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "status fabrics routes",
        "requires applied FRR fabric backend not present in lab",
        "pve sdn status fabrics routes <fabric> --node <node>",
        isolation=False, live_covered=False,
    )

    # A fabric, prefix list, and route map are staged cluster-config entries
    # until `sdn apply`; the FRR routing stack is only engaged at apply time. The
    # mutate phase creates/gets/sets/deletes an isolated openfabric fabric (+ a
    # member node), prefix list (+ entries), and route map (+ entries) WITHOUT
    # ever applying, so the full CRUD cycle is exercised live with no FRR backend.
    ctx.defer("fabric create/get/set/delete + node create/get/set/delete",
              "staged openfabric routing config — covered live by `e2e --mutate` "
              "(never applied, so no FRR backend needed)",
              "pve sdn fabric create pveclifb --protocol openfabric --ip-prefix 172.30.0.0/24",
              isolation=True, live_covered=True)
    ctx.defer("prefix-list create/get/set/delete + entry add/get/set/delete/list",
              "staged route-filter config — covered live by `e2e --mutate` (never applied)",
              "pve sdn prefix-list create pveclipl",
              isolation=True, live_covered=True)
    ctx.defer("route-map get + entry add/get/set/delete",
              "staged BGP route policy — covered live by `e2e --mutate` (never applied)",
              "pve sdn route-map entry add pveclirm --order 10 --action permit",
              isolation=True, live_covered=True)

    # The mutate phase provisions and tears down this exact isolated SDN, so
    # zone/vnet/subnet create+delete and apply are all exercised live by it.
    ctx.defer("zone create/delete", "mutates cluster networking — covered live by `e2e --mutate`",
              f"pve sdn zone create {Isolation.SDN_ZONE} --type simple",
              isolation=True, live_covered=True)
    ctx.defer("zone set",
              "updates a zone's properties — covered live by `e2e --mutate` "
              "(set --nodes on the isolated pvecli zone)",
              f"pve sdn zone set {Isolation.SDN_ZONE} --nodes <node>",
              isolation=True, live_covered=True)
    ctx.defer("vnet create/delete", "mutates cluster networking — covered live by `e2e --mutate`",
              f"pve sdn vnet create {Isolation.SDN_VNET} --zone {Isolation.SDN_ZONE}",
              isolation=True, live_covered=True)
    ctx.defer("subnet create/delete", "mutates cluster networking — covered live by `e2e --mutate`",
              f"pve sdn subnet create {Isolation.SDN_VNET} {Isolation.SDN_SUBNET}",
              isolation=True, live_covered=True)
    ctx.defer("subnet set",
              "updates a subnet's gateway or other properties — covered live by `e2e --mutate`",
              f"pve sdn subnet set {Isolation.SDN_VNET} <subnet-id> "
              f"--gateway {Isolation.SDN_GATEWAY}",
              isolation=True, live_covered=True)
    ctx.defer("vnet ips create/set/delete",
              "manages IPAM IP allocations within a vnet — requires pve IPAM enabled on "
              "the zone; covered live by `e2e --mutate` on the isolated pvecli vnet",
              f"pve sdn vnet ips create {Isolation.SDN_VNET} --ip 10.241.0.10 "
              f"--zone {Isolation.SDN_ZONE}",
              isolation=True, live_covered=True)
    ctx.defer("apply", "reloads network config on all nodes — covered live by `e2e --mutate`",
              "pve sdn apply", isolation=True, live_covered=True)
    # The mutate phase runs a tight acquire→release pair on the global SDN lock
    # (the token `acquire` returns releases it immediately), when the SDN config
    # is freshly applied and no other write is in flight — covered live by it.
    ctx.defer("lock acquire",
              "acquires the global SDN config lock — covered live by `e2e --mutate` "
              "(tight acquire→release pair)",
              "pve sdn lock acquire",
              isolation=True, live_covered=True)
    ctx.defer("lock release",
              "releases the global SDN config lock with its token — covered live by "
              "`e2e --mutate`",
              "pve sdn lock release --lock-token <token> --yes",
              isolation=True, live_covered=True)

    # Controller/IPAM/DNS write verbs are staged config edits; the help surface
    # is checked read-only and the full CRUD cycle runs live in the mutate phase.
    ctx.check("controller create --help", "sdn", "controller", "create", "--help", fmt="")
    ctx.check("ipam create --help", "sdn", "ipam", "create", "--help", fmt="")
    ctx.check("dns create --help", "sdn", "dns", "create", "--help", fmt="")
    # An EVPN controller is a staged config entry until `sdn apply`; the mutate
    # phase creates/gets/sets/deletes the isolated controller without ever
    # applying, so the full CRUD cycle is exercised live with no FRR backend.
    ctx.defer("controller create/get/set/delete",
              "staged EVPN controller config — covered live by `e2e --mutate` (never applied, so no FRR backend needed)",
              "pve sdn controller create pveclictrl --type evpn --asn 65000 --peers 172.30.0.2",
              isolation=True, live_covered=True)
    # A DNS provider (PowerDNS et al.) validates connectivity to its backend when
    # created or updated (the plugin issues a GET to the provider URL). The lab
    # has no PowerDNS, so the mutate phase stages a throwaway HTTP stub on the
    # node host (over passwordless root SSH) that answers any GET with `200 {}`,
    # points the provider URL at it, and runs the full create/get/set/delete
    # cycle staged — `sdn apply` is never called, so no real DNS backend is
    # touched. Each is a separate leaf (one `pve ...` apiece so the scorer maps
    # them individually).
    ctx.defer("dns create",
              "registers a DNS provider — covered live by `e2e --mutate`, which points it at a host-local API stub and stages it (never applied)",
              "pve sdn dns create pveclidns --type powerdns --url URL --key KEY",
              isolation=True, live_covered=True)
    ctx.defer("dns get",
              "reads a staged DNS provider — covered live by `e2e --mutate`",
              "pve sdn dns get pveclidns",
              isolation=True, live_covered=True)
    ctx.defer("dns set",
              "edits a staged DNS provider — covered live by `e2e --mutate` (re-runs the connectivity probe against the host-local stub)",
              "pve sdn dns set pveclidns --ttl 600",
              isolation=True, live_covered=True)
    ctx.defer("dns delete",
              "removes a staged DNS provider — covered live by `e2e --mutate`",
              "pve sdn dns delete pveclidns --yes",
              isolation=True, live_covered=True)
    ctx.defer("ipam create/get/delete",
              "pve-type IPAM CRUD — covered live by `e2e --mutate`",
              "pve sdn ipam create pvecliipam --type pve",
              isolation=True, live_covered=True)
    ctx.defer("ipam set",
              "the pve IPAM exposes no settable properties; the netbox/phpipam types "
              "validate a reachable external backend on create — covered by unit tests",
              "pve sdn ipam set pvecliipam --url URL",
              isolation=True, live_covered=False)
    ctx.defer("vnet set",
              "stages a vnet edit — covered live by `e2e --mutate`",
              f"pve sdn vnet set {Isolation.SDN_VNET} --alias pve-cli-e2e",
              isolation=True, live_covered=True)
    ctx.defer("vnet firewall rules create/get/set/delete",
              "stages a vnet firewall rule — covered live by `e2e --mutate`",
              f"pve sdn vnet firewall rules create {Isolation.SDN_VNET} --type forward --action ACCEPT",
              isolation=True, live_covered=True)
    ctx.defer("vnet firewall options set",
              "stages a vnet forward policy (never --enable, never applied) on the "
              "isolated guest-free pvecli0 vnet — covered live by `e2e --mutate`",
              f"pve sdn vnet firewall options set {Isolation.SDN_VNET} --policy-forward ACCEPT",
              isolation=True, live_covered=True)
