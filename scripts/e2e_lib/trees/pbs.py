"""pbs: Proxmox Backup Server command group (opt-in read-only happy path).

Unlike every other tree, this one targets a *different product*: the checks
need a context whose `product` is `pbs` and a reachable Proxmox Backup Server.
The sweep therefore treats the tree as opt-in — the runner hands it the
`--pbs-context` / `$PVE_E2E_PBS_CONTEXT` context instead of the sweep context
(empty when not given), and `run` records a single SKIP and returns when the
opt-in is absent, the context is not a `product: pbs` context, or the server
does not answer `pbs ping`.

Every check in this module sits lexically inside an `if` on purpose: the whole
tree is conditional on the opt-in, so the coverage matrix must classify its
leaves as prerequisite-gated (◑), never unconditional (✓). Keep new checks
nested (the section helpers wrap their bodies in `if ctx.env.context:`) or the
generated matrix will overstate the guarantee.

There is no PBS mutate phase: every mutating verb is recorded as deferred with
`live_covered=False` and is covered by unit tests instead.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx

NAME = "pbs"
DESCRIPTION = "Proxmox Backup Server admin (opt-in: --pbs-context)"

# The runner swaps this tree's Env.context for the --pbs-context value (and
# clears the discovered PVE node, which is meaningless here).
PRODUCT = "pbs"


def _pick(rows: object, *keys: str) -> str | None:
    """First non-empty value of any of `keys` across a list of JSON rows."""
    if not isinstance(rows, list):
        return None
    for row in rows:
        if isinstance(row, dict):
            for k in keys:
                v = row.get(k)
                if v not in (None, ""):
                    return str(v)
    return None


def _rows(res: CmdResult) -> list:
    try:
        data = res.json()
    except ValueError:
        return []
    return data if isinstance(data, list) else []


def _tail(res: CmdResult) -> str:
    return (res.stderr.strip() or res.stdout.strip())[:80]


def is_list(res: CmdResult) -> str | None:
    return None if isinstance(res.json(), list) else "expected a JSON array"


def run(ctx: Ctx) -> None:
    if not ctx.env.context:
        ctx.skip("pbs sweep", "opt-in: pass --pbs-context or set PVE_E2E_PBS_CONTEXT")
        return
    ok, why = _gate(ctx)
    if not ok:
        ctx.skip("pbs sweep", why)
        return

    _core(ctx)
    _jobs(ctx)
    _access(ctx)
    _notification(ctx)
    _acme(ctx)
    _metrics(ctx)
    _node(ctx)
    _tape(ctx)
    _defers(ctx)


def _gate(ctx: Ctx) -> tuple[bool, str]:
    """Opt-in preconditions: configured `product: pbs` context + reachable server."""
    ls = ctx.run("context", "ls", with_context=False)
    entry = None
    if ls.rc == 0:
        try:
            entry = next((c for c in ls.json() if isinstance(c, dict)
                          and c.get("name") == ctx.env.context), None)
        except ValueError:
            entry = None
    if entry is None:
        return False, f"pbs context {ctx.env.context!r} not in config"
    if entry.get("product") != "pbs":
        return False, f"context {ctx.env.context!r} is not a product: pbs context"
    ping = ctx.run("pbs", "ping")
    if ping.rc != 0:
        return False, f"PBS server unreachable: {_tail(ping)}"
    return True, ""


# --------------------------------------------------------------------------- #
# datastores, snapshots, groups, gc, prune preview                             #
# --------------------------------------------------------------------------- #
def _core(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate — keeps every check conditional (◑)
        ctx.check("ping", "pbs", "ping")
        ctx.check_formats("version", "pbs", "version")
        # raw read passthrough against a known-good path
        ctx.check("api get /version", "pbs", "api", "get", "/version", fmt="")

        ds = ctx.check("datastore ls", "pbs", "datastore", "ls", validate=is_list)
        ctx.check("datastore usage", "pbs", "datastore", "usage", validate=is_list)
        ctx.check("status datastore-usage", "pbs", "status", "datastore-usage",
                  validate=is_list)
        ctx.check("gc ls", "pbs", "gc", "ls", validate=is_list)
        ctx.check("encryption-key ls", "pbs", "encryption-key", "ls", validate=is_list)

        store = _pick(_rows(ds), "name", "store")
        if store is None:
            for miss in ("datastore show", "datastore status", "datastore rrd",
                         "gc status", "group ls", "group notes", "snapshot ls",
                         "snapshot show", "snapshot files", "snapshot notes",
                         "prune simulate"):
                ctx.skip(miss, "no datastore configured")
            return

        ctx.check("datastore show", "pbs", "datastore", "show", store)
        # status/rrd need a healthy, already-collected datastore; probe first so
        # a freshly created store (no RRD yet) skips instead of failing.
        st_probe = ctx.run("pbs", "datastore", "status", store)
        if st_probe.rc == 0:
            ctx.check("datastore status", "pbs", "datastore", "status", store)
        else:
            ctx.skip("datastore status", f"datastore status unavailable: {_tail(st_probe)}")
        rrd_probe = ctx.run("pbs", "datastore", "rrd", store, "--timeframe", "day")
        if rrd_probe.rc == 0:
            ctx.check("datastore rrd", "pbs", "datastore", "rrd", store,
                      "--timeframe", "day")
        else:
            ctx.skip("datastore rrd", f"no RRD data yet: {_tail(rrd_probe)}")
        ctx.check("gc status", "pbs", "gc", "status", "--store", store)

        groups = ctx.check("group ls", "pbs", "group", "ls", "--store", store,
                           validate=is_list)
        grp = None
        rows = _rows(groups)
        row = next((r for r in rows if isinstance(r, dict)
                    and r.get("backup-type") and r.get("backup-id")), None)
        if row:
            grp = f"{row['backup-type']}/{row['backup-id']}"
        if grp:
            notes_probe = ctx.run("pbs", "group", "notes", grp, "--store", store, fmt="")
            if notes_probe.rc == 0:
                ctx.check("group notes", "pbs", "group", "notes", grp,
                          "--store", store, fmt="")
            else:
                ctx.skip("group notes", f"group notes unreadable: {_tail(notes_probe)}")
            # prune simulate is a hard dry-run (the CLI forces dry-run); it only
            # reports the keep/remove decision and never deletes.
            ctx.check("prune simulate", "pbs", "prune", "simulate", grp,
                      "--store", store, "--keep-last", "1", validate=is_list)
        else:
            ctx.skip("group notes", "no backup group in datastore")
            ctx.skip("prune simulate", "no backup group in datastore")

        snaps = ctx.check("snapshot ls", "pbs", "snapshot", "ls", "--store", store,
                          validate=is_list)
        snap = None
        srow = next((r for r in _rows(snaps) if isinstance(r, dict)
                     and r.get("backup-type") and r.get("backup-id")
                     and r.get("backup-time") is not None), None)
        if srow:
            snap = f"{srow['backup-type']}/{srow['backup-id']}/{srow['backup-time']}"
        if snap:
            show_probe = ctx.run("pbs", "snapshot", "show", snap, "--store", store)
            if show_probe.rc == 0:
                ctx.check("snapshot show", "pbs", "snapshot", "show", snap,
                          "--store", store)
                ctx.check("snapshot files", "pbs", "snapshot", "files", snap,
                          "--store", store, validate=is_list)
            else:
                ctx.skip("snapshot show", f"snapshot lookup failed: {_tail(show_probe)}")
                ctx.skip("snapshot files", "snapshot lookup failed")
            sn_probe = ctx.run("pbs", "snapshot", "notes", snap, "--store", store, fmt="")
            if sn_probe.rc == 0:
                ctx.check("snapshot notes", "pbs", "snapshot", "notes", snap,
                          "--store", store, fmt="")
            else:
                ctx.skip("snapshot notes", f"snapshot notes unreadable: {_tail(sn_probe)}")
        else:
            for miss in ("snapshot show", "snapshot files", "snapshot notes"):
                ctx.skip(miss, "no snapshot in datastore")


# --------------------------------------------------------------------------- #
# sync / verify / prune jobs, remotes, traffic control                         #
# --------------------------------------------------------------------------- #
def _jobs(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        ctx.check("sync ls", "pbs", "sync", "ls", validate=is_list)
        sjobs = ctx.check("sync job ls", "pbs", "sync", "job", "ls", validate=is_list)
        sid = _pick(_rows(sjobs), "id")
        if sid:
            ctx.check("sync job show", "pbs", "sync", "job", "show", sid)
        else:
            ctx.skip("sync job show", "no sync job configured")

        vjobs = ctx.check("verify job ls", "pbs", "verify", "job", "ls", validate=is_list)
        vid = _pick(_rows(vjobs), "id")
        if vid:
            ctx.check("verify job show", "pbs", "verify", "job", "show", vid)
        else:
            ctx.skip("verify job show", "no verify job configured")

        pjobs = ctx.check("prune job ls", "pbs", "prune", "job", "ls", validate=is_list)
        pid = _pick(_rows(pjobs), "id")
        if pid:
            ctx.check("prune job show", "pbs", "prune", "job", "show", pid)
        else:
            ctx.skip("prune job show", "no prune job configured")

        remotes = ctx.check("remote ls", "pbs", "remote", "ls", validate=is_list)
        rname = _pick(_rows(remotes), "name")
        if rname:
            ctx.check("remote show", "pbs", "remote", "show", rname)
            # scanning needs the remote to be reachable from the PBS host;
            # probe first and skip gracefully when it is not.
            scan_probe = ctx.run("pbs", "remote", "scan", "ls", rname)
            if scan_probe.rc == 0:
                scan = ctx.check("remote scan ls", "pbs", "remote", "scan", "ls", rname,
                                 validate=is_list)
                rstore = _pick(_rows(scan), "store", "name")
                if rstore:
                    ctx.check("remote scan groups", "pbs", "remote", "scan", "groups",
                              rname, rstore, validate=is_list)
                    ctx.check("remote scan namespaces", "pbs", "remote", "scan",
                              "namespaces", rname, rstore, validate=is_list)
                else:
                    ctx.skip("remote scan groups", "remote has no datastore")
                    ctx.skip("remote scan namespaces", "remote has no datastore")
            else:
                for miss in ("remote scan ls", "remote scan groups",
                             "remote scan namespaces"):
                    ctx.skip(miss, f"remote not reachable: {_tail(scan_probe)}")
        else:
            for miss in ("remote show", "remote scan ls", "remote scan groups",
                         "remote scan namespaces"):
                ctx.skip(miss, "no remote configured")

        traffic = ctx.check("traffic ls", "pbs", "traffic", "ls", validate=is_list)
        ctx.check("traffic current", "pbs", "traffic", "current", validate=is_list)
        tname = _pick(_rows(traffic), "name")
        if tname:
            ctx.check("traffic show", "pbs", "traffic", "show", tname)
        else:
            ctx.skip("traffic show", "no traffic-control rule configured")


# --------------------------------------------------------------------------- #
# users, tokens, acl, roles, permissions, realms                               #
# --------------------------------------------------------------------------- #
def _access(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        users = ctx.check("user ls", "pbs", "user", "ls", validate=is_list)
        uid = _pick(_rows(users), "userid")
        if uid:
            ctx.check("user show", "pbs", "user", "show", uid)
            tokens = ctx.check("user token ls", "pbs", "user", "token", "ls", uid,
                               validate=is_list)
            tok = _pick(_rows(tokens), "token-name", "tokenid", "name")
            if tok:
                ctx.check("user token show", "pbs", "user", "token", "show", uid, tok)
            else:
                ctx.skip("user token show", f"user {uid} has no API token")
        else:
            for miss in ("user show", "user token ls", "user token show"):
                ctx.skip(miss, "no user found")

        ctx.check("acl ls", "pbs", "acl", "ls", validate=is_list)
        ctx.check("role ls", "pbs", "role", "ls", validate=is_list)
        ctx.check("permission ls", "pbs", "permission", "ls")

        ctx.check("realm ls", "pbs", "realm", "ls", validate=is_list)
        ctx.check("realm pam show", "pbs", "realm", "pam", "show")
        ctx.check("realm pbs show", "pbs", "realm", "pbs", "show")

        ad = ctx.check("realm ad ls", "pbs", "realm", "ad", "ls", validate=is_list)
        adr = _pick(_rows(ad), "realm")
        if adr:
            ctx.check("realm ad show", "pbs", "realm", "ad", "show", adr)
        else:
            ctx.skip("realm ad show", "no AD realm configured")
        ldap = ctx.check("realm ldap ls", "pbs", "realm", "ldap", "ls", validate=is_list)
        ldr = _pick(_rows(ldap), "realm")
        if ldr:
            ctx.check("realm ldap show", "pbs", "realm", "ldap", "show", ldr)
        else:
            ctx.skip("realm ldap show", "no LDAP realm configured")
        oid = ctx.check("realm openid ls", "pbs", "realm", "openid", "ls",
                        validate=is_list)
        oir = _pick(_rows(oid), "realm")
        if oir:
            ctx.check("realm openid show", "pbs", "realm", "openid", "show", oir)
        else:
            ctx.skip("realm openid show", "no OpenID realm configured")


# --------------------------------------------------------------------------- #
# notification endpoints, matchers, targets                                    #
# --------------------------------------------------------------------------- #
def _notification(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        gotify = ctx.check("notification endpoint gotify ls",
                           "pbs", "notification", "endpoint", "gotify", "ls",
                           validate=is_list)
        g = _pick(_rows(gotify), "name")
        if g:
            ctx.check("notification endpoint gotify show",
                      "pbs", "notification", "endpoint", "gotify", "show", g)
        else:
            ctx.skip("notification endpoint gotify show", "no gotify endpoint")

        sendmail = ctx.check("notification endpoint sendmail ls",
                             "pbs", "notification", "endpoint", "sendmail", "ls",
                             validate=is_list)
        s = _pick(_rows(sendmail), "name")
        if s:
            ctx.check("notification endpoint sendmail show",
                      "pbs", "notification", "endpoint", "sendmail", "show", s)
        else:
            ctx.skip("notification endpoint sendmail show", "no sendmail endpoint")

        smtp = ctx.check("notification endpoint smtp ls",
                         "pbs", "notification", "endpoint", "smtp", "ls",
                         validate=is_list)
        m = _pick(_rows(smtp), "name")
        if m:
            ctx.check("notification endpoint smtp show",
                      "pbs", "notification", "endpoint", "smtp", "show", m)
        else:
            ctx.skip("notification endpoint smtp show", "no smtp endpoint")

        webhook = ctx.check("notification endpoint webhook ls",
                            "pbs", "notification", "endpoint", "webhook", "ls",
                            validate=is_list)
        w = _pick(_rows(webhook), "name")
        if w:
            ctx.check("notification endpoint webhook show",
                      "pbs", "notification", "endpoint", "webhook", "show", w)
        else:
            ctx.skip("notification endpoint webhook show", "no webhook endpoint")

        matchers = ctx.check("notification matcher ls",
                             "pbs", "notification", "matcher", "ls", validate=is_list)
        mt = _pick(_rows(matchers), "name")
        if mt:
            ctx.check("notification matcher show",
                      "pbs", "notification", "matcher", "show", mt)
        else:
            ctx.skip("notification matcher show", "no matcher configured")
        ctx.check("notification matcher fields ls",
                  "pbs", "notification", "matcher", "fields", "ls", validate=is_list)
        ctx.check("notification matcher field-values ls",
                  "pbs", "notification", "matcher", "field-values", "ls",
                  validate=is_list)
        ctx.check("notification target ls",
                  "pbs", "notification", "target", "ls", validate=is_list)


# --------------------------------------------------------------------------- #
# acme                                                                         #
# --------------------------------------------------------------------------- #
def _acme(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        accounts = ctx.check("acme account ls", "pbs", "acme", "account", "ls",
                             validate=is_list)
        acc = _pick(_rows(accounts), "name")
        if acc:
            # account show refreshes the registration from the CA; probe so a
            # stale/unreachable CA skips instead of failing.
            acc_probe = ctx.run("pbs", "acme", "account", "show", acc)
            if acc_probe.rc == 0:
                ctx.check("acme account show", "pbs", "acme", "account", "show", acc)
            else:
                ctx.skip("acme account show", f"CA not reachable: {_tail(acc_probe)}")
        else:
            ctx.skip("acme account show", "no ACME account configured")

        plugins = ctx.check("acme plugin ls", "pbs", "acme", "plugin", "ls",
                            validate=is_list)
        plug = _pick(_rows(plugins), "plugin", "id", "name")
        if plug:
            ctx.check("acme plugin show", "pbs", "acme", "plugin", "show", plug)
        else:
            ctx.skip("acme plugin show", "no ACME plugin configured")

        ctx.check("acme challenge-schema ls", "pbs", "acme", "challenge-schema", "ls",
                  validate=is_list)
        ctx.check("acme directories ls", "pbs", "acme", "directories", "ls",
                  validate=is_list)
        # tos show queries the ACME directory itself, which needs egress from
        # the PBS host; probe first.
        tos_probe = ctx.run("pbs", "acme", "tos", "show", fmt="")
        if tos_probe.rc == 0:
            ctx.check("acme tos show", "pbs", "acme", "tos", "show", fmt="")
        else:
            ctx.skip("acme tos show", f"ACME directory not reachable: {_tail(tos_probe)}")


# --------------------------------------------------------------------------- #
# metrics                                                                      #
# --------------------------------------------------------------------------- #
def _metrics(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        ctx.check("metrics data", "pbs", "metrics", "data")
        http = ctx.check("metrics influxdb-http ls",
                         "pbs", "metrics", "influxdb-http", "ls", validate=is_list)
        h = _pick(_rows(http), "name")
        if h:
            ctx.check("metrics influxdb-http show",
                      "pbs", "metrics", "influxdb-http", "show", h)
        else:
            ctx.skip("metrics influxdb-http show", "no influxdb-http server configured")
        udp = ctx.check("metrics influxdb-udp ls",
                        "pbs", "metrics", "influxdb-udp", "ls", validate=is_list)
        u = _pick(_rows(udp), "name")
        if u:
            ctx.check("metrics influxdb-udp show",
                      "pbs", "metrics", "influxdb-udp", "show", u)
        else:
            ctx.skip("metrics influxdb-udp show", "no influxdb-udp server configured")


# --------------------------------------------------------------------------- #
# node (single host; every verb defaults --node localhost)                     #
# --------------------------------------------------------------------------- #
def _node(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        ctx.check("node ls", "pbs", "node", "ls", validate=is_list)
        ctx.check("node status", "pbs", "node", "status")
        ctx.check("node report", "pbs", "node", "report", fmt="plain")
        ctx.check("node syslog", "pbs", "node", "syslog")
        ctx.check("node journal", "pbs", "node", "journal", fmt="plain")
        ctx.check("node identity", "pbs", "node", "identity", fmt="plain")
        ctx.check("node rrd", "pbs", "node", "rrd", "--timeframe", "day")
        ctx.check("node dns show", "pbs", "node", "dns", "show")
        ctx.check("node time show", "pbs", "node", "time", "show")
        ctx.check("node config show", "pbs", "node", "config", "show")
        # subscription info may be an error on hosts with no subscription set.
        sub_probe = ctx.run("pbs", "node", "subscription", "show")
        if sub_probe.rc == 0:
            ctx.check("node subscription show", "pbs", "node", "subscription", "show")
        else:
            ctx.skip("node subscription show",
                     f"no subscription info: {_tail(sub_probe)}")
        ctx.check("node certificates info", "pbs", "node", "certificates", "info",
                  validate=is_list)

        tasks = ctx.check("node tasks ls", "pbs", "node", "tasks", "ls",
                          validate=is_list)
        upid = _pick(_rows(tasks), "upid")
        if upid:
            ctx.check("node tasks show", "pbs", "node", "tasks", "show", upid)
            ctx.check("node tasks log", "pbs", "node", "tasks", "log", upid,
                      "--limit", "5")
        else:
            ctx.skip("node tasks show", "no task in history")
            ctx.skip("node tasks log", "no task in history")

        services = ctx.check("node services ls", "pbs", "node", "services", "ls",
                             validate=is_list)
        svc = _pick(_rows(services), "service", "name")
        if svc:
            ctx.check("node services show", "pbs", "node", "services", "show", svc)
            ctx.check("node services state", "pbs", "node", "services", "state", svc)
        else:
            ctx.skip("node services show", "no service listed")
            ctx.skip("node services state", "no service listed")

        apt = ctx.check("node apt ls", "pbs", "node", "apt", "ls", validate=is_list)
        ctx.check("node apt repositories", "pbs", "node", "apt", "repositories")
        ctx.check("node apt versions", "pbs", "node", "apt", "versions",
                  validate=is_list)
        pkg = _pick(_rows(apt), "Package", "package", "Name", "name")
        if pkg:
            chl_probe = ctx.run("pbs", "node", "apt", "changelog", "--name", pkg,
                                fmt="plain")
            if chl_probe.rc == 0:
                ctx.check("node apt changelog", "pbs", "node", "apt", "changelog",
                          "--name", pkg, fmt="plain")
            else:
                ctx.skip("node apt changelog", f"changelog unavailable: {_tail(chl_probe)}")
        else:
            ctx.skip("node apt changelog", "no pending package update to query")

        disks = ctx.check("node disks ls", "pbs", "node", "disks", "ls",
                          validate=is_list)
        disk = _pick(_rows(disks), "name")
        if disk:
            smart_probe = ctx.run("pbs", "node", "disks", "smart", "--disk", disk)
            if smart_probe.rc == 0:
                ctx.check("node disks smart", "pbs", "node", "disks", "smart",
                          "--disk", disk)
            else:
                ctx.skip("node disks smart",
                         f"SMART not readable for {disk}: {_tail(smart_probe)}")
        else:
            ctx.skip("node disks smart", "no disk listed")
        ctx.check("node disks directory ls", "pbs", "node", "disks", "directory", "ls",
                  validate=is_list)
        zfs = ctx.check("node disks zfs ls", "pbs", "node", "disks", "zfs", "ls",
                        validate=is_list)
        zp = _pick(_rows(zfs), "name")
        if zp:
            ctx.check("node disks zfs show", "pbs", "node", "disks", "zfs", "show", zp)
        else:
            ctx.skip("node disks zfs show", "no zpool on the host")

        nets = ctx.check("node network ls", "pbs", "node", "network", "ls",
                         validate=is_list)
        iface = _pick(_rows(nets), "iface", "name")
        if iface:
            ctx.check("node network show", "pbs", "node", "network", "show", iface)
        else:
            ctx.skip("node network show", "no network interface listed")


# --------------------------------------------------------------------------- #
# tape                                                                         #
# --------------------------------------------------------------------------- #
def _tape(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        drives = ctx.check("tape drive ls", "pbs", "tape", "drive", "ls",
                           validate=is_list)
        ctx.check("tape drive scan", "pbs", "tape", "drive", "scan", validate=is_list)
        changers = ctx.check("tape changer ls", "pbs", "tape", "changer", "ls",
                             validate=is_list)
        ctx.check("tape changer scan", "pbs", "tape", "changer", "scan",
                  validate=is_list)
        ctx.check("tape media ls", "pbs", "tape", "media", "ls", validate=is_list)
        ctx.check("tape media content", "pbs", "tape", "media", "content",
                  validate=is_list)
        ctx.check("tape media sets", "pbs", "tape", "media", "sets", validate=is_list)
        ctx.check("tape job status", "pbs", "tape", "job", "status", validate=is_list)

        pools = ctx.check("tape pool ls", "pbs", "tape", "pool", "ls", validate=is_list)
        pool = _pick(_rows(pools), "name")
        if pool:
            ctx.check("tape pool show", "pbs", "tape", "pool", "show", pool)
        else:
            ctx.skip("tape pool show", "no media pool configured")

        keys = ctx.check("tape key ls", "pbs", "tape", "key", "ls", validate=is_list)
        fp = _pick(_rows(keys), "fingerprint")
        if fp:
            ctx.check("tape key show", "pbs", "tape", "key", "show", fp)
        else:
            ctx.skip("tape key show", "no tape encryption key configured")

        jobs = ctx.check("tape job ls", "pbs", "tape", "job", "ls", validate=is_list)
        jid = _pick(_rows(jobs), "id")
        if jid:
            ctx.check("tape job show", "pbs", "tape", "job", "show", jid)
        else:
            ctx.skip("tape job show", "no tape backup job configured")

        drv = _pick(_rows(drives), "name")
        if drv:
            ctx.check("tape drive show", "pbs", "tape", "drive", "show", drv)
            # Hardware reads: each needs a working drive (and mostly a loaded
            # tape). Probe each and skip when the hardware does not cooperate.
            st = ctx.run("pbs", "tape", "drive", "status", drv)
            if st.rc == 0:
                ctx.check("tape drive status", "pbs", "tape", "drive", "status", drv)
            else:
                ctx.skip("tape drive status", f"drive not ready: {_tail(st)}")
            cm = ctx.run("pbs", "tape", "drive", "cartridge-memory", drv)
            if cm.rc == 0:
                ctx.check("tape drive cartridge-memory",
                          "pbs", "tape", "drive", "cartridge-memory", drv)
            else:
                ctx.skip("tape drive cartridge-memory", f"no loaded media: {_tail(cm)}")
            vs = ctx.run("pbs", "tape", "drive", "volume-statistics", drv)
            if vs.rc == 0:
                ctx.check("tape drive volume-statistics",
                          "pbs", "tape", "drive", "volume-statistics", drv)
            else:
                ctx.skip("tape drive volume-statistics", f"no loaded media: {_tail(vs)}")
            rl = ctx.run("pbs", "tape", "drive", "read-label", drv)
            if rl.rc == 0:
                ctx.check("tape drive read-label",
                          "pbs", "tape", "drive", "read-label", drv)
            else:
                ctx.skip("tape drive read-label", f"no loaded media: {_tail(rl)}")
        else:
            for miss in ("tape drive show", "tape drive status",
                         "tape drive cartridge-memory", "tape drive volume-statistics",
                         "tape drive read-label"):
                ctx.skip(miss, "no tape drive configured")

        chg = _pick(_rows(changers), "name")
        if chg:
            ctx.check("tape changer show", "pbs", "tape", "changer", "show", chg)
            # --cache reads the last known robot status without moving hardware.
            cs = ctx.run("pbs", "tape", "changer", "status", chg, "--cache")
            if cs.rc == 0:
                ctx.check("tape changer status", "pbs", "tape", "changer", "status",
                          chg, "--cache")
            else:
                ctx.skip("tape changer status", f"changer not ready: {_tail(cs)}")
        else:
            ctx.skip("tape changer show", "no tape changer configured")
            ctx.skip("tape changer status", "no tape changer configured")


# --------------------------------------------------------------------------- #
# deferred (mutating / hardware-moving) verbs — no PBS mutate phase exists,    #
# so every one is live_covered=False and covered by unit tests instead.        #
# --------------------------------------------------------------------------- #
def _defers(ctx: Ctx) -> None:
    # raw write passthrough
    ctx.defer("api post", "raw write passthrough against the live PBS API — not automatable safely; covered by unit tests",
              "pve pbs api post /pull --data store=main")
    ctx.defer("api put", "raw write passthrough against the live PBS API — not automatable safely; covered by unit tests",
              "pve pbs api put /config/datastore/main --data gc-schedule=daily")
    ctx.defer("api delete", "raw write passthrough against the live PBS API — not automatable safely; covered by unit tests",
              "pve pbs api delete /config/datastore/pve-cli-ds")

    # datastore + data-touching tasks
    ctx.defer("datastore create", "creates a datastore (allocates a chunk store on disk); covered by unit tests",
              "pve pbs datastore create pve-cli-ds --path /tmp/pve-cli-ds")
    ctx.defer("datastore update", "modifies datastore configuration; covered by unit tests",
              "pve pbs datastore update pve-cli-ds --gc-schedule daily")
    ctx.defer("datastore delete", "removes a datastore definition; covered by unit tests",
              "pve pbs datastore delete pve-cli-ds --yes")
    ctx.defer("gc run", "runs garbage collection, which deletes unreferenced chunks; covered by unit tests",
              "pve pbs gc run --store main")
    ctx.defer("prune run", "prunes snapshots by retention policy (deletes data); covered by unit tests",
              "pve pbs prune run ct/100 --store main --keep-last 1")
    ctx.defer("prune job run", "runs a configured prune job (deletes data); covered by unit tests",
              "pve pbs prune job run <id>")
    ctx.defer("prune job add", "creates a prune job; covered by unit tests",
              "pve pbs prune job add pve-cli-prune --store main --keep-last 3 --schedule daily")
    ctx.defer("prune job update", "modifies a prune job; covered by unit tests",
              "pve pbs prune job update pve-cli-prune --keep-last 5")
    ctx.defer("prune job delete", "removes a prune job; covered by unit tests",
              "pve pbs prune job delete pve-cli-prune --yes")
    ctx.defer("verify run", "runs a datastore verification task (long, IO-heavy); covered by unit tests",
              "pve pbs verify run --store main")
    ctx.defer("verify job run", "runs a configured verify job (long, IO-heavy); covered by unit tests",
              "pve pbs verify job run <id>")
    ctx.defer("verify job add", "creates a verify job; covered by unit tests",
              "pve pbs verify job add pve-cli-verify --store main --schedule daily")
    ctx.defer("verify job update", "modifies a verify job; covered by unit tests",
              "pve pbs verify job update pve-cli-verify --schedule weekly")
    ctx.defer("verify job delete", "removes a verify job; covered by unit tests",
              "pve pbs verify job delete pve-cli-verify --yes")
    ctx.defer("group delete", "deletes an entire backup group and all its snapshots; covered by unit tests",
              "pve pbs group delete ct/100 --store main --yes")
    ctx.defer("snapshot delete", "deletes a backup snapshot; covered by unit tests",
              "pve pbs snapshot delete ct/100/<time> --store main --yes")
    ctx.defer("snapshot protect", "sets the protected flag on a snapshot; covered by unit tests",
              "pve pbs snapshot protect ct/100/<time> --store main")
    ctx.defer("snapshot unprotect", "clears the protected flag on a snapshot; covered by unit tests",
              "pve pbs snapshot unprotect ct/100/<time> --store main")
    ctx.defer("encryption-key add", "creates a datastore encryption key; covered by unit tests",
              "pve pbs encryption-key add pve-cli-key --kdf none")
    ctx.defer("encryption-key delete", "removes a datastore encryption key; covered by unit tests",
              "pve pbs encryption-key delete pve-cli-key --yes")
    ctx.defer("encryption-key toggle-archive", "flips the key's archive state on every call — not automatable idempotently; covered by unit tests",
              "pve pbs encryption-key toggle-archive pve-cli-key")

    # sync / remotes / traffic
    ctx.defer("sync pull", "transfers backup data into a local datastore; covered by unit tests",
              "pve pbs sync pull --store main --remote-store other")
    ctx.defer("sync push", "transfers backup data to a remote; covered by unit tests",
              "pve pbs sync push --store main --remote r1 --remote-store other")
    ctx.defer("sync job run", "runs a configured sync job (transfers data); covered by unit tests",
              "pve pbs sync job run <id>")
    ctx.defer("sync job add", "creates a sync job; covered by unit tests",
              "pve pbs sync job add pve-cli-sync --store main --remote r1 --remote-store other")
    ctx.defer("sync job update", "modifies a sync job; covered by unit tests",
              "pve pbs sync job update pve-cli-sync --schedule hourly")
    ctx.defer("sync job delete", "removes a sync job; covered by unit tests",
              "pve pbs sync job delete pve-cli-sync --yes")
    ctx.defer("remote add", "adds a remote PBS connection (stores credentials); covered by unit tests",
              "pve pbs remote add pve-cli-remote --host pbs2.example --auth-id sync@pbs --password ...")
    ctx.defer("remote update", "modifies a remote PBS connection; covered by unit tests",
              "pve pbs remote update pve-cli-remote --port 8007")
    ctx.defer("remote delete", "removes a remote PBS connection; covered by unit tests",
              "pve pbs remote delete pve-cli-remote --yes")
    ctx.defer("traffic add", "creates a traffic-control rule; covered by unit tests",
              "pve pbs traffic add pve-cli-tc --network 192.0.2.0/24 --rate-in 10MB")
    ctx.defer("traffic update", "modifies a traffic-control rule; covered by unit tests",
              "pve pbs traffic update pve-cli-tc --rate-in 20MB")
    ctx.defer("traffic delete", "removes a traffic-control rule; covered by unit tests",
              "pve pbs traffic delete pve-cli-tc --yes")

    # access control
    ctx.defer("acl update", "modifies the access control list; covered by unit tests",
              "pve pbs acl update /datastore/main DatastoreAudit --auth-id audit@pbs")
    ctx.defer("user add", "creates a user; covered by unit tests",
              "pve pbs user add pve-cli-user@pbs --password ...")
    ctx.defer("user update", "modifies a user; covered by unit tests",
              "pve pbs user update pve-cli-user@pbs --comment e2e")
    ctx.defer("user delete", "removes a user; covered by unit tests",
              "pve pbs user delete pve-cli-user@pbs --yes")
    ctx.defer("user passwd", "prompts for the new password interactively; covered by unit tests",
              "pve pbs user passwd pve-cli-user@pbs")
    ctx.defer("user unlock-tfa", "resets a user's second factors; covered by unit tests",
              "pve pbs user unlock-tfa pve-cli-user@pbs")
    ctx.defer("user token add", "creates a credential and prints a once-only secret — out of scope for the automated sweep; covered by unit tests",
              "pve pbs user token add pve-cli-user@pbs e2e")
    ctx.defer("user token update", "modifies an API token; covered by unit tests",
              "pve pbs user token update pve-cli-user@pbs e2e --comment e2e")
    ctx.defer("user token delete", "removes an API token; covered by unit tests",
              "pve pbs user token delete pve-cli-user@pbs e2e --yes")
    ctx.defer("realm ad add", "adds an AD authentication realm; covered by unit tests",
              "pve pbs realm ad add pve-cli-ad --server1 dc.example --base-dn dc=example")
    ctx.defer("realm ad update", "modifies an AD realm; covered by unit tests",
              "pve pbs realm ad update pve-cli-ad --comment e2e")
    ctx.defer("realm ad delete", "removes an AD realm; covered by unit tests",
              "pve pbs realm ad delete pve-cli-ad --yes")
    ctx.defer("realm ldap add", "adds an LDAP authentication realm; covered by unit tests",
              "pve pbs realm ldap add pve-cli-ldap --server1 ldap.example --base-dn dc=example --user-attr uid")
    ctx.defer("realm ldap update", "modifies an LDAP realm; covered by unit tests",
              "pve pbs realm ldap update pve-cli-ldap --comment e2e")
    ctx.defer("realm ldap delete", "removes an LDAP realm; covered by unit tests",
              "pve pbs realm ldap delete pve-cli-ldap --yes")
    ctx.defer("realm openid add", "adds an OpenID authentication realm; covered by unit tests",
              "pve pbs realm openid add pve-cli-oidc --issuer-url https://idp.example --client-id pbs")
    ctx.defer("realm openid update", "modifies an OpenID realm; covered by unit tests",
              "pve pbs realm openid update pve-cli-oidc --comment e2e")
    ctx.defer("realm openid delete", "removes an OpenID realm; covered by unit tests",
              "pve pbs realm openid delete pve-cli-oidc --yes")
    ctx.defer("realm pam update", "modifies the built-in PAM realm; covered by unit tests",
              "pve pbs realm pam update --comment e2e")
    ctx.defer("realm pbs update", "modifies the built-in PBS realm; covered by unit tests",
              "pve pbs realm pbs update --comment e2e")
    ctx.defer("realm sync", "runs a realm sync task that can create or update users; covered by unit tests",
              "pve pbs realm sync pve-cli-ldap")

    # notification config (literal per-kind defers: the coverage matrix reads
    # the command strings statically, so no f-string/loop generation here)
    ctx.defer("notification endpoint gotify add", "creates a gotify notification endpoint; covered by unit tests",
              "pve pbs notification endpoint gotify add pve-cli-gotify --server ... --token ...")
    ctx.defer("notification endpoint gotify update", "modifies a gotify notification endpoint; covered by unit tests",
              "pve pbs notification endpoint gotify update pve-cli-gotify --comment e2e")
    ctx.defer("notification endpoint gotify delete", "removes a gotify notification endpoint; covered by unit tests",
              "pve pbs notification endpoint gotify delete pve-cli-gotify --yes")
    ctx.defer("notification endpoint sendmail add", "creates a sendmail notification endpoint; covered by unit tests",
              "pve pbs notification endpoint sendmail add pve-cli-sendmail --mailto ops@example")
    ctx.defer("notification endpoint sendmail update", "modifies a sendmail notification endpoint; covered by unit tests",
              "pve pbs notification endpoint sendmail update pve-cli-sendmail --comment e2e")
    ctx.defer("notification endpoint sendmail delete", "removes a sendmail notification endpoint; covered by unit tests",
              "pve pbs notification endpoint sendmail delete pve-cli-sendmail --yes")
    ctx.defer("notification endpoint smtp add", "creates an smtp notification endpoint; covered by unit tests",
              "pve pbs notification endpoint smtp add pve-cli-smtp --server smtp.example --from-address pbs@example --mailto ops@example")
    ctx.defer("notification endpoint smtp update", "modifies an smtp notification endpoint; covered by unit tests",
              "pve pbs notification endpoint smtp update pve-cli-smtp --comment e2e")
    ctx.defer("notification endpoint smtp delete", "removes an smtp notification endpoint; covered by unit tests",
              "pve pbs notification endpoint smtp delete pve-cli-smtp --yes")
    ctx.defer("notification endpoint webhook add", "creates a webhook notification endpoint; covered by unit tests",
              "pve pbs notification endpoint webhook add pve-cli-webhook --url https://hooks.example --method post")
    ctx.defer("notification endpoint webhook update", "modifies a webhook notification endpoint; covered by unit tests",
              "pve pbs notification endpoint webhook update pve-cli-webhook --comment e2e")
    ctx.defer("notification endpoint webhook delete", "removes a webhook notification endpoint; covered by unit tests",
              "pve pbs notification endpoint webhook delete pve-cli-webhook --yes")
    ctx.defer("notification matcher add", "creates a notification matcher; covered by unit tests",
              "pve pbs notification matcher add pve-cli-matcher --target mail-to-root")
    ctx.defer("notification matcher update", "modifies a notification matcher; covered by unit tests",
              "pve pbs notification matcher update pve-cli-matcher --mode all")
    ctx.defer("notification matcher delete", "removes a notification matcher; covered by unit tests",
              "pve pbs notification matcher delete pve-cli-matcher --yes")
    ctx.defer("notification target test", "sends a real notification through the live target — out of scope for the automated sweep; covered by unit tests",
              "pve pbs notification target test mail-to-root")

    # acme config
    ctx.defer("acme account add", "registers an account with a live certificate authority; covered by unit tests",
              "pve pbs acme account add pve-cli-acme --contact ops@example")
    ctx.defer("acme account update", "updates the registration at the certificate authority; covered by unit tests",
              "pve pbs acme account update pve-cli-acme --contact ops@example")
    ctx.defer("acme account delete", "deactivates the account at the certificate authority; covered by unit tests",
              "pve pbs acme account delete pve-cli-acme --yes")
    ctx.defer("acme plugin add", "creates an ACME challenge plugin (stores API credentials); covered by unit tests",
              "pve pbs acme plugin add pve-cli-dns --type dns --api cloudflare")
    ctx.defer("acme plugin update", "modifies an ACME challenge plugin; covered by unit tests",
              "pve pbs acme plugin update pve-cli-dns --disable")
    ctx.defer("acme plugin delete", "removes an ACME challenge plugin; covered by unit tests",
              "pve pbs acme plugin delete pve-cli-dns --yes")

    # metrics config
    ctx.defer("metrics influxdb-http add", "creates an influxdb-http metric server; covered by unit tests",
              "pve pbs metrics influxdb-http add pve-cli-metrics --url https://influx.example:8086 --bucket pbs --organization ops --token ...")
    ctx.defer("metrics influxdb-http update", "modifies an influxdb-http metric server; covered by unit tests",
              "pve pbs metrics influxdb-http update pve-cli-metrics --enable=false")
    ctx.defer("metrics influxdb-http delete", "removes an influxdb-http metric server; covered by unit tests",
              "pve pbs metrics influxdb-http delete pve-cli-metrics --yes")
    ctx.defer("metrics influxdb-udp add", "creates an influxdb-udp metric server; covered by unit tests",
              "pve pbs metrics influxdb-udp add pve-cli-metrics --host udp.example --port 8089")
    ctx.defer("metrics influxdb-udp update", "modifies an influxdb-udp metric server; covered by unit tests",
              "pve pbs metrics influxdb-udp update pve-cli-metrics --enable=false")
    ctx.defer("metrics influxdb-udp delete", "removes an influxdb-udp metric server; covered by unit tests",
              "pve pbs metrics influxdb-udp delete pve-cli-metrics --yes")

    # node administration
    ctx.defer("node reboot", "reboots the real host; covered by unit tests",
              "pve pbs node reboot --yes")
    ctx.defer("node shutdown", "shuts down the real host; covered by unit tests",
              "pve pbs node shutdown --yes")
    ctx.defer("node config update", "modifies host configuration; covered by unit tests",
              "pve pbs node config update --email-from pbs@example")
    ctx.defer("node dns update", "modifies host DNS configuration; covered by unit tests",
              "pve pbs node dns update --dns1 192.0.2.53")
    ctx.defer("node time update", "modifies the host timezone; covered by unit tests",
              "pve pbs node time update --timezone UTC")
    ctx.defer("node subscription set", "registers a subscription key with the vendor; covered by unit tests",
              "pve pbs node subscription set <key>")
    ctx.defer("node subscription update", "re-checks the subscription with the vendor; covered by unit tests",
              "pve pbs node subscription update")
    ctx.defer("node subscription delete", "removes the subscription key; covered by unit tests",
              "pve pbs node subscription delete --yes")
    ctx.defer("node tasks delete", "removes a task-log entry; covered by unit tests",
              "pve pbs node tasks delete <upid> --yes")
    ctx.defer("node services start", "starts a PBS system service — disruptive to the server; covered by unit tests",
              "pve pbs node services start <svc>")
    ctx.defer("node services stop", "stops a PBS system service — disruptive to the server; covered by unit tests",
              "pve pbs node services stop <svc>")
    ctx.defer("node services restart", "restarts a PBS system service — disruptive to the server; covered by unit tests",
              "pve pbs node services restart <svc>")
    ctx.defer("node services reload", "reloads a PBS system service — disruptive to the server; covered by unit tests",
              "pve pbs node services reload <svc>")
    ctx.defer("node apt update", "refreshes the package index on the host; covered by unit tests",
              "pve pbs node apt update")
    ctx.defer("node apt repo-add", "adds a package repository to the host; covered by unit tests",
              "pve pbs node apt repo-add --handle no-subscription")
    ctx.defer("node apt repo-update", "enables or disables a package repository on the host; covered by unit tests",
              "pve pbs node apt repo-update --path /etc/apt/sources.list --index 0 --enabled=false")
    ctx.defer("node certificates acme order", "orders a real certificate from the CA and replaces the server cert; covered by unit tests",
              "pve pbs node certificates acme order")
    ctx.defer("node certificates acme renew", "renews the certificate at the CA and replaces the server cert; covered by unit tests",
              "pve pbs node certificates acme renew")
    ctx.defer("node certificates custom upload", "replaces the server's TLS certificate; covered by unit tests",
              "pve pbs node certificates custom upload --certificate cert.pem --key key.pem")
    ctx.defer("node certificates custom delete", "removes the custom TLS certificate; covered by unit tests",
              "pve pbs node certificates custom delete --yes")
    ctx.defer("node disks initgpt", "writes a new GPT, destroying data on a physical disk of the real host; covered by unit tests",
              "pve pbs node disks initgpt --disk sdX --yes")
    ctx.defer("node disks wipe", "wipes a physical disk of the real host, destroying its data; covered by unit tests",
              "pve pbs node disks wipe --disk sdX --yes")
    ctx.defer("node disks directory create", "formats a physical disk of the real host into a directory datastore; covered by unit tests",
              "pve pbs node disks directory create pve-cli-dir --disk sdX")
    ctx.defer("node disks directory delete", "removes a directory mount backed by a physical disk of the real host; covered by unit tests",
              "pve pbs node disks directory delete pve-cli-dir --yes")
    ctx.defer("node disks zfs create", "creates a zpool consuming physical disks of the real host; covered by unit tests",
              "pve pbs node disks zfs create pve-cli-pool --devices sdX")
    ctx.defer("node network create", "changes host network configuration; covered by unit tests",
              "pve pbs node network create pve-cli-br0 --type bridge")
    ctx.defer("node network update", "changes host network configuration; covered by unit tests",
              "pve pbs node network update pve-cli-br0 --comment e2e")
    ctx.defer("node network delete", "changes host network configuration; covered by unit tests",
              "pve pbs node network delete pve-cli-br0 --yes")
    ctx.defer("node network apply", "applies staged host network changes; covered by unit tests",
              "pve pbs node network apply")
    ctx.defer("node network revert", "reverts staged host network changes; covered by unit tests",
              "pve pbs node network revert")

    # tape hardware / config
    ctx.defer("tape backup", "runs a tape backup, writing datastore contents to tape; covered by unit tests",
              "pve pbs tape backup --store main --pool pve-cli-pool --drive drive0")
    ctx.defer("tape restore", "restores from tape into a datastore; covered by unit tests",
              "pve pbs tape restore <media-set> --store main --drive drive0")
    ctx.defer("tape drive add", "adds a tape drive definition; covered by unit tests",
              "pve pbs tape drive add pve-cli-drive --path /dev/tape/by-id/...")
    ctx.defer("tape drive update", "modifies a tape drive definition; covered by unit tests",
              "pve pbs tape drive update pve-cli-drive --changer sl3")
    ctx.defer("tape drive delete", "removes a tape drive definition; covered by unit tests",
              "pve pbs tape drive delete pve-cli-drive --yes")
    ctx.defer("tape drive format", "formats (erases) the loaded tape, destroying media contents — not automatable; covered by unit tests",
              "pve pbs tape drive format drive0 --yes")
    ctx.defer("tape drive label", "writes a new label to the loaded tape, destroying its contents — not automatable; covered by unit tests",
              "pve pbs tape drive label drive0 --label-text pve-cli-tape")
    ctx.defer("tape drive barcode-label", "labels every unlabelled tape in the changer, overwriting media headers — not automatable; covered by unit tests",
              "pve pbs tape drive barcode-label drive0 --pool pve-cli-pool")
    ctx.defer("tape drive restore-key", "prompts for the encryption-key password interactively; covered by unit tests",
              "pve pbs tape drive restore-key drive0")
    ctx.defer("tape drive load-media", "moves tape library hardware (loads a tape into the drive); covered by unit tests",
              "pve pbs tape drive load-media drive0 --label-text <tape>")
    ctx.defer("tape drive load-slot", "moves tape library hardware (loads from a slot); covered by unit tests",
              "pve pbs tape drive load-slot drive0 --slot 1")
    ctx.defer("tape drive unload", "moves tape library hardware (unloads the drive); covered by unit tests",
              "pve pbs tape drive unload drive0")
    ctx.defer("tape drive eject", "ejects the loaded tape from the drive; covered by unit tests",
              "pve pbs tape drive eject drive0")
    ctx.defer("tape drive rewind", "rewinds the loaded tape; covered by unit tests",
              "pve pbs tape drive rewind drive0")
    ctx.defer("tape drive clean", "runs a drive cleaning cycle with a cleaning cartridge; covered by unit tests",
              "pve pbs tape drive clean drive0")
    ctx.defer("tape drive catalog", "reads the whole loaded tape to rebuild its catalog (long, drive-locking); covered by unit tests",
              "pve pbs tape drive catalog drive0")
    ctx.defer("tape drive export", "moves tape library hardware (exports media to the IE slot); covered by unit tests",
              "pve pbs tape drive export drive0")
    ctx.defer("tape drive inventory", "moves tape library hardware (loads each tape to read labels); covered by unit tests",
              "pve pbs tape drive inventory drive0")
    ctx.defer("tape drive update-inventory", "moves tape library hardware (re-reads every tape label); covered by unit tests",
              "pve pbs tape drive update-inventory drive0")
    ctx.defer("tape changer add", "adds a tape changer definition; covered by unit tests",
              "pve pbs tape changer add pve-cli-changer --path /dev/tape/by-id/...")
    ctx.defer("tape changer update", "modifies a tape changer definition; covered by unit tests",
              "pve pbs tape changer update pve-cli-changer --export-slots 15,16")
    ctx.defer("tape changer delete", "removes a tape changer definition; covered by unit tests",
              "pve pbs tape changer delete pve-cli-changer --yes")
    ctx.defer("tape changer transfer", "moves tape library hardware (transfers media between slots); covered by unit tests",
              "pve pbs tape changer transfer sl3 --from 1 --to 2")
    ctx.defer("tape media move", "moves tape library hardware (relocates a tape); covered by unit tests",
              "pve pbs tape media move --label-text <tape> --vault-name offsite")
    ctx.defer("tape media set-status", "changes a tape medium's status flag; covered by unit tests",
              "pve pbs tape media set-status --label-text <tape> --status full")
    ctx.defer("tape media destroy", "destroys all data on a tape medium — not automatable; covered by unit tests",
              "pve pbs tape media destroy --label-text <tape> --yes")
    ctx.defer("tape pool add", "creates a media pool; covered by unit tests",
              "pve pbs tape pool add pve-cli-pool --allocation continue")
    ctx.defer("tape pool update", "modifies a media pool; covered by unit tests",
              "pve pbs tape pool update pve-cli-pool --retention overwrite")
    ctx.defer("tape pool delete", "removes a media pool; covered by unit tests",
              "pve pbs tape pool delete pve-cli-pool --yes")
    ctx.defer("tape key add", "creates a tape encryption key; covered by unit tests",
              "pve pbs tape key add --hint pve-cli --password ...")
    ctx.defer("tape key update", "modifies a tape encryption key; covered by unit tests",
              "pve pbs tape key update <fingerprint> --hint pve-cli")
    ctx.defer("tape key delete", "removes a tape encryption key; covered by unit tests",
              "pve pbs tape key delete <fingerprint> --yes")
    ctx.defer("tape job add", "creates a tape backup job; covered by unit tests",
              "pve pbs tape job add pve-cli-tape --store main --pool pve-cli-pool --drive drive0")
    ctx.defer("tape job update", "modifies a tape backup job; covered by unit tests",
              "pve pbs tape job update pve-cli-tape --schedule weekly")
    ctx.defer("tape job delete", "removes a tape backup job; covered by unit tests",
              "pve pbs tape job delete pve-cli-tape --yes")
    ctx.defer("tape job run", "runs a tape backup job, writing to tape; covered by unit tests",
              "pve pbs tape job run pve-cli-tape")
