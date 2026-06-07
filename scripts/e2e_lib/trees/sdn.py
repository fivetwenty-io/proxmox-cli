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

    # Creating a fabric, prefix list, or route map stages BGP/OSPF routing
    # topology that requires real FRR peers and a multi-node underlay. These are
    # never provisioned on the shared lab — covered by unit tests only.
    ctx.defer("fabric create/set/delete + node create/set/delete",
              "needs a real BGP/OSPF/OpenFabric topology with FRR peers — covered by unit tests",
              "pve sdn fabric create pveclifab --protocol openfabric",
              isolation=False, live_covered=False)
    ctx.defer("prefix-list create/set/delete + entry add/set/delete",
              "stages routing policy tied to a fabric — covered by unit tests",
              "pve sdn prefix-list create pveclipl --entry 'permit 172.30.0.0/24'",
              isolation=False, live_covered=False)
    ctx.defer("route-map entry add/set/delete",
              "stages BGP route policy tied to a fabric — covered by unit tests",
              "pve sdn route-map entry add pveclirm --order 10 --action permit",
              isolation=False, live_covered=False)

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
    ctx.defer("lock acquire",
              "acquires the global SDN config lock — requires a paired release and "
              "blocks all concurrent SDN writes; not exercised live",
              "pve sdn lock acquire",
              isolation=False, live_covered=False)
    ctx.defer("lock release",
              "releases the global SDN config lock — must follow acquire; "
              "not exercised live (paired with acquire, which is also deferred)",
              "pve sdn lock release --lock-token <token> --yes",
              isolation=False, live_covered=False)

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
    # A DNS provider (PowerDNS et al.) validates connectivity to an external DNS
    # backend on create, so it cannot be provisioned on the lab — the read verbs
    # need an existing provider that only such a backend can create. Each is
    # deferred per leaf (one `pve ...` command apiece so the scorer maps them
    # individually) and covered by unit tests.
    ctx.defer("dns create",
              "validates connectivity to an external DNS backend — covered by unit tests",
              "pve sdn dns create pveclidns --type powerdns --url URL --key KEY",
              isolation=True, live_covered=False)
    ctx.defer("dns get",
              "needs an existing DNS provider (creatable only with a reachable external backend) — covered by unit tests",
              "pve sdn dns get pveclidns",
              isolation=True, live_covered=False)
    ctx.defer("dns set",
              "needs an existing DNS provider (creatable only with a reachable external backend) — covered by unit tests",
              "pve sdn dns set pveclidns --ttl 600",
              isolation=True, live_covered=False)
    ctx.defer("dns delete",
              "needs an existing DNS provider (creatable only with a reachable external backend) — covered by unit tests",
              "pve sdn dns delete pveclidns --yes",
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
