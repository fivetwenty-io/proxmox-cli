"""Destructive lifecycle suite for Proxmox Backup Server.

The read-only `pbs` tree defers every mutating verb; this is the counterpart
that actually drives them, the same way `lifecycle.py` does for PVE. Each verb
is recorded individually so the run prints a coverage table proving the
deferred operation was exercised against a real PBS.

Isolation — a PBS has no pool or tag to hang ownership on, so the contract here
is naming plus containment:

  * every created object is named with the `pmx-cli-` prefix (datastores, jobs,
    users, realms, endpoints, remotes, traffic rules, keys),
  * all backup data lands in a scratch datastore the run creates and destroys,
    at a path of its own under /var/lib — no pre-existing datastore is written
    to, pruned, verified, or garbage-collected,
  * outbound-facing objects (notification endpoints, metric servers, remotes)
    point at addresses that are never contacted, and are created disabled where
    the API allows it,
  * nothing that would take the host off the network is run: the node network
    verbs stage a config and then `revert` it, and `network apply` stays
    deferred.

Teardown runs in a `finally` block and is idempotent, so a crashed prior run is
swept clean by the next one.
"""

from __future__ import annotations

import base64
import os
import ssl
import sys
import time
import urllib.error
import urllib.request

from .lifecycle import (
    FAIL,
    Cmd,
    LifecycleError,
    Runner,
    _print_coverage,
    _resolve_host,
    _ssh_node,
)
from .runner import BOLD, DIM, GREEN, RED, find_binary, target_configured
from . import ldapstub

# --- isolation contract -----------------------------------------------------

PREFIX = "pmx-cli-"
DATASTORE = PREFIX + "ds"
DATASTORE_PATH = "/var/lib/" + PREFIX + "ds"
PRUNE_JOB = PREFIX + "prune"
VERIFY_JOB = PREFIX + "verify"
SYNC_JOB = PREFIX + "sync"
# Target namespaces for the pulls, one each. Both live inside the scratch
# datastore, so they disappear with it. They are kept apart because a sync
# refuses to write into a namespace whose groups another identity owns, and the
# ad-hoc pull and the scheduled job do not run as the same auth-id.
SYNC_NS = "pmxcli"
SYNC_NS_JOB = "pmxclijob"
REMOTE = PREFIX + "remote"
TRAFFIC_RULE = PREFIX + "traffic"
ENC_KEY = PREFIX + "key"
USER = PREFIX + "user@pbs"
TOKEN = PREFIX + "tok"
ACL_PATH = "/datastore/" + DATASTORE
ACL_ROLE = "DatastoreAudit"
# Realm ids are bare identifiers; keep the prefix but drop the dash-heavy form
# so they stay inside what PBS accepts for a realm name.
REALM_LDAP = PREFIX + "ldap"
REALM_AD = PREFIX + "ad"
REALM_OIDC = PREFIX + "oidc"
GOTIFY_ENDPOINT = PREFIX + "gotify"
SENDMAIL_ENDPOINT = PREFIX + "sendmail"
SMTP_ENDPOINT = PREFIX + "smtp"
WEBHOOK_ENDPOINT = PREFIX + "webhook"
NOTIFY_MATCHER = PREFIX + "matcher"
INFLUX_UDP = PREFIX + "influx-udp"
INFLUX_HTTP = PREFIX + "influx-http"
ACME_PLUGIN = PREFIX + "acme"
# The plugin's credentials, base64 with padding as the API requires. They point
# at an unroutable TEST-NET address and are only read when a certificate is
# ordered, which this suite never does.
ACME_PLUGIN_DATA = base64.b64encode(
    b"ACMEDNS_BASE_URL=https://192.0.2.10\n"
    b"ACMEDNS_USERNAME=pmx-cli\n"
    b"ACMEDNS_PASSWORD=pmx-cli-e2e\n").decode()
NET_IFACE = "pmxcli0"          # staged-only dummy interface, never applied
# Addresses that are deliberately unroutable/unused: nothing here is contacted.
DUMMY_HOST = "192.0.2.10"      # TEST-NET-1 (RFC 5737)
DUMMY_NET = "192.0.2.0/24"
# Services the start/stop/restart/reload verbs may be driven against, in
# preference order. Each is inert as far as this suite is concerned; what is
# deliberately absent is everything whose restart would cut the run off —
# proxmox-backup and proxmox-backup-proxy carry the API, sshd carries the
# fixtures, and systemd-journald carries the logs the failures are read from.
# The pick is made from what the appliance actually has installed and running:
# `services ls` reports a unit that is not installed at all as "dead", and
# starting one fails with a systemctl exit status rather than doing nothing.
SAFE_SERVICES = ("postfix", "cron", "chrony")

_STORE = "--store"


def _err(res: Cmd, fallback: str) -> str:
    from .text import one_line
    return one_line(res.reason, fallback, limit=120)


# --- scratch datastore ------------------------------------------------------


def sweep_stale(r: Runner) -> None:
    """Remove anything a crashed prior run left behind, newest dependency first.

    Every id carries the `pmx-cli-` prefix, so this can never touch an object
    the operator created. Ordering matters: jobs and ACLs reference the
    datastore, so they go first.
    """
    print(BOLD("sweep: stale pmx-cli objects from a prior run"))
    r.undo(f"prune job {PRUNE_JOB}", "pbs", "prune", "job", "delete", PRUNE_JOB, "-y")
    r.undo(f"verify job {VERIFY_JOB}", "pbs", "verify", "job", "delete", VERIFY_JOB, "-y")
    r.undo(f"sync job {SYNC_JOB}", "pbs", "sync", "job", "delete", SYNC_JOB, "-y")
    r.undo(f"acl {ACL_PATH}", "pbs", "acl", "update", "--path", ACL_PATH,
           "--role", ACL_ROLE, "--auth-id", USER, "--delete")
    r.undo(f"token {USER}!{TOKEN}", "pbs", "user", "token", "delete", USER, TOKEN, "-y")
    r.undo(f"user {USER}", "pbs", "user", "delete", USER, "-y")
    drop_work_token(r)
    r.undo(f"remote {REMOTE}", "pbs", "remote", "delete", REMOTE, "-y")
    r.undo(f"traffic {TRAFFIC_RULE}", "pbs", "traffic", "delete", TRAFFIC_RULE, "-y")
    _purge_encryption_key(r)
    r.undo(f"matcher {NOTIFY_MATCHER}", "pbs", "notification", "matcher", "delete",
           NOTIFY_MATCHER, "-y")
    for kind, name in (("gotify", GOTIFY_ENDPOINT), ("sendmail", SENDMAIL_ENDPOINT),
                       ("smtp", SMTP_ENDPOINT), ("webhook", WEBHOOK_ENDPOINT)):
        r.undo(f"{kind} endpoint {name}", "pbs", "notification", "endpoint", kind,
               "delete", name, "-y")
    r.undo(f"influxdb-udp {INFLUX_UDP}", "pbs", "metrics", "influxdb-udp", "delete",
           INFLUX_UDP, "-y")
    r.undo(f"influxdb-http {INFLUX_HTTP}", "pbs", "metrics", "influxdb-http", "delete",
           INFLUX_HTTP, "-y")
    r.undo(f"acme plugin {ACME_PLUGIN}", "pbs", "acme", "plugin", "delete", ACME_PLUGIN, "-y")
    for kind, name in (("ldap", REALM_LDAP), ("ad", REALM_AD), ("openid", REALM_OIDC)):
        r.undo(f"realm {kind} {name}", "pbs", "realm", kind, "delete", name, "-y")
    # Tape config unwinds in reverse dependency order: job -> pool -> drive -> changer.
    r.undo(f"tape job {TAPE_JOB}", "pbs", "tape", "job", "delete", TAPE_JOB, "-y")
    r.undo(f"tape pool {TAPE_POOL}", "pbs", "tape", "pool", "delete", TAPE_POOL, "-y")
    r.undo(f"tape drive {TAPE_DRIVE}", "pbs", "tape", "drive", "delete", TAPE_DRIVE, "-y")
    r.undo(f"tape changer {TAPE_CHANGER}", "pbs", "tape", "changer", "delete",
           TAPE_CHANGER, "-y")
    r.undo(f"datastore {DATASTORE}", "pbs", "datastore", "delete", DATASTORE,
           "--destroy-data", "-y")


def _purge_encryption_key(r: Runner) -> None:
    """Remove the scratch encryption key, archiving it first if it is still active.

    A run that died between `add` and `toggle-archive` leaves an active key, and
    an active key refuses to be deleted — so the sweep has to archive it before
    it can clear it, or every later run trips over the leftover.
    """
    first = r.pmx("pbs", "encryption-key", "delete", ENC_KEY, "-y")
    if first.rc == 0:
        print(f"  {GREEN('✓')} encryption-key {ENC_KEY}")
        return
    if "still active" in first.reason.lower():
        r.pmx("pbs", "encryption-key", "toggle-archive", ENC_KEY)
        r.undo(f"encryption-key {ENC_KEY} (archived first)",
               "pbs", "encryption-key", "delete", ENC_KEY, "-y")
        return
    print(f"  {DIM('· encryption-key ' + ENC_KEY + ' (skip: ' + _err(first, 'failed') + ')')}")


def datastore_lifecycle(r: Runner) -> None:
    """create → update → (the rest of the suite runs) — the delete is teardown.

    The datastore is the anchor every data-touching block needs, so its delete
    lives in the caller's `finally` rather than here.
    """
    print(BOLD("datastore: create + update (scratch store, own path)"))
    r.step("datastore", "datastore create", f"create {DATASTORE} at {DATASTORE_PATH}",
           "pbs", "datastore", "create", DATASTORE, "--path", DATASTORE_PATH,
           "--comment", "pmx-cli e2e scratch datastore")
    # Retention deliberately is NOT set here: current PBS releases rejected the
    # per-datastore keep-*/prune-schedule settings when prune jobs replaced them,
    # so the update exercises a field the server still owns. Retention itself is
    # covered by the prune-job round-trip.
    r.step("datastore", "datastore update", f"update {DATASTORE} gc-schedule",
           "pbs", "datastore", "update", DATASTORE, "--gc-schedule", "daily",
           "--comment", "pmx-cli e2e scratch datastore (updated)")


def gc_prune_verify_lifecycle(r: Runner) -> None:
    """Drive gc/prune/verify — the ad-hoc runs and their scheduled job forms.

    All of them target the scratch datastore. It holds at most the one tiny
    host backup `backup_data_lifecycle` stages, so every task completes in
    seconds and no real backup data is at risk.
    """
    print(BOLD("gc/prune/verify: ad-hoc runs and scheduled jobs on the scratch store"))
    r.step("datastore", "gc run", f"gc run on {DATASTORE}",
           "pbs", "gc", "run", _STORE, DATASTORE)
    # --dry-run keeps the ad-hoc prune from deleting the snapshot the later
    # group/snapshot block still needs; the deleting form is proven by the job.
    r.step("datastore", "prune run", f"prune run --dry-run on {DATASTORE}",
           "pbs", "prune", "run", _STORE, DATASTORE, "--keep-last", "1", "--dry-run")
    r.step("datastore", "verify run", f"verify run on {DATASTORE}",
           "pbs", "verify", "run", _STORE, DATASTORE)

    r.step("datastore", "prune job add", f"prune job add {PRUNE_JOB}",
           "pbs", "prune", "job", "add", PRUNE_JOB, _STORE, DATASTORE,
           "--schedule", "daily", "--keep-last", "1", "--comment", "pmx-cli e2e")
    try:
        r.step("datastore", "prune job update", f"prune job update {PRUNE_JOB}",
               "pbs", "prune", "job", "update", PRUNE_JOB, "--keep-last", "2",
               "--comment", "pmx-cli e2e (updated)")
        r.step("datastore", "prune job run", f"prune job run {PRUNE_JOB}",
               "pbs", "prune", "job", "run", PRUNE_JOB)
    finally:
        r.del_step("datastore", "prune job delete", f"prune job delete {PRUNE_JOB}",
                   "pbs", "prune", "job", "delete", PRUNE_JOB, "-y")

    r.step("datastore", "verify job add", f"verify job add {VERIFY_JOB}",
           "pbs", "verify", "job", "add", VERIFY_JOB, _STORE, DATASTORE,
           "--schedule", "daily", "--comment", "pmx-cli e2e")
    try:
        r.step("datastore", "verify job update", f"verify job update {VERIFY_JOB}",
               "pbs", "verify", "job", "update", VERIFY_JOB, "--ignore-verified",
               "--comment", "pmx-cli e2e (updated)")
        r.step("datastore", "verify job run", f"verify job run {VERIFY_JOB}",
               "pbs", "verify", "job", "run", VERIFY_JOB)
    finally:
        r.del_step("datastore", "verify job delete", f"verify job delete {VERIFY_JOB}",
                   "pbs", "verify", "job", "delete", VERIFY_JOB, "-y")


# --- backup data ------------------------------------------------------------


# A throwaway API token minted for the blocks that need a credential in the
# clear — the backup client and the self-referential remote. The context's own
# secret is deliberately unreadable (`context show` masks it and it may live in
# the keychain), so the suite mints its own rather than digging one out.
WORK_TOKEN = PREFIX + "work"
# Scoped to the scratch datastore alone. Admin rather than Backup because the
# self-referential remote reads through this same token during `sync pull`.
WORK_ROLE = "DatastoreAdmin"


def _server_fingerprint(r: Runner) -> str:
    """SHA-256 fingerprint of the API certificate, or "".

    A PBS appliance serves a self-signed certificate, so both the remote
    connection and the backup client have to pin it explicitly — without the pin
    they refuse to connect and report only `client error (Connect)`.
    """
    res = r.pmx("pbs", "node", "certificates", "info", json_out=True)
    if res.rc != 0:
        return ""
    try:
        rows = res.json()
    except ValueError:
        return ""
    if isinstance(rows, dict):
        rows = rows.get("data", [rows])
    for c in rows if isinstance(rows, list) else []:
        if isinstance(c, dict) and c.get("filename") == "proxy.pem" and c.get("fingerprint"):
            return str(c["fingerprint"])
    for c in rows if isinstance(rows, list) else []:
        if isinstance(c, dict) and c.get("fingerprint"):
            return str(c["fingerprint"])
    return ""


def _context_user(r: Runner) -> str:
    """Return the userid the context authenticates as, or ""."""
    show = r.pmx("context", "show", r.context, json_out=True)
    if show.rc != 0:
        return ""
    try:
        data = show.json()
    except ValueError:
        return ""
    if not isinstance(data, dict):
        return ""
    data = data.get("data", data)
    return str(data.get("username", "") or "")


def mint_work_token(r: Runner) -> tuple[str, str]:
    """Mint a throwaway token on the context's own user and return (authid, secret).

    A PBS token starts with no privileges of its own, so it is also granted
    `WORK_ROLE` on the scratch datastore — and nothing else, so the credential
    that ends up on the host in an environment variable can only touch the store
    this run created. Returns ("", "") if either step fails; the callers then
    record their verbs as skips.
    """
    user = _context_user(r)
    if not user:
        return "", ""
    r.undo(f"drop stale token {user}!{WORK_TOKEN}",
           "pbs", "user", "token", "delete", user, WORK_TOKEN, "-y")
    res = r.pmx("pbs", "user", "token", "add", user, WORK_TOKEN,
                "--comment", "pmx-cli e2e worker", json_out=True)
    if res.rc != 0:
        return "", ""
    try:
        data = res.json()
    except ValueError:
        return "", ""
    if isinstance(data, dict):
        data = data.get("data", data)
    authid = str(data.get("tokenid", "") or "") if isinstance(data, dict) else ""
    secret = str(data.get("value", "") or "") if isinstance(data, dict) else ""
    if not authid or not secret:
        return "", ""
    grant = r.pmx("pbs", "acl", "update", "--path", ACL_PATH,
                  "--role", WORK_ROLE, "--auth-id", authid)
    if grant.rc != 0:
        return "", ""
    if not _wait_token_ready(r, authid, secret):
        return "", ""
    return authid, secret


def _wait_token_ready(r: Runner, authid: str, secret: str, timeout: float = 15.0) -> bool:
    """Block until a freshly granted ACL is actually visible to the token.

    PBS re-reads acl.cfg only when the file's mtime changes, and that mtime has
    one-second resolution — so a request issued in the same second as the grant
    is answered from the pre-grant ACL and rejected. Without this wait the
    backup and sync blocks fail intermittently with a permission error that
    describes a permission the token demonstrably has.
    """
    endpoint = _api_base(r)
    if not endpoint:
        # Cannot probe; a short unconditional wait still clears the mtime edge.
        time.sleep(2)
        return True
    url = f"{endpoint}/api2/json/admin/datastore/{DATASTORE}/snapshots"
    req = urllib.request.Request(url, headers={
        "Authorization": f"PBSAPIToken={authid}:{secret}"})
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(req, timeout=10, context=ctx):
                return True
        except urllib.error.HTTPError as exc:
            if exc.code not in (401, 403):
                return True  # reachable and authorised; some other API detail
        except (urllib.error.URLError, OSError, ssl.SSLError):
            return True  # not a permission problem — let the real verb report it
        time.sleep(1)
    return False


def _api_base(r: Runner) -> str:
    """Return `https://host:port` for the context, or ""."""
    show = r.pmx("context", "show", r.context, json_out=True)
    if show.rc != 0:
        return ""
    try:
        data = show.json()
    except ValueError:
        return ""
    if not isinstance(data, dict):
        return ""
    data = data.get("data", data)
    host = str(data.get("host", "") or "")
    port = data.get("port") or 8007
    proto = str(data.get("protocol", "") or "https")
    return f"{proto}://{host}:{port}" if host else ""


def drop_work_token(r: Runner) -> None:
    """Revoke and delete the throwaway worker token. Idempotent."""
    user = _context_user(r)
    if not user:
        return
    authid = f"{user}!{WORK_TOKEN}"
    r.undo(f"revoke {authid} on {ACL_PATH}",
           "pbs", "acl", "update", "--path", ACL_PATH, "--role", "DatastoreBackup",
           "--auth-id", authid, "--delete")
    r.undo(f"delete token {authid}",
           "pbs", "user", "token", "delete", user, WORK_TOKEN, "-y")


def stage_backup_data(r: Runner, authid: str, secret: str) -> tuple[str, str]:
    """Push two tiny host backups into the scratch datastore. Returns their ids.

    Real chunks in the store are what make the blocks that follow meaningful:
    gc, prune, verify, and both sync directions all act on data rather than on
    an empty directory. Returns ("", "") if the backups could not be staged, and
    the dependent block then records skips.

    The API cannot create a snapshot — only the backup protocol can — so the
    push runs on the PBS host itself over root SSH with the throwaway worker
    token, a credential scoped to this one datastore.
    """
    print(BOLD("backup data: stage two host snapshots in the scratch store"))
    host = _resolve_host(r)
    if not host or _ssh_node(host, "true")[0] != 0:
        print(DIM("  PBS host not reachable over root SSH; skipping the staging"))
        return "", ""
    if _ssh_node(host, "command", "-v", "proxmox-backup-client")[0] != 0:
        print(DIM("  proxmox-backup-client not installed on the PBS host"))
        return "", ""
    if not authid or not secret:
        print(DIM("  no scoped worker token to authenticate a backup with"))
        return "", ""
    fingerprint = _server_fingerprint(r)
    if not fingerprint:
        print(DIM("  could not read the API certificate fingerprint to pin"))
        return "", ""

    # Two groups, because `snapshot delete` and `group delete` each consume what
    # they act on: deleting the only snapshot of a group takes the group's
    # directory with it, and `group delete` would then have nothing to remove.
    snap_id = PREFIX + "snapprobe"
    group_id = PREFIX + "grpprobe"
    repo = f"{authid}@localhost:8007:{DATASTORE}"
    env = (f"PBS_PASSWORD={secret} PBS_FINGERPRINT={fingerprint} "
           f"PBS_REPOSITORY={repo} ")
    for backup_id in (snap_id, group_id):
        # LC_ALL=C: the appliance ships no en_US locale, so an unset one makes
        # the login shell emit a setlocale warning that would otherwise be the
        # last stderr line — and therefore the reason reported for any failure.
        stage = (f"export LC_ALL=C; set -e; d=$(mktemp -d); "
                 f"printf 'pmx-cli-e2e' > $d/marker.txt; "
                 f"{env} proxmox-backup-client backup marker.pxar:$d "
                 f"--backup-id {backup_id} --backup-type host "
                 f"--crypt-mode none --skip-lost-and-found 2>&1; rm -rf $d")
        rc, out, err = _ssh_node(host, stage, timeout=180)
        if rc != 0:
            lines = [ln.strip() for ln in (out + "\n" + err).splitlines() if ln.strip()]
            # `Error: ...` is the client's own verdict; anything else is
            # progress chatter that happens to come last.
            errs = [ln for ln in lines if ln.lower().startswith("error")]
            print(DIM("  could not stage a backup snapshot: "
                      + ((errs or lines)[-1][:120] if lines else f"exit {rc}")))
            return "", ""
        print(f"  {GREEN('✓')} staged host/{backup_id} in {DATASTORE}")
    return snap_id, group_id


def snapshot_group_lifecycle(r: Runner, snap_id: str, group_id: str) -> None:
    """Drive the verbs that delete backup data, once everything else has run.

    This block is destructive to the staged snapshots, so it comes last: gc,
    prune, verify, and both sync directions all want the data still present.
    """
    print(BOLD("datastore: snapshot protect/unprotect/delete and group delete"))
    verbs = ("snapshot protect", "snapshot unprotect", "snapshot delete", "group delete")
    if not snap_id or not group_id:
        for v in verbs:
            r.cover_skip("datastore", v, v,
                         "no backup snapshot could be staged in the scratch datastore")
        return

    snap = _latest_snapshot(r, f"host/{snap_id}")
    if not snap:
        for v in verbs:
            r.cover_skip("datastore", v, v,
                         "staged backup did not appear in the datastore listing")
        return

    r.step("datastore", "snapshot protect", f"protect {snap}",
           "pbs", "snapshot", "protect", snap, _STORE, DATASTORE)
    # A protected snapshot refuses deletion; unprotect is what makes the delete
    # below possible, so the pairing proves both verbs really took.
    r.step("datastore", "snapshot unprotect", f"unprotect {snap}",
           "pbs", "snapshot", "unprotect", snap, _STORE, DATASTORE)
    r.step("datastore", "snapshot delete", f"delete {snap}",
           "pbs", "snapshot", "delete", snap, _STORE, DATASTORE, "-y")
    r.del_step("datastore", "group delete", f"group delete host/{group_id}",
               "pbs", "group", "delete", f"host/{group_id}", _STORE, DATASTORE, "-y")


def _latest_snapshot(r: Runner, group: str) -> str:
    """Return `<type>/<id>/<time>` for the newest snapshot in group, or ""."""
    res = r.pmx("pbs", "snapshot", "ls", _STORE, DATASTORE, json_out=True)
    if res.rc != 0:
        return ""
    try:
        rows = res.json()
    except ValueError:
        return ""
    if isinstance(rows, dict):
        rows = rows.get("data", rows.get("rows", []))
    if not isinstance(rows, list):
        return ""
    best = ""
    best_t = -1.0
    for s in rows:
        if not isinstance(s, dict):
            continue
        btype = str(s.get("backup-type") or s.get("backup_type") or "")
        bid = str(s.get("backup-id") or s.get("backup_id") or "")
        btime = s.get("backup-time", s.get("backup_time"))
        if f"{btype}/{bid}" != group or btime is None:
            continue
        try:
            t = float(btime)
        except (TypeError, ValueError):
            continue
        if t > best_t:
            best_t, best = t, f"{btype}/{bid}/{_iso(t)}"
    return best


def _iso(epoch: float) -> str:
    """Format a backup time the way PBS names a snapshot (UTC, RFC3339-ish)."""
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(epoch))


# --- remote / sync / traffic ------------------------------------------------


def remote_sync_lifecycle(r: Runner, authid: str, secret: str) -> None:
    """Round-trip remote, sync job, sync pull/push, and traffic rules.

    The remote points the PBS at *itself* over localhost: a sync then has a real
    peer to talk to without a second server, and the scratch datastore is both
    ends of the transfer, so nothing leaves the host. Both directions go through
    that remote — PBS rejects a remote-less pull whose source and target are the
    same local datastore ("can't sync to same datastore"), and standing up a
    second scratch store just to satisfy that check would buy no extra coverage.
    """
    print(BOLD("remote/sync/traffic: self-referential remote on the scratch store"))
    r.step("infra", "traffic add", f"traffic add {TRAFFIC_RULE}",
           "pbs", "traffic", "add", TRAFFIC_RULE, "--network", DUMMY_NET,
           "--rate-in", "10MB", "--rate-out", "10MB", "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "traffic update", f"traffic update {TRAFFIC_RULE}",
               "pbs", "traffic", "update", TRAFFIC_RULE, "--rate-in", "20MB",
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("infra", "traffic delete", f"traffic delete {TRAFFIC_RULE}",
                   "pbs", "traffic", "delete", TRAFFIC_RULE, "-y")

    if not authid or not secret:
        for v in ("remote add", "remote update", "remote delete", "sync push", "sync pull",
                  "sync job add", "sync job update", "sync job run", "sync job delete"):
            r.cover_skip("infra", v, v,
                         "could not mint a scoped worker token; a remote needs credentials")
    else:
        # The appliance's certificate is self-signed, so the remote has to pin
        # it or the connection fails with a bare `client error (Connect)`.
        add = ["pbs", "remote", "add", REMOTE, "--host", "localhost", "--port", "8007",
               "--auth-id", authid, "--password", secret, "--comment", "pmx-cli e2e"]
        fingerprint = _server_fingerprint(r)
        if fingerprint:
            add += ["--fingerprint", fingerprint]
        r.step("infra", "remote add", f"remote add {REMOTE} -> localhost", *add)
        try:
            r.step("infra", "remote update", f"remote update {REMOTE}",
                   "pbs", "remote", "update", REMOTE, "--comment", "pmx-cli e2e (updated)")
            _sync_jobs(r)
        finally:
            r.del_step("infra", "remote delete", f"remote delete {REMOTE}",
                       "pbs", "remote", "delete", REMOTE, "-y")


def _sync_jobs(r: Runner) -> None:
    """The sync-job round-trip plus both ad-hoc directions, via the self-remote."""
    r.step("infra", "sync push", f"sync push {DATASTORE} -> {REMOTE}",
           "pbs", "sync", "push", _STORE, DATASTORE, "--remote", REMOTE,
           "--remote-store", DATASTORE)
    # Into a namespace of its own, not the root: the staged groups are owned by
    # the worker token, and a pull into the same namespace would fail its owner
    # check ("root@pam!e2e != root@pam!pmx-cli-work") rather than transfer.
    r.step("infra", "sync pull", f"sync pull {DATASTORE}:{SYNC_NS} <- {REMOTE}",
           "pbs", "sync", "pull", _STORE, DATASTORE, "--remote", REMOTE,
           "--remote-store", DATASTORE, "--ns", SYNC_NS)
    # Its own namespace, for the same reason the ad-hoc pull needed one — and a
    # different one, because the job runs as the API token while the ad-hoc pull
    # ran as the user, and each owns what it writes.
    r.step("infra", "sync job add", f"sync job add {SYNC_JOB}",
           "pbs", "sync", "job", "add", SYNC_JOB, _STORE, DATASTORE,
           "--remote", REMOTE, "--remote-store", DATASTORE, "--ns", SYNC_NS_JOB,
           "--schedule", "daily", "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "sync job update", f"sync job update {SYNC_JOB}",
               "pbs", "sync", "job", "update", SYNC_JOB,
               "--comment", "pmx-cli e2e (updated)")
        r.step("infra", "sync job run", f"sync job run {SYNC_JOB}",
               "pbs", "sync", "job", "run", SYNC_JOB)
    finally:
        r.del_step("infra", "sync job delete", f"sync job delete {SYNC_JOB}",
                   "pbs", "sync", "job", "delete", SYNC_JOB, "-y")


# --- access -----------------------------------------------------------------


def access_lifecycle(r: Runner) -> None:
    """User, token, ACL, and encryption-key round-trips, all prefix-isolated.

    The ACL is granted on the scratch datastore only, so the throwaway user can
    never see anything else on the server, and it is revoked before the user is
    deleted.
    """
    print(BOLD("access: user/token/acl/encryption-key round-trips"))
    r.step("access", "user add", f"user add {USER}",
           "pbs", "user", "add", USER, "--comment", "pmx-cli e2e",
           "--email", "pmx-cli@example.invalid")
    try:
        r.step("access", "user update", f"user update {USER}",
               "pbs", "user", "update", USER, "--comment", "pmx-cli e2e (updated)")
        # The unlock endpoint reads the TFA config, not the user config, so a
        # user with no enrolled factor is "no such user" to it. Enrolling one
        # needs an interactive second-factor exchange, so the verb is recorded
        # as a skip naming that rather than failing.
        r.soft_step("access", "user unlock-tfa", f"user unlock-tfa {USER}",
                    "pbs", "user", "unlock-tfa", USER,
                    skip_markers=("no such user", "no such entry", "not found"),
                    skip_reason="the user has no enrolled second factor, so the TFA "
                                "config has no entry to unlock; enrolling one needs an "
                                "interactive exchange")

        r.step("access", "user token add", f"token add {USER}!{TOKEN}",
               "pbs", "user", "token", "add", USER, TOKEN, "--comment", "pmx-cli e2e")
        try:
            r.step("access", "user token update", f"token update {USER}!{TOKEN}",
                   "pbs", "user", "token", "update", USER, TOKEN,
                   "--comment", "pmx-cli e2e (updated)")
        finally:
            r.del_step("access", "user token delete", f"token delete {USER}!{TOKEN}",
                       "pbs", "user", "token", "delete", USER, TOKEN, "-y")

        r.step("access", "acl update", f"acl grant {ACL_ROLE} on {ACL_PATH}",
               "pbs", "acl", "update", "--path", ACL_PATH, "--role", ACL_ROLE,
               "--auth-id", USER)
        r.step("access", "acl update revoke", f"acl revoke {ACL_ROLE} on {ACL_PATH}",
               "pbs", "acl", "update", "--path", ACL_PATH, "--role", ACL_ROLE,
               "--auth-id", USER, "--delete")
    finally:
        r.del_step("access", "user delete", f"user delete {USER}",
                   "pbs", "user", "delete", USER, "-y")

    # The three key verbs are one sequence, not three independent round-trips:
    # PBS refuses to delete an *active* datastore encryption key, so the key has
    # to be archived first — which is what toggle-archive does, and why it is
    # exercised here rather than deferred.
    r.step("access", "encryption-key add", f"encryption-key add {ENC_KEY}",
           "pbs", "encryption-key", "add", ENC_KEY)
    try:
        r.step("access", "encryption-key toggle-archive",
               f"encryption-key toggle-archive {ENC_KEY} (active -> archived)",
               "pbs", "encryption-key", "toggle-archive", ENC_KEY)
    finally:
        r.del_step("access", "encryption-key delete", f"encryption-key delete {ENC_KEY}",
                   "pbs", "encryption-key", "delete", ENC_KEY, "-y")
    r.cover_skip("access", "user passwd", "user passwd",
                 "reads the new password from an interactive prompt")


def realm_lifecycle(r: Runner) -> None:
    """LDAP/AD/OpenID realm round-trips plus the two built-in realm updates.

    PBS connects to the directory when an LDAP or AD realm is created, so those
    two cannot point at a dead address — a throwaway responder is staged on the
    appliance itself for the duration of the block (see e2e_lib.ldapstub) and
    killed afterwards. The OpenID realm needs no such thing: its issuer is only
    contacted at login, so it points at an unroutable TEST-NET address. None of
    the three is ever made the default, so no login path changes.
    """
    print(BOLD("realm: ldap/ad/openid round-trips"))
    host = _resolve_host(r)
    port = ldapstub.start(_ssh_node, host) if host else 0
    try:
        if port:
            print(DIM(f"  ldap responder staged on the appliance at 127.0.0.1:{port}"))
            _ldap_ad_realms(r, port)
        else:
            for v in ("realm ldap add", "realm ldap update", "realm ldap delete",
                      "realm ad add", "realm ad update", "realm ad delete", "realm sync"):
                r.cover_skip("access", v, v,
                             "an LDAP/AD realm is validated against a live directory when "
                             "it is created, and no responder could be staged on the "
                             "appliance (host unreachable over root SSH)")
    finally:
        if host:
            ldapstub.stop(_ssh_node, host)

    r.step("access", "realm openid add", f"realm openid add {REALM_OIDC}",
           "pbs", "realm", "openid", "add", REALM_OIDC,
           "--issuer-url", f"https://{DUMMY_HOST}/", "--client-id", "pmx-cli-e2e",
           "--comment", "pmx-cli e2e")
    try:
        r.step("access", "realm openid update", f"realm openid update {REALM_OIDC}",
               "pbs", "realm", "openid", "update", REALM_OIDC,
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("access", "realm openid delete", f"realm openid delete {REALM_OIDC}",
                   "pbs", "realm", "openid", "delete", REALM_OIDC, "-y")

    # The two built-in realms cannot be created or deleted, only updated. Their
    # comment is the one field with no behavioural effect, so it is set and then
    # restored to whatever the server had.
    _builtin_realm_comment(r, "pam")
    _builtin_realm_comment(r, "pbs")


def _ldap_ad_realms(r: Runner, port: int) -> None:
    """LDAP and AD realm round-trips against the staged responder.

    The AD realm passes no --base-dn: PBS derives it from the root DSE, which is
    exactly the code path the responder exists to satisfy.
    """
    r.step("access", "realm ldap add", f"realm ldap add {REALM_LDAP}",
           "pbs", "realm", "ldap", "add", REALM_LDAP, "--server1", "127.0.0.1",
           "--port", str(port), "--base-dn", ldapstub.BASE_DN, "--user-attr", "uid",
           "--comment", "pmx-cli e2e")
    try:
        r.step("access", "realm ldap update", f"realm ldap update {REALM_LDAP}",
               "pbs", "realm", "ldap", "update", REALM_LDAP,
               "--comment", "pmx-cli e2e (updated)")
        # The responder returns no entries for a user search, so a real sync
        # would see an empty directory; --dry-run keeps it from acting on that.
        r.step("access", "realm sync", f"realm sync {REALM_LDAP} --dry-run",
               "pbs", "realm", "sync", REALM_LDAP, "--dry-run")
    finally:
        r.del_step("access", "realm ldap delete", f"realm ldap delete {REALM_LDAP}",
                   "pbs", "realm", "ldap", "delete", REALM_LDAP, "-y")

    r.step("access", "realm ad add", f"realm ad add {REALM_AD}",
           "pbs", "realm", "ad", "add", REALM_AD, "--server1", "127.0.0.1",
           "--port", str(port), "--comment", "pmx-cli e2e")
    try:
        r.step("access", "realm ad update", f"realm ad update {REALM_AD}",
               "pbs", "realm", "ad", "update", REALM_AD,
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("access", "realm ad delete", f"realm ad delete {REALM_AD}",
                   "pbs", "realm", "ad", "delete", REALM_AD, "-y")


def _builtin_realm_comment(r: Runner, kind: str) -> None:
    """Set and restore the comment on a built-in realm (`pam` / `pbs`)."""
    before = r.pmx("pbs", "realm", kind, "show", json_out=True)
    orig = ""
    if before.rc == 0:
        try:
            data = before.json()
            if isinstance(data, dict):
                orig = str(data.get("data", data).get("comment", "") or "")
        except (ValueError, AttributeError):
            orig = ""
    r.step("access", f"realm {kind} update", f"realm {kind} update comment",
           "pbs", "realm", kind, "update", "--comment", "pmx-cli e2e")
    if orig:
        r.undo(f"restore realm {kind} comment",
               "pbs", "realm", kind, "update", "--comment", orig)
    else:
        r.undo(f"clear realm {kind} comment",
               "pbs", "realm", kind, "update", "--delete", "comment")


# --- notification + metrics + acme ------------------------------------------


def notification_lifecycle(r: Runner) -> None:
    """Endpoint and matcher round-trips for all four endpoint types.

    Every endpoint is created disabled and aimed at an address that is never
    contacted, so no mail, webhook, or push ever leaves the host. `target test`
    is the one verb that deliberately does try to deliver: it is driven against
    the sendmail endpoint, whose delivery is local, and recorded as a skip if
    the appliance has no MTA configured.
    """
    print(BOLD("notification: endpoint + matcher round-trips (all disabled)"))
    r.step("infra", "notification endpoint gotify add", f"gotify add {GOTIFY_ENDPOINT}",
           "pbs", "notification", "endpoint", "gotify", "add", GOTIFY_ENDPOINT,
           "--server", f"https://{DUMMY_HOST}", "--token", "pmx-cli-e2e",
           "--disable", "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "notification endpoint gotify update", f"gotify update {GOTIFY_ENDPOINT}",
               "pbs", "notification", "endpoint", "gotify", "update", GOTIFY_ENDPOINT,
               "--comment", "pmx-cli e2e (updated)")
        _matcher_roundtrip(r, GOTIFY_ENDPOINT)
    finally:
        r.del_step("infra", "notification endpoint gotify delete",
                   f"gotify delete {GOTIFY_ENDPOINT}",
                   "pbs", "notification", "endpoint", "gotify", "delete",
                   GOTIFY_ENDPOINT, "-y")

    r.step("infra", "notification endpoint sendmail add", f"sendmail add {SENDMAIL_ENDPOINT}",
           "pbs", "notification", "endpoint", "sendmail", "add", SENDMAIL_ENDPOINT,
           "--mailto", "root@localhost", "--disable", "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "notification endpoint sendmail update",
               f"sendmail update {SENDMAIL_ENDPOINT}",
               "pbs", "notification", "endpoint", "sendmail", "update", SENDMAIL_ENDPOINT,
               "--comment", "pmx-cli e2e (updated)")
        # An endpoint has to be enabled for the server to attempt delivery.
        r.undo("enable sendmail endpoint for the delivery test",
               "pbs", "notification", "endpoint", "sendmail", "update",
               SENDMAIL_ENDPOINT, "--delete", "disable")
        r.soft_step("infra", "notification target test", f"target test {SENDMAIL_ENDPOINT}",
                    "pbs", "notification", "target", "test", SENDMAIL_ENDPOINT,
                    skip_markers=("sendmail", "mail", "no such file", "exit code",
                                  "failed to execute", "not found"),
                    skip_reason="the appliance has no working local MTA to deliver through")
    finally:
        r.del_step("infra", "notification endpoint sendmail delete",
                   f"sendmail delete {SENDMAIL_ENDPOINT}",
                   "pbs", "notification", "endpoint", "sendmail", "delete",
                   SENDMAIL_ENDPOINT, "-y")

    r.step("infra", "notification endpoint smtp add", f"smtp add {SMTP_ENDPOINT}",
           "pbs", "notification", "endpoint", "smtp", "add", SMTP_ENDPOINT,
           "--server", DUMMY_HOST, "--from-address", "pmx-cli@example.invalid",
           "--mailto", "root@localhost", "--mode", "insecure",
           "--disable", "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "notification endpoint smtp update", f"smtp update {SMTP_ENDPOINT}",
               "pbs", "notification", "endpoint", "smtp", "update", SMTP_ENDPOINT,
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("infra", "notification endpoint smtp delete", f"smtp delete {SMTP_ENDPOINT}",
                   "pbs", "notification", "endpoint", "smtp", "delete", SMTP_ENDPOINT, "-y")

    r.step("infra", "notification endpoint webhook add", f"webhook add {WEBHOOK_ENDPOINT}",
           "pbs", "notification", "endpoint", "webhook", "add", WEBHOOK_ENDPOINT,
           "--url", f"https://{DUMMY_HOST}/hook", "--method", "post",
           "--disable", "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "notification endpoint webhook update", f"webhook update {WEBHOOK_ENDPOINT}",
               "pbs", "notification", "endpoint", "webhook", "update", WEBHOOK_ENDPOINT,
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("infra", "notification endpoint webhook delete",
                   f"webhook delete {WEBHOOK_ENDPOINT}",
                   "pbs", "notification", "endpoint", "webhook", "delete",
                   WEBHOOK_ENDPOINT, "-y")


def _matcher_roundtrip(r: Runner, target: str) -> None:
    """Create, update, and delete a matcher pointed at a disabled endpoint."""
    r.step("infra", "notification matcher add", f"matcher add {NOTIFY_MATCHER}",
           "pbs", "notification", "matcher", "add", NOTIFY_MATCHER,
           "--target", target, "--match-severity", "error",
           "--disable", "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "notification matcher update", f"matcher update {NOTIFY_MATCHER}",
               "pbs", "notification", "matcher", "update", NOTIFY_MATCHER,
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("infra", "notification matcher delete", f"matcher delete {NOTIFY_MATCHER}",
                   "pbs", "notification", "matcher", "delete", NOTIFY_MATCHER, "-y")


def metrics_lifecycle(r: Runner) -> None:
    """InfluxDB UDP and HTTP metric-server round-trips, both created disabled.

    Both are created disabled and aimed at the appliance's own loopback, where
    nothing listens: PBS reaches out to an *enabled* HTTP metric server while
    the create request is still open, and an unroutable address makes that block
    until the client gives up — a refused loopback connection fails instantly
    and sends nothing anywhere.
    """
    print(BOLD("metrics: influxdb udp/http round-trips (disabled, loopback target)"))
    r.step("infra", "metrics influxdb-udp add", f"influxdb-udp add {INFLUX_UDP}",
           "pbs", "metrics", "influxdb-udp", "add", INFLUX_UDP,
           "--host", "127.0.0.1:8089", "--enable=false", "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "metrics influxdb-udp update", f"influxdb-udp update {INFLUX_UDP}",
               "pbs", "metrics", "influxdb-udp", "update", INFLUX_UDP, "--mtu", "1400",
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("infra", "metrics influxdb-udp delete", f"influxdb-udp delete {INFLUX_UDP}",
                   "pbs", "metrics", "influxdb-udp", "delete", INFLUX_UDP, "-y")

    r.step("infra", "metrics influxdb-http add", f"influxdb-http add {INFLUX_HTTP}",
           "pbs", "metrics", "influxdb-http", "add", INFLUX_HTTP,
           "--url", "http://127.0.0.1:8086", "--organization", "pmx-cli",
           "--bucket", "pmx-cli", "--enable=false", "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "metrics influxdb-http update", f"influxdb-http update {INFLUX_HTTP}",
               "pbs", "metrics", "influxdb-http", "update", INFLUX_HTTP,
               "--bucket", "pmx-cli-2", "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("infra", "metrics influxdb-http delete", f"influxdb-http delete {INFLUX_HTTP}",
                   "pbs", "metrics", "influxdb-http", "delete", INFLUX_HTTP, "-y")


def acme_lifecycle(r: Runner) -> None:
    """ACME challenge-plugin round-trip; the account verbs stay out.

    A DNS-01 plugin is pure local configuration — nothing is contacted when it
    is written. Registering an ACME *account*, by contrast, creates a real
    identity at a real CA and is rate-limited there, so those three verbs are
    recorded as skips instead.
    """
    print(BOLD("acme: dns-01 challenge plugin round-trip"))
    r.step("infra", "acme plugin add", f"acme plugin add {ACME_PLUGIN}",
           "pbs", "acme", "plugin", "add", ACME_PLUGIN, "--type", "dns",
           "--api", "acmedns", "--data", ACME_PLUGIN_DATA, "--validation-delay", "30")
    try:
        r.step("infra", "acme plugin update", f"acme plugin update {ACME_PLUGIN}",
               "pbs", "acme", "plugin", "update", ACME_PLUGIN, "--validation-delay", "60")
    finally:
        r.del_step("infra", "acme plugin delete", f"acme plugin delete {ACME_PLUGIN}",
                   "pbs", "acme", "plugin", "delete", ACME_PLUGIN, "-y")

    for verb in ("acme account add", "acme account update", "acme account delete"):
        r.cover_skip("infra", verb, verb,
                     "registers a real account at a real ACME CA, which is rate-limited "
                     "and leaves state outside this lab")


# --- node -------------------------------------------------------------------


def node_config_lifecycle(r: Runner) -> None:
    """Round-trip the host-level config verbs that cannot cut the host off.

    Each one reads the current value, writes a benign change, and restores what
    was there — so a completed run leaves the appliance byte-identical. The
    verbs that *can* cut the host off (network apply, reboot, shutdown) or that
    reach outside the lab (ACME order/renew, subscription set) are recorded as
    skips with the reason instead.
    """
    print(BOLD("node: dns/time/config/apt/tasks round-trips"))

    # --- DNS: rewrite the servers the host already has.
    dns = r.pmx("pbs", "node", "dns", "show", json_out=True)
    args = ["pbs", "node", "dns", "update"]
    if dns.rc == 0:
        try:
            d = dns.json()
            d = d.get("data", d) if isinstance(d, dict) else {}
        except ValueError:
            d = {}
        for key, flag in (("dns1", "--dns1"), ("dns2", "--dns2"), ("dns3", "--dns3"),
                          ("search", "--search")):
            if str(d.get(key) or "").strip():
                args += [flag, str(d[key]).strip()]
    if len(args) > 4:
        r.step("node", "node dns update", "dns update (same servers)", *args)
    else:
        r.cover_skip("node", "node dns update", "node dns update",
                     "host has no resolver configured to rewrite identically")

    # --- time: rewrite the timezone the host already runs in.
    tz = r.pmx("pbs", "node", "time", "show", json_out=True)
    zone = ""
    if tz.rc == 0:
        try:
            t = tz.json()
            t = t.get("data", t) if isinstance(t, dict) else {}
            # The API returns the zone with the trailing newline it read from
            # /etc/timezone; writing that back fails the parameter's regex.
            zone = str(t.get("timezone", "") or "").strip()
        except ValueError:
            zone = ""
    if zone:
        r.step("node", "node time update", f"time update (same zone {zone})",
               "pbs", "node", "time", "update", "--timezone", zone)
    else:
        r.cover_skip("node", "node time update", "node time update",
                     "host did not report a timezone to rewrite identically")

    # --- node config: set a description, then reset it.
    r.step("node", "node config update", "config update description",
           "pbs", "node", "config", "update", "--description", "pmx-cli e2e")
    r.undo("reset node description",
           "pbs", "node", "config", "update", "--delete", "description")

    # --- apt: refreshing the package index changes nothing on disk but the lists.
    r.step("node", "node apt update", "apt update (refresh package index)",
           "pbs", "node", "apt", "update", "--quiet")
    _apt_repo_roundtrip(r)

    # --- tasks: delete the log of a task this very run produced.
    _task_log_delete(r)

    # --- subscription: this lab has none, so update/delete are no-ops that
    # still prove the verb reaches the API. `set` needs a real purchased key.
    r.step("node", "node subscription update", "subscription update (no key installed)",
           "pbs", "node", "subscription", "update")
    # With no key installed there is no subscription file to unlink, and PBS
    # reports the missing file rather than treating the delete as a no-op.
    r.soft_step("node", "node subscription delete", "subscription delete",
                "pbs", "node", "subscription", "delete", "-y",
                skip_markers=("no such file", "os error 2"),
                skip_reason="the appliance has no subscription installed, so there is "
                            "no key file to remove")
    r.cover_skip("node", "node subscription set", "node subscription set",
                 "needs a real purchased subscription key, which the lab has none of")

    for verb, why in (
        ("node reboot", "would take the shared e2e stack server down mid-suite"),
        ("node shutdown", "would take the shared e2e stack server down mid-suite"),
        ("node certificates acme order",
         "orders a real certificate from a real ACME CA, which is rate-limited"),
        ("node certificates acme renew",
         "renews against a real ACME CA, which is rate-limited"),
        ("node certificates custom upload",
         "replaces the API certificate and restarts the proxy, which would break "
         "the fingerprint this context pins"),
        ("node certificates custom delete",
         "removes the API certificate and restarts the proxy, which would break "
         "the fingerprint this context pins"),
    ):
        r.cover_skip("node", verb, verb, why)


def _apt_repo_roundtrip(r: Runner) -> None:
    """Add the no-subscription repo if absent, then flip one entry's enabled bit.

    `repo-add` takes a standard handle, so the only thing it can write is a
    repository Proxmox itself ships. `repo-update` is then driven against that
    entry and restored to the state it had.
    """
    handle = "no-subscription"
    before = _repo_entry(r, handle)
    if before is None:
        r.step("node", "node apt repo-add", f"apt repo-add {handle}",
               "pbs", "node", "apt", "repo-add", "--handle", handle)
        after = _repo_entry(r, handle)
    else:
        r.cover_skip("node", "node apt repo-add", "node apt repo-add",
                     f"the {handle} repository is already configured on this host")
        after = before
    if after is None:
        r.cover_skip("node", "node apt repo-update", "node apt repo-update",
                     f"could not locate the {handle} repository entry to toggle")
        return
    path, index, enabled = after
    r.step("node", "node apt repo-update", f"apt repo-update {handle} (toggle enabled)",
           "pbs", "node", "apt", "repo-update", "--path", path, "--index", str(index),
           *(["--enabled"] if not enabled else []))
    # Restore the bit we flipped.
    r.undo(f"restore {handle} enabled={enabled}",
           "pbs", "node", "apt", "repo-update", "--path", path, "--index", str(index),
           *(["--enabled"] if enabled else []))


def _repo_entry(r: Runner, handle: str) -> tuple[str, int, bool] | None:
    """Locate a configured APT repository by its standard handle.

    Returns (sources-file path, index within that file, enabled) or None. The
    listing nests repositories inside their files, and the handle only appears
    on the `standard-repos` side, so the match is made on the URL suffix the
    handle corresponds to.
    """
    res = r.pmx("pbs", "node", "apt", "repositories", json_out=True)
    if res.rc != 0:
        return None
    try:
        data = res.json()
    except ValueError:
        return None
    if isinstance(data, dict):
        data = data.get("data", data)
    if not isinstance(data, dict):
        return None
    for f in data.get("files", []) or []:
        if not isinstance(f, dict):
            continue
        path = str(f.get("path", "") or "")
        for i, repo in enumerate(f.get("repositories", []) or []):
            if not isinstance(repo, dict):
                continue
            uris = repo.get("URIs", repo.get("uris", [])) or []
            comps = repo.get("Components", repo.get("components", [])) or []
            if any(handle in str(c) for c in comps) and any("proxmox" in str(u) for u in uris):
                enabled = repo.get("Enabled", repo.get("enabled", True))
                return path, i, bool(enabled)
    return None


def _task_log_delete(r: Runner) -> None:
    """Delete the task log of a finished task this run produced.

    Only tasks the suite itself started are eligible: the UPID is matched
    against the scratch datastore, so an operator's task log can never be the
    one that gets removed.
    """
    res = r.pmx("pbs", "node", "tasks", "ls", "--store", DATASTORE, "--limit", "20",
                json_out=True)
    upid = ""
    if res.rc == 0:
        try:
            rows = res.json()
        except ValueError:
            rows = []
        if isinstance(rows, dict):
            rows = rows.get("data", rows.get("rows", []))
        for t in rows if isinstance(rows, list) else []:
            if not isinstance(t, dict):
                continue
            cand = str(t.get("upid", "") or "")
            # The --store filter above already confines this to the scratch
            # datastore's own tasks, so no name matching is needed here — and a
            # UPID escapes the dashes in the store id, so matching on the plain
            # name would never hit. A still-running task has no endtime and
            # cannot have its log removed.
            if cand and t.get("endtime") is not None:
                upid = cand
                break
    if not upid:
        r.cover_skip("node", "node tasks delete", "node tasks delete",
                     f"no finished task against {DATASTORE} to remove the log of")
        return
    r.step("node", "node tasks delete", "tasks delete (own scratch-store task log)",
           "pbs", "node", "tasks", "delete", upid)


def node_services_lifecycle(r: Runner) -> None:
    """start/stop/restart/reload against an inert service, state restored.

    The service is picked from what the appliance actually runs (see
    SAFE_SERVICES) rather than assumed, and whatever state it was in before the
    block is what it is left in. The order — stop, start, reload, restart —
    leaves it running, matching the state it was picked in.
    """
    svc = _pick_safe_service(r)
    if not svc:
        for v in ("node services start", "node services stop",
                  "node services restart", "node services reload"):
            r.cover_skip("node", v, v,
                         "the appliance runs none of the services this suite is willing "
                         "to cycle (" + ", ".join(SAFE_SERVICES) + "); the rest carry the "
                         "API, SSH, or the logs the run reads")
        return
    print(BOLD(f"node: services start/stop/restart/reload on {svc}"))
    try:
        r.step("node", "node services stop", f"services stop {svc}",
               "pbs", "node", "services", "stop", svc)
        r.step("node", "node services start", f"services start {svc}",
               "pbs", "node", "services", "start", svc)
        r.step("node", "node services reload", f"services reload {svc}",
               "pbs", "node", "services", "reload", svc)
        r.step("node", "node services restart", f"services restart {svc}",
               "pbs", "node", "services", "restart", svc)
    finally:
        r.undo(f"ensure {svc} is running again",
               "pbs", "node", "services", "start", svc)


def _pick_safe_service(r: Runner) -> str:
    """Return the first SAFE_SERVICES entry the appliance reports as running.

    Running, not merely listed: PBS reports a unit that is not installed as
    "dead" too, and `systemctl start` on one of those fails outright.
    """
    res = r.pmx("pbs", "node", "services", "ls", json_out=True)
    if res.rc != 0:
        return ""
    try:
        rows = res.json()
    except ValueError:
        return ""
    if isinstance(rows, dict):
        rows = rows.get("data", rows.get("rows", []))
    running = {str(s.get("service") or s.get("name") or "")
               for s in (rows if isinstance(rows, list) else [])
               if isinstance(s, dict) and s.get("state") == "running"}
    for svc in SAFE_SERVICES:
        if svc in running:
            return svc
    return ""


def node_network_lifecycle(r: Runner) -> None:
    """create → update → delete a dummy interface, then revert the staged config.

    PBS stages interface changes in `interfaces.new` and only touches the live
    configuration on `apply`. Everything here therefore writes to the staging
    file and the block ends with `revert`, which discards it — the host's
    networking is never reconfigured. `network apply` is the one verb that would
    commit, and it stays out.
    """
    print(BOLD("node: network create/update/delete staged, then reverted"))
    # Writing the interface configuration is root-only on PBS; an API token,
    # whatever its ACL, is refused. That is a server-side rule the suite cannot
    # work around without running as root@pam, so the three writes are recorded
    # as skips when it bites — the revert below still runs either way.
    rootonly = ("permission check failed", "only root", "403")
    reason = ("the interface configuration is root-only on PBS; an API token "
              "cannot write it")
    try:
        staged = r.soft_step(
            "node", "node network create", f"network create {NET_IFACE}",
            "pbs", "node", "network", "create", NET_IFACE, "--type", "bridge",
            "--method", "manual", "--comments", "pmx-cli e2e (staged, never applied)",
            skip_markers=rootonly, skip_reason=reason)
        if staged:
            r.step("node", "node network update", f"network update {NET_IFACE}",
                   "pbs", "node", "network", "update", NET_IFACE, "--mtu", "1400",
                   "--comments", "pmx-cli e2e (staged, updated)")
            r.step("node", "node network delete", f"network delete {NET_IFACE}",
                   "pbs", "node", "network", "delete", NET_IFACE, "-y")
        else:
            for v in ("node network update", "node network delete"):
                r.cover_skip("node", v, v, reason)
    finally:
        # Discards the staging file whatever happened above, so the appliance
        # keeps the live configuration it booted with.
        r.soft_step("node", "node network revert", "network revert (discard staged config)",
                    "pbs", "node", "network", "revert", "-y",
                    skip_markers=rootonly, skip_reason=reason)
    r.cover_skip("node", "node network apply", "node network apply",
                 "commits the staged interface configuration to the live host, which can "
                 "sever the suite's own connection to it")


def node_disks_lifecycle(r: Runner) -> None:
    """initgpt / directory create+delete / zfs create on one spare disk.

    The disk is hard-asserted unused — first from the API's own `used` field,
    then from a host-side wipefs/holders/zpool probe — before a byte is written,
    exactly as the PVE suite does. Nothing is created *as a datastore*
    (`--add-datastore` is never passed), so a failed teardown cannot leave a
    datastore pointing at a wiped disk.
    """
    print(BOLD("node: disks initgpt/directory/zfs on a spare disk"))
    verbs = ("node disks initgpt", "node disks directory create",
             "node disks directory delete", "node disks zfs create", "node disks wipe")

    def skip_all(why: str) -> None:
        for v in verbs:
            r.cover_skip("node", v, v, why)

    host = _resolve_host(r)
    if not host or _ssh_node(host, "true")[0] != 0:
        skip_all("PBS host not reachable over root SSH for the spare-disk safety checks")
        return
    dev = _spare_disk(r)
    if not dev:
        skip_all("no unused disk on the PBS host to run the destructive disk verbs "
                 "against; attach a scratch disk to cover them")
        return
    short = dev.rsplit("/", 1)[-1]
    probe = (f"( test -z \"$(lsblk -no FSTYPE {dev} 2>/dev/null | tr -d '[:space:]')\" "
             f"&& test -z \"$(ls /sys/block/{short}/holders/ 2>/dev/null)\" "
             f"&& test -z \"$(ls -d /sys/block/{short}/{short}* 2>/dev/null | grep -v '{short}$')\" "
             f"&& ! zpool status 2>/dev/null | grep -q {short} "
             f"&& echo SPARE_OK ) || echo SPARE_DIRTY")
    pr = _ssh_node(host, probe)
    if "SPARE_OK" not in (pr[1] + pr[2]):
        skip_all(f"spare disk {dev} failed the host-side cleanliness probe")
        return

    print(DIM(f"  device={dev}"))
    dir_name = PREFIX + "dir"
    zfs_name = PREFIX + "zfs"
    try:
        r.step("node", "node disks initgpt", f"initgpt {short}",
               "pbs", "node", "disks", "initgpt", "--disk", short)
        r.step("node", "node disks directory create", f"directory create {dir_name}",
               "pbs", "node", "disks", "directory", "create", dir_name,
               "--disk", short, "--filesystem", "ext4")
        r.del_step("node", "node disks directory delete", f"directory delete {dir_name}",
                   "pbs", "node", "disks", "directory", "delete", dir_name, "-y")
        # A directory delete leaves the partition in place; wipe is what returns
        # the disk to a state zfs create will accept.
        r.step("node", "node disks wipe", f"wipe {short}",
               "pbs", "node", "disks", "wipe", "--disk", short, "-y")
        # A stock Debian cloud kernel ships no ZFS module, and the PBS packages
        # do not pull one in — so on a stack appliance built that way the pool
        # cannot be created however clean the disk is. The check is made on the
        # host rather than from the failure: PBS reports only "Error during
        # 'zpool create', see task log for more details", and the reason stays
        # in the task log where a skip message cannot reach it.
        if _ssh_node(host, "modprobe zfs")[0] != 0:
            r.cover_skip("node", "node disks zfs create", "node disks zfs create",
                         "the appliance's kernel has no ZFS module, so zpool create "
                         "cannot run there")
        else:
            r.step("node", "node disks zfs create", f"zfs create {zfs_name}",
                   "pbs", "node", "disks", "zfs", "create", zfs_name,
                   "--devices", short, "--raidlevel", "single")
    finally:
        _ssh_node(host, f"zpool destroy -f {zfs_name} 2>/dev/null; "
                        f"umount /mnt/datastore/{dir_name} 2>/dev/null; "
                        f"rm -f /etc/systemd/system/mnt-datastore-{dir_name}.mount; "
                        f"systemctl daemon-reload 2>/dev/null; "
                        f"zpool labelclear -f {dev} 2>/dev/null; "
                        f"sgdisk --zap-all {dev} >/dev/null 2>&1; "
                        f"wipefs -a {dev} >/dev/null 2>&1; "
                        f"partprobe {dev} 2>/dev/null; true", timeout=60)
        print(DIM(f"  spare {dev} restored to unused state"))


def _spare_disk(r: Runner) -> str:
    """Return the /dev path of an unused disk on the PBS host, or ""."""
    pinned = os.environ.get("PMX_E2E_PBS_SPARE_SERIAL", "")
    res = r.pmx("pbs", "node", "disks", "ls", "--skip-smart", json_out=True)
    if res.rc != 0:
        return ""
    try:
        rows = res.json()
    except ValueError:
        return ""
    if isinstance(rows, dict):
        rows = rows.get("data", rows.get("rows", []))
    for d in rows if isinstance(rows, list) else []:
        if not isinstance(d, dict):
            continue
        if pinned and d.get("serial") != pinned:
            continue
        # PBS spells an idle disk `used: "unused"`, where PVE leaves the field
        # empty. Anything else ("mounted", "LVM", "ZFS", …) is in service.
        if str(d.get("used") or "").lower() not in ("", "0", "unused"):
            continue
        dev = str(d.get("devpath") or "")
        if dev.startswith("/dev/"):
            return dev
    return ""


TAPE_CHANGER = PREFIX + "changer"
TAPE_DRIVE = PREFIX + "drive"
TAPE_POOL = PREFIX + "pool"
TAPE_JOB = PREFIX + "tapejob"
TAPE_KEY_PASSWORD = "pmx-cli-e2e-tape-key"

# Everything a tape operation needs a real drive, changer, or cartridge for.
# Listed individually so the coverage table names exactly what tape hardware
# would unlock, rather than hiding it behind one blanket note.
TAPE_HARDWARE_VERBS = (
    "tape backup", "tape restore", "tape job run",
    "tape changer transfer",
    "tape drive barcode-label", "tape drive catalog", "tape drive clean",
    "tape drive eject", "tape drive export", "tape drive format",
    "tape drive inventory", "tape drive label", "tape drive load-media",
    "tape drive load-slot", "tape drive restore-key", "tape drive rewind",
    "tape drive unload", "tape drive update-inventory",
    "tape media destroy", "tape media move", "tape media set-status",
)


def tape_lifecycle(r: Runner) -> None:
    """Round-trip the tape verbs that need no hardware, and name the ones that do.

    A media pool and an encryption key are pure configuration and are exercised
    live. A *changer* or *drive* is not, despite also being config: PBS opens
    the SCSI-generic path when the entry is written and rejects anything that is
    not an LTO device, so those two cannot be created against a synthetic path —
    and a backup job, which must name a drive, is blocked behind them.
    """
    print(BOLD("tape: media-pool and key round-trips; hardware-gated verbs recorded"))
    _tape_pool_roundtrip(r)
    _tape_key_roundtrip(r)

    device = ("needs a real LTO device: PBS opens the SCSI-generic path when the entry "
              "is written and refuses a path that is not a tape device")
    for v in ("tape changer add", "tape changer update", "tape changer delete",
              "tape drive add", "tape drive update", "tape drive delete"):
        r.cover_skip("tape", v, v, device)
    for v in ("tape job add", "tape job update", "tape job delete"):
        r.cover_skip("tape", v, v,
                     "a tape backup job must name a configured drive, and a drive "
                     "cannot be configured without real LTO hardware")

    why = ("needs a tape drive, changer, or cartridge attached to the host; this lab "
           "has no tape hardware and PBS has no virtual-library mode to stand in for it")
    for v in TAPE_HARDWARE_VERBS:
        r.cover_skip("tape", v, v, why)


def _tape_pool_roundtrip(r: Runner) -> None:
    """Media-pool config round-trip. A pool names no device, so it needs none."""
    r.step("tape", "tape pool add", f"pool add {TAPE_POOL}",
           "pbs", "tape", "pool", "add", TAPE_POOL, "--allocation", "continue",
           "--retention", "overwrite", "--comment", "pmx-cli e2e")
    try:
        r.step("tape", "tape pool update", f"pool update {TAPE_POOL}",
               "pbs", "tape", "pool", "update", TAPE_POOL,
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("tape", "tape pool delete", f"pool delete {TAPE_POOL}",
                   "pbs", "tape", "pool", "delete", TAPE_POOL, "-y")


def _tape_key_roundtrip(r: Runner) -> None:
    """Create a tape encryption key, change its passphrase, then delete it.

    The key is identified by the fingerprint PBS derives when it is created, so
    the id is read back from the listing rather than chosen. `kdf=none` keeps
    creation instant; the update then sets a real passphrase, which is the path
    that actually re-wraps the key material.
    """
    before = _tape_key_fingerprints(r)
    r.step("tape", "tape key add", "tape key add",
           "pbs", "tape", "key", "add", "--password", TAPE_KEY_PASSWORD,
           "--hint", "pmx-cli e2e")
    fp = ""
    for cand in _tape_key_fingerprints(r):
        if cand not in before:
            fp = cand
            break
    if not fp:
        r.cover_skip("tape", "tape key update", "tape key update",
                     "could not identify the fingerprint of the key just created")
        r.cover_skip("tape", "tape key delete", "tape key delete",
                     "could not identify the fingerprint of the key just created")
        return
    try:
        r.step("tape", "tape key update", f"key update {fp[:16]}…",
               "pbs", "tape", "key", "update", fp, "--password", TAPE_KEY_PASSWORD,
               "--new-password", TAPE_KEY_PASSWORD + "-2", "--hint", "pmx-cli e2e (updated)")
    finally:
        r.del_step("tape", "tape key delete", f"key delete {fp[:16]}…",
                   "pbs", "tape", "key", "delete", fp, "-y")


def _tape_key_fingerprints(r: Runner) -> set[str]:
    res = r.pmx("pbs", "tape", "key", "ls", json_out=True)
    if res.rc != 0:
        return set()
    try:
        rows = res.json()
    except ValueError:
        return set()
    if isinstance(rows, dict):
        rows = rows.get("data", rows.get("rows", []))
    return {str(k["fingerprint"]) for k in rows
            if isinstance(k, dict) and k.get("fingerprint")}


# --- entry point ------------------------------------------------------------


def teardown(r: Runner) -> None:
    """Idempotent teardown: everything the run could have created, in order."""
    print(BOLD("teardown: scratch datastore and any residue"))
    sweep_stale(r)


def run(context: str, binary: str | None, build: bool, strict: bool) -> int:
    bin_path = find_binary(binary, build=build)
    ok, why = target_configured(bin_path, context)
    if not ok:
        msg = f"context {context!r} not usable: {why}"
        if strict:
            print(f"pbs-lifecycle: error: {msg}", file=sys.stderr)
            return 3
        print(f"pbs-lifecycle: skipping — {msg}")
        return 0

    # A PBS context addresses one appliance; there is no --node to inject.
    r = Runner(bin_path, context, node="")
    probe = r.pmx("pbs", "datastore", "ls", json_out=True)
    if probe.rc != 0:
        msg = f"PBS at context {context!r} not reachable: {_err(probe, 'unknown error')}"
        if strict:
            print(f"pbs-lifecycle: error: {msg}", file=sys.stderr)
            return 3
        print(f"pbs-lifecycle: skipping — {msg}")
        return 0

    print(BOLD(f"pbs-lifecycle: context={context}"))
    print(DIM(f"  isolation: name-prefix={PREFIX} datastore={DATASTORE} "
              f"path={DATASTORE_PATH}"))
    print()

    failed = False
    started = time.monotonic()
    try:
        sweep_stale(r)
        print()
        datastore_lifecycle(r)
        print()
        # One scoped credential serves both the backup client and the
        # self-referential remote; it is revoked in the finally block.
        authid, secret = mint_work_token(r)
        snap_id, group_id = stage_backup_data(r, authid, secret)
        print()
        gc_prune_verify_lifecycle(r)
        print()
        remote_sync_lifecycle(r, authid, secret)
        print()
        # Destructive to the staged data, so it runs after everything that
        # wants that data present.
        snapshot_group_lifecycle(r, snap_id, group_id)
        print()
        access_lifecycle(r)
        print()
        realm_lifecycle(r)
        print()
        notification_lifecycle(r)
        print()
        metrics_lifecycle(r)
        print()
        acme_lifecycle(r)
        print()
        node_config_lifecycle(r)
        print()
        node_services_lifecycle(r)
        print()
        node_network_lifecycle(r)
        print()
        node_disks_lifecycle(r)
        print()
        tape_lifecycle(r)
        print()
    except LifecycleError as exc:
        failed = True
        print(RED(f"pbs-lifecycle: aborted at step: {exc}"))
    except KeyboardInterrupt:
        failed = True
        print(RED("pbs-lifecycle: interrupted"))
    finally:
        print()
        drop_work_token(r)
        # The datastore delete is both teardown and the coverage target.
        r.del_step("datastore", "datastore delete", f"datastore delete {DATASTORE}",
                   "pbs", "datastore", "delete", DATASTORE, "--destroy-data", "-y")
        teardown(r)

    print()
    _print_coverage(r)
    if any(s.status == FAIL for s in r.cov):
        failed = True

    dur = time.monotonic() - started
    print()
    if failed:
        print(BOLD("pbs-lifecycle: ") + RED("FAILED") + DIM(f"  ({dur:.0f}s)"))
        return 1
    print(BOLD("pbs-lifecycle: ") + GREEN("PASSED") + DIM(f"  ({dur:.0f}s)"))
    return 0

