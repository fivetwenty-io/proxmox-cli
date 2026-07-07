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
| `cluster` | 168 | 46 | 16 | 109 | 5 | 13 | 0 | 0 |
| `context` | 9 | 8 | 0 | 0 | 0 | 0 | 1 | 0 |
| `init` | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 |
| `lxc` | 64 | 5 | 19 | 39 | 0 | 7 | 0 | 0 |
| `node` | 162 | 3 | 73 | 47 | 0 | 38 | 6 | 0 |
| `pool` | 6 | 1 | 2 | 3 | 0 | 0 | 0 | 0 |
| `qemu` | 68 | 6 | 14 | 52 | 1 | 1 | 1 | 0 |
| `rsync` | 1 | 0 | 0 | 0 | 0 | 1 | 0 | 0 |
| `sdn` | 85 | 6 | 14 | 62 | 0 | 6 | 0 | 0 |
| `ssh` | 1 | 0 | 0 | 0 | 0 | 1 | 0 | 0 |
| `storage` | 26 | 2 | 10 | 12 | 0 | 5 | 0 | 0 |
| `task` | 6 | 2 | 2 | 2 | 0 | 0 | 0 | 0 |
| `version` | 2 | 2 | 0 | 0 | 0 | 0 | 0 | 0 |
| **Total** | **645** | **94** | **159** | **357** | **6** | **72** | **8** | **0** |

Leaf commands are counted from a walk of the built command tree (`pve <tree> … --help`); each `create`/`delete` and `get`/`set` verb is its own leaf. Of **645** leaves, **565** are exercised by at least one live suite, **72** are deferred from the live suites (irreversible, interactive, or environment-bound — covered by unit tests), **8** are n/a by design, and **0** are not yet exercised by either suite — see [Uncovered leaves](#uncovered-leaves).

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
| `cluster backup info` | ◑ | — |  |
| `cluster backup list` | ✓ | ✓ |  |
| `cluster backup set` | — | ✓ |  |
| `cluster backup-info not-backed-up` | ◑ | — |  |
| `cluster bulk guest` | ✓ | — |  |
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
| `cluster ha status list` | ✓ | — |  |
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
| `lxc firewall alias list` | — | ✓ |  |
| `lxc firewall alias update` | — | ✓ |  |
| `lxc firewall ipset add` | — | ✓ |  |
| `lxc firewall ipset create` | — | ✓ |  |
| `lxc firewall ipset delete` | — | ✓ |  |
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

## `pool`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `pool create` | — | ✓ | error-contract checked |
| `pool delete` | — | ✓ |  |
| `pool get` | ◑ | — |  |
| `pool list` | ✓ | — |  |
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
| `qemu firewall alias list` | — | ✓ |  |
| `qemu firewall alias update` | — | ✓ |  |
| `qemu firewall ipset add` | — | ✓ |  |
| `qemu firewall ipset create` | — | ✓ |  |
| `qemu firewall ipset delete` | — | ✓ |  |
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
| `qemu migrate check` | ◑ | — |  |
| `qemu monitor` | — | ✓ |  |
| `qemu reboot` | — | · |  |
| `qemu remote-migrate` | — | — | deferred — migrates a VM to a different Proxmox VE cluster — requires two live clusters with shared or compatible storage; no rollback without manual intervention; not exercised live |
| `qemu reset` | — | ✓ |  |
| `qemu resume` | — | ✓ |  |
| `qemu rrd` | ◑ | — |  |
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
| `sdn status fabrics get` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `sdn status fabrics interfaces` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `sdn status fabrics neighbors` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `sdn status fabrics routes` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `sdn status vnets get` | — | ✓ |  |
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
| `sdn vnet set` | — | ✓ |  |
| `sdn vnet show` | ◑ | — |  |
| `sdn zone create` | — | ✓ |  |
| `sdn zone delete` | — | ✓ |  |
| `sdn zone list` | ✓ | — |  |
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

_None — every leaf is exercised or explicitly deferred._

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

