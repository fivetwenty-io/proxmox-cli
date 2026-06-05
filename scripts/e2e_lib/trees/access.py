"""access: users, tokens, groups, roles, ACLs (read-only happy path)."""

from __future__ import annotations

from ..context import CmdResult, Ctx
from ..model import Isolation

NAME = "access"
DESCRIPTION = "Manage users, tokens, groups, roles, and access control"


def run(ctx: Ctx) -> None:
    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    users = ctx.check("user list", "access", "user", "list", validate=is_list)
    roles = ctx.check("role list", "access", "role", "list", validate=is_list)
    groups = ctx.check("group list", "access", "group", "list", validate=is_list)
    ctx.check("acl list", "access", "acl", "list", validate=is_list)

    def is_perm_tree(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a permissions object keyed by path"
        if not any(str(p).startswith("/") for p in data):
            return "no '/'-rooted path in the permissions tree"
        return None

    ctx.check("permissions (self)", "access", "permissions", validate=is_perm_tree)

    # Authentication realms (domains). `pam` and `pve` always exist, so a get of
    # the first listed realm is unconditional.
    domains = ctx.check("domain list", "access", "domain", "list", validate=is_list)
    realm = None
    if domains.rc == 0:
        try:
            realm = ctx.first(domains.json(), "realm")
        except ValueError:
            realm = None
    if realm:
        ctx.check("domain get", "access", "domain", "get", str(realm))
    else:
        ctx.skip("domain get", "no realm returned")

    # Two-factor authentication entries. The list is server-wide; labs commonly
    # have no entries, so `tfa get` of the first user is conditional (◑).
    tfa = ctx.check("tfa list", "access", "tfa", "list", validate=is_list)
    tfa_user = None
    if tfa.rc == 0:
        try:
            tfa_user = ctx.first(tfa.json(), "userid")
        except ValueError:
            tfa_user = None
    if tfa_user:
        ctx.check("tfa get", "access", "tfa", "get", str(tfa_user))
    else:
        ctx.skip("tfa get", "no user has a tfa entry")

    uid = None
    if users.rc == 0:
        try:
            uid = ctx.first(users.json(), "userid") or ctx.first(users.json(), "user")
        except ValueError:
            uid = None
    if uid:
        ctx.check("user get", "access", "user", "get", str(uid))
        tokens = ctx.check("user token list", "access", "user", "token", "list", str(uid))
        # `user token get` reads one token's detail; most users (e.g. root@pam)
        # have none, so this is conditional (◑) — a skip still passes.
        tid = None
        if tokens.rc == 0:
            try:
                tid = ctx.first(tokens.json(), "tokenid")
            except ValueError:
                tid = None
        if tid:
            ctx.check("user token get", "access", "user", "token", "get", str(uid), str(tid))
        else:
            ctx.skip("user token get", "no token on the first user")
    else:
        ctx.skip("user get", "no user returned")
        ctx.skip("user token list", "no user returned")
        ctx.skip("user token get", "no user returned")

    rid = None
    if roles.rc == 0:
        try:
            rid = ctx.first(roles.json(), "roleid") or ctx.first(roles.json(), "role")
        except ValueError:
            rid = None
    if rid:
        ctx.check("role get", "access", "role", "get", str(rid))
    else:
        ctx.skip("role get", "no role returned")

    # `group get` reads one group's detail; labs may have no groups, so ◑.
    gid = None
    if groups.rc == 0:
        try:
            gid = ctx.first(groups.json(), "groupid")
        except ValueError:
            gid = None
    if gid:
        ctx.check("group get", "access", "group", "get", str(gid))
    else:
        ctx.skip("group get", "no group returned")

    # The mutate phase provisions an isolated `pve-cli-probe` user/group/token
    # and an ACL on the `pve-cli` pool path, exercises every mutating verb, and
    # tears them down — so these are covered live by it. (Role create/delete is
    # read-only in the CLI, so there is no such verb to exercise: not a gap.)
    ctx.defer("user create/delete", "mutates access control — covered live by `e2e --mutate`",
              f"pve access user create {Isolation.NAME_PREFIX}probe@pve",
              isolation=True, live_covered=True)
    ctx.defer("group create/delete", "mutates access control — covered live by `e2e --mutate`",
              f"pve access group create {Isolation.NAME_PREFIX}probe",
              isolation=True, live_covered=True)
    ctx.defer("user token create/delete", "issues/revokes credentials — covered live by `e2e --mutate`",
              f"pve access user token create {Isolation.NAME_PREFIX}probe@pve e2e",
              isolation=True, live_covered=True)
    ctx.defer("acl set", "grants/revokes permissions — covered live by `e2e --mutate`",
              f"pve access acl set --path /pool/{Isolation.POOL} --roles PVEAuditor --users <user>",
              isolation=True, live_covered=True)
    ctx.defer("password set", "changes a user password — covered live by `e2e --mutate`",
              f"pve access password set --userid {Isolation.NAME_PREFIX}probe@pve",
              isolation=True, live_covered=True)
    ctx.defer("domain create/set/delete", "creates an auth realm — covered live by `e2e --mutate`",
              f"pve access domain create {Isolation.NAME_PREFIX}realm --type ldap",
              isolation=True, live_covered=True)
    ctx.defer("domain sync", "syncs users from an ldap/ad realm — covered live by `e2e --mutate`",
              f"pve access domain sync {Isolation.NAME_PREFIX}realm --dry-run",
              isolation=True, live_covered=True)
    ctx.defer("role create/set/delete", "mutates a role definition — covered live by `e2e --mutate`",
              f"pve access role create e2e-{Isolation.NAME_PREFIX}role --privs VM.Audit",
              isolation=True, live_covered=True)
    ctx.defer("tfa unlock", "clears a user's tfa lockout — covered live by `e2e --mutate`",
              f"pve access tfa unlock {Isolation.NAME_PREFIX}probe@pve --yes",
              isolation=True, live_covered=True)
