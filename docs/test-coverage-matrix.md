# Test Coverage Matrix

> **Generated file — do not edit by hand.** Regenerate with
> `go build -o ./dist/pve ./cmd/pve && python3 scripts/coverage_matrix.py`.
> The classification is derived statically from the built command tree, the
> read-only sweep definitions in `scripts/e2e_lib/trees/*.py`, and the mutate
> phase in `scripts/e2e_lib/lifecycle.py`, so it stays correct as commands and
> tests change.

This document maps every invocable leaf command to its automated test coverage
across the two live suites:

- **e2e** (`scripts/e2e`, `make test-e2e`) — a read-only, parallel happy-path
  sweep against a configured context. Mutating operations are never executed;
  they are recorded as deferred.

- **lifecycle / mutate** (`scripts/lifecycle`, `make test-lifecycle`, or
  `scripts/e2e --mutate`) — the destructive counterpart. It provisions an
  isolated SDN zone and resource pool, drives the mutating sub-commands on
  purpose-built throwaway resources, records each verb, and tears everything
  down.

A third tree, **negative** (`scripts/e2e_lib/trees/negative.py`), asserts the
CLI's error contract: bad input must fail cleanly (non-zero exit plus a useful
message). It never mutates, so it does not set a happy-path ✓; leaves whose
failure path it guards are tagged `error-contract checked` in the Notes column.

## Legend

- **e2e ✓** — exercised unconditionally by the read-only sweep on every run.

- **e2e ◑** — exercised by the sweep only when prerequisite inventory exists
  (a VM, user, vnet, …); otherwise skipped (a skip still passes, exit 0).

- **mutate ✓** — driven live by the mutate phase on a purpose-built resource.

- **mutate ·** — driven by the mutate phase but recorded as SKIP because the
  host/guest cannot complete it (the reason is recorded); not a CLI gap.

- **—** — not exercised by that suite (a mutating verb is `—` for e2e because
  the read sweep never mutates; a read verb is `—` for mutate).

- **Notes** — `live via mutate phase` (deferred in the sweep, driven by
  `--mutate`), `deferred — …` (intentionally not run live, with reason),
  `n/a — …` (interactive or host-daemon, no automated coverage by design),
  `help-only` (only the `--help` parse is checked), `error-contract checked`
  (the failure path is guarded by the negative tree), or **uncovered** (a
  genuine gap, listed in the appendix).

## Isolation contract

Every resource the lifecycle suite creates is shielded from other lab efforts
(see `scripts/e2e_lib/model.py`, the single source of truth):

- named or hostnamed with the `pve-cli-` prefix,

- placed in the `pve-cli` resource pool and tagged `pve-cli`,

- attached to a dedicated `pvecli` simple SDN zone and `pvecli0` vnet on the
  `172.30.0.0/24` subnet, deliberately off the host management network.

Teardown runs in a `finally` block and is idempotent: a crashed prior run is
swept clean before the next provisions.

## Coverage summary

| Tree | Leaves | e2e ✓ | e2e ◑ | mutate ✓ | mutate · | deferred | n/a | uncovered |
|------|-------:|------:|------:|---------:|---------:|---------:|----:|----------:|
| `access` | 39 | 9 | 8 | 28 | 0 | 0 | 0 | 0 |
| `api` | 7 | 3 | 1 | 3 | 0 | 0 | 0 | 0 |
| `cluster` | 165 | 44 | 15 | 109 | 5 | 13 | 0 | 0 |
| `context` | 9 | 8 | 0 | 0 | 0 | 0 | 1 | 0 |
| `init` | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 |
| `lxc` | 70 | 5 | 21 | 39 | 0 | 11 | 0 | 0 |
| `node` | 166 | 3 | 75 | 47 | 0 | 40 | 6 | 0 |
| `pbs` | 276 | 0 | 0 | 0 | 0 | 0 | 0 | 276 |
| `pool` | 10 | 1 | 4 | 3 | 0 | 2 | 0 | 0 |
| `qemu` | 94 | 8 | 24 | 52 | 1 | 15 | 1 | 0 |
| `rsync` | 1 | 0 | 0 | 0 | 0 | 1 | 0 | 0 |
| `sdn` | 91 | 6 | 18 | 61 | 0 | 9 | 0 | 0 |
| `ssh` | 1 | 0 | 0 | 0 | 0 | 1 | 0 | 0 |
| `storage` | 30 | 2 | 12 | 12 | 0 | 7 | 0 | 0 |
| `task` | 6 | 2 | 2 | 2 | 0 | 0 | 0 | 0 |
| `version` | 2 | 2 | 0 | 0 | 0 | 0 | 0 | 0 |
| **Total** | **968** | **94** | **180** | **356** | **6** | **99** | **8** | **276** |

Leaf commands are counted from a walk of the built command tree (`pve <tree> … --help`); each `create`/`delete` and `get`/`set` verb is its own leaf. Of **968** leaves, **585** are exercised by at least one live suite, **99** are deferred from the live suites (irreversible, interactive, or environment-bound — covered by unit tests), **8** are n/a by design, and **276** are not yet exercised by either suite — see [Uncovered leaves](#uncovered-leaves).

## `access`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `access acl list` | ✓ | — |  |
| `access acl set` | — | ✓ |  |
| `access domain create` | — | ✓ |  |
| `access domain delete` | — | ✓ |  |
| `access domain get` | ◑ | ✓ |  |
| `access domain list` | ✓ | — |  |
| `access domain set` | — | ✓ |  |
| `access domain sync` | — | ✓ |  |
| `access group create` | — | ✓ |  |
| `access group delete` | — | ✓ | error-contract checked |
| `access group get` | ◑ | ✓ |  |
| `access group list` | ✓ | — |  |
| `access group set` | — | ✓ |  |
| `access openid list` | ✓ | — |  |
| `access password set` | — | ✓ |  |
| `access permissions` | ✓ | — |  |
| `access role create` | — | ✓ |  |
| `access role delete` | — | ✓ |  |
| `access role get` | ◑ | ✓ |  |
| `access role list` | ✓ | — |  |
| `access role set` | — | ✓ |  |
| `access tfa create` | — | ✓ |  |
| `access tfa delete` | — | ✓ |  |
| `access tfa get` | ◑ | — |  |
| `access tfa get-entry` | ◑ | — |  |
| `access tfa list` | ✓ | — |  |
| `access tfa set` | — | ✓ |  |
| `access tfa types` | ✓ | — |  |
| `access tfa unlock` | — | ✓ |  |
| `access user create` | — | ✓ |  |
| `access user delete` | — | ✓ |  |
| `access user get` | ◑ | ✓ |  |
| `access user list` | ✓ | — |  |
| `access user set` | — | ✓ |  |
| `access user token create` | — | ✓ |  |
| `access user token delete` | — | ✓ |  |
| `access user token get` | ◑ | ✓ |  |
| `access user token list` | ◑ | ✓ |  |
| `access user token set` | — | ✓ |  |

## `api`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `api auth login` | — | ✓ |  |
| `api auth logout` | — | ✓ |  |
| `api auth refresh` | — | ✓ |  |
| `api auth set-password` | ✓ | — |  |
| `api auth set-token` | ✓ | — |  |
| `api auth status` | ✓ | — |  |
| `api auth whoami` | ◑ | — |  |

## `cluster`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `cluster acme account create` | — | — | deferred — registers a new account against the ACME CA — the endpoint is restricted to root@pam and rejects API-token auth; not exercisable by the e2e suite — covered by unit tests |
| `cluster acme account delete` | — | — | deferred — deactivates and removes an account at the ACME CA — the endpoint is restricted to root@pam and rejects API-token auth; not exercisable by the e2e suite — covered by unit tests |
| `cluster acme account get` | ◑ | — |  |
| `cluster acme account list` | ✓ | — |  |
| `cluster acme account set` | — | — | deferred — updates an account's contact at the ACME CA — the endpoint is restricted to root@pam and rejects API-token auth; not exercisable by the e2e suite — covered by unit tests |
| `cluster acme challenge-schema` | ✓ | — |  |
| `cluster acme directories` | ✓ | — |  |
| `cluster acme plugin create` | — | ✓ |  |
| `cluster acme plugin delete` | — | ✓ |  |
| `cluster acme plugin get` | — | ✓ |  |
| `cluster acme plugin list` | ✓ | ✓ |  |
| `cluster acme plugin set` | — | ✓ |  |
| `cluster backup create` | — | ✓ |  |
| `cluster backup delete` | — | ✓ |  |
| `cluster backup get` | — | ✓ |  |
| `cluster backup included-volumes` | ◑ | — |  |
| `cluster backup list` | ✓ | ✓ |  |
| `cluster backup set` | — | ✓ |  |
| `cluster backup-info not-backed-up` | ◑ | — |  |
| `cluster bulk migrate` | — | — | deferred — migrates guests cluster-wide — requires a second node; not exercisable on a single-node lab |
| `cluster bulk shutdown` | — | ✓ |  |
| `cluster bulk start` | — | ✓ |  |
| `cluster bulk suspend` | — | ✓ |  |
| `cluster ceph flags get` | ◑ | — |  |
| `cluster ceph flags list` | ◑ | — |  |
| `cluster ceph flags set` | — | — | deferred — toggles a cluster-wide Ceph OSD flag (e.g. noout/pause) — cluster-disruptive, not run live |
| `cluster ceph flags set-all` | — | — | deferred — toggles several cluster-wide Ceph OSD flags atomically (e.g. noout, norebalance) in one request during maintenance — cluster-disruptive; not exercised live; covered by unit tests |
| `cluster ceph metadata` | ◑ | — |  |
| `cluster ceph status` | ◑ | — |  |
| `cluster config apiversion` | ✓ | — |  |
| `cluster config create` | — | — | deferred — creates/initializes a new corosync cluster on the local node — one-time and disruptive to run against an already-clustered target; not exercised live; covered by unit tests |
| `cluster config join add` | — | — | deferred — joins the local node to an existing cluster — changes membership and quorum; not exercised live; covered by unit tests |
| `cluster config join list` | ◑ | — |  |
| `cluster config nodes add` | — | — | deferred — registers a new node in the cluster configuration — changes membership and quorum; not exercised live; covered by unit tests |
| `cluster config nodes delete` | — | — | deferred — removes a node from the cluster configuration — changes membership and quorum; not exercised live; covered by unit tests |
| `cluster config nodes list` | ✓ | — |  |
| `cluster config qdevice` | ◑ | — |  |
| `cluster config totem` | ◑ | — |  |
| `cluster cpu-model create` | — | ✓ |  |
| `cluster cpu-model delete` | — | ✓ |  |
| `cluster cpu-model get` | — | ✓ |  |
| `cluster cpu-model list` | ✓ | ✓ |  |
| `cluster cpu-model set` | — | ✓ |  |
| `cluster firewall alias create` | — | ✓ |  |
| `cluster firewall alias delete` | — | ✓ |  |
| `cluster firewall alias get` | ◑ | — |  |
| `cluster firewall alias list` | ✓ | ✓ |  |
| `cluster firewall alias update` | — | ✓ |  |
| `cluster firewall group create` | — | ✓ |  |
| `cluster firewall group delete` | — | ✓ |  |
| `cluster firewall group get` | ◑ | — |  |
| `cluster firewall group list` | ✓ | ✓ |  |
| `cluster firewall group rule-add` | — | ✓ |  |
| `cluster firewall group rule-delete` | — | ✓ |  |
| `cluster firewall group rule-update` | — | ✓ |  |
| `cluster firewall group rules` | — | ✓ |  |
| `cluster firewall ipset add` | — | ✓ |  |
| `cluster firewall ipset create` | — | ✓ |  |
| `cluster firewall ipset delete` | — | ✓ |  |
| `cluster firewall ipset get` | ◑ | — |  |
| `cluster firewall ipset list` | ✓ | ✓ |  |
| `cluster firewall ipset remove` | — | ✓ |  |
| `cluster firewall ipset update` | — | ✓ |  |
| `cluster firewall macros list` | ✓ | — |  |
| `cluster firewall options describe` | ✓ | — |  |
| `cluster firewall options get` | ✓ | ✓ |  |
| `cluster firewall options set` | — | ✓ |  |
| `cluster firewall refs list` | ✓ | — |  |
| `cluster firewall rules create` | — | ✓ |  |
| `cluster firewall rules delete` | — | ✓ |  |
| `cluster firewall rules get` | — | ✓ |  |
| `cluster firewall rules list` | ✓ | ✓ |  |
| `cluster firewall rules update` | — | ✓ |  |
| `cluster ha group create` | — | ✓ |  |
| `cluster ha group delete` | — | ✓ |  |
| `cluster ha group get` | — | ✓ |  |
| `cluster ha group list` | ◑ | ✓ |  |
| `cluster ha group set` | — | ✓ |  |
| `cluster ha resource create` | — | ✓ |  |
| `cluster ha resource delete` | — | ✓ |  |
| `cluster ha resource get` | — | ✓ |  |
| `cluster ha resource list` | ✓ | ✓ |  |
| `cluster ha resource migrate` | — | · |  |
| `cluster ha resource relocate` | — | — | deferred — requires a second node as the relocation target — not exercisable on a single-node lab |
| `cluster ha resource set` | — | ✓ |  |
| `cluster ha rule create` | — | ✓ |  |
| `cluster ha rule delete` | — | ✓ |  |
| `cluster ha rule get` | — | ✓ |  |
| `cluster ha rule list` | ✓ | ✓ |  |
| `cluster ha rule set` | — | ✓ |  |
| `cluster ha status arm` | — | — | deferred — re-enables the cluster-wide HA stack — would disrupt every HA-managed resource on the lab |
| `cluster ha status current` | ✓ | — |  |
| `cluster ha status disarm` | — | — | deferred — disables the cluster-wide HA stack — would disrupt every HA-managed resource on the lab |
| `cluster ha status manager` | ✓ | — |  |
| `cluster jobs realm-sync create` | — | ✓ |  |
| `cluster jobs realm-sync delete` | — | ✓ |  |
| `cluster jobs realm-sync get` | — | ✓ |  |
| `cluster jobs realm-sync list` | ✓ | ✓ |  |
| `cluster jobs realm-sync set` | — | ✓ |  |
| `cluster jobs schedule-analyze` | ✓ | — |  |
| `cluster log` | ✓ | — |  |
| `cluster mapping dir create` | — | ✓ |  |
| `cluster mapping dir delete` | — | ✓ |  |
| `cluster mapping dir get` | — | ✓ |  |
| `cluster mapping dir list` | ✓ | ✓ |  |
| `cluster mapping dir set` | — | ✓ |  |
| `cluster mapping pci create` | — | ✓ |  |
| `cluster mapping pci delete` | — | ✓ |  |
| `cluster mapping pci get` | — | ✓ |  |
| `cluster mapping pci list` | ✓ | — |  |
| `cluster mapping pci set` | — | ✓ |  |
| `cluster mapping usb create` | — | ✓ |  |
| `cluster mapping usb delete` | — | ✓ |  |
| `cluster mapping usb get` | — | ✓ |  |
| `cluster mapping usb list` | ✓ | — |  |
| `cluster mapping usb set` | — | ✓ |  |
| `cluster metrics export` | ◑ | — |  |
| `cluster metrics server create` | — | ✓ |  |
| `cluster metrics server delete` | — | ✓ |  |
| `cluster metrics server get` | — | ✓ |  |
| `cluster metrics server list` | ✓ | ✓ |  |
| `cluster metrics server set` | — | ✓ |  |
| `cluster next-id` | ✓ | — |  |
| `cluster notifications endpoints` | ✓ | — |  |
| `cluster notifications gotify create` | — | ✓ |  |
| `cluster notifications gotify delete` | — | ✓ |  |
| `cluster notifications gotify get` | — | ✓ |  |
| `cluster notifications gotify list` | ✓ | ✓ |  |
| `cluster notifications gotify set` | — | ✓ |  |
| `cluster notifications matcher create` | — | ✓ |  |
| `cluster notifications matcher delete` | — | ✓ |  |
| `cluster notifications matcher get` | — | ✓ |  |
| `cluster notifications matcher list` | ✓ | — |  |
| `cluster notifications matcher set` | — | ✓ |  |
| `cluster notifications matcher-field-values` | ✓ | — |  |
| `cluster notifications matcher-fields` | ✓ | — |  |
| `cluster notifications sendmail create` | — | ✓ |  |
| `cluster notifications sendmail delete` | — | ✓ |  |
| `cluster notifications sendmail get` | — | ✓ |  |
| `cluster notifications sendmail list` | ✓ | ✓ |  |
| `cluster notifications sendmail set` | — | ✓ |  |
| `cluster notifications smtp create` | — | ✓ |  |
| `cluster notifications smtp delete` | — | ✓ |  |
| `cluster notifications smtp get` | — | ✓ |  |
| `cluster notifications smtp list` | ✓ | ✓ |  |
| `cluster notifications smtp set` | — | ✓ |  |
| `cluster notifications targets` | ✓ | ✓ |  |
| `cluster notifications targets-test` | — | ✓ |  |
| `cluster notifications webhook create` | — | ✓ |  |
| `cluster notifications webhook delete` | — | ✓ |  |
| `cluster notifications webhook get` | — | ✓ |  |
| `cluster notifications webhook list` | ✓ | ✓ |  |
| `cluster notifications webhook set` | — | ✓ |  |
| `cluster options describe` | ✓ | — |  |
| `cluster options get` | ✓ | ✓ |  |
| `cluster options set` | — | ✓ |  |
| `cluster qemu cpu-flags` | ✓ | — |  |
| `cluster replication create` | — | · |  |
| `cluster replication delete` | — | · |  |
| `cluster replication get` | — | · |  |
| `cluster replication list` | ✓ | ✓ |  |
| `cluster replication set` | — | · |  |
| `cluster resources` | ✓ | — |  |
| `cluster status` | ✓ | — |  |
| `cluster tasks` | ✓ | — |  |

## `context`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `context add` | ✓ | — |  |
| `context copy` | ✓ | — |  |
| `context edit` | — | — | n/a — requires $EDITOR / interactive TTY — not safe to drive in headless e2e; covered in unit tests via EDITOR=true trick (test-strategy §4.2) |
| `context ls` | ✓ | — |  |
| `context previous` | ✓ | — |  |
| `context rm` | ✓ | — |  |
| `context select` | ✓ | — |  |
| `context show` | ✓ | — |  |
| `context validate` | ✓ | — |  |

## `init`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `init config` | ✓ | — |  |

## `lxc`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `lxc clone` | — | ✓ |  |
| `lxc config describe` | ✓ | — |  |
| `lxc config get` | ◑ | — |  |
| `lxc config pending` | ◑ | — |  |
| `lxc config set` | — | ✓ |  |
| `lxc console` | ◑ | ✓ |  |
| `lxc create` | — | ✓ |  |
| `lxc delete` | — | ✓ |  |
| `lxc disk move` | — | ✓ |  |
| `lxc disk resize` | — | ✓ |  |
| `lxc feature` | ◑ | — |  |
| `lxc firewall alias create` | — | ✓ |  |
| `lxc firewall alias delete` | — | ✓ |  |
| `lxc firewall alias get` | — | — | deferred — reads a single firewall alias by name — needs a pre-existing alias; not wired into the mutate phase; covered by unit tests |
| `lxc firewall alias list` | — | ✓ |  |
| `lxc firewall alias update` | — | ✓ |  |
| `lxc firewall ipset add` | — | ✓ |  |
| `lxc firewall ipset create` | — | ✓ |  |
| `lxc firewall ipset delete` | — | ✓ |  |
| `lxc firewall ipset get-member` | — | — | deferred — reads a single CIDR entry of an IP set — needs a pre-existing member; not wired into the mutate phase; covered by unit tests |
| `lxc firewall ipset list` | — | ✓ |  |
| `lxc firewall ipset remove` | — | ✓ |  |
| `lxc firewall ipset update-member` | — | ✓ |  |
| `lxc firewall log` | ◑ | — |  |
| `lxc firewall options describe` | ✓ | — |  |
| `lxc firewall options get` | ◑ | ✓ |  |
| `lxc firewall options set` | — | ✓ |  |
| `lxc firewall refs` | ◑ | — |  |
| `lxc firewall rules create` | — | ✓ |  |
| `lxc firewall rules delete` | — | ✓ |  |
| `lxc firewall rules get` | — | ✓ |  |
| `lxc firewall rules list` | ◑ | ✓ |  |
| `lxc firewall rules update` | — | ✓ |  |
| `lxc interfaces` | ◑ | ✓ |  |
| `lxc list` | ✓ | — |  |
| `lxc metrics` | ◑ | — |  |
| `lxc migrate` | — | ✓ |  |
| `lxc migrate check` | ◑ | — |  |
| `lxc permissions effective` | ◑ | — |  |
| `lxc permissions grant` | — | — | deferred — grants ACL roles on the container's /vms/{vmid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `lxc permissions list` | ◑ | — |  |
| `lxc permissions revoke` | — | — | deferred — revokes ACL roles on the container's /vms/{vmid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `lxc reboot` | — | ✓ |  |
| `lxc remote-migrate` | — | — | deferred — migrates a container to a different Proxmox VE cluster — requires two live clusters; no rollback without manual intervention; not exercised live |
| `lxc resume` | — | ✓ |  |
| `lxc rrd` | ◑ | — |  |
| `lxc security caps add` | — | — | deferred — grants a capability by editing /etc/pve/lxc/<vmid>.conf over root ssh, so it cannot be driven head-less by the read-only sweep; not wired into the mutate phase; covered by unit tests |
| `lxc security caps describe` | ✓ | — |  |
| `lxc security caps remove` | — | — | deferred — revokes a capability by editing /etc/pve/lxc/<vmid>.conf over root ssh, so it cannot be driven head-less by the read-only sweep; not wired into the mutate phase; covered by unit tests |
| `lxc security caps reset` | — | — | deferred — clears the capability whitelist in /etc/pve/lxc/<vmid>.conf over root ssh, so it cannot be driven head-less by the read-only sweep; not wired into the mutate phase; covered by unit tests |
| `lxc security caps set` | — | — | deferred — rewrites the container capability whitelist in /etc/pve/lxc/<vmid>.conf over root ssh, so it cannot be driven head-less by the read-only sweep; not wired into the mutate phase; covered by unit tests |
| `lxc security caps show` | ◑ | — |  |
| `lxc security features set` | — | — | deferred — mutates the container features= flags via the config API; not wired into the mutate phase; covered by unit tests |
| `lxc security features show` | ◑ | — |  |
| `lxc security list` | ◑ | — |  |
| `lxc security show` | ◑ | — |  |
| `lxc shutdown` | — | ✓ |  |
| `lxc snapshot create` | — | ✓ |  |
| `lxc snapshot delete` | — | ✓ |  |
| `lxc snapshot list` | ◑ | ✓ |  |
| `lxc snapshot rollback` | — | ✓ |  |
| `lxc snapshot show` | ◑ | — |  |
| `lxc snapshot update` | — | ✓ |  |
| `lxc start` | — | ✓ |  |
| `lxc status` | ◑ | ✓ |  |
| `lxc stop` | — | ✓ |  |
| `lxc suspend` | — | ✓ |  |
| `lxc template download` | — | ✓ |  |
| `lxc template list` | ✓ | — |  |
| `lxc to-template` | — | — | deferred — converts the discovered container into a template — irreversible for that instance and only sensible as the terminal step of a dedicated throwaway guest lifecycle; not exercised against a live container; covered by unit tests |

## `node`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `node apt changelog` | ◑ | — |  |
| `node apt list` | ◑ | — |  |
| `node apt repositories add` | — | — | deferred — adds a standard APT repository to the node's sources; not exercised live |
| `node apt repositories enable` | — | — | deferred — enables or disables a configured APT repository on the node; not exercised live |
| `node apt repositories list` | ◑ | — |  |
| `node apt templates download` | — | — | deferred — downloads a real appliance template tarball to a storage — bandwidth/storage-consuming; not exercised live; covered by unit tests |
| `node apt templates list` | ◑ | — |  |
| `node apt update` | — | ✓ |  |
| `node apt versions` | ◑ | — |  |
| `node capabilities qemu cpu` | ◑ | — |  |
| `node capabilities qemu cpu-flags` | ◑ | — |  |
| `node capabilities qemu machines` | ◑ | — |  |
| `node capabilities qemu migration` | ◑ | — |  |
| `node ceph cfg db` | ◑ | — |  |
| `node ceph cfg index` | ◑ | — |  |
| `node ceph cfg raw` | ◑ | — |  |
| `node ceph cfg value` | ◑ | — |  |
| `node ceph cmd-safety` | ◑ | — |  |
| `node ceph crush` | ◑ | — |  |
| `node ceph fs create` | — | — | deferred — creates a CephFS filesystem and its backing pools; not exercised live |
| `node ceph fs delete` | — | — | deferred — destroys a CephFS filesystem and optionally its pools; not exercised live |
| `node ceph fs list` | ◑ | — |  |
| `node ceph init` | — | — | deferred — initializes a Ceph cluster configuration on the node — cluster-wide and destructive; not exercised live |
| `node ceph log` | ◑ | — |  |
| `node ceph mds create` | — | — | deferred — provisions a Ceph metadata-server daemon on the node; not exercised live |
| `node ceph mds delete` | — | — | deferred — destroys a Ceph metadata-server daemon on the node; not exercised live |
| `node ceph mds list` | ◑ | — |  |
| `node ceph mgr create` | — | — | deferred — provisions a Ceph manager daemon on the node; not exercised live |
| `node ceph mgr delete` | — | — | deferred — destroys a Ceph manager daemon on the node; not exercised live |
| `node ceph mgr list` | ◑ | — |  |
| `node ceph mon create` | — | — | deferred — provisions a Ceph monitor daemon on the node; not exercised live |
| `node ceph mon delete` | — | — | deferred — destroys a Ceph monitor daemon on the node; not exercised live |
| `node ceph mon list` | ◑ | — |  |
| `node ceph osd create` | — | — | deferred — creates an OSD by wiping and consuming a block device; not exercised live |
| `node ceph osd delete` | — | — | deferred — destroys an OSD and optionally zaps its underlying volumes; not exercised live |
| `node ceph osd get` | ◑ | — |  |
| `node ceph osd in` | — | — | deferred — marks an OSD in, triggering cluster data movement; not exercised live |
| `node ceph osd list` | ◑ | — |  |
| `node ceph osd lv-info` | ◑ | — |  |
| `node ceph osd metadata` | ◑ | — |  |
| `node ceph osd out` | — | — | deferred — marks an OSD out, draining its data across the cluster; not exercised live |
| `node ceph osd scrub` | — | — | deferred — triggers an OSD scrub that adds cluster I/O load; not exercised live |
| `node ceph pool create` | — | — | deferred — creates a Ceph pool, consuming cluster capacity; not exercised live |
| `node ceph pool delete` | — | — | deferred — destroys a Ceph pool and permanently loses its data; not exercised live |
| `node ceph pool get` | ◑ | — |  |
| `node ceph pool list` | ◑ | — |  |
| `node ceph pool set` | — | — | deferred — reconfigures an existing Ceph pool's parameters; not exercised live |
| `node ceph pool status` | ◑ | — |  |
| `node ceph restart` | — | — | deferred — restarts Ceph services on the node — disruptive; not exercised live |
| `node ceph rules` | ◑ | — |  |
| `node ceph start` | — | — | deferred — starts Ceph services on the node — disruptive; not exercised live |
| `node ceph status` | ◑ | — |  |
| `node ceph stop` | — | — | deferred — stops Ceph services on the node — disruptive; not exercised live |
| `node cert acme delete` | — | — | deferred — removes the node's ACME certificate; not exercised live |
| `node cert acme list` | ◑ | — |  |
| `node cert acme order` | — | — | deferred — orders the node's ACME certificate (contacts Let's Encrypt); not exercised live |
| `node cert acme renew` | — | — | deferred — renews the node's ACME certificate (contacts Let's Encrypt); not exercised live |
| `node cert custom delete` | — | — | deferred — removes the node's custom API TLS certificate — could break TLS to the node; not exercised live |
| `node cert custom upload` | — | — | deferred — replaces the node's API TLS certificate — could break TLS to the node; not exercised live |
| `node cert list` | ◑ | — |  |
| `node config describe` | ✓ | — |  |
| `node config get` | ◑ | — |  |
| `node config set` | — | — | deferred — mutates node-level configuration (description, ACME, wake-on-LAN, ballooning target, startall delay); not exercised live; covered by unit tests |
| `node console` | — | — | deferred — opens a live SSH terminal aliased to `node shell`, so it cannot be driven head-less; not run live; covered by unit tests |
| `node disks create directory` | — | ✓ |  |
| `node disks create lvm` | — | ✓ |  |
| `node disks create lvmthin` | — | ✓ |  |
| `node disks create zfs` | — | ✓ |  |
| `node disks delete directory` | — | ✓ |  |
| `node disks delete lvm` | — | ✓ |  |
| `node disks delete lvmthin` | — | ✓ |  |
| `node disks delete zfs` | — | ✓ |  |
| `node disks get zfs` | ◑ | — |  |
| `node disks init-gpt` | — | ✓ |  |
| `node disks list` | ◑ | — |  |
| `node disks ls directory` | ◑ | — |  |
| `node disks ls lvm` | ◑ | — |  |
| `node disks ls lvmthin` | ◑ | — |  |
| `node disks ls zfs` | ◑ | — |  |
| `node disks smart` | ◑ | — |  |
| `node disks wipe` | — | — | deferred — BLOCKED: /nodes/{node}/disks/wipedisk is root@pam-only and rejects the API token ('user != root@pam'), like storage volume copy and cluster acme account; not invokable by the suite |
| `node dns get` | ◑ | ✓ |  |
| `node dns set` | — | ✓ |  |
| `node exec` | — | ✓ |  |
| `node execute` | — | — | n/a — runs arbitrary commands on the real host via the PVE API — security-sensitive; out of scope for automated e2e regardless of guarding |
| `node firewall log` | ◑ | — |  |
| `node firewall options describe` | ✓ | — |  |
| `node firewall options get` | ◑ | ✓ |  |
| `node firewall options set` | — | — | deferred — changes the host firewall policy — could cut the node off the network; not exercised live |
| `node firewall rules create` | — | ✓ |  |
| `node firewall rules delete` | — | ✓ |  |
| `node firewall rules get` | — | ✓ |  |
| `node firewall rules list` | ◑ | ✓ |  |
| `node firewall rules update` | — | ✓ |  |
| `node hardware mdev` | ◑ | — |  |
| `node hardware pci` | ◑ | — |  |
| `node hardware usb` | ◑ | — |  |
| `node hosts get` | ◑ | ✓ |  |
| `node hosts set` | — | ✓ |  |
| `node journal` | ◑ | — |  |
| `node list` | ✓ | — |  |
| `node migrateall` | — | — | deferred — migrates every guest off the node to a target (needs a second node); not exercised live; covered by unit tests |
| `node netstat` | ◑ | — |  |
| `node network apply` | — | — | deferred — reloads the staged host network configuration — could cut the node off the network; not exercised live |
| `node network create` | — | ✓ |  |
| `node network delete` | — | ✓ |  |
| `node network get` | ◑ | — |  |
| `node network list` | ◑ | — |  |
| `node network revert` | — | ✓ |  |
| `node network set` | — | ✓ |  |
| `node oci pull` | — | ✓ |  |
| `node oci tags` | — | ✓ |  |
| `node permissions effective` | ◑ | — |  |
| `node permissions grant` | — | — | deferred — grants ACL roles on the node's /nodes/{node} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `node permissions list` | ◑ | — |  |
| `node permissions revoke` | — | — | deferred — revokes ACL roles on the node's /nodes/{node} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `node query-url-metadata` | — | ✓ |  |
| `node reboot` | — | — | n/a — reboots the real host — would take the shared lab node offline; not automatable |
| `node replication get` | ◑ | — |  |
| `node replication list` | ◑ | — |  |
| `node replication log` | ◑ | — |  |
| `node replication run` | — | — | deferred — triggers an immediate replication sync to the target node (needs a configured job); not exercised live |
| `node replication status` | ◑ | — |  |
| `node report` | ◑ | — |  |
| `node rrddata` | ◑ | — |  |
| `node rsync` | — | ✓ |  |
| `node scan cifs` | — | ✓ |  |
| `node scan iscsi` | — | ✓ |  |
| `node scan lvm` | ◑ | — |  |
| `node scan lvmthin` | ◑ | — |  |
| `node scan nfs` | — | ✓ |  |
| `node scan pbs` | — | ✓ |  |
| `node scan zfs` | ◑ | — |  |
| `node services get` | ◑ | — |  |
| `node services list` | ◑ | — |  |
| `node services reload` | — | ✓ |  |
| `node services restart` | — | ✓ |  |
| `node services start` | — | ✓ |  |
| `node services state` | ◑ | — |  |
| `node services stop` | — | ✓ |  |
| `node shell` | — | — | deferred — opens a live SSH terminal on the node, so it cannot be driven head-less; not run live; covered by unit tests |
| `node shutdown` | — | — | n/a — shuts down the real host — would take the shared lab node offline; not automatable |
| `node spiceshell` | — | — | n/a — requests an interactive SPICE console-proxy ticket — not automatable head-less; covered by unit tests |
| `node ssh` | — | ✓ |  |
| `node startall` | — | ✓ |  |
| `node status` | ◑ | — |  |
| `node stopall` | — | ✓ |  |
| `node subscription delete` | — | ✓ |  |
| `node subscription get` | ◑ | — |  |
| `node subscription set` | — | — | deferred — sets the node's subscription key (changes licensing state); not exercised live; covered by unit tests |
| `node subscription update` | — | ✓ |  |
| `node suspendall` | — | ✓ |  |
| `node syslog` | ◑ | — |  |
| `node task list` | ◑ | — |  |
| `node task log` | ◑ | — |  |
| `node task status` | ◑ | — |  |
| `node task stop` | — | ✓ |  |
| `node task wait` | ◑ | — |  |
| `node termproxy` | — | — | n/a — requests an interactive websocket terminal-proxy ticket — not automatable head-less; covered by unit tests |
| `node time get` | ◑ | ✓ |  |
| `node time set` | — | ✓ |  |
| `node vncshell` | — | — | n/a — requests an interactive VNC console-proxy ticket — not automatable head-less; covered by unit tests |
| `node vzdump` | — | ✓ |  |
| `node vzdump defaults` | ◑ | — |  |
| `node vzdump extract-config` | ◑ | — |  |
| `node wakeonlan` | — | — | deferred — sends a Wake-on-LAN packet to power on another node — the API rejects waking the local node, and this is a single-node cluster, so there is no remote target; not exercised live; covered by unit tests |

## `pbs`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `pbs acl ls` | — | — | **uncovered** |
| `pbs acl update` | — | — | **uncovered** |
| `pbs acme account add` | — | — | **uncovered** |
| `pbs acme account delete` | — | — | **uncovered** |
| `pbs acme account ls` | — | — | **uncovered** |
| `pbs acme account show` | — | — | **uncovered** |
| `pbs acme account update` | — | — | **uncovered** |
| `pbs acme challenge-schema ls` | — | — | **uncovered** |
| `pbs acme directories ls` | — | — | **uncovered** |
| `pbs acme plugin add` | — | — | **uncovered** |
| `pbs acme plugin delete` | — | — | **uncovered** |
| `pbs acme plugin ls` | — | — | **uncovered** |
| `pbs acme plugin show` | — | — | **uncovered** |
| `pbs acme plugin update` | — | — | **uncovered** |
| `pbs acme tos show` | — | — | **uncovered** |
| `pbs api delete` | — | — | **uncovered** |
| `pbs api get` | — | — | **uncovered** |
| `pbs api post` | — | — | **uncovered** |
| `pbs api put` | — | — | **uncovered** |
| `pbs datastore create` | — | — | **uncovered** |
| `pbs datastore delete` | — | — | **uncovered** |
| `pbs datastore ls` | — | — | **uncovered** |
| `pbs datastore rrd` | — | — | **uncovered** |
| `pbs datastore show` | — | — | **uncovered** |
| `pbs datastore status` | — | — | **uncovered** |
| `pbs datastore update` | — | — | **uncovered** |
| `pbs datastore usage` | — | — | **uncovered** |
| `pbs encryption-key add` | — | — | **uncovered** |
| `pbs encryption-key delete` | — | — | **uncovered** |
| `pbs encryption-key ls` | — | — | **uncovered** |
| `pbs encryption-key toggle-archive` | — | — | **uncovered** |
| `pbs gc ls` | — | — | **uncovered** |
| `pbs gc run` | — | — | **uncovered** |
| `pbs gc status` | — | — | **uncovered** |
| `pbs group delete` | — | — | **uncovered** |
| `pbs group ls` | — | — | **uncovered** |
| `pbs group notes` | — | — | **uncovered** |
| `pbs metrics data` | — | — | **uncovered** |
| `pbs metrics influxdb-http add` | — | — | **uncovered** |
| `pbs metrics influxdb-http delete` | — | — | **uncovered** |
| `pbs metrics influxdb-http ls` | — | — | **uncovered** |
| `pbs metrics influxdb-http show` | — | — | **uncovered** |
| `pbs metrics influxdb-http update` | — | — | **uncovered** |
| `pbs metrics influxdb-udp add` | — | — | **uncovered** |
| `pbs metrics influxdb-udp delete` | — | — | **uncovered** |
| `pbs metrics influxdb-udp ls` | — | — | **uncovered** |
| `pbs metrics influxdb-udp show` | — | — | **uncovered** |
| `pbs metrics influxdb-udp update` | — | — | **uncovered** |
| `pbs node apt changelog` | — | — | **uncovered** |
| `pbs node apt ls` | — | — | **uncovered** |
| `pbs node apt repo-add` | — | — | **uncovered** |
| `pbs node apt repo-update` | — | — | **uncovered** |
| `pbs node apt repositories` | — | — | **uncovered** |
| `pbs node apt update` | — | — | **uncovered** |
| `pbs node apt versions` | — | — | **uncovered** |
| `pbs node certificates acme order` | — | — | **uncovered** |
| `pbs node certificates acme renew` | — | — | **uncovered** |
| `pbs node certificates custom delete` | — | — | **uncovered** |
| `pbs node certificates custom upload` | — | — | **uncovered** |
| `pbs node certificates info` | — | — | **uncovered** |
| `pbs node config show` | — | — | **uncovered** |
| `pbs node config update` | — | — | **uncovered** |
| `pbs node disks directory create` | — | — | **uncovered** |
| `pbs node disks directory delete` | — | — | **uncovered** |
| `pbs node disks directory ls` | — | — | **uncovered** |
| `pbs node disks initgpt` | — | — | **uncovered** |
| `pbs node disks ls` | — | — | **uncovered** |
| `pbs node disks smart` | — | — | **uncovered** |
| `pbs node disks wipe` | — | — | **uncovered** |
| `pbs node disks zfs create` | — | — | **uncovered** |
| `pbs node disks zfs ls` | — | — | **uncovered** |
| `pbs node disks zfs show` | — | — | **uncovered** |
| `pbs node dns show` | — | — | **uncovered** |
| `pbs node dns update` | — | — | **uncovered** |
| `pbs node identity` | — | — | **uncovered** |
| `pbs node journal` | — | — | **uncovered** |
| `pbs node ls` | — | — | **uncovered** |
| `pbs node network apply` | — | — | **uncovered** |
| `pbs node network create` | — | — | **uncovered** |
| `pbs node network delete` | — | — | **uncovered** |
| `pbs node network ls` | — | — | **uncovered** |
| `pbs node network revert` | — | — | **uncovered** |
| `pbs node network show` | — | — | **uncovered** |
| `pbs node network update` | — | — | **uncovered** |
| `pbs node reboot` | — | — | **uncovered** |
| `pbs node report` | — | — | **uncovered** |
| `pbs node rrd` | — | — | **uncovered** |
| `pbs node services ls` | — | — | **uncovered** |
| `pbs node services reload` | — | — | **uncovered** |
| `pbs node services restart` | — | — | **uncovered** |
| `pbs node services show` | — | — | **uncovered** |
| `pbs node services start` | — | — | **uncovered** |
| `pbs node services state` | — | — | **uncovered** |
| `pbs node services stop` | — | — | **uncovered** |
| `pbs node shutdown` | — | — | **uncovered** |
| `pbs node status` | — | — | **uncovered** |
| `pbs node subscription delete` | — | — | **uncovered** |
| `pbs node subscription set` | — | — | **uncovered** |
| `pbs node subscription show` | — | — | **uncovered** |
| `pbs node subscription update` | — | — | **uncovered** |
| `pbs node syslog` | — | — | **uncovered** |
| `pbs node tasks delete` | — | — | **uncovered** |
| `pbs node tasks log` | — | — | **uncovered** |
| `pbs node tasks ls` | — | — | **uncovered** |
| `pbs node tasks show` | — | — | **uncovered** |
| `pbs node time show` | — | — | **uncovered** |
| `pbs node time update` | — | — | **uncovered** |
| `pbs notification endpoint gotify add` | — | — | **uncovered** |
| `pbs notification endpoint gotify delete` | — | — | **uncovered** |
| `pbs notification endpoint gotify ls` | — | — | **uncovered** |
| `pbs notification endpoint gotify show` | — | — | **uncovered** |
| `pbs notification endpoint gotify update` | — | — | **uncovered** |
| `pbs notification endpoint sendmail add` | — | — | **uncovered** |
| `pbs notification endpoint sendmail delete` | — | — | **uncovered** |
| `pbs notification endpoint sendmail ls` | — | — | **uncovered** |
| `pbs notification endpoint sendmail show` | — | — | **uncovered** |
| `pbs notification endpoint sendmail update` | — | — | **uncovered** |
| `pbs notification endpoint smtp add` | — | — | **uncovered** |
| `pbs notification endpoint smtp delete` | — | — | **uncovered** |
| `pbs notification endpoint smtp ls` | — | — | **uncovered** |
| `pbs notification endpoint smtp show` | — | — | **uncovered** |
| `pbs notification endpoint smtp update` | — | — | **uncovered** |
| `pbs notification endpoint webhook add` | — | — | **uncovered** |
| `pbs notification endpoint webhook delete` | — | — | **uncovered** |
| `pbs notification endpoint webhook ls` | — | — | **uncovered** |
| `pbs notification endpoint webhook show` | — | — | **uncovered** |
| `pbs notification endpoint webhook update` | — | — | **uncovered** |
| `pbs notification matcher add` | — | — | **uncovered** |
| `pbs notification matcher delete` | — | — | **uncovered** |
| `pbs notification matcher field-values ls` | — | — | **uncovered** |
| `pbs notification matcher fields ls` | — | — | **uncovered** |
| `pbs notification matcher ls` | — | — | **uncovered** |
| `pbs notification matcher show` | — | — | **uncovered** |
| `pbs notification matcher update` | — | — | **uncovered** |
| `pbs notification target ls` | — | — | **uncovered** |
| `pbs notification target test` | — | — | **uncovered** |
| `pbs permission ls` | — | — | **uncovered** |
| `pbs ping` | — | — | **uncovered** |
| `pbs prune job add` | — | — | **uncovered** |
| `pbs prune job delete` | — | — | **uncovered** |
| `pbs prune job ls` | — | — | **uncovered** |
| `pbs prune job run` | — | — | **uncovered** |
| `pbs prune job show` | — | — | **uncovered** |
| `pbs prune job update` | — | — | **uncovered** |
| `pbs prune run` | — | — | **uncovered** |
| `pbs prune simulate` | — | — | **uncovered** |
| `pbs realm ad add` | — | — | **uncovered** |
| `pbs realm ad delete` | — | — | **uncovered** |
| `pbs realm ad ls` | — | — | **uncovered** |
| `pbs realm ad show` | — | — | **uncovered** |
| `pbs realm ad update` | — | — | **uncovered** |
| `pbs realm ldap add` | — | — | **uncovered** |
| `pbs realm ldap delete` | — | — | **uncovered** |
| `pbs realm ldap ls` | — | — | **uncovered** |
| `pbs realm ldap show` | — | — | **uncovered** |
| `pbs realm ldap update` | — | — | **uncovered** |
| `pbs realm ls` | — | — | **uncovered** |
| `pbs realm openid add` | — | — | **uncovered** |
| `pbs realm openid delete` | — | — | **uncovered** |
| `pbs realm openid ls` | — | — | **uncovered** |
| `pbs realm openid show` | — | — | **uncovered** |
| `pbs realm openid update` | — | — | **uncovered** |
| `pbs realm pam show` | — | — | **uncovered** |
| `pbs realm pam update` | — | — | **uncovered** |
| `pbs realm pbs show` | — | — | **uncovered** |
| `pbs realm pbs update` | — | — | **uncovered** |
| `pbs realm sync` | — | — | **uncovered** |
| `pbs remote add` | — | — | **uncovered** |
| `pbs remote delete` | — | — | **uncovered** |
| `pbs remote ls` | — | — | **uncovered** |
| `pbs remote scan groups` | — | — | **uncovered** |
| `pbs remote scan ls` | — | — | **uncovered** |
| `pbs remote scan namespaces` | — | — | **uncovered** |
| `pbs remote show` | — | — | **uncovered** |
| `pbs remote update` | — | — | **uncovered** |
| `pbs role ls` | — | — | **uncovered** |
| `pbs snapshot delete` | — | — | **uncovered** |
| `pbs snapshot files` | — | — | **uncovered** |
| `pbs snapshot ls` | — | — | **uncovered** |
| `pbs snapshot notes` | — | — | **uncovered** |
| `pbs snapshot protect` | — | — | **uncovered** |
| `pbs snapshot show` | — | — | **uncovered** |
| `pbs snapshot unprotect` | — | — | **uncovered** |
| `pbs status datastore-usage` | — | — | **uncovered** |
| `pbs sync job add` | — | — | **uncovered** |
| `pbs sync job delete` | — | — | **uncovered** |
| `pbs sync job ls` | — | — | **uncovered** |
| `pbs sync job run` | — | — | **uncovered** |
| `pbs sync job show` | — | — | **uncovered** |
| `pbs sync job update` | — | — | **uncovered** |
| `pbs sync ls` | — | — | **uncovered** |
| `pbs sync pull` | — | — | **uncovered** |
| `pbs sync push` | — | — | **uncovered** |
| `pbs tape backup` | — | — | **uncovered** |
| `pbs tape changer add` | — | — | **uncovered** |
| `pbs tape changer delete` | — | — | **uncovered** |
| `pbs tape changer ls` | — | — | **uncovered** |
| `pbs tape changer scan` | — | — | **uncovered** |
| `pbs tape changer show` | — | — | **uncovered** |
| `pbs tape changer status` | — | — | **uncovered** |
| `pbs tape changer transfer` | — | — | **uncovered** |
| `pbs tape changer update` | — | — | **uncovered** |
| `pbs tape drive add` | — | — | **uncovered** |
| `pbs tape drive barcode-label` | — | — | **uncovered** |
| `pbs tape drive cartridge-memory` | — | — | **uncovered** |
| `pbs tape drive catalog` | — | — | **uncovered** |
| `pbs tape drive clean` | — | — | **uncovered** |
| `pbs tape drive delete` | — | — | **uncovered** |
| `pbs tape drive eject` | — | — | **uncovered** |
| `pbs tape drive export` | — | — | **uncovered** |
| `pbs tape drive format` | — | — | **uncovered** |
| `pbs tape drive inventory` | — | — | **uncovered** |
| `pbs tape drive label` | — | — | **uncovered** |
| `pbs tape drive load-media` | — | — | **uncovered** |
| `pbs tape drive load-slot` | — | — | **uncovered** |
| `pbs tape drive ls` | — | — | **uncovered** |
| `pbs tape drive read-label` | — | — | **uncovered** |
| `pbs tape drive restore-key` | — | — | **uncovered** |
| `pbs tape drive rewind` | — | — | **uncovered** |
| `pbs tape drive scan` | — | — | **uncovered** |
| `pbs tape drive show` | — | — | **uncovered** |
| `pbs tape drive status` | — | — | **uncovered** |
| `pbs tape drive unload` | — | — | **uncovered** |
| `pbs tape drive update` | — | — | **uncovered** |
| `pbs tape drive update-inventory` | — | — | **uncovered** |
| `pbs tape drive volume-statistics` | — | — | **uncovered** |
| `pbs tape job add` | — | — | **uncovered** |
| `pbs tape job delete` | — | — | **uncovered** |
| `pbs tape job ls` | — | — | **uncovered** |
| `pbs tape job run` | — | — | **uncovered** |
| `pbs tape job show` | — | — | **uncovered** |
| `pbs tape job status` | — | — | **uncovered** |
| `pbs tape job update` | — | — | **uncovered** |
| `pbs tape key add` | — | — | **uncovered** |
| `pbs tape key delete` | — | — | **uncovered** |
| `pbs tape key ls` | — | — | **uncovered** |
| `pbs tape key show` | — | — | **uncovered** |
| `pbs tape key update` | — | — | **uncovered** |
| `pbs tape media content` | — | — | **uncovered** |
| `pbs tape media destroy` | — | — | **uncovered** |
| `pbs tape media ls` | — | — | **uncovered** |
| `pbs tape media move` | — | — | **uncovered** |
| `pbs tape media set-status` | — | — | **uncovered** |
| `pbs tape media sets` | — | — | **uncovered** |
| `pbs tape pool add` | — | — | **uncovered** |
| `pbs tape pool delete` | — | — | **uncovered** |
| `pbs tape pool ls` | — | — | **uncovered** |
| `pbs tape pool show` | — | — | **uncovered** |
| `pbs tape pool update` | — | — | **uncovered** |
| `pbs tape restore` | — | — | **uncovered** |
| `pbs traffic add` | — | — | **uncovered** |
| `pbs traffic current` | — | — | **uncovered** |
| `pbs traffic delete` | — | — | **uncovered** |
| `pbs traffic ls` | — | — | **uncovered** |
| `pbs traffic show` | — | — | **uncovered** |
| `pbs traffic update` | — | — | **uncovered** |
| `pbs user add` | — | — | **uncovered** |
| `pbs user delete` | — | — | **uncovered** |
| `pbs user ls` | — | — | **uncovered** |
| `pbs user passwd` | — | — | **uncovered** |
| `pbs user show` | — | — | **uncovered** |
| `pbs user token add` | — | — | **uncovered** |
| `pbs user token delete` | — | — | **uncovered** |
| `pbs user token ls` | — | — | **uncovered** |
| `pbs user token show` | — | — | **uncovered** |
| `pbs user token update` | — | — | **uncovered** |
| `pbs user unlock-tfa` | — | — | **uncovered** |
| `pbs user update` | — | — | **uncovered** |
| `pbs verify job add` | — | — | **uncovered** |
| `pbs verify job delete` | — | — | **uncovered** |
| `pbs verify job ls` | — | — | **uncovered** |
| `pbs verify job run` | — | — | **uncovered** |
| `pbs verify job show` | — | — | **uncovered** |
| `pbs verify job update` | — | — | **uncovered** |
| `pbs verify run` | — | — | **uncovered** |
| `pbs version` | — | — | **uncovered** |

## `pool`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `pool create` | — | ✓ | error-contract checked |
| `pool delete` | — | ✓ |  |
| `pool get` | ◑ | — |  |
| `pool list` | ✓ | — |  |
| `pool permissions effective` | ◑ | — |  |
| `pool permissions grant` | — | — | deferred — grants ACL roles on the pool's singular /pool/{poolid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pool permissions list` | ◑ | — |  |
| `pool permissions revoke` | — | — | deferred — revokes ACL roles on the pool's singular /pool/{poolid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pool set` | — | ✓ |  |
| `pool show` | ◑ | — |  |

## `qemu`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `qemu agent` | — | ✓ |  |
| `qemu agent exec` | — | ✓ |  |
| `qemu agent exec-status` | — | ✓ |  |
| `qemu agent file-read` | — | ✓ |  |
| `qemu agent file-write` | — | ✓ |  |
| `qemu agent set-user-password` | — | ✓ |  |
| `qemu clone` | — | ✓ |  |
| `qemu cloudinit dump` | — | ✓ |  |
| `qemu cloudinit pending` | ◑ | ✓ |  |
| `qemu cloudinit update` | — | ✓ |  |
| `qemu config describe` | ✓ | — |  |
| `qemu config get` | ◑ | ✓ |  |
| `qemu config pending` | — | ✓ |  |
| `qemu config set` | — | ✓ |  |
| `qemu console` | ◑ | ✓ |  |
| `qemu cpu list` | ✓ | — |  |
| `qemu cpu-flags` | ✓ | — |  |
| `qemu create` | — | ✓ |  |
| `qemu delete` | — | ✓ |  |
| `qemu disk move` | — | ✓ |  |
| `qemu disk resize` | — | ✓ |  |
| `qemu disk unlink` | — | ✓ |  |
| `qemu feature` | ◑ | — |  |
| `qemu firewall alias create` | — | ✓ |  |
| `qemu firewall alias delete` | — | ✓ |  |
| `qemu firewall alias get` | — | — | deferred — reads a single firewall alias by name — needs a pre-existing alias; not wired into the mutate phase; covered by unit tests |
| `qemu firewall alias list` | — | ✓ |  |
| `qemu firewall alias update` | — | ✓ |  |
| `qemu firewall ipset add` | — | ✓ |  |
| `qemu firewall ipset create` | — | ✓ |  |
| `qemu firewall ipset delete` | — | ✓ |  |
| `qemu firewall ipset get-member` | — | — | deferred — reads a single CIDR entry of an IP set — needs a pre-existing member; not wired into the mutate phase; covered by unit tests |
| `qemu firewall ipset list` | — | ✓ |  |
| `qemu firewall ipset remove` | — | ✓ |  |
| `qemu firewall ipset update-member` | — | ✓ |  |
| `qemu firewall log` | ◑ | — |  |
| `qemu firewall options describe` | ✓ | — |  |
| `qemu firewall options get` | ◑ | ✓ |  |
| `qemu firewall options set` | — | ✓ |  |
| `qemu firewall refs` | ◑ | — |  |
| `qemu firewall rules create` | — | ✓ |  |
| `qemu firewall rules delete` | — | ✓ |  |
| `qemu firewall rules get` | — | ✓ |  |
| `qemu firewall rules list` | ◑ | ✓ |  |
| `qemu firewall rules update` | — | ✓ |  |
| `qemu list` | ✓ | — |  |
| `qemu machine list` | ✓ | — |  |
| `qemu metrics` | ◑ | — |  |
| `qemu migrate` | — | ✓ |  |
| `qemu migrate capabilities` | ✓ | — |  |
| `qemu migrate check` | ◑ | — |  |
| `qemu monitor` | — | ✓ |  |
| `qemu permissions effective` | ◑ | — |  |
| `qemu permissions grant` | — | — | deferred — grants ACL roles on the VM's /vms/{vmid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `qemu permissions list` | ◑ | — |  |
| `qemu permissions revoke` | — | — | deferred — revokes ACL roles on the VM's /vms/{vmid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `qemu reboot` | — | · |  |
| `qemu remote-migrate` | — | — | deferred — migrates a VM to a different Proxmox VE cluster — requires two live clusters with shared or compatible storage; no rollback without manual intervention; not exercised live |
| `qemu reset` | — | ✓ |  |
| `qemu resume` | — | ✓ |  |
| `qemu rrd` | ◑ | — |  |
| `qemu security agent set` | — | — | deferred — sets the guest-agent config option (agent=); not wired into the mutate phase; covered by unit tests |
| `qemu security agent show` | ◑ | — |  |
| `qemu security confidential clear` | — | — | deferred — removes the confidential-computing configuration; not wired into the mutate phase; covered by unit tests |
| `qemu security confidential set` | — | — | deferred — configures AMD SEV / Intel TDX memory encryption, which needs matching host CPU/firmware support; not wired into the mutate phase; covered by unit tests |
| `qemu security confidential show` | ◑ | — |  |
| `qemu security cpu-flags describe` | ✓ | — |  |
| `qemu security cpu-flags set` | — | — | deferred — edits the VM's security-relevant CPU flags; not wired into the mutate phase; covered by unit tests |
| `qemu security cpu-flags show` | ◑ | — |  |
| `qemu security list` | ◑ | — |  |
| `qemu security nic firewall` | — | — | deferred — toggles per-NIC firewall coverage; not wired into the mutate phase; covered by unit tests |
| `qemu security nic show` | ◑ | — |  |
| `qemu security protection disable` | — | — | deferred — clears the VM protection flag; not wired into the mutate phase; covered by unit tests |
| `qemu security protection enable` | — | — | deferred — sets the VM protection flag; not wired into the mutate phase; covered by unit tests |
| `qemu security secureboot enable` | — | — | deferred — switches firmware to OVMF and allocates an EFI vars disk; not wired into the mutate phase; covered by unit tests |
| `qemu security secureboot show` | ◑ | — |  |
| `qemu security show` | ◑ | — |  |
| `qemu security tpm add` | — | — | deferred — allocates a TPM state disk; not wired into the mutate phase; covered by unit tests |
| `qemu security tpm remove` | — | — | deferred — destroys the TPM state device and every key sealed in it; not wired into the mutate phase; covered by unit tests |
| `qemu security tpm show` | ◑ | — |  |
| `qemu sendkey` | — | ✓ |  |
| `qemu shutdown` | — | ✓ |  |
| `qemu snapshot create` | — | ✓ | error-contract checked |
| `qemu snapshot delete` | — | ✓ |  |
| `qemu snapshot list` | ◑ | ✓ |  |
| `qemu snapshot rollback` | — | ✓ |  |
| `qemu snapshot show` | ◑ | — |  |
| `qemu snapshot update` | — | ✓ |  |
| `qemu ssh` | — | — | n/a — opens an interactive SSH tunnel into a guest — not automatable head-less, same class as `node shell`/`node console`; covered by unit tests |
| `qemu start` | — | ✓ |  |
| `qemu status` | ◑ | ✓ |  |
| `qemu stop` | — | ✓ |  |
| `qemu suspend` | — | ✓ |  |
| `qemu template` | — | ✓ |  |

## `rsync`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `rsync` | — | — | deferred — transfers files to/from a live node over SSH, so it cannot be driven head-less by the read-only sweep; shares the `pve node rsync` code path (SSH-gated live coverage there) but this top-level alias is not yet wired into the mutate phase; covered by unit tests |

## `sdn`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `sdn apply` | — | ✓ |  |
| `sdn controller create` | — | ✓ |  |
| `sdn controller delete` | — | ✓ |  |
| `sdn controller get` | — | ✓ |  |
| `sdn controller list` | ✓ | — |  |
| `sdn controller set` | — | ✓ |  |
| `sdn dns create` | — | ✓ |  |
| `sdn dns delete` | — | ✓ |  |
| `sdn dns get` | — | ✓ |  |
| `sdn dns list` | ✓ | — |  |
| `sdn dns set` | — | ✓ |  |
| `sdn dry-run` | ◑ | — |  |
| `sdn fabric create` | — | ✓ |  |
| `sdn fabric delete` | — | ✓ |  |
| `sdn fabric get` | — | ✓ |  |
| `sdn fabric list` | ◑ | — |  |
| `sdn fabric list-all` | ◑ | — |  |
| `sdn fabric node create` | — | ✓ |  |
| `sdn fabric node delete` | — | ✓ |  |
| `sdn fabric node get` | — | ✓ |  |
| `sdn fabric node list` | ◑ | — |  |
| `sdn fabric node set` | — | ✓ |  |
| `sdn fabric set` | — | ✓ |  |
| `sdn ipam create` | — | ✓ |  |
| `sdn ipam delete` | — | ✓ |  |
| `sdn ipam get` | — | ✓ |  |
| `sdn ipam list` | ✓ | ✓ |  |
| `sdn ipam set` | — | — | deferred — the pve IPAM exposes no settable properties; the netbox/phpipam types validate a reachable external backend on create — covered by unit tests |
| `sdn ipam status` | ◑ | — |  |
| `sdn lock acquire` | — | ✓ |  |
| `sdn lock release` | — | ✓ |  |
| `sdn prefix-list create` | — | ✓ |  |
| `sdn prefix-list delete` | — | ✓ |  |
| `sdn prefix-list entry add` | — | ✓ |  |
| `sdn prefix-list entry delete` | — | ✓ |  |
| `sdn prefix-list entry get` | — | ✓ |  |
| `sdn prefix-list entry list` | — | ✓ |  |
| `sdn prefix-list entry set` | — | ✓ |  |
| `sdn prefix-list get` | — | ✓ |  |
| `sdn prefix-list list` | ◑ | — |  |
| `sdn prefix-list set` | — | ✓ |  |
| `sdn rollback` | — | — | deferred — discards ALL pending SDN changes cluster-wide; not exercised live; covered by unit tests |
| `sdn route-map entry add` | — | ✓ |  |
| `sdn route-map entry delete` | — | ✓ |  |
| `sdn route-map entry get` | — | ✓ |  |
| `sdn route-map entry list` | ◑ | — |  |
| `sdn route-map entry set` | — | ✓ |  |
| `sdn route-map get` | — | ✓ |  |
| `sdn route-map list` | ◑ | — |  |
| `sdn status fabrics interfaces` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `sdn status fabrics neighbors` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `sdn status fabrics routes` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `sdn status vnets mac-vrf` | — | ✓ |  |
| `sdn status zones bridges` | — | ✓ |  |
| `sdn status zones content` | — | ✓ |  |
| `sdn status zones get` | — | ✓ |  |
| `sdn status zones ip-vrf` | — | ✓ |  |
| `sdn subnet create` | — | ✓ |  |
| `sdn subnet delete` | — | ✓ |  |
| `sdn subnet list` | ◑ | — |  |
| `sdn subnet set` | — | ✓ |  |
| `sdn subnet show` | ◑ | — |  |
| `sdn vnet create` | — | ✓ |  |
| `sdn vnet delete` | — | ✓ |  |
| `sdn vnet firewall options describe` | ✓ | — |  |
| `sdn vnet firewall options get` | ◑ | ✓ |  |
| `sdn vnet firewall options set` | — | ✓ |  |
| `sdn vnet firewall rules create` | — | ✓ |  |
| `sdn vnet firewall rules delete` | — | ✓ |  |
| `sdn vnet firewall rules get` | — | ✓ |  |
| `sdn vnet firewall rules list` | ◑ | ✓ |  |
| `sdn vnet firewall rules set` | — | ✓ |  |
| `sdn vnet ips create` | — | ✓ |  |
| `sdn vnet ips delete` | — | ✓ |  |
| `sdn vnet ips set` | — | ✓ |  |
| `sdn vnet list` | ✓ | — |  |
| `sdn vnet permissions effective` | ◑ | — |  |
| `sdn vnet permissions grant` | — | — | deferred — grants ACL roles on the vnet's derived /sdn/zones/{zone}/{vnet} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `sdn vnet permissions list` | ◑ | — |  |
| `sdn vnet permissions revoke` | — | — | deferred — revokes ACL roles on the vnet's derived /sdn/zones/{zone}/{vnet} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `sdn vnet set` | — | ✓ |  |
| `sdn vnet show` | ◑ | — |  |
| `sdn zone create` | — | ✓ |  |
| `sdn zone delete` | — | ✓ |  |
| `sdn zone list` | ✓ | — |  |
| `sdn zone permissions effective` | ◑ | — |  |
| `sdn zone permissions grant` | — | — | deferred — grants ACL roles on the zone's /sdn/zones/{zone} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `sdn zone permissions list` | ◑ | — |  |
| `sdn zone permissions revoke` | — | — | deferred — revokes ACL roles on the zone's /sdn/zones/{zone} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `sdn zone set` | — | ✓ |  |
| `sdn zone show` | ◑ | — |  |

## `ssh`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `ssh` | — | — | deferred — opens a live SSH session on the resolved node, so it cannot be driven head-less by the read-only sweep; shares the `pve node ssh` code path (SSH-gated live coverage there) but this top-level alias is not yet wired into the mutate phase; covered by unit tests |

## `storage`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `storage aplinfo download` | — | — | deferred — downloads a real appliance template tarball to a storage — bandwidth/storage-consuming; not exercised live; covered by unit tests |
| `storage aplinfo list` | ◑ | — |  |
| `storage content` | ◑ | — |  |
| `storage create` | — | ✓ |  |
| `storage delete` | — | ✓ |  |
| `storage describe` | ✓ | — |  |
| `storage download-url` | — | ✓ |  |
| `storage file-restore download` | — | — | deferred — extracts a file from a PBS snapshot — lab has no Proxmox Backup Server storage; not exercised live; covered by unit tests |
| `storage file-restore list` | — | — | deferred — browses files inside a PBS snapshot — lab has no Proxmox Backup Server storage; not exercised live; covered by unit tests |
| `storage get` | ◑ | ✓ |  |
| `storage identity` | ◑ | — |  |
| `storage import-metadata` | — | ✓ |  |
| `storage list` | ✓ | — |  |
| `storage node-list` | ◑ | — |  |
| `storage oci-pull` | — | — | deferred — pulls a real OCI image from a registry into a storage — needs registry egress and consumes storage; not exercised live from this tree; covered by unit tests |
| `storage permissions effective` | ◑ | — |  |
| `storage permissions grant` | — | — | deferred — grants ACL roles on the storage's /storage/{storage} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `storage permissions list` | ◑ | — |  |
| `storage permissions revoke` | — | — | deferred — revokes ACL roles on the storage's /storage/{storage} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `storage prune` | ◑ | ✓ |  |
| `storage rrd` | ◑ | — |  |
| `storage rrddata` | ◑ | — |  |
| `storage set` | — | ✓ |  |
| `storage status` | ◑ | — |  |
| `storage upload` | — | ✓ |  |
| `storage volume alloc` | — | ✓ |  |
| `storage volume copy` | — | — | deferred — copies a volume to a new target — the copy endpoint is restricted to root@pam and rejects API-token auth; not exercisable by the e2e suite — covered by unit tests |
| `storage volume delete` | — | ✓ |  |
| `storage volume get` | ◑ | ✓ |  |
| `storage volume set` | — | ✓ |  |

## `task`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `task cluster-list` | ✓ | — |  |
| `task list` | ✓ | — |  |
| `task log` | ◑ | — |  |
| `task status` | ◑ | — |  |
| `task stop` | — | ✓ |  |
| `task wait` | — | ✓ |  |

## `version`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `version` | ✓ | — |  |
| `version client` | ✓ | — |  |

## Uncovered leaves

Leaves exercised by neither suite. These are genuine coverage gaps — candidates for read-only sweep checks (the `get`/`list`/`show` verbs) or isolated mutate-phase coverage (the `create`/`set`/`delete` verbs). Each is listed inline per tree for a compact gap view.

**`pbs`** (276) — `pbs acl ls`, `pbs acl update`, `pbs acme account add`, `pbs acme account delete`, `pbs acme account ls`, `pbs acme account show`, `pbs acme account update`, `pbs acme challenge-schema ls`, `pbs acme directories ls`, `pbs acme plugin add`, `pbs acme plugin delete`, `pbs acme plugin ls`, `pbs acme plugin show`, `pbs acme plugin update`, `pbs acme tos show`, `pbs api delete`, `pbs api get`, `pbs api post`, `pbs api put`, `pbs datastore create`, `pbs datastore delete`, `pbs datastore ls`, `pbs datastore rrd`, `pbs datastore show`, `pbs datastore status`, `pbs datastore update`, `pbs datastore usage`, `pbs encryption-key add`, `pbs encryption-key delete`, `pbs encryption-key ls`, `pbs encryption-key toggle-archive`, `pbs gc ls`, `pbs gc run`, `pbs gc status`, `pbs group delete`, `pbs group ls`, `pbs group notes`, `pbs metrics data`, `pbs metrics influxdb-http add`, `pbs metrics influxdb-http delete`, `pbs metrics influxdb-http ls`, `pbs metrics influxdb-http show`, `pbs metrics influxdb-http update`, `pbs metrics influxdb-udp add`, `pbs metrics influxdb-udp delete`, `pbs metrics influxdb-udp ls`, `pbs metrics influxdb-udp show`, `pbs metrics influxdb-udp update`, `pbs node apt changelog`, `pbs node apt ls`, `pbs node apt repo-add`, `pbs node apt repo-update`, `pbs node apt repositories`, `pbs node apt update`, `pbs node apt versions`, `pbs node certificates acme order`, `pbs node certificates acme renew`, `pbs node certificates custom delete`, `pbs node certificates custom upload`, `pbs node certificates info`, `pbs node config show`, `pbs node config update`, `pbs node disks directory create`, `pbs node disks directory delete`, `pbs node disks directory ls`, `pbs node disks initgpt`, `pbs node disks ls`, `pbs node disks smart`, `pbs node disks wipe`, `pbs node disks zfs create`, `pbs node disks zfs ls`, `pbs node disks zfs show`, `pbs node dns show`, `pbs node dns update`, `pbs node identity`, `pbs node journal`, `pbs node ls`, `pbs node network apply`, `pbs node network create`, `pbs node network delete`, `pbs node network ls`, `pbs node network revert`, `pbs node network show`, `pbs node network update`, `pbs node reboot`, `pbs node report`, `pbs node rrd`, `pbs node services ls`, `pbs node services reload`, `pbs node services restart`, `pbs node services show`, `pbs node services start`, `pbs node services state`, `pbs node services stop`, `pbs node shutdown`, `pbs node status`, `pbs node subscription delete`, `pbs node subscription set`, `pbs node subscription show`, `pbs node subscription update`, `pbs node syslog`, `pbs node tasks delete`, `pbs node tasks log`, `pbs node tasks ls`, `pbs node tasks show`, `pbs node time show`, `pbs node time update`, `pbs notification endpoint gotify add`, `pbs notification endpoint gotify delete`, `pbs notification endpoint gotify ls`, `pbs notification endpoint gotify show`, `pbs notification endpoint gotify update`, `pbs notification endpoint sendmail add`, `pbs notification endpoint sendmail delete`, `pbs notification endpoint sendmail ls`, `pbs notification endpoint sendmail show`, `pbs notification endpoint sendmail update`, `pbs notification endpoint smtp add`, `pbs notification endpoint smtp delete`, `pbs notification endpoint smtp ls`, `pbs notification endpoint smtp show`, `pbs notification endpoint smtp update`, `pbs notification endpoint webhook add`, `pbs notification endpoint webhook delete`, `pbs notification endpoint webhook ls`, `pbs notification endpoint webhook show`, `pbs notification endpoint webhook update`, `pbs notification matcher add`, `pbs notification matcher delete`, `pbs notification matcher field-values ls`, `pbs notification matcher fields ls`, `pbs notification matcher ls`, `pbs notification matcher show`, `pbs notification matcher update`, `pbs notification target ls`, `pbs notification target test`, `pbs permission ls`, `pbs ping`, `pbs prune job add`, `pbs prune job delete`, `pbs prune job ls`, `pbs prune job run`, `pbs prune job show`, `pbs prune job update`, `pbs prune run`, `pbs prune simulate`, `pbs realm ad add`, `pbs realm ad delete`, `pbs realm ad ls`, `pbs realm ad show`, `pbs realm ad update`, `pbs realm ldap add`, `pbs realm ldap delete`, `pbs realm ldap ls`, `pbs realm ldap show`, `pbs realm ldap update`, `pbs realm ls`, `pbs realm openid add`, `pbs realm openid delete`, `pbs realm openid ls`, `pbs realm openid show`, `pbs realm openid update`, `pbs realm pam show`, `pbs realm pam update`, `pbs realm pbs show`, `pbs realm pbs update`, `pbs realm sync`, `pbs remote add`, `pbs remote delete`, `pbs remote ls`, `pbs remote scan groups`, `pbs remote scan ls`, `pbs remote scan namespaces`, `pbs remote show`, `pbs remote update`, `pbs role ls`, `pbs snapshot delete`, `pbs snapshot files`, `pbs snapshot ls`, `pbs snapshot notes`, `pbs snapshot protect`, `pbs snapshot show`, `pbs snapshot unprotect`, `pbs status datastore-usage`, `pbs sync job add`, `pbs sync job delete`, `pbs sync job ls`, `pbs sync job run`, `pbs sync job show`, `pbs sync job update`, `pbs sync ls`, `pbs sync pull`, `pbs sync push`, `pbs tape backup`, `pbs tape changer add`, `pbs tape changer delete`, `pbs tape changer ls`, `pbs tape changer scan`, `pbs tape changer show`, `pbs tape changer status`, `pbs tape changer transfer`, `pbs tape changer update`, `pbs tape drive add`, `pbs tape drive barcode-label`, `pbs tape drive cartridge-memory`, `pbs tape drive catalog`, `pbs tape drive clean`, `pbs tape drive delete`, `pbs tape drive eject`, `pbs tape drive export`, `pbs tape drive format`, `pbs tape drive inventory`, `pbs tape drive label`, `pbs tape drive load-media`, `pbs tape drive load-slot`, `pbs tape drive ls`, `pbs tape drive read-label`, `pbs tape drive restore-key`, `pbs tape drive rewind`, `pbs tape drive scan`, `pbs tape drive show`, `pbs tape drive status`, `pbs tape drive unload`, `pbs tape drive update`, `pbs tape drive update-inventory`, `pbs tape drive volume-statistics`, `pbs tape job add`, `pbs tape job delete`, `pbs tape job ls`, `pbs tape job run`, `pbs tape job show`, `pbs tape job status`, `pbs tape job update`, `pbs tape key add`, `pbs tape key delete`, `pbs tape key ls`, `pbs tape key show`, `pbs tape key update`, `pbs tape media content`, `pbs tape media destroy`, `pbs tape media ls`, `pbs tape media move`, `pbs tape media set-status`, `pbs tape media sets`, `pbs tape pool add`, `pbs tape pool delete`, `pbs tape pool ls`, `pbs tape pool show`, `pbs tape pool update`, `pbs tape restore`, `pbs traffic add`, `pbs traffic current`, `pbs traffic delete`, `pbs traffic ls`, `pbs traffic show`, `pbs traffic update`, `pbs user add`, `pbs user delete`, `pbs user ls`, `pbs user passwd`, `pbs user show`, `pbs user token add`, `pbs user token delete`, `pbs user token ls`, `pbs user token show`, `pbs user token update`, `pbs user unlock-tfa`, `pbs user update`, `pbs verify job add`, `pbs verify job delete`, `pbs verify job ls`, `pbs verify job run`, `pbs verify job show`, `pbs verify job update`, `pbs verify run`, `pbs version`

## Running the suites

```bash
make test-e2e                  # all trees, read-only, against the `lab` context
make test-e2e TREES=qemu       # a subset
make test-e2e CONTEXT=prod     # a different configured context
scripts/e2e --list             # list trees and the isolation contract

make test-e2e-mutate           # read-only sweep + the destructive verb matrix
make test-lifecycle            # the destructive verb matrix only, against `lab`
scripts/e2e --mutate --vm-only # sweep + VM verb matrix (skip the container)
scripts/lifecycle --vm-only    # VM verb matrix only
scripts/lifecycle --ct-only    # container verb matrix only
```

Both suites skip gracefully (exit 0) when no context is configured; pass
`--strict` to fail instead. The mutate phase prints a per-guest coverage table
listing every verb it drove and its result.

