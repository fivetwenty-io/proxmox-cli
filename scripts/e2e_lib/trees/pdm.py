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
nested (the section helpers wrap their bodies in `if ctx.env.context:`) or the
generated matrix will overstate the guarantee.

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

NAME = "pdm"
DESCRIPTION = "Proxmox Datacenter Manager admin (opt-in: --pdm-context)"

# The runner swaps this tree's Env.context for the --pdm-context value (and
# clears the discovered PVE node, which is meaningless here).
PRODUCT = "pdm"


def is_list(res: CmdResult) -> str | None:
    return None if isinstance(res.json(), list) else "expected a JSON array"


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
    _remotes(ctx)
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
        ctx.check("ceph ls", "pdm", "ceph", "ls", validate=is_list)
        ctx.check("subscription key ls", "pdm", "subscription", "key", "ls",
                  validate=is_list)


# --------------------------------------------------------------------------- #
# access control: users, roles, permissions, realms                           #
# --------------------------------------------------------------------------- #
def _access(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        ctx.check("user ls", "pdm", "user", "ls", validate=is_list)
        ctx.check("role ls", "pdm", "role", "ls", validate=is_list)
        # permission ls renders a path -> {priv: propagate} map, not a list.
        ctx.check("permission ls", "pdm", "permission", "ls")
        ctx.check("realm ls", "pdm", "realm", "ls", validate=is_list)


# --------------------------------------------------------------------------- #
# this PDM's own configuration and nodes                                      #
# --------------------------------------------------------------------------- #
def _remotes(ctx: Ctx) -> None:
    if ctx.env.context:  # opt-in gate (see module docstring)
        ctx.check("config view ls", "pdm", "config", "view", "ls", validate=is_list)
        ctx.check("node ls", "pdm", "node", "ls", validate=is_list, skip_on=TICKET_ONLY)
        ctx.check("auto-install prepared ls", "pdm", "auto-install", "prepared", "ls",
                  validate=is_list)


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

    # proxied PVE operations (mutate a managed remote's real cluster)
    ctx.defer("pve remote scan", "scans a PVE host's connection info before adding it as a remote; covered by unit tests",
              "pmx pdm pve scan --hostname pve.example --token-id ... --token-secret ...")
    ctx.defer("pve remote probe-tls", "re-probes and stores a PVE host's TLS fingerprint; covered by unit tests",
              "pmx pdm pve probe-tls --hostname pve.example")
    ctx.defer("pve firewall options update", "modifies a PVE remote's cluster firewall options; covered by unit tests",
              "pmx pdm pve firewall options update pmx-cli-remote --enable")
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
