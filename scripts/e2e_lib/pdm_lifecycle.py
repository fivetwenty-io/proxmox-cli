"""Destructive lifecycle suite for Proxmox Datacenter Manager.

The read-only `pdm` tree defers every mutating verb; this is the counterpart
that drives them, as `lifecycle.py` does for PVE and `pbs_lifecycle.py` for PBS.
Each verb is recorded individually so the run prints a coverage table proving
the deferred operation was exercised against a real PDM.

Isolation — a PDM manages *other* people's clusters, so the contract is about
what the suite is allowed to reach:

  * every object it creates is named with the `pmx-cli-` prefix (users, tokens,
    realms, views, remotes, ACME plugins, auto-install answers and tokens),
  * nothing is written *through* a managed remote unless the run created the
    target itself: no guest on a registered cluster is started, stopped,
    snapshotted, or migrated, and no subscription key is applied to a remote's
    node,
  * outbound-facing objects point at unroutable TEST-NET addresses,
  * the node-level verbs that would take the appliance off the network (network
    apply, reboot, shutdown) or reach a real CA (ACME order/renew) are recorded
    as skips naming what they would do.

Teardown runs in a `finally` block and is idempotent, so a crashed prior run is
swept clean by the next one.
"""

from __future__ import annotations

import base64
import sys
import time

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
USER = PREFIX + "user@pdm"
TOKEN = PREFIX + "tok"
ACL_PATH = "/resource"
ACL_ROLE = "Auditor"
REALM_LDAP = PREFIX + "ldap"
REALM_AD = PREFIX + "ad"
REALM_OIDC = PREFIX + "oidc"
VIEW = PREFIX + "view"
ACME_PLUGIN = PREFIX + "acme"
# The plugin's credentials, base64 with padding as the API requires. They point
# at an unroutable TEST-NET address and are only read when a certificate is
# ordered, which this suite never does.
ACME_PLUGIN_DATA = base64.b64encode(
    b"ACMEDNS_BASE_URL=https://192.0.2.10\n"
    b"ACMEDNS_USERNAME=pmx-cli\n"
    b"ACMEDNS_PASSWORD=pmx-cli-e2e\n").decode()
REMOTE = PREFIX + "remote"
AUTOINSTALL_ANSWER = PREFIX + "answer"
AUTOINSTALL_TOKEN = PREFIX + "aitok"
NET_IFACE = "pmxcli0"           # staged-only dummy interface, never applied
# A syntactically valid Proxmox key that is registered to nobody. PDM stores it
# and reports status "notfound"; it is never applied to a managed node.
FAKE_SUB_KEY = "pve4c-0000000000"
DUMMY_HOST = "192.0.2.10"       # TEST-NET-1 (RFC 5737); never contacted
DUMMY_TOKEN = "00000000-0000-0000-0000-000000000000"
# PDM addresses its own appliance as this node name; `node ls` is not readable
# with a plain token, so the name is not discovered from the API.
NODE = "localhost"


def _err(res: Cmd, fallback: str) -> str:
    from .text import one_line
    return one_line(res.reason, fallback, limit=120)


# --- sweep / teardown -------------------------------------------------------


def sweep_stale(r: Runner) -> None:
    """Remove anything a crashed prior run left behind. Every id is prefixed."""
    print(BOLD("sweep: stale pmx-cli objects from a prior run"))
    r.undo(f"acl {ACL_PATH}", "pdm", "acl", "update", "--path", ACL_PATH,
           "--role", ACL_ROLE, "--auth-id", USER, "--delete")
    r.undo(f"token {USER}!{TOKEN}", "pdm", "token", "delete", USER, TOKEN, "-y")
    r.undo(f"user {USER}", "pdm", "user", "delete", USER, "-y")
    for kind, name in (("ldap", REALM_LDAP), ("ad", REALM_AD), ("openid", REALM_OIDC)):
        r.undo(f"realm {kind} {name}", "pdm", "realm", kind, "delete", name, "-y")
    r.undo(f"view {VIEW}", "pdm", "config", "view", "delete", VIEW, "-y")
    r.undo(f"acme plugin {ACME_PLUGIN}", "pdm", "config", "acme", "plugin", "delete",
           ACME_PLUGIN, "-y")
    r.undo(f"remote {REMOTE}", "pdm", "remote", "delete", REMOTE, "-y")
    r.undo(f"auto-install answer {AUTOINSTALL_ANSWER}",
           "pdm", "auto-install", "prepared", "delete", AUTOINSTALL_ANSWER, "-y")
    r.undo(f"auto-install token {AUTOINSTALL_TOKEN}",
           "pdm", "auto-install", "token", "delete", AUTOINSTALL_TOKEN, "-y")
    r.undo(f"subscription key {FAKE_SUB_KEY}",
           "pdm", "subscription", "key", "delete", FAKE_SUB_KEY, "-y")
    r.undo("pending subscription changes",
           "pdm", "subscription", "clear-pending", "-y")


# --- access -----------------------------------------------------------------


def access_lifecycle(r: Runner) -> None:
    """User, token, and ACL round-trips, all prefix-isolated.

    The ACL grants read-only Auditor on the resource tree and is revoked before
    the user goes away, so the throwaway identity never outlives its permission.
    """
    print(BOLD("access: user/token/acl round-trips"))
    r.step("access", "user add", f"user add {USER}",
           "pdm", "user", "add", USER, "--comment", "pmx-cli e2e",
           "--email", "pmx-cli@example.invalid")
    try:
        r.step("access", "user update", f"user update {USER}",
               "pdm", "user", "update", USER, "--comment", "pmx-cli e2e (updated)")

        r.step("access", "token add", f"token add {USER}!{TOKEN}",
               "pdm", "token", "add", USER, TOKEN, "--comment", "pmx-cli e2e")
        try:
            r.step("access", "token update", f"token update {USER}!{TOKEN}",
                   "pdm", "token", "update", USER, TOKEN,
                   "--comment", "pmx-cli e2e (updated)")
        finally:
            r.del_step("access", "token delete", f"token delete {USER}!{TOKEN}",
                       "pdm", "token", "delete", USER, TOKEN, "-y")

        r.step("access", "acl update", f"acl grant {ACL_ROLE} on {ACL_PATH}",
               "pdm", "acl", "update", "--path", ACL_PATH, "--role", ACL_ROLE,
               "--auth-id", USER)
        r.step("access", "acl update revoke", f"acl revoke {ACL_ROLE} on {ACL_PATH}",
               "pdm", "acl", "update", "--path", ACL_PATH, "--role", ACL_ROLE,
               "--auth-id", USER, "--delete")

        # The TFA verbs act on an enrolled second factor, and enrolling one is an
        # interactive exchange (a TOTP confirmation or a WebAuthn ceremony) that
        # no unattended run can complete.
        for verb in ("tfa update", "tfa delete"):
            r.cover_skip("access", verb, verb,
                         "needs an enrolled second factor, and enrolment is an "
                         "interactive TOTP/WebAuthn exchange")
    finally:
        r.del_step("access", "user delete", f"user delete {USER}",
                   "pdm", "user", "delete", USER, "-y")


def realm_lifecycle(r: Runner) -> None:
    """LDAP/AD/OpenID realm round-trips plus the two built-in realm updates.

    PDM connects to the directory when an LDAP or AD realm is created, so those
    two need something to answer: a throwaway responder is staged on the
    appliance itself for the duration of the block (see e2e_lib.ldapstub) and
    killed afterwards. The OpenID realm's issuer is only contacted at login, so
    it points at an unroutable TEST-NET address. None of the three is ever made
    the default, so no login path changes.
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
           "pdm", "realm", "openid", "add", REALM_OIDC,
           "--issuer-url", f"https://{DUMMY_HOST}/", "--client-id", "pmx-cli-e2e",
           "--comment", "pmx-cli e2e")
    try:
        r.step("access", "realm openid update", f"realm openid update {REALM_OIDC}",
               "pdm", "realm", "openid", "update", REALM_OIDC,
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("access", "realm openid delete", f"realm openid delete {REALM_OIDC}",
                   "pdm", "realm", "openid", "delete", REALM_OIDC, "-y")

    _builtin_realm_comment(r, "pam")
    _builtin_realm_comment(r, "pdm")


def _ldap_ad_realms(r: Runner, port: int) -> None:
    """LDAP and AD realm round-trips against the staged responder.

    The AD realm passes no --base-dn: PDM derives it from the root DSE, which is
    exactly the code path the responder exists to satisfy.
    """
    r.step("access", "realm ldap add", f"realm ldap add {REALM_LDAP}",
           "pdm", "realm", "ldap", "add", REALM_LDAP, "--server1", "127.0.0.1",
           "--port", str(port), "--base-dn", ldapstub.BASE_DN, "--user-attr", "uid",
           "--comment", "pmx-cli e2e")
    try:
        r.step("access", "realm ldap update", f"realm ldap update {REALM_LDAP}",
               "pdm", "realm", "ldap", "update", REALM_LDAP,
               "--comment", "pmx-cli e2e (updated)")
        # The responder returns no entries for a user search, so a real sync
        # would see an empty directory; --dry-run keeps it from acting on that.
        r.step("access", "realm sync", f"realm sync {REALM_LDAP} --dry-run",
               "pdm", "realm", "sync", REALM_LDAP, "--dry-run")
    finally:
        r.del_step("access", "realm ldap delete", f"realm ldap delete {REALM_LDAP}",
                   "pdm", "realm", "ldap", "delete", REALM_LDAP, "-y")

    r.step("access", "realm ad add", f"realm ad add {REALM_AD}",
           "pdm", "realm", "ad", "add", REALM_AD, "--server1", "127.0.0.1",
           "--port", str(port), "--comment", "pmx-cli e2e")
    try:
        r.step("access", "realm ad update", f"realm ad update {REALM_AD}",
               "pdm", "realm", "ad", "update", REALM_AD,
               "--comment", "pmx-cli e2e (updated)")
    finally:
        r.del_step("access", "realm ad delete", f"realm ad delete {REALM_AD}",
                   "pdm", "realm", "ad", "delete", REALM_AD, "-y")


def _builtin_realm_comment(r: Runner, kind: str) -> None:
    """Set and restore the comment on a built-in realm (`pam` / `pdm`).

    Built-in realms cannot be created or deleted, only updated, and the comment
    is the one field with no behavioural effect — so it is written and then put
    back exactly as the server had it.
    """
    before = r.pmx("pdm", "realm", kind, "show", json_out=True)
    orig = ""
    if before.rc == 0:
        try:
            data = before.json()
            if isinstance(data, dict):
                orig = str(data.get("data", data).get("comment", "") or "")
        except (ValueError, AttributeError):
            orig = ""
    r.step("access", f"realm {kind} update", f"realm {kind} update comment",
           "pdm", "realm", kind, "update", "--comment", "pmx-cli e2e")
    if orig:
        r.undo(f"restore realm {kind} comment",
               "pdm", "realm", kind, "update", "--comment", orig)
    else:
        r.undo(f"clear realm {kind} comment",
               "pdm", "realm", kind, "update", "--delete", "comment")


# --- own configuration ------------------------------------------------------


def config_lifecycle(r: Runner) -> None:
    """Round-trip PDM's own configuration: notes, dashboard views, WebAuthn, ACME.

    Everything here is read first and restored afterwards, so a completed run
    leaves the appliance's configuration exactly as it found it. The ACME
    *account* verbs are the exception: registering one creates a real identity
    at a real CA, so they are recorded as skips.
    """
    print(BOLD("config: notes/view/webauthn/certificate/acme-plugin round-trips"))

    # --- notes: read, overwrite, restore.
    before = r.pmx("pdm", "config", "notes", "show", json_out=True)
    orig = ""
    if before.rc == 0:
        try:
            data = before.json()
            if isinstance(data, dict):
                orig = str(data.get("data", data).get("notes", "") or "")
            elif isinstance(data, str):
                orig = data
        except ValueError:
            orig = ""
    r.step("infra", "config notes update", "notes update",
           "pdm", "config", "notes", "update", "--notes", "pmx-cli e2e")
    r.undo("restore notes", "pdm", "config", "notes", "update", "--notes", orig)

    # --- dashboard view: filtered on a tag nothing in the lab carries, so the
    # view resolves to an empty set and shows nobody anything new.
    r.step("infra", "config view add", f"view add {VIEW}",
           "pdm", "config", "view", "add", VIEW, "--include", f"tag={PREFIX}none")
    try:
        r.step("infra", "config view update", f"view update {VIEW}",
               "pdm", "config", "view", "update", VIEW, "--include-all")
    finally:
        r.del_step("infra", "config view delete", f"view delete {VIEW}",
                   "pdm", "config", "view", "delete", VIEW, "-y")

    _webauthn_roundtrip(r)
    _certificate_config_roundtrip(r)

    # --- ACME challenge plugin: pure local configuration, contacts nothing.
    r.step("infra", "config acme plugin add", f"acme plugin add {ACME_PLUGIN}",
           "pdm", "config", "acme", "plugin", "add", ACME_PLUGIN, "--type", "dns",
           "--api", "acmedns", "--data", ACME_PLUGIN_DATA, "--validation-delay", "30")
    try:
        r.step("infra", "config acme plugin update", f"acme plugin update {ACME_PLUGIN}",
               "pdm", "config", "acme", "plugin", "update", ACME_PLUGIN,
               "--validation-delay", "60")
    finally:
        r.del_step("infra", "config acme plugin delete", f"acme plugin delete {ACME_PLUGIN}",
                   "pdm", "config", "acme", "plugin", "delete", ACME_PLUGIN, "-y")

    for verb in ("config acme account add", "config acme account update",
                 "config acme account delete"):
        r.cover_skip("infra", verb, verb,
                     "registers a real account at a real ACME CA, which is rate-limited "
                     "and leaves state outside this lab")


def _webauthn_roundtrip(r: Runner) -> None:
    """Write a WebAuthn relying-party config, then restore what was there.

    Changing this while factors are enrolled would invalidate them, so the
    original is read first and put back — and on an appliance that has none
    configured, the keys are deleted again rather than left set.
    """
    before = r.pmx("pdm", "config", "webauthn", "show", json_out=True)
    orig: dict = {}
    if before.rc == 0:
        try:
            data = before.json()
            if isinstance(data, dict):
                orig = data.get("data", data)
        except ValueError:
            orig = {}
    orig = orig if isinstance(orig, dict) else {}

    # The WebAuthn relying-party config is root-only on PDM: an API token, even
    # one holding Administrator, is refused. That is a server-side rule the
    # suite cannot work around without running as root@pam.
    wrote = r.soft_step("infra", "config webauthn update", "webauthn update",
                        "pdm", "config", "webauthn", "update",
                        "--rp", "pmx-cli e2e", "--id", "pmx-cli.example.invalid",
                        "--origin", "https://pmx-cli.example.invalid",
                        skip_markers=("permission check failed", "only root", "403"),
                        skip_reason="the WebAuthn relying-party config is root-only; "
                                    "an API token cannot write it")
    if not wrote:
        return
    restore = ["pdm", "config", "webauthn", "update"]
    for key, flag in (("rp", "--rp"), ("id", "--id"), ("origin", "--origin")):
        if orig.get(key):
            restore += [flag, str(orig[key])]
    if len(restore) > 4:
        r.undo("restore webauthn config", *restore)
    else:
        r.undo("clear webauthn config", "pdm", "config", "webauthn", "update",
               "--delete", "rp", "--delete", "id", "--delete", "origin")


def _certificate_config_roundtrip(r: Runner) -> None:
    """Set and clear an ACME domain slot on the certificate configuration.

    This records *which* domain a future ACME order would cover; it neither
    orders nor installs a certificate, so the serving certificate is untouched.
    """
    r.step("infra", "config certificate update", "certificate update acmedomain0",
           "pdm", "config", "certificate", "update",
           "--acmedomain0", "domain=pmx-cli.example.invalid")
    r.undo("clear acmedomain0",
           "pdm", "config", "certificate", "update", "--delete", "acmedomain0")


# --- remotes ----------------------------------------------------------------


def remote_lifecycle(r: Runner) -> None:
    """Round-trip a throwaway remote, and drive the remote-wide operations.

    The throwaway remote points at an unroutable TEST-NET address with a dummy
    token: PDM records it without contacting it, which is exactly what makes it
    safe to create and delete. The operations that reach out — certificate
    probe, task refresh, update refresh, metric collection — are driven against
    it too; each is recorded as a skip when the (deliberately unreachable) host
    does not answer, because the verb reached the API and the environment is
    what refused.
    """
    print(BOLD("remote: throwaway remote round-trip and remote-wide operations"))
    r.step("infra", "remote add", f"remote add {REMOTE}",
           "pdm", "remote", "add", REMOTE, "--type", "pve", "--node", DUMMY_HOST,
           "--authid", "root@pam!pmx-cli", "--token", DUMMY_TOKEN)
    unreachable = ("connection refused", "connect", "timed out", "timeout", "no route",
                   "unreachable", "could not", "resolve", "network", "error sending",
                   "tls", "certificate", "permission", "authentication")
    try:
        r.step("infra", "remote update", f"remote update {REMOTE}",
               "pdm", "remote", "update", REMOTE, "--web-url", f"https://{DUMMY_HOST}")
        r.soft_step("infra", "remote probe-certificate", f"probe-certificate {REMOTE}",
                    "pdm", "remote", "probe-certificate", REMOTE, "--node", DUMMY_HOST,
                    skip_markers=unreachable,
                    skip_reason=f"the throwaway remote points at {DUMMY_HOST}, which by "
                                "design answers nothing")
    finally:
        r.del_step("infra", "remote delete", f"remote delete {REMOTE}",
                   "pdm", "remote", "delete", REMOTE, "-y")

    # The remote-wide refreshes iterate every registered remote and only read
    # from them, so they are safe to run against the lab's real remotes.
    r.soft_step("infra", "remote task refresh", "remote task refresh",
                "pdm", "remote", "task", "refresh",
                skip_markers=unreachable,
                skip_reason="no registered remote answered the task poll")
    r.soft_step("infra", "remote updates refresh", "remote updates refresh",
                "pdm", "remote", "updates", "refresh",
                skip_markers=unreachable,
                skip_reason="no registered remote answered the update poll")
    r.soft_step("infra", "remote metric-collection trigger", "metric-collection trigger",
                "pdm", "remote", "metric-collection", "trigger",
                skip_markers=unreachable,
                skip_reason="no registered remote answered the metric poll")


# --- subscription -----------------------------------------------------------


def subscription_lifecycle(r: Runner) -> None:
    """Key pool round-trip plus the pending-change queue, without touching a node.

    The key is syntactically valid and registered to nobody, so PDM stores it,
    reports it as `notfound`, and nothing is ever activated. Assigning it stages
    a *pending* change; the run clears that queue rather than applying it, so no
    managed node's subscription is ever written.
    """
    print(BOLD("subscription: key pool + pending queue (never applied to a node)"))
    remote, node = _first_remote_node(r)

    r.step("infra", "subscription key add", f"key add {FAKE_SUB_KEY}",
           "pdm", "subscription", "key", "add", "--key", FAKE_SUB_KEY)
    try:
        if remote and node:
            r.step("infra", "subscription key assign", f"key assign -> {remote}/{node}",
                   "pdm", "subscription", "key", "assign", FAKE_SUB_KEY,
                   "--remote", remote, "--node", node)
            # queue-clear stages the removal of the node's subscription and only
            # works while the pool manages an assignment for it — so it runs
            # before the unassign, and is reverted immediately rather than
            # applied. Nothing reaches the node either way: applying the queue
            # is what would, and that verb is deliberately out.
            r.step("infra", "subscription queue-clear", f"queue-clear {remote}/{node}",
                   "pdm", "subscription", "queue-clear", "--remote", remote,
                   "--node", node, "-y")
            r.step("infra", "subscription revert-pending-clear",
                   f"revert-pending-clear {remote}/{node}",
                   "pdm", "subscription", "revert-pending-clear", "--remote", remote,
                   "--node", node)
            r.step("infra", "subscription key unassign", f"key unassign {FAKE_SUB_KEY}",
                   "pdm", "subscription", "key", "unassign", FAKE_SUB_KEY, "-y")
        else:
            for v in ("subscription key assign", "subscription key unassign",
                      "subscription queue-clear", "subscription revert-pending-clear"):
                r.cover_skip("infra", v, v,
                             "no remote with a reachable node is registered to stage a "
                             "pending subscription change against")
        r.step("infra", "subscription auto-assign", "auto-assign (proposal only)",
               "pdm", "subscription", "auto-assign")
        r.step("infra", "subscription clear-pending", "clear-pending",
               "pdm", "subscription", "clear-pending", "-y")
    finally:
        r.del_step("infra", "subscription key delete", f"key delete {FAKE_SUB_KEY}",
                   "pdm", "subscription", "key", "delete", FAKE_SUB_KEY, "-y")

    for verb, why in (
        ("subscription apply-pending",
         "writes the queued subscription changes onto the managed remotes' nodes, "
         "which would replace a real cluster's subscription with a test key"),
        ("subscription bulk-assign",
         "applies an auto-assign proposal across every remote at once, for the same "
         "reason apply-pending is out"),
        ("subscription adopt-all",
         "pulls the subscription keys off every managed remote into this PDM's pool, "
         "taking ownership of licences the lab does not own"),
        ("subscription adopt-key",
         "pulls one managed remote's subscription key into this PDM's pool, taking "
         "ownership of a licence the lab does not own"),
        ("subscription check",
         "queries the Proxmox licensing servers for a key's status; the lab has no "
         "real key to check and the request leaves the lab"),
    ):
        r.cover_skip("infra", verb, verb, why)


def _first_remote_node(r: Runner) -> tuple[str, str]:
    """Return (remote-id, node-name) for the first registered remote, or ("", "")."""
    res = r.pmx("pdm", "remote", "ls", json_out=True)
    if res.rc != 0:
        return "", ""
    try:
        rows = res.json()
    except ValueError:
        return "", ""
    if isinstance(rows, dict):
        rows = rows.get("data", rows.get("rows", []))
    for rem in rows if isinstance(rows, list) else []:
        if not isinstance(rem, dict) or rem.get("type") != "pve":
            continue
        rid = str(rem.get("id", "") or "")
        if not rid or rid == REMOTE:
            continue
        node = _remote_node_name(r, rid)
        if node:
            return rid, node
    return "", ""


def _remote_node_name(r: Runner, remote: str) -> str:
    """Return a node name inside a managed PVE remote, or ""."""
    res = r.pmx("pdm", "pve", "node", "ls", remote, json_out=True)
    if res.rc != 0:
        return ""
    try:
        rows = res.json()
    except ValueError:
        return ""
    if isinstance(rows, dict):
        rows = rows.get("data", rows.get("rows", []))
    for n in rows if isinstance(rows, list) else []:
        if isinstance(n, dict) and n.get("node"):
            return str(n["node"])
    return ""


# --- auto-install -----------------------------------------------------------


def auto_install_lifecycle(r: Runner) -> None:
    """Answer-file and token round-trips for the automated-installation service.

    Both are inert configuration: an answer file is only read when a machine
    boots the auto-install ISO and asks for one, and this answer is bound to a
    target filter that matches nothing. `installation delete` needs a recorded
    installation to remove, which only a real unattended install produces.
    """
    print(BOLD("auto-install: prepared answer + token round-trips"))
    r.step("infra", "auto-install token add", f"token add {AUTOINSTALL_TOKEN}",
           "pdm", "auto-install", "token", "add", AUTOINSTALL_TOKEN,
           "--comment", "pmx-cli e2e")
    try:
        r.step("infra", "auto-install token update", f"token update {AUTOINSTALL_TOKEN}",
               "pdm", "auto-install", "token", "update", AUTOINSTALL_TOKEN,
               "--comment", "pmx-cli e2e (updated)")

        # --target-filter deliberately matches nothing, so a machine that did
        # ask for an answer would never be handed this one.
        created = r.soft_step(
            "infra", "auto-install prepared add", f"prepared add {AUTOINSTALL_ANSWER}",
            "pdm", "auto-install", "prepared", "add", AUTOINSTALL_ANSWER,
            "--fqdn", "pmx-cli-e2e.example.invalid",
            "--country", "at", "--timezone", "UTC", "--keyboard", "en-us",
            "--root-password", "pmx-cli-e2e-never-used",
            "--mailto", "pmx-cli@example.invalid",
            "--use-dhcp-network", "--disk-mode", "fixed",
            "--disk-list", "sda", "--filesystem", '{"ext4":{}}',
            "--target-filter", '{"/sysinfo/product":"pmx-cli-e2e-no-such-machine"}',
            "--authorized-token", AUTOINSTALL_TOKEN,
            skip_markers=("complex (sub) objects",),
            skip_reason="--filesystem is a required nested JSON object, and the "
                        "form-encoded API cannot carry one; the endpoint needs a JSON "
                        "request body this client does not yet send")
        if created:
            try:
                r.step("infra", "auto-install prepared update",
                       f"prepared update {AUTOINSTALL_ANSWER}",
                       "pdm", "auto-install", "prepared", "update", AUTOINSTALL_ANSWER,
                       "--timezone", "Etc/UTC")
            finally:
                r.del_step("infra", "auto-install prepared delete",
                           f"prepared delete {AUTOINSTALL_ANSWER}",
                           "pdm", "auto-install", "prepared", "delete",
                           AUTOINSTALL_ANSWER, "-y")
        else:
            for v in ("auto-install prepared update", "auto-install prepared delete"):
                r.cover_skip("infra", v, v,
                             "no prepared answer could be created to act on")
    finally:
        r.del_step("infra", "auto-install token delete", f"token delete {AUTOINSTALL_TOKEN}",
                   "pdm", "auto-install", "token", "delete", AUTOINSTALL_TOKEN, "-y")

    r.cover_skip("infra", "auto-install installation delete",
                 "auto-install installation delete",
                 "removes the record of a completed unattended installation, which only "
                 "a real machine booting the auto-install ISO produces")


# --- node -------------------------------------------------------------------


def node_lifecycle(r: Runner) -> None:
    """Host-level verbs that cannot cut the appliance off, each restored after.

    DNS and the timezone are rewritten to the values the host already has, the
    node config change is reset, and the APT index refresh touches nothing but
    the package lists. What is left out is what would take the host off the
    network or reach a real CA.
    """
    print(BOLD("node: dns/time/config/apt round-trips on the PDM appliance"))

    dns = r.pmx("pdm", "node", "dns", "show", NODE, json_out=True)
    args = ["pdm", "node", "dns", "update", NODE]
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
    if len(args) > 5:
        r.step("node", "node dns update", "dns update (same servers)", *args)
    else:
        r.cover_skip("node", "node dns update", "node dns update",
                     "host has no resolver configured to rewrite identically")

    tz = r.pmx("pdm", "node", "time", "show", NODE, json_out=True)
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
               "pdm", "node", "time", "update", NODE, "--timezone", zone)
    else:
        r.cover_skip("node", "node time update", "node time update",
                     "host did not report a timezone to rewrite identically")

    r.step("node", "node config update", "config update email-from",
           "pdm", "node", "config", "update", NODE,
           "--email-from", "pmx-cli@example.invalid")
    r.undo("reset email-from",
           "pdm", "node", "config", "update", NODE, "--delete", "email-from")

    r.step("node", "node apt update-database", "apt update-database",
           "pdm", "node", "apt", "update-database", NODE, "--quiet")
    _apt_repo_roundtrip(r)
    _task_stop(r)
    _network_staging(r)

    # PDM refuses the refresh outright when the remotes it manages have no
    # subscription of their own — which a lab's nested clusters never do.
    r.soft_step("node", "node subscription update", "subscription update",
                "pdm", "node", "subscription", "update", NODE,
                skip_markers=("without active basic", "subscription"),
                skip_reason="PDM refuses the refresh while its managed remotes have no "
                            "active subscription, and the lab's clusters have none")

    for verb, why in (
        ("node reboot", "would take the shared e2e stack server down mid-suite"),
        ("node shutdown", "would take the shared e2e stack server down mid-suite"),
        ("node certificate acme order",
         "orders a real certificate from a real ACME CA, which is rate-limited"),
        ("node certificate acme renew",
         "renews against a real ACME CA, which is rate-limited"),
        ("node certificate upload",
         "replaces the API certificate and restarts the proxy, which would break the "
         "certificate this context trusts"),
        ("node certificate delete-custom",
         "removes the API certificate and restarts the proxy, which would break the "
         "certificate this context trusts"),
    ):
        r.cover_skip("node", verb, verb, why)


def _apt_repo_roundtrip(r: Runner) -> None:
    """Ensure the no-subscription repo exists, then flip one entry's enabled bit."""
    handle = "no-subscription"
    before = _repo_entry(r, handle)
    if before is None:
        r.step("node", "node apt repository add", f"apt repository add {handle}",
               "pdm", "node", "apt", "repository", "add", NODE, "--handle", handle)
        after = _repo_entry(r, handle)
    else:
        r.cover_skip("node", "node apt repository add", "node apt repository add",
                     f"the {handle} repository is already configured on this host")
        after = before
    if after is None:
        r.cover_skip("node", "node apt repository change", "node apt repository change",
                     f"could not locate the {handle} repository entry to toggle")
        return
    path, index, enabled = after
    r.step("node", "node apt repository change",
           f"apt repository change {handle} (toggle enabled)",
           "pdm", "node", "apt", "repository", "change", NODE, "--path", path,
           "--index", str(index), *(["--enabled"] if not enabled else []))
    r.undo(f"restore {handle} enabled={enabled}",
           "pdm", "node", "apt", "repository", "change", NODE, "--path", path,
           "--index", str(index), *(["--enabled"] if enabled else []))


def _repo_entry(r: Runner, handle: str) -> tuple[str, int, bool] | None:
    """Locate a configured APT repository by its standard handle.

    Returns (sources-file path, index within that file, enabled) or None. The
    listing nests repositories inside their files and carries the handle only on
    the `standard-repos` side, so the match is made on the component name the
    handle corresponds to within a Proxmox URI.
    """
    res = r.pmx("pdm", "node", "apt", "repositories", NODE, json_out=True)
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


def _task_stop(r: Runner) -> None:
    """Stop a task this run started, or record why there was none to stop.

    Only a task the suite itself owns is eligible — stopping somebody else's
    running task is exactly what the isolation contract forbids. A metric
    collection is triggered to produce one; if it finishes before the stop
    lands, that is the common case and the verb is recorded as a skip rather
    than as a failure.
    """
    trigger = r.pmx("pdm", "remote", "metric-collection", "trigger", "--async")
    upid = ""
    if trigger.rc == 0:
        upid = trigger.out.strip().splitlines()[-1].strip() if trigger.out.strip() else ""
    if not upid.startswith("UPID"):
        upid = _own_running_task(r)
    if not upid:
        r.cover_skip("node", "node task stop", "node task stop",
                     "no task this run started was still running to stop; stopping "
                     "anyone else's task is out of bounds")
        return
    r.soft_step("node", "node task stop", f"task stop {upid[:48]}…",
                "pdm", "node", "task", "stop", NODE, upid,
                skip_markers=("no such task", "not running", "already finished",
                              "no longer", "not found", "unable to open"),
                skip_reason="the triggered task finished before the stop reached it")


def _own_running_task(r: Runner) -> str:
    """Return the UPID of a still-running task started by this run, or ""."""
    res = r.pmx("pdm", "node", "task", "ls", NODE, "--running", json_out=True)
    if res.rc != 0:
        return ""
    try:
        rows = res.json()
    except ValueError:
        return ""
    if isinstance(rows, dict):
        rows = rows.get("data", rows.get("rows", []))
    for t in rows if isinstance(rows, list) else []:
        if isinstance(t, dict) and str(t.get("upid", "")).startswith("UPID"):
            return str(t["upid"])
    return ""


def _network_staging(r: Runner) -> None:
    """create → update → delete a dummy interface, then revert the staged config.

    Interface changes are staged in `interfaces.new` and only touch the live
    configuration on `apply`, so everything here is discarded by the closing
    revert and the appliance keeps the networking it booted with.
    """
    # Writing the interface configuration is root-only; an API token, whatever
    # its ACL, is refused. That is a server-side rule the suite cannot work
    # around without running as root@pam, so the three writes are recorded as
    # skips when it bites — the revert below still runs either way.
    rootonly = ("permission check failed", "only root", "403")
    reason = ("the interface configuration is root-only; an API token cannot write it")
    try:
        staged = r.soft_step(
            "node", "node network create", f"network create {NET_IFACE}",
            "pdm", "node", "network", "create", NODE, NET_IFACE, "--type", "bridge",
            "--method", "manual", "--comments", "pmx-cli e2e (staged, never applied)",
            skip_markers=rootonly, skip_reason=reason)
        if staged:
            r.step("node", "node network update", f"network update {NET_IFACE}",
                   "pdm", "node", "network", "update", NODE, NET_IFACE, "--mtu", "1400")
            r.step("node", "node network delete", f"network delete {NET_IFACE}",
                   "pdm", "node", "network", "delete", NODE, NET_IFACE, "-y")
        else:
            for v in ("node network update", "node network delete"):
                r.cover_skip("node", v, v, reason)
    finally:
        r.soft_step("node", "node network revert", "network revert (discard staged config)",
                    "pdm", "node", "network", "revert", NODE, "-y",
                    skip_markers=rootonly, skip_reason=reason)
    r.cover_skip("node", "node network apply", "node network apply",
                 "commits the staged interface configuration to the live host, which can "
                 "sever the suite's own connection to it")


# --- verbs that reach through a managed remote ------------------------------

# Guest power, snapshot, migration, and firewall verbs proxied through PDM act
# on somebody else's cluster: the remote registered with a PDM is a production
# datacentre, and this suite has no resource of its own there to act on. The PVE
# suite covers each of these verbs directly against the guests it creates in its
# own isolated pool, so the CLI path is proven — what is not proven is the PDM
# proxy in front of it, and proving that would mean mutating a registered
# cluster's guests. Each is recorded with that reason.
REMOTE_GUEST_VERBS = (
    ("pve qemu start", "starts a guest on a registered cluster"),
    ("pve qemu stop", "hard-stops a guest on a registered cluster"),
    ("pve qemu shutdown", "shuts down a guest on a registered cluster"),
    ("pve qemu resume", "resumes a guest on a registered cluster"),
    ("pve qemu migrate", "moves a guest between nodes of a registered cluster"),
    ("pve qemu remote-migrate", "moves a guest between two registered clusters"),
    ("pve qemu snapshot add", "writes a snapshot onto a registered cluster's guest"),
    ("pve qemu snapshot update", "edits a snapshot on a registered cluster's guest"),
    ("pve qemu snapshot rollback", "reverts a registered cluster's guest to a snapshot"),
    ("pve qemu snapshot delete", "deletes a snapshot from a registered cluster's guest"),
    ("pve qemu firewall options update", "rewrites a registered guest's firewall policy"),
    ("pve lxc start", "starts a container on a registered cluster"),
    ("pve lxc stop", "hard-stops a container on a registered cluster"),
    ("pve lxc shutdown", "shuts down a container on a registered cluster"),
    ("pve lxc migrate", "moves a container between nodes of a registered cluster"),
    ("pve lxc remote-migrate", "moves a container between two registered clusters"),
    ("pve lxc snapshot add", "writes a snapshot onto a registered cluster's container"),
    ("pve lxc snapshot update", "edits a snapshot on a registered cluster's container"),
    ("pve lxc snapshot rollback", "reverts a registered container to a snapshot"),
    ("pve lxc snapshot delete", "deletes a snapshot from a registered container"),
    ("pve lxc firewall options update", "rewrites a registered container's firewall policy"),
    ("pve firewall options update", "rewrites a registered cluster's firewall policy"),
    ("pve node firewall options update", "rewrites a registered node's firewall policy"),
    ("pve node apt update-database", "refreshes the package index on a registered node"),
    ("pve task stop", "stops a task running on a registered cluster"),
    ("pve remote scan", "enumerates a registered cluster's nodes over its API"),
    ("pve remote probe-tls", "opens a TLS probe against a registered cluster"),
    ("pve realms", "reads the realm list from a registered cluster"),
    ("pbs node apt update-database", "refreshes the package index on a registered PBS"),
    ("pbs task stop", "stops a task running on a registered PBS"),
    ("pbs probe-tls", "opens a TLS probe against a registered PBS"),
    ("pbs realms", "reads the realm list from a registered PBS"),
    ("pbs scan", "enumerates a registered PBS's datastores over its API"),
    ("sdn zone add", "creates an SDN zone on a registered cluster"),
    ("sdn vnet add", "creates an SDN vnet on a registered cluster"),
    ("resource location-info", "resolves every managed resource's location, which "
                               "polls every registered remote"),
)


def remote_reach_skips(r: Runner) -> None:
    """Record every verb whose target is a cluster this suite does not own."""
    print(BOLD("proxy verbs: recorded as skips (they act on registered clusters)"))
    for verb, what in REMOTE_GUEST_VERBS:
        r.cover_skip("proxy", verb, verb,
                     f"{what}; this suite owns no resource there, and the same CLI path "
                     "is driven directly by the PVE mutate suite")


# --- entry point ------------------------------------------------------------


def run(context: str, binary: str | None, build: bool, strict: bool) -> int:
    # Line-buffer stdout: redirected to a file or a CI log it is otherwise
    # block-buffered, so a run that stalls on a slow step appears to be stuck
    # several steps earlier — whichever one last filled a 4K block.
    sys.stdout.reconfigure(line_buffering=True)
    bin_path = find_binary(binary, build=build)
    ok, why = target_configured(bin_path, context)
    if not ok:
        msg = f"context {context!r} not usable: {why}"
        if strict:
            print(f"pdm-lifecycle: error: {msg}", file=sys.stderr)
            return 3
        print(f"pdm-lifecycle: skipping — {msg}")
        return 0

    # A PDM context addresses one appliance; there is no --node to inject.
    r = Runner(bin_path, context, node="")
    probe = r.pmx("pdm", "remote", "ls", json_out=True)
    if probe.rc != 0:
        msg = f"PDM at context {context!r} not reachable: {_err(probe, 'unknown error')}"
        if strict:
            print(f"pdm-lifecycle: error: {msg}", file=sys.stderr)
            return 3
        print(f"pdm-lifecycle: skipping — {msg}")
        return 0

    print(BOLD(f"pdm-lifecycle: context={context}"))
    print(DIM(f"  isolation: name-prefix={PREFIX} node={NODE}; nothing is written "
              f"through a managed remote"))
    print()

    failed = False
    started = time.monotonic()
    try:
        sweep_stale(r)
        print()
        access_lifecycle(r)
        print()
        realm_lifecycle(r)
        print()
        config_lifecycle(r)
        print()
        remote_lifecycle(r)
        print()
        subscription_lifecycle(r)
        print()
        auto_install_lifecycle(r)
        print()
        node_lifecycle(r)
        print()
        remote_reach_skips(r)
        print()
    except LifecycleError as exc:
        failed = True
        print(RED(f"pdm-lifecycle: aborted at step: {exc}"))
    except KeyboardInterrupt:
        failed = True
        print(RED("pdm-lifecycle: interrupted"))
    finally:
        print()
        sweep_stale(r)

    print()
    _print_coverage(r)
    if any(s.status == FAIL for s in r.cov):
        failed = True

    dur = time.monotonic() - started
    print()
    if failed:
        print(BOLD("pdm-lifecycle: ") + RED("FAILED") + DIM(f"  ({dur:.0f}s)"))
        return 1
    print(BOLD("pdm-lifecycle: ") + GREEN("PASSED") + DIM(f"  ({dur:.0f}s)"))
    return 0
