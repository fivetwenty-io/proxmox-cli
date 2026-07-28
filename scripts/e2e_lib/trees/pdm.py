"""pdm: Proxmox Datacenter Manager command group (opt-in read-only happy path).

Unlike every other tree, this one targets a *different product*: the checks
need a context whose `product` is `pdm` and a reachable Proxmox Datacenter
Manager. The sweep therefore treats the tree as opt-in — the runner hands it
the `--pdm-context` / `$PMX_E2E_PDM_CONTEXT` context instead of the sweep
context (empty when not given), and `run` records a single SKIP and returns
when the opt-in is absent, the context is not a `product: pdm` context, or the
server does not answer the shared root `version` command (PDM has no `ping`
equivalent — `version ping` is PBS-only — so reachability is proven by a
successful `pmx version`, which for a PDM context queries GET /version; see
internal/cli/version).

Every check in this module sits lexically inside an `if` on purpose: the whole
tree is conditional on the opt-in, so the coverage matrix must classify its
leaves as prerequisite-gated (◑), never unconditional (✓). Keep new checks
nested (the section helpers wrap their bodies in `if ctx.env.context:`, and the
proxy helpers in `if remote:` once the caller has already returned on None) or
the generated matrix will overstate the guarantee.

There is no PDM mutate phase: every mutating verb is recorded as deferred with
`live_covered=False` and is covered by unit tests instead, with one exception
— the confirmation gate on a destructive command is cheap, local, and never
touches the network, so it is exercised live here as a negative check (assert
the refusal, confirm nothing mutated).
"""

from __future__ import annotations

from ..context import CmdResult, Ctx

# Like PBS, several PDM endpoints (GET /nodes, remote inventories, proxied
# firewall status) only accept ticket auth and 403 any API token regardless
# of its ACLs; the sweep's token context skips them instead of failing.
TICKET_ONLY = {"permission check failed": "endpoint accepts only ticket auth (API tokens get 403)"}

# A PDM answers about remotes it cannot currently reach by returning the
# remote's own transport error. That is the remote being down, not the CLI
# being wrong, so the proxied checks skip on it.
REMOTE_DOWN = {
    "connection failed": "PDM cannot reach the remote",
    "request timed out": "PDM cannot reach the remote",
    "Could not establish a TLS connection": "PDM cannot reach the remote (fingerprint or cert mismatch)",
}

# PDM's own node is always addressable as "localhost"; GET /nodes needs ticket
# auth, so a token context cannot enumerate it and falls back to this.
LOCAL_NODE = "localhost"

NAME = "pdm"
DESCRIPTION = "Proxmox Datacenter Manager admin (opt-in: --pdm-context)"

# The runner swaps this tree's Env.context for the --pdm-context value (and
# clears the discovered PVE node, which is meaningless here).
PRODUCT = "pdm"


def is_list(res: CmdResult) -> str | None:
    return None if isinstance(res.json(), list) else "expected a JSON array"


def _rows(res: CmdResult) -> list:
    """The result's JSON as a list of rows, or [] if it is neither."""
    try:
        data = res.json()
    except ValueError:
        return []
    return data if isinstance(data, list) else []


def _pick(rows: list, *keys: str) -> str | None:
    """First non-empty value of any of `keys` across a list of JSON rows."""
    for row in rows:
        if isinstance(row, dict):
            for k in keys:
                v = row.get(k)
                if v not in (None, ""):
                    return str(v)
    return None


def _tail(res: CmdResult) -> str:
    return (res.stderr.strip() or res.stdout.strip())[:80]


def run(ctx: Ctx) -> None:
    if not ctx.env.context:
        ctx.skip("pdm sweep", "opt-in: pass --pdm-context or set PMX_E2E_PDM_CONTEXT")
        return
    ok, why = _gate(ctx)
    if not ok:
        ctx.skip("pdm sweep", why)
        return

    _core(ctx)
    _access(ctx)
    _config(ctx)
    _remotes(ctx)
    _node(ctx)
    _proxy(ctx)
    _negative(ctx)
    _defers(ctx)


def _gate(ctx: Ctx) -> tuple[bool, str]:
    """Opt-in preconditions: configured `product: pdm` context + reachable server."""
    ls = ctx.run("context", "ls", with_context=False)
    entry = None
    if ls.rc == 0:
        try:
            entry = next((c for c in ls.json() if isinstance(c, dict)
                          and c.get("name") == ctx.env.context), None)
        except ValueError:
            entry = None
    if entry is None:
        return False, f"pdm context {ctx.env.context!r} not in config"
    if entry.get("product") != "pdm":
        return False, f"context {ctx.env.context!r} is not a product: pdm context"
    ver = ctx.run("version")
    if ver.rc != 0:
        return False, f"PDM server unreachable: {(ver.stderr.strip() or ver.stdout.strip())[:80]}"
    return True, ""


# --------------------------------------------------------------------------- #
# remotes, resources, ceph, subscription pool                                 #
# --------------------------------------------------------------------------- #
def _core(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate — keeps every check conditional (◑)
        # version is the shared root command (product:context), not nested
        # under "pdm" — see internal/cli/version.
        ctx.check("version", "version")

        ctx.check("remote ls", "pdm", "remote", "ls", validate=is_list)
        ctx.check("resource ls", "pdm", "resource", "ls", validate=is_list)
        ctx.check("resource status", "pdm", "resource", "status")
        ctx.check("resource subscription", "pdm", "resource", "subscription",
                  validate=is_list)
        # top-entities renders null when no metrics have been collected yet,
        # which is still valid JSON, so exit 0 is the whole assertion.
        ctx.check("resource top-entities", "pdm", "resource", "top-entities",
                  "--timeframe", "day")

        # Cross-remote task views and the update summary are served from PDM's
        # own cache, so they answer even when every remote is unreachable.
        ctx.check("remote task ls", "pdm", "remote", "task", "ls", validate=is_list)
        ctx.check("remote task statistics", "pdm", "remote", "task", "statistics")
        ctx.check("remote updates summary", "pdm", "remote", "updates", "summary")
        ctx.check("remote metric-collection status", "pdm", "remote",
                  "metric-collection", "status", skip_on=TICKET_ONLY)

        # SDN inventories aggregate across every managed remote; an empty array
        # is the normal answer on a PDM with no SDN-configured remote.
        ctx.check("sdn controller ls", "pdm", "sdn", "controller", "ls", validate=is_list)
        ctx.check("sdn vnet ls", "pdm", "sdn", "vnet", "ls", validate=is_list)
        ctx.check("sdn zone ls", "pdm", "sdn", "zone", "ls", validate=is_list)

        ctx.check("subscription node-status", "pdm", "subscription", "node-status",
                  validate=is_list)
        keys = ctx.check("subscription key ls", "pdm", "subscription", "key", "ls",
                         validate=is_list)
        key = _pick(_rows(keys), "key", "id")
        if key:
            ctx.check("subscription key show", "pdm", "subscription", "key", "show", key)
        else:
            ctx.skip("subscription key show", "no subscription key in the pool")

        clusters = ctx.check("ceph ls", "pdm", "ceph", "ls", validate=is_list)
        cluster = _pick(_rows(clusters), "cluster", "display-name")
        if cluster:
            for verb in ("flags", "fs", "mds", "mgr", "mon", "osd-tree", "pools",
                         "status", "summary"):
                ctx.check(f"ceph {verb}", "pdm", "ceph", verb, cluster)
        else:
            for verb in ("flags", "fs", "mds", "mgr", "mon", "osd-tree", "pools",
                         "status", "summary"):
                ctx.skip(f"ceph {verb}", "no Ceph cluster registered")


# --------------------------------------------------------------------------- #
# access control: users, roles, permissions, realms                           #
# --------------------------------------------------------------------------- #
def _access(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        users = ctx.check("user ls", "pdm", "user", "ls", validate=is_list)
        ctx.check("role ls", "pdm", "role", "ls", validate=is_list)
        # permission ls renders a path -> {priv: propagate} map, not a list.
        ctx.check("permission ls", "pdm", "permission", "ls")
        ctx.check("realm ls", "pdm", "realm", "ls", validate=is_list)
        ctx.check("acl ls", "pdm", "acl", "ls", validate=is_list)
        # The TFA index lists only users that have a factor enrolled, so it is
        # empty on a fresh PDM; `tfa show` is per-user and answers regardless.
        ctx.check("tfa ls", "pdm", "tfa", "ls", validate=is_list)

        userid = _pick(_rows(users), "userid", "id")
        if userid:
            ctx.check("user show", "pdm", "user", "show", userid)
            ctx.check("tfa show", "pdm", "tfa", "show", userid, validate=is_list)
            tokens = ctx.check("token ls", "pdm", "token", "ls", userid,
                               validate=is_list)
            token = _pick(_rows(tokens), "token-name", "tokenid", "name")
            if token:
                ctx.check("token show", "pdm", "token", "show", userid, token)
            else:
                ctx.skip("token show", f"user {userid} has no API token")
        else:
            for miss in ("user show", "tfa show", "token ls", "token show"):
                ctx.skip(miss, "no user listed")

        # The two built-in realms always exist; the pluggable ones are listed
        # per type and only have a `show` target once one is configured.
        ctx.check("realm pam show", "pdm", "realm", "pam", "show")
        ctx.check("realm pdm show", "pdm", "realm", "pdm", "show")
        for kind in ("ad", "ldap", "openid"):
            listed = ctx.check(f"realm {kind} ls", "pdm", "realm", kind, "ls",
                               validate=is_list)
            name = _pick(_rows(listed), "realm", "name")
            if name:
                ctx.check(f"realm {kind} show", "pdm", "realm", kind, "show", name)
            else:
                ctx.skip(f"realm {kind} show", f"no {kind} realm configured")


# --------------------------------------------------------------------------- #
# this PDM's own configuration: views, notes, certificates, ACME, auto-install #
# --------------------------------------------------------------------------- #
def _config(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        # notes/webauthn/certificate render an empty object on an unconfigured
        # PDM rather than failing, so exit 0 is the assertion.
        ctx.check("config notes show", "pdm", "config", "notes", "show")
        ctx.check("config webauthn show", "pdm", "config", "webauthn", "show")
        ctx.check("config certificate show", "pdm", "config", "certificate", "show")

        views = ctx.check("config view ls", "pdm", "config", "view", "ls",
                          validate=is_list)
        view = _pick(_rows(views), "id", "name")
        if view:
            ctx.check("config view show", "pdm", "config", "view", "show", view)
        else:
            ctx.skip("config view show", "no saved resource view")

        # The ACME catalogs (directories, challenge schemas, the default
        # directory's ToS) are served without any account being registered.
        ctx.check("config acme directories ls", "pdm", "config", "acme",
                  "directories", "ls", validate=is_list)
        ctx.check("config acme challenge-schema ls", "pdm", "config", "acme",
                  "challenge-schema", "ls", validate=is_list)
        ctx.check("config acme tos show", "pdm", "config", "acme", "tos", "show")

        accounts = ctx.check("config acme account ls", "pdm", "config", "acme",
                             "account", "ls", validate=is_list)
        account = _pick(_rows(accounts), "name", "account")
        if account:
            ctx.check("config acme account show", "pdm", "config", "acme",
                      "account", "show", account)
        else:
            ctx.skip("config acme account show", "no ACME account registered")

        plugins = ctx.check("config acme plugin ls", "pdm", "config", "acme",
                            "plugin", "ls", validate=is_list)
        plugin = _pick(_rows(plugins), "plugin", "id")
        if plugin:
            ctx.check("config acme plugin show", "pdm", "config", "acme",
                      "plugin", "show", plugin)
        else:
            ctx.skip("config acme plugin show", "no ACME plugin configured")

        ctx.check("auto-install installation ls", "pdm", "auto-install",
                  "installation", "ls", validate=is_list)
        ctx.check("auto-install token ls", "pdm", "auto-install", "token", "ls",
                  validate=is_list)
        prepared = ctx.check("auto-install prepared ls", "pdm", "auto-install",
                             "prepared", "ls", validate=is_list)
        answer = _pick(_rows(prepared), "id", "name")
        if answer:
            ctx.check("auto-install prepared show", "pdm", "auto-install",
                      "prepared", "show", answer)
        else:
            ctx.skip("auto-install prepared show", "no prepared answer file")


# --------------------------------------------------------------------------- #
# this PDM's own nodes                                                        #
# --------------------------------------------------------------------------- #
def _node(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        listed = ctx.check("node ls", "pdm", "node", "ls", validate=is_list,
                           skip_on=TICKET_ONLY)
        # GET /nodes needs ticket auth, so on the sweep's token context the list
        # above skips. Every per-node endpoint still answers for "localhost",
        # which is how PDM names its own single node.
        node = _pick(_rows(listed), "node", "name") or LOCAL_NODE

        ctx.check("node status", "pdm", "node", "status", node)
        ctx.check("node config show", "pdm", "node", "config", "show", node)
        ctx.check("node dns show", "pdm", "node", "dns", "show", node)
        ctx.check("node time show", "pdm", "node", "time", "show", node)
        ctx.check("node certificate info", "pdm", "node", "certificate", "info",
                  node, validate=is_list)
        ctx.check("node subscription show", "pdm", "node", "subscription", "show",
                  node)
        ctx.check("node report", "pdm", "node", "report", node, fmt="plain")
        ctx.check("node syslog", "pdm", "node", "syslog", node, "--limit", "5",
                  validate=is_list)
        ctx.check("node journal", "pdm", "node", "journal", node,
                  "--lastentries", "5")
        # rrddata is one of the ticket-only endpoints on this host.
        ctx.check("node rrddata", "pdm", "node", "rrddata", node,
                  "--timeframe", "hour", skip_on=TICKET_ONLY)

        ctx.check("node apt repositories", "pdm", "node", "apt", "repositories", node)
        ctx.check("node apt updates", "pdm", "node", "apt", "updates", node,
                  validate=is_list)
        versions = ctx.check("node apt versions", "pdm", "node", "apt", "versions",
                             node, validate=is_list)
        # The changelog is per package; take one PDM ships rather than hardcoding
        # a name that a future release could drop.
        pkg = _pick(_rows(versions), "package", "name")
        if pkg:
            ctx.check("node apt changelog", "pdm", "node", "apt", "changelog", node,
                      "--name", pkg, fmt="plain")
        else:
            ctx.skip("node apt changelog", "no package listed by apt versions")

        ifaces = ctx.check("node network ls", "pdm", "node", "network", "ls", node,
                           validate=is_list)
        iface = _pick(_rows(ifaces), "iface", "name")
        if iface:
            ctx.check("node network show", "pdm", "node", "network", "show", node,
                      iface)
        else:
            ctx.skip("node network show", "no network interface listed")

        tasks = ctx.check("node task ls", "pdm", "node", "task", "ls", node,
                          validate=is_list)
        upid = _pick(_rows(tasks), "upid")
        if upid:
            ctx.check("node task status", "pdm", "node", "task", "status", node, upid)
            ctx.check("node task log", "pdm", "node", "task", "log", node, upid,
                      "--limit", "5")
        else:
            ctx.skip("node task status", "no task in history")
            ctx.skip("node task log", "no task in history")

        # The node's EVPN views are proxied to a remote, so they need both a
        # configured vnet/zone and the remote that owns it.
        _node_sdn(ctx, node)


def _node_sdn(ctx: Ctx, node: str) -> None:
    """PDM node EVPN reads: `--remote` is required and names the owning remote."""
    if ctx.env.context:  # opt-in gate (see module docstring)
        vnets = _rows(ctx.run("pdm", "sdn", "vnet", "ls"))
        zones = _rows(ctx.run("pdm", "sdn", "zone", "ls"))
        vnet = _pick(vnets, "vnet", "name")
        vnet_remote = _pick(vnets, "remote")
        zone = _pick(zones, "zone", "name")
        zone_remote = _pick(zones, "remote")
        if vnet and vnet_remote:
            ctx.check("node sdn vnet mac-vrf", "pdm", "node", "sdn", "vnet",
                      "mac-vrf", node, vnet, "--remote", vnet_remote,
                      skip_on=REMOTE_DOWN)
        else:
            ctx.skip("node sdn vnet mac-vrf", "no SDN vnet on any managed remote")
        if zone and zone_remote:
            ctx.check("node sdn zone ip-vrf", "pdm", "node", "sdn", "zone",
                      "ip-vrf", node, zone, "--remote", zone_remote,
                      skip_on=REMOTE_DOWN)
        else:
            ctx.skip("node sdn zone ip-vrf", "no SDN zone on any managed remote")


# --------------------------------------------------------------------------- #
# the managed-remote registry                                                 #
# --------------------------------------------------------------------------- #
def _remotes(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        remotes = _rows(ctx.run("pdm", "remote", "ls"))
        rid = _pick(remotes, "id", "name")
        if rid:
            ctx.check("remote show", "pdm", "remote", "show", rid)
            # version is fetched from the remote itself, so it needs the remote
            # up; rrddata is served from PDM's cache but is ticket-only here.
            ctx.check("remote version", "pdm", "remote", "version", rid,
                      skip_on=REMOTE_DOWN)
            ctx.check("remote rrddata", "pdm", "remote", "rrddata", rid,
                      "--timeframe", "hour", skip_on={**TICKET_ONLY, **REMOTE_DOWN})
        else:
            for miss in ("remote show", "remote version", "remote rrddata"):
                ctx.skip(miss, "no managed remote registered")


# --------------------------------------------------------------------------- #
# proxied operations against managed PVE/PBS remotes                          #
# --------------------------------------------------------------------------- #
def _proxy(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        ctx.check("pve remote ls", "pdm", "pve", "remote", "ls", validate=is_list,
                  skip_on=TICKET_ONLY)
        ctx.check("pbs remote ls", "pdm", "pbs", "remote", "ls", validate=is_list,
                  skip_on=TICKET_ONLY)
        ctx.check("pve firewall status", "pdm", "pve", "firewall", "status",
                  validate=is_list, skip_on=TICKET_ONLY)

    by_kind = _reachable(ctx)
    _proxy_pve(ctx, by_kind.get("pve"))
    _proxy_pbs(ctx, by_kind.get("pbs"))


def _reachable(ctx: Ctx) -> dict[str, str]:
    """One managed remote per type that the PDM can currently talk to.

    Every proxied read forwards to the remote, so pointing the sections at an
    unreachable one would turn ~45 checks into ~45 connection timeouts. Rather
    than probe each remote in turn, this reads the aggregate `resource ls`,
    which reports one row per remote and carries an `error` field for exactly
    the remotes the PDM could not reach.
    """
    if not ctx.env.context:
        return {}
    healthy = {str(r.get("remote")) for r in _rows(ctx.run("pdm", "resource", "ls"))
               if isinstance(r, dict) and r.get("remote") and not r.get("error")}
    found: dict[str, str] = {}
    for row in _rows(ctx.run("pdm", "remote", "ls")):
        if not isinstance(row, dict):
            continue
        kind, rid = row.get("type"), row.get("id") or row.get("name")
        if kind in ("pve", "pbs") and rid and str(rid) in healthy:
            found.setdefault(str(kind), str(rid))
    return found


def _proxy_pve(ctx: Ctx, remote: str | None) -> None:
    """Reads proxied to a managed PVE remote's cluster, nodes, and guests."""
    if remote is None:
        ctx.skip("pve proxy", "no reachable managed PVE remote")
        return
    if remote:  # keeps every check below prerequisite-gated (◑)
        ctx.check("pve cluster status", "pdm", "pve", "cluster", "status", remote,
                  validate=is_list)
        ctx.check("pve cluster resources", "pdm", "pve", "cluster", "resources",
                  remote, validate=is_list)
        ctx.check("pve cluster next-id", "pdm", "pve", "cluster", "next-id", remote)
        ctx.check("pve options", "pdm", "pve", "options", remote)
        ctx.check("pve updates", "pdm", "pve", "updates", remote)
        ctx.check("pve firewall show", "pdm", "pve", "firewall", "show", remote)
        ctx.check("pve firewall rules", "pdm", "pve", "firewall", "rules", remote,
                  validate=is_list)
        ctx.check("pve firewall options show", "pdm", "pve", "firewall", "options",
                  "show", remote)

        tasks = ctx.check("pve task ls", "pdm", "pve", "task", "ls", remote,
                          validate=is_list)
        upid = _pick(_rows(tasks), "upid")
        if upid:
            ctx.check("pve task status", "pdm", "pve", "task", "status", remote, upid)
            ctx.check("pve task log", "pdm", "pve", "task", "log", remote, upid,
                      "--limit", "5")
        else:
            ctx.skip("pve task status", "no task in the remote's history")
            ctx.skip("pve task log", "no task in the remote's history")

        _proxy_pve_guests(ctx, remote)
        _proxy_pve_node(ctx, remote)


def _proxy_pve_guests(ctx: Ctx, remote: str) -> None:
    """Per-guest reads; qemu and lxc expose the same shape, so they share a loop."""
    if remote:  # keeps every check below prerequisite-gated (◑)
        for kind in ("qemu", "lxc"):
            listed = ctx.check(f"pve {kind} ls", "pdm", "pve", kind, "ls", remote,
                               validate=is_list)
            vmid = _pick(_rows(listed), "vmid", "id")
            if not vmid:
                for verb in ("config", "status", "pending", "rrddata", "snapshot ls",
                             "firewall options show", "firewall rules"):
                    ctx.skip(f"pve {kind} {verb}", f"no {kind} guest on the remote")
                continue
            ctx.check(f"pve {kind} config", "pdm", "pve", kind, "config", remote, vmid)
            ctx.check(f"pve {kind} status", "pdm", "pve", kind, "status", remote, vmid)
            ctx.check(f"pve {kind} pending", "pdm", "pve", kind, "pending", remote,
                      vmid, validate=is_list)
            ctx.check(f"pve {kind} rrddata", "pdm", "pve", kind, "rrddata", remote,
                      vmid, "--timeframe", "hour", validate=is_list)
            ctx.check(f"pve {kind} snapshot ls", "pdm", "pve", kind, "snapshot", "ls",
                      remote, vmid, validate=is_list)
            ctx.check(f"pve {kind} firewall options show", "pdm", "pve", kind,
                      "firewall", "options", "show", remote, vmid)
            ctx.check(f"pve {kind} firewall rules", "pdm", "pve", kind, "firewall",
                      "rules", remote, vmid, validate=is_list)
            if kind == "qemu":
                # A migration dry run: it reports what would block the move and
                # changes nothing.
                ctx.check("pve qemu migrate-preconditions", "pdm", "pve", "qemu",
                          "migrate-preconditions", remote, vmid)


def _proxy_pve_node(ctx: Ctx, remote: str) -> None:
    """Per-node reads on a managed PVE remote, plus its storage and EVPN views."""
    if remote:  # keeps every check below prerequisite-gated (◑)
        nodes = ctx.check("pve node ls", "pdm", "pve", "node", "ls", remote,
                          validate=is_list)
        node = _pick(_rows(nodes), "node", "name")
        if not node:
            ctx.skip("pve node reads", "remote reported no node")
            return

        ctx.check("pve node config", "pdm", "pve", "node", "config", remote, node)
        ctx.check("pve node status", "pdm", "pve", "node", "status", remote, node)
        ctx.check("pve node network", "pdm", "pve", "node", "network", remote, node,
                  validate=is_list)
        ctx.check("pve node subscription", "pdm", "pve", "node", "subscription",
                  remote, node)
        ctx.check("pve node rrddata", "pdm", "pve", "node", "rrddata", remote, node,
                  "--timeframe", "hour", validate=is_list)
        ctx.check("pve node firewall status", "pdm", "pve", "node", "firewall",
                  "status", remote, node)
        ctx.check("pve node firewall rules", "pdm", "pve", "node", "firewall",
                  "rules", remote, node, validate=is_list)
        ctx.check("pve node firewall options show", "pdm", "pve", "node", "firewall",
                  "options", "show", remote, node)
        ctx.check("pve node apt repositories", "pdm", "pve", "node", "apt",
                  "repositories", remote, node)
        updates = ctx.check("pve node apt updates", "pdm", "pve", "node", "apt",
                            "updates", remote, node, validate=is_list)
        pkg = _pick(_rows(updates), "package", "name")
        if pkg:
            ctx.check("pve node apt changelog", "pdm", "pve", "node", "apt",
                      "changelog", remote, node, pkg, fmt="plain")
        else:
            ctx.skip("pve node apt changelog", "no pending package update to read")

        storages = ctx.check("pve storage ls", "pdm", "pve", "storage", "ls", remote,
                             node, validate=is_list)
        storage = _pick(_rows(storages), "storage", "name")
        if storage:
            ctx.check("pve storage status", "pdm", "pve", "storage", "status", remote,
                      node, storage)
            ctx.check("pve storage rrddata", "pdm", "pve", "storage", "rrddata",
                      remote, node, storage, "--timeframe", "hour", validate=is_list)
        else:
            ctx.skip("pve storage status", "no storage on the remote's node")
            ctx.skip("pve storage rrddata", "no storage on the remote's node")

        # EVPN reads need an SDN vnet/zone actually defined on this remote.
        vnet = _pick([r for r in _rows(ctx.run("pdm", "sdn", "vnet", "ls"))
                      if isinstance(r, dict) and r.get("remote") == remote],
                     "vnet", "name")
        zone = _pick([r for r in _rows(ctx.run("pdm", "sdn", "zone", "ls"))
                      if isinstance(r, dict) and r.get("remote") == remote],
                     "zone", "name")
        if vnet:
            ctx.check("pve node sdn vnet mac-vrf", "pdm", "pve", "node", "sdn",
                      "vnet", "mac-vrf", remote, node, vnet)
        else:
            ctx.skip("pve node sdn vnet mac-vrf", "no SDN vnet on this remote")
        if zone:
            ctx.check("pve node sdn zone ip-vrf", "pdm", "pve", "node", "sdn",
                      "zone", "ip-vrf", remote, node, zone)
        else:
            ctx.skip("pve node sdn zone ip-vrf", "no SDN zone on this remote")


def _proxy_pbs(ctx: Ctx, remote: str | None) -> None:
    """Reads proxied to a managed PBS remote's datastores, tasks, and node."""
    if remote is None:
        ctx.skip("pbs proxy", "no reachable managed PBS remote")
        return
    if remote:  # keeps every check below prerequisite-gated (◑)
        ctx.check("pbs status", "pdm", "pbs", "status", remote)
        ctx.check("pbs rrddata", "pdm", "pbs", "rrddata", remote,
                  "--timeframe", "hour", validate=is_list)

        tasks = ctx.check("pbs task ls", "pdm", "pbs", "task", "ls", remote,
                          validate=is_list)
        upid = _pick(_rows(tasks), "upid")
        if upid:
            ctx.check("pbs task status", "pdm", "pbs", "task", "status", remote, upid)
            ctx.check("pbs task log", "pdm", "pbs", "task", "log", remote, upid,
                      "--limit", "5")
        else:
            ctx.skip("pbs task status", "no task in the remote's history")
            ctx.skip("pbs task log", "no task in the remote's history")

        stores = ctx.check("pbs datastore ls", "pdm", "pbs", "datastore", "ls",
                           remote, validate=is_list)
        store = _pick(_rows(stores), "store", "name")
        if store:
            ctx.check("pbs datastore namespaces", "pdm", "pbs", "datastore",
                      "namespaces", remote, store, validate=is_list)
            ctx.check("pbs datastore snapshots", "pdm", "pbs", "datastore",
                      "snapshots", remote, store, validate=is_list)
            ctx.check("pbs datastore rrddata", "pdm", "pbs", "datastore", "rrddata",
                      remote, store, "--timeframe", "hour", validate=is_list)
        else:
            for miss in ("pbs datastore namespaces", "pbs datastore snapshots",
                         "pbs datastore rrddata"):
                ctx.skip(miss, "no datastore on the remote")

        # PBS remotes are single-node and PDM names that node "localhost", the
        # same convention it uses for its own node.
        node = LOCAL_NODE
        ctx.check("pbs node subscription", "pdm", "pbs", "node", "subscription",
                  remote, node)
        ctx.check("pbs node apt repositories", "pdm", "pbs", "node", "apt",
                  "repositories", remote, node)
        updates = ctx.check("pbs node apt updates", "pdm", "pbs", "node", "apt",
                            "updates", remote, node, validate=is_list)
        pkg = _pick(_rows(updates), "package", "name")
        if pkg:
            ctx.check("pbs node apt changelog", "pdm", "pbs", "node", "apt",
                      "changelog", remote, node, pkg, fmt="plain")
        else:
            ctx.skip("pbs node apt changelog", "no pending package update to read")


# --------------------------------------------------------------------------- #
# confirmation gate (local, no network call — safe to run live)               #
# --------------------------------------------------------------------------- #
def _negative(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        # `remote delete` refuses before touching the API when --yes/-y is
        # absent (see internal/cli/pdm/remote.go); this proves the refusal
        # without registering or deleting anything.
        ctx.expect_fail("remote delete without --yes",
                        "pdm", "remote", "delete", "pmx-cli-nonexistent",
                        must_contain="--yes")


# --------------------------------------------------------------------------- #
# deferred (mutating) verbs — no PDM mutate phase exists, so every one is     #
# live_covered=False and covered by unit tests instead.                       #
# --------------------------------------------------------------------------- #
def _defers(ctx: Ctx) -> None:
    # remotes
    ctx.defer("remote add", "registers a managed remote (stores credentials); covered by unit tests",
              "pmx pdm remote add pmx-cli-remote --hostname pve.example --fingerprint ... --token-id ... --token-secret ...")
    ctx.defer("remote update", "modifies a managed remote; covered by unit tests",
              "pmx pdm remote update pmx-cli-remote --comment e2e")
    ctx.defer("remote delete", "removes a managed remote; covered by unit tests",
              "pmx pdm remote delete pmx-cli-remote --yes")
    ctx.defer("remote probe-certificate", "re-probes and stores a remote's TLS fingerprint; covered by unit tests",
              "pmx pdm remote probe-certificate pmx-cli-remote")
    ctx.defer("remote metric-collection trigger", "triggers a metric-collection run against a remote; covered by unit tests",
              "pmx pdm remote metric-collection trigger --remote pmx-cli-remote")
    ctx.defer("remote updates refresh", "refreshes the available-package summary for every managed remote; covered by unit tests",
              "pmx pdm remote updates refresh")
    ctx.defer("remote task refresh", "forces a task-cache refresh against every managed remote; covered by unit tests",
              "pmx pdm remote task refresh")

    # cross-remote SDN
    ctx.defer("sdn vnet add", "creates a VNet on several managed remotes at once; covered by unit tests",
              "pmx pdm sdn vnet add pmx-cli-vnet --remote pmx-cli-remote=pmx-cli-zone")
    ctx.defer("sdn zone add", "creates an SDN zone on several managed remotes at once; covered by unit tests",
              "pmx pdm sdn zone add pmx-cli-zone --remote pmx-cli-remote")

    # resources
    ctx.defer("resource location-info", "refreshes the location-info cache for a view; covered by unit tests",
              "pmx pdm resource location-info --view pmx-cli-view")

    # subscription pool
    ctx.defer("subscription key add", "adds subscription keys to the pool; covered by unit tests",
              "pmx pdm subscription key add XXXXX-XXXXX-XXXXX-XXXXX")
    ctx.defer("subscription key delete", "removes a subscription key from the pool; covered by unit tests",
              "pmx pdm subscription key delete XXXXX-XXXXX-XXXXX-XXXXX --yes")
    ctx.defer("subscription key assign", "binds a pool key to a remote node; covered by unit tests",
              "pmx pdm subscription key assign XXXXX-XXXXX-XXXXX-XXXXX --remote pmx-cli-remote --node pmx-cli-node")
    ctx.defer("subscription key unassign", "drops the remote-node binding for a pool key; covered by unit tests",
              "pmx pdm subscription key unassign XXXXX-XXXXX-XXXXX-XXXXX --yes")
    ctx.defer("subscription check", "triggers a fresh subscription check on a remote node; covered by unit tests",
              "pmx pdm subscription check --remote pmx-cli-remote --node pmx-cli-node")
    ctx.defer("subscription adopt-key", "adopts a live subscription on a remote node into the pool; covered by unit tests",
              "pmx pdm subscription adopt-key --remote pmx-cli-remote --node pmx-cli-node")
    ctx.defer("subscription adopt-all", "adopts every foreign live subscription into the pool; covered by unit tests",
              "pmx pdm subscription adopt-all")
    ctx.defer("subscription auto-assign", "computes a proposed key-to-node assignment plan; covered by unit tests",
              "pmx pdm subscription auto-assign")
    ctx.defer("subscription bulk-assign", "applies a proposal returned by auto-assign; covered by unit tests",
              "pmx pdm subscription bulk-assign --file plan.json")
    ctx.defer("subscription apply-pending", "applies every pending pool change to its remote node; covered by unit tests",
              "pmx pdm subscription apply-pending")
    ctx.defer("subscription clear-pending", "drops every queued pending subscription change; covered by unit tests",
              "pmx pdm subscription clear-pending")
    ctx.defer("subscription queue-clear", "queues a subscription clear on a remote node; covered by unit tests",
              "pmx pdm subscription queue-clear --remote pmx-cli-remote --node pmx-cli-node")
    ctx.defer("subscription revert-pending-clear", "drops a queued clear on a remote node; covered by unit tests",
              "pmx pdm subscription revert-pending-clear --remote pmx-cli-remote --node pmx-cli-node")

    # access control
    ctx.defer("user add", "creates a user; covered by unit tests",
              "pmx pdm user add pmx-cli-user@pam")
    ctx.defer("user update", "modifies a user; covered by unit tests",
              "pmx pdm user update pmx-cli-user@pam --comment e2e")
    ctx.defer("user delete", "removes a user; covered by unit tests",
              "pmx pdm user delete pmx-cli-user@pam --yes")
    ctx.defer("token add", "creates an API token and prints a once-only secret — out of scope for the automated sweep; covered by unit tests",
              "pmx pdm token add pmx-cli-user@pam e2e")
    ctx.defer("token update", "modifies an API token; covered by unit tests",
              "pmx pdm token update pmx-cli-user@pam e2e --comment e2e")
    ctx.defer("token delete", "removes an API token; covered by unit tests",
              "pmx pdm token delete pmx-cli-user@pam e2e --yes")
    ctx.defer("acl update", "modifies the access control list; covered by unit tests",
              "pmx pdm acl update /resource/pmx-cli-remote PVEAuditor --auth-id audit@pam")
    ctx.defer("tfa update", "modifies a user's TFA entry description; covered by unit tests",
              "pmx pdm tfa update pmx-cli-user@pam <tfa-id> --description e2e")
    ctx.defer("tfa delete", "removes a user's TFA entry; covered by unit tests",
              "pmx pdm tfa delete pmx-cli-user@pam <tfa-id> --yes")

    # realms
    ctx.defer("realm ad add", "adds an AD authentication realm; covered by unit tests",
              "pmx pdm realm ad add pmx-cli-ad --server1 dc.example --base-dn dc=example")
    ctx.defer("realm ad update", "modifies an AD realm; covered by unit tests",
              "pmx pdm realm ad update pmx-cli-ad --comment e2e")
    ctx.defer("realm ad delete", "removes an AD realm; covered by unit tests",
              "pmx pdm realm ad delete pmx-cli-ad --yes")
    ctx.defer("realm ldap add", "adds an LDAP authentication realm; covered by unit tests",
              "pmx pdm realm ldap add pmx-cli-ldap --server1 ldap.example --base-dn dc=example --user-attr uid")
    ctx.defer("realm ldap update", "modifies an LDAP realm; covered by unit tests",
              "pmx pdm realm ldap update pmx-cli-ldap --comment e2e")
    ctx.defer("realm ldap delete", "removes an LDAP realm; covered by unit tests",
              "pmx pdm realm ldap delete pmx-cli-ldap --yes")
    ctx.defer("realm openid add", "adds an OpenID authentication realm; covered by unit tests",
              "pmx pdm realm openid add pmx-cli-oidc --issuer-url https://idp.example --client-id pdm")
    ctx.defer("realm openid update", "modifies an OpenID realm; covered by unit tests",
              "pmx pdm realm openid update pmx-cli-oidc --comment e2e")
    ctx.defer("realm openid delete", "removes an OpenID realm; covered by unit tests",
              "pmx pdm realm openid delete pmx-cli-oidc --yes")
    ctx.defer("realm pam update", "modifies the built-in PAM realm; covered by unit tests",
              "pmx pdm realm pam update --comment e2e")
    ctx.defer("realm pdm update", "modifies the built-in PDM realm; covered by unit tests",
              "pmx pdm realm pdm update --comment e2e")
    ctx.defer("realm sync", "runs a realm sync task that can create or update users; covered by unit tests",
              "pmx pdm realm sync pmx-cli-ldap")

    # this PDM's own configuration
    ctx.defer("config view add", "creates a saved resource view; covered by unit tests",
              "pmx pdm config view add pmx-cli-view --include type=qemu")
    ctx.defer("config view update", "modifies a saved resource view; covered by unit tests",
              "pmx pdm config view update pmx-cli-view --comment e2e")
    ctx.defer("config view delete", "removes a saved resource view; covered by unit tests",
              "pmx pdm config view delete pmx-cli-view --yes")
    ctx.defer("config notes update", "modifies the dashboard welcome notes; covered by unit tests",
              "pmx pdm config notes update --text 'e2e'")
    ctx.defer("config webauthn update", "modifies the WebAuthn relying-party configuration; covered by unit tests",
              "pmx pdm config webauthn update --rp-id pdm.example")
    ctx.defer("config certificate update", "modifies the certificate/ACME-domain configuration; covered by unit tests",
              "pmx pdm config certificate update --acme-domain pdm.example")
    ctx.defer("config acme account add", "registers an account with a live certificate authority; covered by unit tests",
              "pmx pdm config acme account add pmx-cli-acme --contact ops@example")
    ctx.defer("config acme account update", "updates the registration at the certificate authority; covered by unit tests",
              "pmx pdm config acme account update pmx-cli-acme --contact ops@example")
    ctx.defer("config acme account delete", "deactivates the account at the certificate authority; covered by unit tests",
              "pmx pdm config acme account delete pmx-cli-acme --yes")
    ctx.defer("config acme plugin add", "creates an ACME challenge plugin (stores API credentials); covered by unit tests",
              "pmx pdm config acme plugin add pmx-cli-dns --type dns --api cloudflare")
    ctx.defer("config acme plugin update", "modifies an ACME challenge plugin; covered by unit tests",
              "pmx pdm config acme plugin update pmx-cli-dns --disable")
    ctx.defer("config acme plugin delete", "removes an ACME challenge plugin; covered by unit tests",
              "pmx pdm config acme plugin delete pmx-cli-dns --yes")

    # this PDM's own node administration (real host)
    ctx.defer("node reboot", "reboots the real host; covered by unit tests",
              "pmx pdm node reboot --yes")
    ctx.defer("node shutdown", "shuts down the real host; covered by unit tests",
              "pmx pdm node shutdown --yes")
    ctx.defer("node config update", "modifies host configuration; covered by unit tests",
              "pmx pdm node config update --email-from pdm@example")
    ctx.defer("node dns update", "modifies host DNS configuration; covered by unit tests",
              "pmx pdm node dns update --dns1 192.0.2.53")
    ctx.defer("node time update", "modifies the host timezone; covered by unit tests",
              "pmx pdm node time update --timezone UTC")
    ctx.defer("node subscription update", "re-checks the subscription with the vendor; covered by unit tests",
              "pmx pdm node subscription update pmx-cli-node")
    ctx.defer("node task stop", "cancels a running background task; covered by unit tests",
              "pmx pdm node task stop <upid> --yes")
    ctx.defer("node apt update-database", "refreshes the package index on the host; covered by unit tests",
              "pmx pdm node apt update-database")
    ctx.defer("node apt repository add", "adds a package repository to the host; covered by unit tests",
              "pmx pdm node apt repository add --handle no-subscription")
    ctx.defer("node apt repository change", "enables or disables a package repository on the host; covered by unit tests",
              "pmx pdm node apt repository change localhost --path /etc/apt/sources.list --index 0 --enabled")
    ctx.defer("node certificate acme order", "orders a real certificate from the CA and replaces the server cert; covered by unit tests",
              "pmx pdm node certificate acme order")
    ctx.defer("node certificate acme renew", "renews the certificate at the CA and replaces the server cert; covered by unit tests",
              "pmx pdm node certificate acme renew")
    ctx.defer("node certificate upload", "replaces the server's TLS certificate; covered by unit tests",
              "pmx pdm node certificate upload --certificate cert.pem --key key.pem")
    ctx.defer("node certificate delete-custom", "removes the custom TLS certificate; covered by unit tests",
              "pmx pdm node certificate delete-custom --yes")
    ctx.defer("node network create", "changes host network configuration; covered by unit tests",
              "pmx pdm node network create pmx-cli-br0 --type bridge")
    ctx.defer("node network update", "changes host network configuration; covered by unit tests",
              "pmx pdm node network update pmx-cli-br0 --comment e2e")
    ctx.defer("node network delete", "changes host network configuration; covered by unit tests",
              "pmx pdm node network delete pmx-cli-br0 --yes")
    ctx.defer("node network apply", "applies staged host network changes; covered by unit tests",
              "pmx pdm node network apply")
    ctx.defer("node network revert", "reverts staged host network changes; covered by unit tests",
              "pmx pdm node network revert")

    # auto-install
    ctx.defer("auto-install prepared add", "creates a prepared auto-installer answer configuration; covered by unit tests",
              "pmx pdm auto-install prepared add pmx-cli-answer --config answers.toml")
    ctx.defer("auto-install prepared update", "modifies a prepared auto-installer answer configuration; covered by unit tests",
              "pmx pdm auto-install prepared update pmx-cli-answer --config answers.toml")
    ctx.defer("auto-install prepared delete", "removes a prepared auto-installer answer configuration; covered by unit tests",
              "pmx pdm auto-install prepared delete pmx-cli-answer --yes")
    ctx.defer("auto-install installation delete", "removes an automated installation record; covered by unit tests",
              "pmx pdm auto-install installation delete <uuid> --yes")
    ctx.defer("auto-install token add", "creates an automated-installation authentication token; covered by unit tests",
              "pmx pdm auto-install token add pmx-cli-token")
    ctx.defer("auto-install token delete", "removes an automated-installation authentication token; covered by unit tests",
              "pmx pdm auto-install token delete pmx-cli-token --yes")
    ctx.defer("auto-install token update", "modifies an automated-installation authentication token, and --regenerate mints a new secret; covered by unit tests",
              "pmx pdm auto-install token update pmx-cli-token --comment e2e")

    # proxied PVE operations (mutate a managed remote's real cluster)
    ctx.defer("pve remote scan", "scans a PVE host's connection info before adding it as a remote; covered by unit tests",
              "pmx pdm pve scan --hostname pve.example --token-id ... --token-secret ...")
    ctx.defer("pve remote probe-tls", "re-probes and stores a PVE host's TLS fingerprint; covered by unit tests",
              "pmx pdm pve probe-tls --hostname pve.example")
    ctx.defer("pve firewall options update", "modifies a PVE remote's cluster firewall options; covered by unit tests",
              "pmx pdm pve firewall options update pmx-cli-remote --enable")
    ctx.defer("pve node firewall options update", "modifies a PVE remote node's firewall options; covered by unit tests",
              "pmx pdm pve node firewall options update pmx-cli-remote pmx-cli-node --enable")
    ctx.defer("pve qemu firewall options update", "modifies a VM's firewall options on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu firewall options update pmx-cli-remote 100 --enable")
    ctx.defer("pve lxc firewall options update", "modifies a container's firewall options on a managed PVE remote; covered by unit tests",
              "pmx pdm pve lxc firewall options update pmx-cli-remote 200 --enable")
    ctx.defer("pve node apt update-database", "refreshes the package index on a managed PVE remote's node; covered by unit tests",
              "pmx pdm pve node apt update-database pmx-cli-remote pmx-cli-node")
    # `realms` only reads, but it takes a --hostname the PDM must dial directly:
    # the host is not yet a managed remote, so the sweep has nothing to point it
    # at. `scan` and `probe-tls` additionally store what they find.
    ctx.defer("pve realms", "reads the realms of a PVE host the PDM dials by hostname before it is registered as a remote; covered by unit tests",
              "pmx pdm pve realms --hostname pve.example")
    ctx.defer("pve task stop", "cancels a running background task on a managed PVE remote; covered by unit tests",
              "pmx pdm pve task stop pmx-cli-remote <upid> --yes")
    ctx.defer("pve qemu start", "starts a QEMU VM on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu start pmx-cli-remote 100")
    ctx.defer("pve qemu stop", "stops a QEMU VM on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu stop pmx-cli-remote 100 --yes")
    ctx.defer("pve qemu shutdown", "shuts down a QEMU VM on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu shutdown pmx-cli-remote 100")
    ctx.defer("pve qemu resume", "resumes a QEMU VM on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu resume pmx-cli-remote 100")
    ctx.defer("pve qemu migrate", "migrates a QEMU VM between nodes on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu migrate pmx-cli-remote 100 --target-node pmx-cli-node2 --yes")
    ctx.defer("pve qemu remote-migrate", "migrates a QEMU VM to a different remote cluster; covered by unit tests",
              "pmx pdm pve qemu remote-migrate pmx-cli-remote 100 --target-remote pmx-cli-remote2 --target-vmid 100 --yes")
    ctx.defer("pve qemu snapshot add", "creates a QEMU VM snapshot on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu snapshot add pmx-cli-remote 100 pmx-cli-snap")
    ctx.defer("pve qemu snapshot update", "updates a QEMU VM snapshot's description on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu snapshot update pmx-cli-remote 100 pmx-cli-snap --description e2e")
    ctx.defer("pve qemu snapshot rollback", "rolls back a QEMU VM snapshot on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu snapshot rollback pmx-cli-remote 100 pmx-cli-snap --yes")
    ctx.defer("pve qemu snapshot delete", "deletes a QEMU VM snapshot on a managed PVE remote; covered by unit tests",
              "pmx pdm pve qemu snapshot delete pmx-cli-remote 100 pmx-cli-snap --yes")
    ctx.defer("pve lxc start", "starts an LXC container on a managed PVE remote; covered by unit tests",
              "pmx pdm pve lxc start pmx-cli-remote 200")
    ctx.defer("pve lxc stop", "stops an LXC container on a managed PVE remote; covered by unit tests",
              "pmx pdm pve lxc stop pmx-cli-remote 200 --yes")
    ctx.defer("pve lxc shutdown", "shuts down an LXC container on a managed PVE remote; covered by unit tests",
              "pmx pdm pve lxc shutdown pmx-cli-remote 200")
    ctx.defer("pve lxc migrate", "migrates an LXC container between nodes on a managed PVE remote; covered by unit tests",
              "pmx pdm pve lxc migrate pmx-cli-remote 200 --target-node pmx-cli-node2 --yes")
    ctx.defer("pve lxc remote-migrate", "migrates an LXC container to a different remote cluster; covered by unit tests",
              "pmx pdm pve lxc remote-migrate pmx-cli-remote 200 --target-remote pmx-cli-remote2 --target-vmid 200 --yes")
    ctx.defer("pve lxc snapshot add", "creates an LXC container snapshot on a managed PVE remote; covered by unit tests",
              "pmx pdm pve lxc snapshot add pmx-cli-remote 200 pmx-cli-snap")
    ctx.defer("pve lxc snapshot update", "updates an LXC container snapshot's description on a managed PVE remote; covered by unit tests",
              "pmx pdm pve lxc snapshot update pmx-cli-remote 200 pmx-cli-snap --description e2e")
    ctx.defer("pve lxc snapshot rollback", "rolls back an LXC container snapshot on a managed PVE remote; covered by unit tests",
              "pmx pdm pve lxc snapshot rollback pmx-cli-remote 200 pmx-cli-snap --yes")
    ctx.defer("pve lxc snapshot delete", "deletes an LXC container snapshot on a managed PVE remote; covered by unit tests",
              "pmx pdm pve lxc snapshot delete pmx-cli-remote 200 pmx-cli-snap --yes")

    # proxied PBS operations (mutate a managed remote, or dial an unregistered host)
    ctx.defer("pbs scan", "scans a PBS host's connection info before adding it as a remote; covered by unit tests",
              "pmx pdm pbs scan --hostname pbs.example --authid ... --token ...")
    ctx.defer("pbs probe-tls", "re-probes and stores a PBS host's TLS fingerprint; covered by unit tests",
              "pmx pdm pbs probe-tls --hostname pbs.example")
    ctx.defer("pbs realms", "reads the realms of a PBS host the PDM dials by hostname before it is registered as a remote; covered by unit tests",
              "pmx pdm pbs realms --hostname pbs.example")
    ctx.defer("pbs task stop", "cancels a running background task on a managed PBS remote; covered by unit tests",
              "pmx pdm pbs task stop pmx-cli-remote <upid> --yes")
    ctx.defer("pbs node apt update-database", "refreshes the package index on a managed PBS remote's node; covered by unit tests",
              "pmx pdm pbs node apt update-database pmx-cli-remote pmx-cli-node")
