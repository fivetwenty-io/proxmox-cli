# Test Coverage Matrix

> **Generated file — do not edit by hand.** Regenerate with
> `make coverage-matrix`; CI runs `make check-coverage-matrix` and fails if
> this file is stale. The classification is derived statically from the built
> command tree, the read-only sweep definitions in
> `scripts/e2e_lib/trees/*.py`, and the mutate phases in
> `scripts/e2e_lib/{lifecycle,pbs_lifecycle,pdm_lifecycle}.py`, so it stays
> correct as commands and tests change.

This document maps every invocable leaf command to its automated test coverage
across the live suites:

- **e2e** (`scripts/e2e`, `make test-e2e`) — a read-only, parallel happy-path
  sweep against a configured context. Mutating operations are never executed;
  they are recorded as deferred. The `pbs` and `pdm` trees are opt-in: each
  runs only when `--pbs-context`/`--pdm-context` (or
  `make test-e2e PBS_CONTEXT=…`/`PDM_CONTEXT=…`) names a configured
  `product: pbs`/`product: pdm` context whose server is reachable, so all of
  their leaves are prerequisite-gated (◑).

- **lifecycle / mutate** — the destructive counterpart, one suite per product.
  Each drives the mutating sub-commands on purpose-built throwaway resources,
  records every verb individually, and tears everything down in a `finally`
  block:

  - `scripts/lifecycle` (`make test-lifecycle`, or `scripts/e2e --mutate`) for
    PVE, on an isolated SDN zone and resource pool;

  - `scripts/pbs-lifecycle` (`make test-pbs-lifecycle`) for PBS, on a scratch
    datastore it creates and destroys;

  - `scripts/pdm-lifecycle` (`make test-pdm-lifecycle`) for PDM, on the manager
    itself — it writes nothing through a managed remote.

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

Every resource the PVE lifecycle suite creates is shielded from other lab
efforts (see `scripts/e2e_lib/model.py`, the single source of truth):

- named or hostnamed with the `pmx-cli-` prefix,

- placed in the `pmx-cli` resource pool and tagged `pmx-cli`,

- attached to a dedicated `pmxcli` simple SDN zone and `pmxcli0` vnet on the
  `172.30.0.0/24` subnet, deliberately off the host management network.

The PBS and PDM suites keep the same `pmx-cli-` naming, and add the constraint
that fits their product: PBS confines all backup data to a scratch datastore of
its own, so no pre-existing datastore is written to, pruned, verified, or
garbage-collected; PDM writes nothing *through* a managed remote, so no guest on
a registered cluster is touched and no subscription key reaches a remote's node.

Teardown runs in a `finally` block and is idempotent: a crashed prior run is
swept clean before the next provisions.

## Coverage summary

| Tree | Leaves | e2e ✓ | e2e ◑ | mutate ✓ | mutate · | deferred | n/a | uncovered |
|------|-------:|------:|------:|---------:|---------:|---------:|----:|----------:|
| `api` | 4 | 0 | 1 | 3 | 0 | 0 | 0 | 0 |
| `auth` | 7 | 3 | 1 | 3 | 0 | 0 | 0 | 0 |
| `context` | 11 | 10 | 0 | 0 | 0 | 0 | 1 | 0 |
| `init` | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 |
| `lab` | 26 | 4 | 1 | 0 | 0 | 20 | 1 | 0 |
| `logs` | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 |
| `pbs` | 270 | 0 | 122 | 104 | 42 | 2 | 0 | 0 |
| `pdm` | 260 | 0 | 145 | 59 | 52 | 4 | 0 | 0 |
| `pve` | 676 | 80 | 181 | 394 | 6 | 82 | 7 | 0 |
| `rsync` | 1 | 0 | 0 | 1 | 0 | 0 | 0 | 0 |
| `ssh` | 1 | 0 | 0 | 1 | 0 | 0 | 0 | 0 |
| `version` | 3 | 2 | 1 | 0 | 0 | 0 | 0 | 0 |
| **Total** | **1261** | **101** | **452** | **565** | **100** | **108** | **9** | **0** |

Leaf commands are counted from a walk of the built command tree (`pmx <tree> … --help`); each `create`/`delete` and `get`/`set` verb is its own leaf. Of **1261** leaves, **1144** are exercised by at least one live suite, **108** are deferred from the live suites (irreversible, interactive, or environment-bound — covered by unit tests), **9** are n/a by design, and **0** are not yet exercised by either suite — see [Uncovered leaves](#uncovered-leaves).

## `api`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `api delete` | — | ✓ |  |
| `api get` | ◑ | — |  |
| `api post` | — | ✓ |  |
| `api put` | — | ✓ |  |

## `auth`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `auth login` | — | ✓ |  |
| `auth logout` | — | ✓ |  |
| `auth refresh` | — | ✓ |  |
| `auth set-password` | ✓ | — |  |
| `auth set-token` | ✓ | — |  |
| `auth status` | ✓ | — |  |
| `auth whoami` | ◑ | — |  |

## `context`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `context add` | ✓ | — |  |
| `context copy` | ✓ | — |  |
| `context edit` | — | — | n/a — requires $EDITOR / interactive TTY — not safe to drive in headless e2e; covered in unit tests via EDITOR=true trick (test-strategy §4.2) |
| `context ls` | ✓ | — |  |
| `context previous` | ✓ | — |  |
| `context rename` | ✓ | — |  |
| `context rm` | ✓ | — |  |
| `context select` | ✓ | — |  |
| `context show` | ✓ | — |  |
| `context update` | ✓ | — |  |
| `context validate` | ✓ | — |  |

## `init`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `init config` | ✓ | — |  |

## `lab`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `lab access grant` | — | — | deferred — creates a pve user and grants pool ACLs cluster-wide; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab cluster init` | — | — | deferred — runs `pvecm create` over ssh on a lab's node 0, forming a corosync cluster; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab cluster join` | — | — | deferred — runs `pvecm add` over ssh on each non-zero lab node, joining it to node 0's cluster; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab cluster status` | — | — | deferred — reads corosync quorum and link state over ssh on a lab's nodes, so it needs a provisioned and running lab; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab config add` | ✓ | — |  |
| `lab config init` | ✓ | — |  |
| `lab config show` | ✓ | — |  |
| `lab context rm` | — | — | deferred — deletes a `lab-<name>` context from the operator's own config.yml and its keychain secret; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab context sync` | — | — | deferred — creates the pmx@pve token user and ACL on a lab's nested cluster and rewrites the local `lab-<name>` context and its keychain secret; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab create` | — | — | deferred — provisions SDN zone/vnet/subnet, storage, pool, and a VM on the cluster; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab destroy` | — | — | deferred — deletes a lab's VM, pool, storage, and SDN resources; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab hostnet apply` | — | — | deferred — rewrites the outer node's bridge and bond configuration, which can sever the suite's own connection to it; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab list` | ✓ | — |  |
| `lab net apply` | — | — | deferred — reconciles and commits cluster-wide SDN configuration; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab nfs attach` | — | — | deferred — creates ZFS datasets, quotas, and sharenfs ACLs on the outer node, opens 2049/111 in its firewall, and adds pvesm storage entries on the lab; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab nfs detach` | — | — | deferred — removes a lab's pvesm storage entries and can narrow an aliased export's sharenfs ACL; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab nfs status` | — | — | deferred — runs `pvesm status` over ssh on a lab's node 0, so it needs a provisioned and running lab; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab qdevice add` | — | — | deferred — provisions a QDevice VM and runs `pvecm qdevice setup` against the lab's cluster; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab qdevice remove` | — | — | deferred — tears the QDevice out of the lab's cluster and destroys its VM; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab quota set` | — | — | n/a — runs `zfs set refquota` over ssh on the real host's dataset; no PVE API endpoint exists for it |
| `lab scale` | — | — | deferred — creates or destroys lab node VMs, joins or removes them from the cluster, and moves the QDevice; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab sdn apply` | — | — | deferred — reconciles the inner VXLAN zone, vnet, and subnet inside a lab's own nested cluster; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab sdn vlan apply` | — | — | deferred — reconciles the inner vlan-type zone and its vnets inside a lab's own nested cluster; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab start` | — | — | deferred — powers on a lab VM; needs the dedicated lab-pmx destructive test lab as the standing target |
| `lab status` | ◑ | — |  |
| `lab stop` | — | — | deferred — hard powers off a lab VM; needs the dedicated lab-pmx destructive test lab as the standing target |

## `logs`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `logs prune` | ✓ | — |  |

## `pbs`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `pbs acl ls` | ◑ | — |  |
| `pbs acl update` | — | ✓ |  |
| `pbs acme account add` | — | · |  |
| `pbs acme account delete` | — | · |  |
| `pbs acme account ls` | ◑ | — |  |
| `pbs acme account show` | ◑ | — |  |
| `pbs acme account update` | — | · |  |
| `pbs acme challenge-schema ls` | ◑ | — |  |
| `pbs acme directories ls` | ◑ | — |  |
| `pbs acme plugin add` | — | ✓ |  |
| `pbs acme plugin delete` | — | ✓ |  |
| `pbs acme plugin ls` | ◑ | — |  |
| `pbs acme plugin show` | ◑ | — |  |
| `pbs acme plugin update` | — | ✓ |  |
| `pbs acme tos show` | ◑ | — |  |
| `pbs datastore create` | — | ✓ |  |
| `pbs datastore delete` | — | ✓ |  |
| `pbs datastore ls` | ◑ | — |  |
| `pbs datastore rrd` | ◑ | — |  |
| `pbs datastore show` | ◑ | — |  |
| `pbs datastore status` | ◑ | — |  |
| `pbs datastore update` | — | ✓ |  |
| `pbs datastore usage` | ◑ | — |  |
| `pbs encryption-key add` | — | ✓ |  |
| `pbs encryption-key delete` | — | ✓ |  |
| `pbs encryption-key ls` | ◑ | — |  |
| `pbs encryption-key toggle-archive` | — | ✓ |  |
| `pbs gc ls` | ◑ | — |  |
| `pbs gc run` | — | ✓ |  |
| `pbs gc status` | ◑ | — |  |
| `pbs group delete` | — | ✓ |  |
| `pbs group ls` | ◑ | — |  |
| `pbs group notes` | ◑ | — |  |
| `pbs metrics data` | ◑ | — |  |
| `pbs metrics influxdb-http add` | — | ✓ |  |
| `pbs metrics influxdb-http delete` | — | ✓ |  |
| `pbs metrics influxdb-http ls` | ◑ | — |  |
| `pbs metrics influxdb-http show` | ◑ | — |  |
| `pbs metrics influxdb-http update` | — | ✓ |  |
| `pbs metrics influxdb-udp add` | — | ✓ |  |
| `pbs metrics influxdb-udp delete` | — | ✓ |  |
| `pbs metrics influxdb-udp ls` | ◑ | — |  |
| `pbs metrics influxdb-udp show` | ◑ | — |  |
| `pbs metrics influxdb-udp update` | — | ✓ |  |
| `pbs node apt changelog` | ◑ | — |  |
| `pbs node apt ls` | ◑ | — |  |
| `pbs node apt repo-add` | — | ✓ |  |
| `pbs node apt repo-update` | — | ✓ |  |
| `pbs node apt repositories` | ◑ | — |  |
| `pbs node apt update` | — | ✓ |  |
| `pbs node apt versions` | ◑ | — |  |
| `pbs node certificates acme order` | — | · |  |
| `pbs node certificates acme renew` | — | · |  |
| `pbs node certificates custom delete` | — | · |  |
| `pbs node certificates custom upload` | — | · |  |
| `pbs node certificates info` | ◑ | — |  |
| `pbs node config show` | ◑ | — |  |
| `pbs node config update` | — | ✓ |  |
| `pbs node disks directory create` | — | ✓ |  |
| `pbs node disks directory delete` | — | ✓ |  |
| `pbs node disks directory ls` | ◑ | — |  |
| `pbs node disks initgpt` | — | ✓ |  |
| `pbs node disks ls` | ◑ | — |  |
| `pbs node disks smart` | ◑ | — |  |
| `pbs node disks wipe` | — | ✓ |  |
| `pbs node disks zfs create` | — | ✓ |  |
| `pbs node disks zfs ls` | ◑ | — |  |
| `pbs node disks zfs show` | ◑ | — |  |
| `pbs node dns show` | ◑ | — |  |
| `pbs node dns update` | — | ✓ |  |
| `pbs node identity` | ◑ | — |  |
| `pbs node journal` | ◑ | — |  |
| `pbs node ls` | ◑ | — |  |
| `pbs node network apply` | — | · |  |
| `pbs node network create` | — | ✓ |  |
| `pbs node network delete` | — | ✓ |  |
| `pbs node network ls` | ◑ | — |  |
| `pbs node network revert` | — | ✓ |  |
| `pbs node network show` | ◑ | — |  |
| `pbs node network update` | — | ✓ |  |
| `pbs node reboot` | — | · |  |
| `pbs node report` | ◑ | — |  |
| `pbs node rrd` | ◑ | — |  |
| `pbs node services ls` | ◑ | — |  |
| `pbs node services reload` | — | ✓ |  |
| `pbs node services restart` | — | ✓ |  |
| `pbs node services show` | ◑ | — |  |
| `pbs node services start` | — | ✓ |  |
| `pbs node services state` | ◑ | — |  |
| `pbs node services stop` | — | ✓ |  |
| `pbs node shutdown` | — | · |  |
| `pbs node status` | ◑ | — |  |
| `pbs node subscription delete` | — | ✓ |  |
| `pbs node subscription set` | — | · |  |
| `pbs node subscription show` | ◑ | — |  |
| `pbs node subscription update` | — | ✓ |  |
| `pbs node syslog` | ◑ | — |  |
| `pbs node tasks delete` | — | ✓ |  |
| `pbs node tasks log` | ◑ | — |  |
| `pbs node tasks ls` | ◑ | — |  |
| `pbs node tasks show` | ◑ | — |  |
| `pbs node time show` | ◑ | — |  |
| `pbs node time update` | — | ✓ |  |
| `pbs notification endpoint gotify add` | — | ✓ |  |
| `pbs notification endpoint gotify delete` | — | ✓ |  |
| `pbs notification endpoint gotify ls` | ◑ | — |  |
| `pbs notification endpoint gotify show` | ◑ | — |  |
| `pbs notification endpoint gotify update` | — | ✓ |  |
| `pbs notification endpoint sendmail add` | — | ✓ |  |
| `pbs notification endpoint sendmail delete` | — | ✓ |  |
| `pbs notification endpoint sendmail ls` | ◑ | — |  |
| `pbs notification endpoint sendmail show` | ◑ | — |  |
| `pbs notification endpoint sendmail update` | — | ✓ |  |
| `pbs notification endpoint smtp add` | — | ✓ |  |
| `pbs notification endpoint smtp delete` | — | ✓ |  |
| `pbs notification endpoint smtp ls` | ◑ | — |  |
| `pbs notification endpoint smtp show` | ◑ | — |  |
| `pbs notification endpoint smtp update` | — | ✓ |  |
| `pbs notification endpoint webhook add` | — | ✓ |  |
| `pbs notification endpoint webhook delete` | — | ✓ |  |
| `pbs notification endpoint webhook ls` | ◑ | — |  |
| `pbs notification endpoint webhook show` | ◑ | — |  |
| `pbs notification endpoint webhook update` | — | ✓ |  |
| `pbs notification matcher add` | — | ✓ |  |
| `pbs notification matcher delete` | — | ✓ |  |
| `pbs notification matcher field-values ls` | ◑ | — |  |
| `pbs notification matcher fields ls` | ◑ | — |  |
| `pbs notification matcher ls` | ◑ | — |  |
| `pbs notification matcher show` | ◑ | — |  |
| `pbs notification matcher update` | — | ✓ |  |
| `pbs notification target ls` | ◑ | — |  |
| `pbs notification target test` | — | ✓ |  |
| `pbs permission ls` | ◑ | — |  |
| `pbs prune job add` | — | ✓ |  |
| `pbs prune job delete` | — | ✓ |  |
| `pbs prune job ls` | ◑ | — |  |
| `pbs prune job run` | — | ✓ |  |
| `pbs prune job show` | ◑ | — |  |
| `pbs prune job update` | — | ✓ |  |
| `pbs prune run` | — | ✓ |  |
| `pbs prune simulate` | ◑ | — |  |
| `pbs realm ad add` | — | ✓ |  |
| `pbs realm ad delete` | — | ✓ |  |
| `pbs realm ad ls` | ◑ | — |  |
| `pbs realm ad show` | ◑ | — |  |
| `pbs realm ad update` | — | ✓ |  |
| `pbs realm ldap add` | — | ✓ |  |
| `pbs realm ldap delete` | — | ✓ |  |
| `pbs realm ldap ls` | ◑ | — |  |
| `pbs realm ldap show` | ◑ | — |  |
| `pbs realm ldap update` | — | ✓ |  |
| `pbs realm ls` | ◑ | — |  |
| `pbs realm openid add` | — | ✓ |  |
| `pbs realm openid delete` | — | ✓ |  |
| `pbs realm openid ls` | ◑ | — |  |
| `pbs realm openid show` | ◑ | — |  |
| `pbs realm openid update` | — | ✓ |  |
| `pbs realm pam show` | ◑ | — |  |
| `pbs realm pam update` | — | — | deferred — modifies the built-in PAM realm; covered by unit tests |
| `pbs realm pbs show` | ◑ | — |  |
| `pbs realm pbs update` | — | — | deferred — modifies the built-in PBS realm; covered by unit tests |
| `pbs realm sync` | — | ✓ |  |
| `pbs remote add` | — | ✓ |  |
| `pbs remote delete` | — | ✓ |  |
| `pbs remote ls` | ◑ | — |  |
| `pbs remote scan groups` | ◑ | — |  |
| `pbs remote scan ls` | ◑ | — |  |
| `pbs remote scan namespaces` | ◑ | — |  |
| `pbs remote show` | ◑ | — |  |
| `pbs remote update` | — | ✓ |  |
| `pbs role ls` | ◑ | — |  |
| `pbs snapshot delete` | — | ✓ |  |
| `pbs snapshot files` | ◑ | — |  |
| `pbs snapshot ls` | ◑ | — |  |
| `pbs snapshot notes` | ◑ | — |  |
| `pbs snapshot protect` | — | ✓ |  |
| `pbs snapshot show` | ◑ | — |  |
| `pbs snapshot unprotect` | — | ✓ |  |
| `pbs status datastore-usage` | ◑ | — |  |
| `pbs sync job add` | — | ✓ |  |
| `pbs sync job delete` | — | ✓ |  |
| `pbs sync job ls` | ◑ | — |  |
| `pbs sync job run` | — | ✓ |  |
| `pbs sync job show` | ◑ | — |  |
| `pbs sync job update` | — | ✓ |  |
| `pbs sync ls` | ◑ | — |  |
| `pbs sync pull` | — | ✓ |  |
| `pbs sync push` | — | ✓ |  |
| `pbs tape backup` | — | · |  |
| `pbs tape changer add` | — | · |  |
| `pbs tape changer delete` | — | · |  |
| `pbs tape changer ls` | ◑ | — |  |
| `pbs tape changer scan` | ◑ | — |  |
| `pbs tape changer show` | ◑ | — |  |
| `pbs tape changer status` | ◑ | — |  |
| `pbs tape changer transfer` | — | · |  |
| `pbs tape changer update` | — | · |  |
| `pbs tape drive add` | — | · |  |
| `pbs tape drive barcode-label` | — | · |  |
| `pbs tape drive cartridge-memory` | ◑ | — |  |
| `pbs tape drive catalog` | — | · |  |
| `pbs tape drive clean` | — | · |  |
| `pbs tape drive delete` | — | · |  |
| `pbs tape drive eject` | — | · |  |
| `pbs tape drive export` | — | · |  |
| `pbs tape drive format` | — | · |  |
| `pbs tape drive inventory` | — | · |  |
| `pbs tape drive label` | — | · |  |
| `pbs tape drive load-media` | — | · |  |
| `pbs tape drive load-slot` | — | · |  |
| `pbs tape drive ls` | ◑ | — |  |
| `pbs tape drive read-label` | ◑ | — |  |
| `pbs tape drive restore-key` | — | · |  |
| `pbs tape drive rewind` | — | · |  |
| `pbs tape drive scan` | ◑ | — |  |
| `pbs tape drive show` | ◑ | — |  |
| `pbs tape drive status` | ◑ | — |  |
| `pbs tape drive unload` | — | · |  |
| `pbs tape drive update` | — | · |  |
| `pbs tape drive update-inventory` | — | · |  |
| `pbs tape drive volume-statistics` | ◑ | — |  |
| `pbs tape job add` | — | · |  |
| `pbs tape job delete` | — | · |  |
| `pbs tape job ls` | ◑ | — |  |
| `pbs tape job run` | — | · |  |
| `pbs tape job show` | ◑ | — |  |
| `pbs tape job status` | ◑ | — |  |
| `pbs tape job update` | — | · |  |
| `pbs tape key add` | — | ✓ |  |
| `pbs tape key delete` | — | ✓ |  |
| `pbs tape key ls` | ◑ | — |  |
| `pbs tape key show` | ◑ | — |  |
| `pbs tape key update` | — | ✓ |  |
| `pbs tape media content` | ◑ | — |  |
| `pbs tape media destroy` | — | · |  |
| `pbs tape media ls` | ◑ | — |  |
| `pbs tape media move` | — | · |  |
| `pbs tape media set-status` | — | · |  |
| `pbs tape media sets` | ◑ | — |  |
| `pbs tape pool add` | — | ✓ |  |
| `pbs tape pool delete` | — | ✓ |  |
| `pbs tape pool ls` | ◑ | — |  |
| `pbs tape pool show` | ◑ | — |  |
| `pbs tape pool update` | — | ✓ |  |
| `pbs tape restore` | — | · |  |
| `pbs traffic add` | — | ✓ |  |
| `pbs traffic current` | ◑ | — |  |
| `pbs traffic delete` | — | ✓ |  |
| `pbs traffic ls` | ◑ | — |  |
| `pbs traffic show` | ◑ | — |  |
| `pbs traffic update` | — | ✓ |  |
| `pbs user add` | — | ✓ |  |
| `pbs user delete` | — | ✓ |  |
| `pbs user ls` | ◑ | — |  |
| `pbs user passwd` | — | · |  |
| `pbs user show` | ◑ | — |  |
| `pbs user token add` | — | ✓ |  |
| `pbs user token delete` | — | ✓ |  |
| `pbs user token ls` | ◑ | — |  |
| `pbs user token show` | ◑ | — |  |
| `pbs user token update` | — | ✓ |  |
| `pbs user unlock-tfa` | — | ✓ |  |
| `pbs user update` | — | ✓ |  |
| `pbs verify job add` | — | ✓ |  |
| `pbs verify job delete` | — | ✓ |  |
| `pbs verify job ls` | ◑ | — |  |
| `pbs verify job run` | — | ✓ |  |
| `pbs verify job show` | ◑ | — |  |
| `pbs verify job update` | — | ✓ |  |
| `pbs verify run` | — | ✓ |  |

## `pdm`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `pdm acl ls` | ◑ | — |  |
| `pdm acl update` | — | ✓ |  |
| `pdm auto-install installation delete` | — | · |  |
| `pdm auto-install installation ls` | ◑ | — |  |
| `pdm auto-install prepared add` | — | ✓ |  |
| `pdm auto-install prepared delete` | — | ✓ |  |
| `pdm auto-install prepared ls` | ◑ | — |  |
| `pdm auto-install prepared show` | ◑ | — |  |
| `pdm auto-install prepared update` | — | ✓ |  |
| `pdm auto-install token add` | — | ✓ |  |
| `pdm auto-install token delete` | — | ✓ |  |
| `pdm auto-install token ls` | ◑ | — |  |
| `pdm auto-install token update` | — | ✓ |  |
| `pdm ceph flags` | ◑ | — |  |
| `pdm ceph fs` | ◑ | — |  |
| `pdm ceph ls` | ◑ | — |  |
| `pdm ceph mds` | ◑ | — |  |
| `pdm ceph mgr` | ◑ | — |  |
| `pdm ceph mon` | ◑ | — |  |
| `pdm ceph osd-tree` | ◑ | — |  |
| `pdm ceph pools` | ◑ | — |  |
| `pdm ceph status` | ◑ | — |  |
| `pdm ceph summary` | ◑ | — |  |
| `pdm config acme account add` | — | · |  |
| `pdm config acme account delete` | — | · |  |
| `pdm config acme account ls` | ◑ | — |  |
| `pdm config acme account show` | ◑ | — |  |
| `pdm config acme account update` | — | · |  |
| `pdm config acme challenge-schema ls` | ◑ | — |  |
| `pdm config acme directories ls` | ◑ | — |  |
| `pdm config acme plugin add` | — | ✓ |  |
| `pdm config acme plugin delete` | — | ✓ |  |
| `pdm config acme plugin ls` | ◑ | — |  |
| `pdm config acme plugin show` | ◑ | — |  |
| `pdm config acme plugin update` | — | ✓ |  |
| `pdm config acme tos show` | ◑ | — |  |
| `pdm config certificate show` | ◑ | — |  |
| `pdm config certificate update` | — | ✓ |  |
| `pdm config notes show` | ◑ | — |  |
| `pdm config notes update` | — | ✓ |  |
| `pdm config view add` | — | ✓ |  |
| `pdm config view delete` | — | ✓ |  |
| `pdm config view ls` | ◑ | — |  |
| `pdm config view show` | ◑ | — |  |
| `pdm config view update` | — | ✓ |  |
| `pdm config webauthn show` | ◑ | — |  |
| `pdm config webauthn update` | — | ✓ |  |
| `pdm node apt changelog` | ◑ | — |  |
| `pdm node apt repositories` | ◑ | — |  |
| `pdm node apt repository add` | — | ✓ |  |
| `pdm node apt repository change` | — | ✓ |  |
| `pdm node apt update-database` | — | ✓ |  |
| `pdm node apt updates` | ◑ | — |  |
| `pdm node apt versions` | ◑ | — |  |
| `pdm node certificate acme order` | — | · |  |
| `pdm node certificate acme renew` | — | · |  |
| `pdm node certificate delete-custom` | — | · |  |
| `pdm node certificate info` | ◑ | — |  |
| `pdm node certificate upload` | — | · |  |
| `pdm node config show` | ◑ | — |  |
| `pdm node config update` | — | ✓ |  |
| `pdm node dns show` | ◑ | — |  |
| `pdm node dns update` | — | ✓ |  |
| `pdm node journal` | ◑ | — |  |
| `pdm node ls` | ◑ | — |  |
| `pdm node network apply` | — | · |  |
| `pdm node network create` | — | ✓ |  |
| `pdm node network delete` | — | ✓ |  |
| `pdm node network ls` | ◑ | — |  |
| `pdm node network revert` | — | ✓ |  |
| `pdm node network show` | ◑ | — |  |
| `pdm node network update` | — | ✓ |  |
| `pdm node reboot` | — | · |  |
| `pdm node report` | ◑ | — |  |
| `pdm node rrddata` | ◑ | — |  |
| `pdm node sdn vnet mac-vrf` | ◑ | — |  |
| `pdm node sdn zone ip-vrf` | ◑ | — |  |
| `pdm node shutdown` | — | · |  |
| `pdm node status` | ◑ | — |  |
| `pdm node subscription show` | ◑ | — |  |
| `pdm node subscription update` | — | ✓ |  |
| `pdm node syslog` | ◑ | — |  |
| `pdm node task log` | ◑ | — |  |
| `pdm node task ls` | ◑ | — |  |
| `pdm node task status` | ◑ | — |  |
| `pdm node task stop` | — | ✓ |  |
| `pdm node time show` | ◑ | — |  |
| `pdm node time update` | — | ✓ |  |
| `pdm pbs datastore ls` | ◑ | — |  |
| `pdm pbs datastore namespaces` | ◑ | — |  |
| `pdm pbs datastore rrddata` | ◑ | — |  |
| `pdm pbs datastore snapshots` | ◑ | — |  |
| `pdm pbs node apt changelog` | ◑ | — |  |
| `pdm pbs node apt repositories` | ◑ | — |  |
| `pdm pbs node apt update-database` | — | · |  |
| `pdm pbs node apt updates` | ◑ | — |  |
| `pdm pbs node subscription` | ◑ | — |  |
| `pdm pbs probe-tls` | — | · |  |
| `pdm pbs realms` | — | · |  |
| `pdm pbs remote ls` | ◑ | — |  |
| `pdm pbs rrddata` | ◑ | — |  |
| `pdm pbs scan` | — | · |  |
| `pdm pbs status` | ◑ | — |  |
| `pdm pbs task log` | ◑ | — |  |
| `pdm pbs task ls` | ◑ | — |  |
| `pdm pbs task status` | ◑ | — |  |
| `pdm pbs task stop` | — | · |  |
| `pdm permission ls` | ◑ | — |  |
| `pdm pve cluster next-id` | ◑ | — |  |
| `pdm pve cluster resources` | ◑ | — |  |
| `pdm pve cluster status` | ◑ | — |  |
| `pdm pve firewall options show` | ◑ | — |  |
| `pdm pve firewall options update` | — | · |  |
| `pdm pve firewall rules` | ◑ | — |  |
| `pdm pve firewall show` | ◑ | — |  |
| `pdm pve firewall status` | ◑ | — |  |
| `pdm pve lxc config` | ◑ | — |  |
| `pdm pve lxc firewall options show` | ◑ | — |  |
| `pdm pve lxc firewall options update` | — | · |  |
| `pdm pve lxc firewall rules` | ◑ | — |  |
| `pdm pve lxc ls` | ◑ | — |  |
| `pdm pve lxc migrate` | — | · |  |
| `pdm pve lxc pending` | ◑ | — |  |
| `pdm pve lxc remote-migrate` | — | · |  |
| `pdm pve lxc rrddata` | ◑ | — |  |
| `pdm pve lxc shutdown` | — | · |  |
| `pdm pve lxc snapshot add` | — | · |  |
| `pdm pve lxc snapshot delete` | — | · |  |
| `pdm pve lxc snapshot ls` | ◑ | — |  |
| `pdm pve lxc snapshot rollback` | — | · |  |
| `pdm pve lxc snapshot update` | — | · |  |
| `pdm pve lxc start` | — | · |  |
| `pdm pve lxc status` | ◑ | — |  |
| `pdm pve lxc stop` | — | · |  |
| `pdm pve node apt changelog` | ◑ | — |  |
| `pdm pve node apt repositories` | ◑ | — |  |
| `pdm pve node apt update-database` | — | · |  |
| `pdm pve node apt updates` | ◑ | — |  |
| `pdm pve node config` | ◑ | — |  |
| `pdm pve node firewall options show` | ◑ | — |  |
| `pdm pve node firewall options update` | — | · |  |
| `pdm pve node firewall rules` | ◑ | — |  |
| `pdm pve node firewall status` | ◑ | — |  |
| `pdm pve node ls` | ◑ | — |  |
| `pdm pve node network` | ◑ | — |  |
| `pdm pve node rrddata` | ◑ | — |  |
| `pdm pve node sdn vnet mac-vrf` | ◑ | — |  |
| `pdm pve node sdn zone ip-vrf` | ◑ | — |  |
| `pdm pve node status` | ◑ | — |  |
| `pdm pve node subscription` | ◑ | — |  |
| `pdm pve options` | ◑ | — |  |
| `pdm pve probe-tls` | — | — | deferred — re-probes and stores a PVE host's TLS fingerprint; covered by unit tests |
| `pdm pve qemu config` | ◑ | — |  |
| `pdm pve qemu firewall options show` | ◑ | — |  |
| `pdm pve qemu firewall options update` | — | · |  |
| `pdm pve qemu firewall rules` | ◑ | — |  |
| `pdm pve qemu ls` | ◑ | — |  |
| `pdm pve qemu migrate` | — | · |  |
| `pdm pve qemu migrate-preconditions` | ◑ | — |  |
| `pdm pve qemu pending` | ◑ | — |  |
| `pdm pve qemu remote-migrate` | — | · |  |
| `pdm pve qemu resume` | — | · |  |
| `pdm pve qemu rrddata` | ◑ | — |  |
| `pdm pve qemu shutdown` | — | · |  |
| `pdm pve qemu snapshot add` | — | · |  |
| `pdm pve qemu snapshot delete` | — | · |  |
| `pdm pve qemu snapshot ls` | ◑ | — |  |
| `pdm pve qemu snapshot rollback` | — | · |  |
| `pdm pve qemu snapshot update` | — | · |  |
| `pdm pve qemu start` | — | · |  |
| `pdm pve qemu status` | ◑ | — |  |
| `pdm pve qemu stop` | — | · |  |
| `pdm pve realms` | — | · |  |
| `pdm pve remote ls` | ◑ | — |  |
| `pdm pve scan` | — | — | deferred — scans a PVE host's connection info before adding it as a remote; covered by unit tests |
| `pdm pve storage ls` | ◑ | — |  |
| `pdm pve storage rrddata` | ◑ | — |  |
| `pdm pve storage status` | ◑ | — |  |
| `pdm pve task log` | ◑ | — |  |
| `pdm pve task ls` | ◑ | — |  |
| `pdm pve task status` | ◑ | — |  |
| `pdm pve task stop` | — | · |  |
| `pdm pve updates` | ◑ | — |  |
| `pdm realm ad add` | — | ✓ |  |
| `pdm realm ad delete` | — | ✓ |  |
| `pdm realm ad ls` | ◑ | — |  |
| `pdm realm ad show` | ◑ | — |  |
| `pdm realm ad update` | — | ✓ |  |
| `pdm realm ldap add` | — | ✓ |  |
| `pdm realm ldap delete` | — | ✓ |  |
| `pdm realm ldap ls` | ◑ | — |  |
| `pdm realm ldap show` | ◑ | — |  |
| `pdm realm ldap update` | — | ✓ |  |
| `pdm realm ls` | ◑ | — |  |
| `pdm realm openid add` | — | ✓ |  |
| `pdm realm openid delete` | — | ✓ |  |
| `pdm realm openid ls` | ◑ | — |  |
| `pdm realm openid show` | ◑ | — |  |
| `pdm realm openid update` | — | ✓ |  |
| `pdm realm pam show` | ◑ | — |  |
| `pdm realm pam update` | — | — | deferred — modifies the built-in PAM realm; covered by unit tests |
| `pdm realm pdm show` | ◑ | — |  |
| `pdm realm pdm update` | — | — | deferred — modifies the built-in PDM realm; covered by unit tests |
| `pdm realm sync` | — | ✓ |  |
| `pdm remote add` | — | ✓ |  |
| `pdm remote delete` | — | ✓ | error-contract checked |
| `pdm remote ls` | ◑ | — |  |
| `pdm remote metric-collection status` | ◑ | — |  |
| `pdm remote metric-collection trigger` | — | ✓ |  |
| `pdm remote probe-certificate` | — | ✓ |  |
| `pdm remote rrddata` | ◑ | — |  |
| `pdm remote show` | ◑ | — |  |
| `pdm remote task ls` | ◑ | — |  |
| `pdm remote task refresh` | — | ✓ |  |
| `pdm remote task statistics` | ◑ | — |  |
| `pdm remote update` | — | ✓ |  |
| `pdm remote updates refresh` | — | ✓ |  |
| `pdm remote updates summary` | ◑ | — |  |
| `pdm remote version` | ◑ | — |  |
| `pdm resource location-info` | — | · |  |
| `pdm resource ls` | ◑ | — |  |
| `pdm resource status` | ◑ | — |  |
| `pdm resource subscription` | ◑ | — |  |
| `pdm resource top-entities` | ◑ | — |  |
| `pdm role ls` | ◑ | — |  |
| `pdm sdn controller ls` | ◑ | — |  |
| `pdm sdn vnet add` | — | · |  |
| `pdm sdn vnet ls` | ◑ | — |  |
| `pdm sdn zone add` | — | · |  |
| `pdm sdn zone ls` | ◑ | — |  |
| `pdm subscription adopt-all` | — | · |  |
| `pdm subscription adopt-key` | — | · |  |
| `pdm subscription apply-pending` | — | · |  |
| `pdm subscription auto-assign` | — | ✓ |  |
| `pdm subscription bulk-assign` | — | · |  |
| `pdm subscription check` | — | · |  |
| `pdm subscription clear-pending` | — | ✓ |  |
| `pdm subscription key add` | — | ✓ |  |
| `pdm subscription key assign` | — | ✓ |  |
| `pdm subscription key delete` | — | ✓ |  |
| `pdm subscription key ls` | ◑ | — |  |
| `pdm subscription key show` | ◑ | — |  |
| `pdm subscription key unassign` | — | ✓ |  |
| `pdm subscription node-status` | ◑ | — |  |
| `pdm subscription queue-clear` | — | ✓ |  |
| `pdm subscription revert-pending-clear` | — | ✓ |  |
| `pdm tfa delete` | — | · |  |
| `pdm tfa ls` | ◑ | — |  |
| `pdm tfa show` | ◑ | — |  |
| `pdm tfa update` | — | · |  |
| `pdm token add` | — | ✓ |  |
| `pdm token delete` | — | ✓ |  |
| `pdm token ls` | ◑ | — |  |
| `pdm token show` | ◑ | — |  |
| `pdm token update` | — | ✓ |  |
| `pdm user add` | — | ✓ |  |
| `pdm user delete` | — | ✓ |  |
| `pdm user ls` | ◑ | — |  |
| `pdm user show` | ◑ | — |  |
| `pdm user update` | — | ✓ |  |

## `pve`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `pve access acl list` | ✓ | — |  |
| `pve access acl set` | — | ✓ |  |
| `pve access domain create` | — | ✓ |  |
| `pve access domain delete` | — | ✓ |  |
| `pve access domain get` | ◑ | ✓ |  |
| `pve access domain list` | ✓ | — |  |
| `pve access domain set` | — | ✓ |  |
| `pve access domain sync` | — | ✓ |  |
| `pve access group create` | — | ✓ |  |
| `pve access group delete` | — | ✓ | error-contract checked |
| `pve access group get` | ◑ | ✓ |  |
| `pve access group list` | ✓ | — |  |
| `pve access group set` | — | ✓ |  |
| `pve access openid list` | ✓ | — |  |
| `pve access password set` | — | ✓ |  |
| `pve access permissions` | ✓ | — |  |
| `pve access role create` | — | ✓ |  |
| `pve access role delete` | — | ✓ |  |
| `pve access role get` | ◑ | ✓ |  |
| `pve access role list` | ✓ | — |  |
| `pve access role set` | — | ✓ |  |
| `pve access tfa create` | — | ✓ |  |
| `pve access tfa delete` | — | ✓ |  |
| `pve access tfa get` | ◑ | ✓ |  |
| `pve access tfa get-entry` | ◑ | ✓ |  |
| `pve access tfa list` | ✓ | — |  |
| `pve access tfa set` | — | ✓ |  |
| `pve access tfa types` | ✓ | — |  |
| `pve access tfa unlock` | — | ✓ |  |
| `pve access user create` | — | ✓ |  |
| `pve access user delete` | — | ✓ |  |
| `pve access user get` | ◑ | ✓ |  |
| `pve access user list` | ✓ | — |  |
| `pve access user set` | — | ✓ |  |
| `pve access user token create` | — | ✓ |  |
| `pve access user token delete` | — | ✓ |  |
| `pve access user token get` | ◑ | ✓ |  |
| `pve access user token list` | ◑ | ✓ |  |
| `pve access user token set` | — | ✓ |  |
| `pve cluster acme account create` | — | — | deferred — registers a new account against the ACME CA — the endpoint is restricted to root@pam and rejects API-token auth; not exercisable by the e2e suite — covered by unit tests |
| `pve cluster acme account delete` | — | — | deferred — deactivates and removes an account at the ACME CA — the endpoint is restricted to root@pam and rejects API-token auth; not exercisable by the e2e suite — covered by unit tests |
| `pve cluster acme account get` | ◑ | — |  |
| `pve cluster acme account list` | ✓ | — |  |
| `pve cluster acme account set` | — | — | deferred — updates an account's contact at the ACME CA — the endpoint is restricted to root@pam and rejects API-token auth; not exercisable by the e2e suite — covered by unit tests |
| `pve cluster acme challenge-schema` | ✓ | — |  |
| `pve cluster acme directories` | ✓ | — |  |
| `pve cluster acme plugin create` | — | ✓ |  |
| `pve cluster acme plugin delete` | — | ✓ |  |
| `pve cluster acme plugin get` | — | ✓ |  |
| `pve cluster acme plugin list` | ✓ | ✓ |  |
| `pve cluster acme plugin set` | — | ✓ |  |
| `pve cluster backup create` | — | ✓ |  |
| `pve cluster backup delete` | — | ✓ |  |
| `pve cluster backup get` | — | ✓ |  |
| `pve cluster backup included-volumes` | ◑ | ✓ |  |
| `pve cluster backup list` | ✓ | ✓ |  |
| `pve cluster backup set` | — | ✓ |  |
| `pve cluster backup-info not-backed-up` | ◑ | — |  |
| `pve cluster bulk migrate` | — | — | deferred — migrates guests cluster-wide — requires a second node; not exercisable on a single-node lab |
| `pve cluster bulk shutdown` | — | ✓ |  |
| `pve cluster bulk start` | — | ✓ |  |
| `pve cluster bulk suspend` | — | ✓ |  |
| `pve cluster ceph flags get` | ◑ | — |  |
| `pve cluster ceph flags list` | ◑ | — |  |
| `pve cluster ceph flags set` | — | — | deferred — toggles a cluster-wide Ceph OSD flag (e.g. noout/pause) — cluster-disruptive, not run live |
| `pve cluster ceph flags set-all` | — | — | deferred — toggles several cluster-wide Ceph OSD flags atomically (e.g. noout, norebalance) in one request during maintenance — cluster-disruptive; not exercised live; covered by unit tests |
| `pve cluster ceph metadata` | ◑ | — |  |
| `pve cluster ceph status` | ◑ | — |  |
| `pve cluster config apiversion` | ✓ | — |  |
| `pve cluster config create` | — | — | deferred — creates/initializes a new corosync cluster on the local node — one-time and disruptive to run against an already-clustered target; not exercised live; covered by unit tests |
| `pve cluster config join add` | — | — | deferred — joins the local node to an existing cluster — changes membership and quorum; not exercised live; covered by unit tests |
| `pve cluster config join list` | ◑ | — |  |
| `pve cluster config nodes add` | — | — | deferred — registers a new node in the cluster configuration — changes membership and quorum; not exercised live; covered by unit tests |
| `pve cluster config nodes delete` | — | — | deferred — removes a node from the cluster configuration — changes membership and quorum; not exercised live; covered by unit tests |
| `pve cluster config nodes list` | ✓ | — |  |
| `pve cluster config qdevice` | ◑ | — |  |
| `pve cluster config totem` | ◑ | — |  |
| `pve cluster cpu-model create` | — | ✓ |  |
| `pve cluster cpu-model delete` | — | ✓ |  |
| `pve cluster cpu-model get` | — | ✓ |  |
| `pve cluster cpu-model list` | ✓ | ✓ |  |
| `pve cluster cpu-model set` | — | ✓ |  |
| `pve cluster firewall alias create` | — | ✓ |  |
| `pve cluster firewall alias delete` | — | ✓ |  |
| `pve cluster firewall alias get` | ◑ | — |  |
| `pve cluster firewall alias list` | ✓ | ✓ |  |
| `pve cluster firewall alias update` | — | ✓ |  |
| `pve cluster firewall group create` | — | ✓ |  |
| `pve cluster firewall group delete` | — | ✓ |  |
| `pve cluster firewall group get` | ◑ | — |  |
| `pve cluster firewall group list` | ✓ | ✓ |  |
| `pve cluster firewall group rule-add` | — | ✓ |  |
| `pve cluster firewall group rule-delete` | — | ✓ |  |
| `pve cluster firewall group rule-update` | — | ✓ |  |
| `pve cluster firewall group rules` | — | ✓ |  |
| `pve cluster firewall ipset add` | — | ✓ |  |
| `pve cluster firewall ipset create` | — | ✓ |  |
| `pve cluster firewall ipset delete` | — | ✓ |  |
| `pve cluster firewall ipset get` | ◑ | ✓ |  |
| `pve cluster firewall ipset list` | ✓ | ✓ |  |
| `pve cluster firewall ipset remove` | — | ✓ |  |
| `pve cluster firewall ipset update` | — | ✓ |  |
| `pve cluster firewall macros list` | ✓ | — |  |
| `pve cluster firewall options describe` | ✓ | — |  |
| `pve cluster firewall options get` | ✓ | ✓ |  |
| `pve cluster firewall options set` | — | ✓ |  |
| `pve cluster firewall refs list` | ✓ | — |  |
| `pve cluster firewall rules create` | — | ✓ |  |
| `pve cluster firewall rules delete` | — | ✓ |  |
| `pve cluster firewall rules get` | — | ✓ |  |
| `pve cluster firewall rules list` | ✓ | ✓ |  |
| `pve cluster firewall rules update` | — | ✓ |  |
| `pve cluster ha group create` | — | ✓ |  |
| `pve cluster ha group delete` | — | ✓ |  |
| `pve cluster ha group get` | — | ✓ |  |
| `pve cluster ha group list` | ◑ | ✓ |  |
| `pve cluster ha group set` | — | ✓ |  |
| `pve cluster ha resource create` | — | ✓ |  |
| `pve cluster ha resource delete` | — | ✓ |  |
| `pve cluster ha resource get` | — | ✓ |  |
| `pve cluster ha resource list` | ✓ | ✓ |  |
| `pve cluster ha resource migrate` | — | · |  |
| `pve cluster ha resource relocate` | — | — | deferred — requires a second node as the relocation target — not exercisable on a single-node lab |
| `pve cluster ha resource set` | — | ✓ |  |
| `pve cluster ha rule create` | — | ✓ |  |
| `pve cluster ha rule delete` | — | ✓ |  |
| `pve cluster ha rule get` | — | ✓ |  |
| `pve cluster ha rule list` | ✓ | ✓ |  |
| `pve cluster ha rule set` | — | ✓ |  |
| `pve cluster ha status arm` | — | — | deferred — re-enables the cluster-wide HA stack — would disrupt every HA-managed resource on the lab |
| `pve cluster ha status current` | ✓ | — |  |
| `pve cluster ha status disarm` | — | — | deferred — disables the cluster-wide HA stack — would disrupt every HA-managed resource on the lab |
| `pve cluster ha status manager` | ✓ | — |  |
| `pve cluster jobs realm-sync create` | — | ✓ |  |
| `pve cluster jobs realm-sync delete` | — | ✓ |  |
| `pve cluster jobs realm-sync get` | — | ✓ |  |
| `pve cluster jobs realm-sync list` | ✓ | ✓ |  |
| `pve cluster jobs realm-sync set` | — | ✓ |  |
| `pve cluster jobs schedule-analyze` | ✓ | — |  |
| `pve cluster log` | ✓ | — |  |
| `pve cluster mapping dir create` | — | ✓ |  |
| `pve cluster mapping dir delete` | — | ✓ |  |
| `pve cluster mapping dir get` | — | ✓ |  |
| `pve cluster mapping dir list` | ✓ | ✓ |  |
| `pve cluster mapping dir set` | — | ✓ |  |
| `pve cluster mapping pci create` | — | ✓ |  |
| `pve cluster mapping pci delete` | — | ✓ |  |
| `pve cluster mapping pci get` | — | ✓ |  |
| `pve cluster mapping pci list` | ✓ | — |  |
| `pve cluster mapping pci set` | — | ✓ |  |
| `pve cluster mapping usb create` | — | ✓ |  |
| `pve cluster mapping usb delete` | — | ✓ |  |
| `pve cluster mapping usb get` | — | ✓ |  |
| `pve cluster mapping usb list` | ✓ | — |  |
| `pve cluster mapping usb set` | — | ✓ |  |
| `pve cluster metrics export` | ◑ | — |  |
| `pve cluster metrics server create` | — | ✓ |  |
| `pve cluster metrics server delete` | — | ✓ |  |
| `pve cluster metrics server get` | — | ✓ |  |
| `pve cluster metrics server list` | ✓ | ✓ |  |
| `pve cluster metrics server set` | — | ✓ |  |
| `pve cluster next-id` | ✓ | — |  |
| `pve cluster notifications endpoints` | ✓ | — |  |
| `pve cluster notifications gotify create` | — | ✓ |  |
| `pve cluster notifications gotify delete` | — | ✓ |  |
| `pve cluster notifications gotify get` | — | ✓ |  |
| `pve cluster notifications gotify list` | ✓ | ✓ |  |
| `pve cluster notifications gotify set` | — | ✓ |  |
| `pve cluster notifications matcher create` | — | ✓ |  |
| `pve cluster notifications matcher delete` | — | ✓ |  |
| `pve cluster notifications matcher get` | — | ✓ |  |
| `pve cluster notifications matcher list` | ✓ | — |  |
| `pve cluster notifications matcher set` | — | ✓ |  |
| `pve cluster notifications matcher-field-values` | ✓ | — |  |
| `pve cluster notifications matcher-fields` | ✓ | — |  |
| `pve cluster notifications sendmail create` | — | ✓ |  |
| `pve cluster notifications sendmail delete` | — | ✓ |  |
| `pve cluster notifications sendmail get` | — | ✓ |  |
| `pve cluster notifications sendmail list` | ✓ | ✓ |  |
| `pve cluster notifications sendmail set` | — | ✓ |  |
| `pve cluster notifications smtp create` | — | ✓ |  |
| `pve cluster notifications smtp delete` | — | ✓ |  |
| `pve cluster notifications smtp get` | — | ✓ |  |
| `pve cluster notifications smtp list` | ✓ | ✓ |  |
| `pve cluster notifications smtp set` | — | ✓ |  |
| `pve cluster notifications targets` | ✓ | ✓ |  |
| `pve cluster notifications targets-test` | — | ✓ |  |
| `pve cluster notifications webhook create` | — | ✓ |  |
| `pve cluster notifications webhook delete` | — | ✓ |  |
| `pve cluster notifications webhook get` | — | ✓ |  |
| `pve cluster notifications webhook list` | ✓ | ✓ |  |
| `pve cluster notifications webhook set` | — | ✓ |  |
| `pve cluster options describe` | ✓ | — |  |
| `pve cluster options get` | ✓ | ✓ |  |
| `pve cluster options set` | — | ✓ |  |
| `pve cluster qemu cpu-flags` | ✓ | — |  |
| `pve cluster replication create` | — | · |  |
| `pve cluster replication delete` | — | · |  |
| `pve cluster replication get` | ◑ | · |  |
| `pve cluster replication list` | ✓ | ✓ |  |
| `pve cluster replication set` | — | · |  |
| `pve cluster resources` | ✓ | — |  |
| `pve cluster status` | ✓ | — |  |
| `pve cluster tasks` | ✓ | — |  |
| `pve lxc clone` | — | ✓ |  |
| `pve lxc config describe` | ✓ | — |  |
| `pve lxc config get` | ◑ | ✓ |  |
| `pve lxc config pending` | ◑ | ✓ |  |
| `pve lxc config set` | — | ✓ |  |
| `pve lxc console` | ◑ | ✓ |  |
| `pve lxc create` | — | ✓ |  |
| `pve lxc delete` | — | ✓ |  |
| `pve lxc disk move` | — | ✓ |  |
| `pve lxc disk resize` | — | ✓ |  |
| `pve lxc feature` | ◑ | ✓ |  |
| `pve lxc firewall alias create` | — | ✓ |  |
| `pve lxc firewall alias delete` | — | ✓ |  |
| `pve lxc firewall alias get` | — | ✓ |  |
| `pve lxc firewall alias list` | — | ✓ |  |
| `pve lxc firewall alias update` | — | ✓ |  |
| `pve lxc firewall ipset add` | — | ✓ |  |
| `pve lxc firewall ipset create` | — | ✓ |  |
| `pve lxc firewall ipset delete` | — | ✓ |  |
| `pve lxc firewall ipset get-member` | — | ✓ |  |
| `pve lxc firewall ipset list` | — | ✓ |  |
| `pve lxc firewall ipset remove` | — | ✓ |  |
| `pve lxc firewall ipset update-member` | — | ✓ |  |
| `pve lxc firewall log` | ◑ | ✓ |  |
| `pve lxc firewall options describe` | ✓ | — |  |
| `pve lxc firewall options get` | ◑ | ✓ |  |
| `pve lxc firewall options set` | — | ✓ |  |
| `pve lxc firewall refs` | ◑ | ✓ |  |
| `pve lxc firewall rules create` | — | ✓ |  |
| `pve lxc firewall rules delete` | — | ✓ |  |
| `pve lxc firewall rules get` | — | ✓ |  |
| `pve lxc firewall rules list` | ◑ | ✓ |  |
| `pve lxc firewall rules update` | — | ✓ |  |
| `pve lxc hookscript get` | ◑ | ✓ |  |
| `pve lxc hookscript set` | — | — | deferred — PVE restricts the hookscript config key to the root user and the suites run on an API token; the volume must also already exist on a snippets storage; covered by unit tests |
| `pve lxc hookscript unset` | — | — | deferred — PVE restricts the hookscript config key to the root user, including its deletion, and the suites run on an API token; covered by unit tests |
| `pve lxc interfaces` | ◑ | ✓ |  |
| `pve lxc list` | ✓ | — |  |
| `pve lxc metrics` | ◑ | ✓ |  |
| `pve lxc migrate` | — | ✓ |  |
| `pve lxc migrate check` | ◑ | ✓ |  |
| `pve lxc permissions effective` | ◑ | — |  |
| `pve lxc permissions grant` | — | — | deferred — grants ACL roles on the container's /vms/{vmid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve lxc permissions list` | ◑ | — |  |
| `pve lxc permissions revoke` | — | — | deferred — revokes ACL roles on the container's /vms/{vmid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve lxc reboot` | — | ✓ |  |
| `pve lxc remote-migrate` | — | — | deferred — migrates a container to a different Proxmox VE cluster — requires two live clusters; no rollback without manual intervention; not exercised live |
| `pve lxc resume` | — | ✓ |  |
| `pve lxc rrd` | ◑ | ✓ |  |
| `pve lxc security caps add` | — | ✓ |  |
| `pve lxc security caps describe` | ✓ | — |  |
| `pve lxc security caps remove` | — | ✓ |  |
| `pve lxc security caps reset` | — | ✓ |  |
| `pve lxc security caps set` | — | ✓ |  |
| `pve lxc security caps show` | ◑ | ✓ |  |
| `pve lxc security features set` | — | ✓ |  |
| `pve lxc security features show` | ◑ | ✓ |  |
| `pve lxc security list` | ◑ | — |  |
| `pve lxc security show` | ◑ | ✓ |  |
| `pve lxc shutdown` | — | ✓ |  |
| `pve lxc snapshot create` | — | ✓ |  |
| `pve lxc snapshot delete` | — | ✓ |  |
| `pve lxc snapshot list` | ◑ | ✓ |  |
| `pve lxc snapshot rollback` | — | ✓ |  |
| `pve lxc snapshot show` | ◑ | ✓ |  |
| `pve lxc snapshot update` | — | ✓ |  |
| `pve lxc start` | — | ✓ |  |
| `pve lxc status` | ◑ | ✓ |  |
| `pve lxc stop` | — | ✓ |  |
| `pve lxc suspend` | — | ✓ |  |
| `pve lxc template download` | — | ✓ |  |
| `pve lxc template list` | ✓ | — |  |
| `pve lxc to-template` | — | — | deferred — converts the discovered container into a template — irreversible for that instance and only sensible as the terminal step of a dedicated throwaway guest lifecycle; not exercised against a live container; covered by unit tests |
| `pve node apt changelog` | ◑ | — |  |
| `pve node apt list` | ◑ | — |  |
| `pve node apt repositories add` | — | — | deferred — adds a standard APT repository to the node's sources; not exercised live |
| `pve node apt repositories enable` | — | — | deferred — enables or disables a configured APT repository on the node; not exercised live |
| `pve node apt repositories list` | ◑ | — |  |
| `pve node apt templates download` | — | — | deferred — downloads a real appliance template tarball to a storage — bandwidth/storage-consuming; not exercised live; covered by unit tests |
| `pve node apt templates list` | ◑ | — |  |
| `pve node apt update` | — | ✓ |  |
| `pve node apt versions` | ◑ | — |  |
| `pve node capabilities qemu cpu` | ◑ | — |  |
| `pve node capabilities qemu cpu-flags` | ◑ | — |  |
| `pve node capabilities qemu machines` | ◑ | — |  |
| `pve node capabilities qemu migration` | ◑ | — |  |
| `pve node ceph cfg db` | ◑ | — |  |
| `pve node ceph cfg index` | ◑ | — |  |
| `pve node ceph cfg raw` | ◑ | — |  |
| `pve node ceph cfg value` | ◑ | — |  |
| `pve node ceph cmd-safety` | ◑ | — |  |
| `pve node ceph crush` | ◑ | — |  |
| `pve node ceph fs create` | — | — | deferred — creates a CephFS filesystem and its backing pools; not exercised live |
| `pve node ceph fs delete` | — | — | deferred — destroys a CephFS filesystem and optionally its pools; not exercised live |
| `pve node ceph fs list` | ◑ | — |  |
| `pve node ceph init` | — | — | deferred — initializes a Ceph cluster configuration on the node — cluster-wide and destructive; not exercised live |
| `pve node ceph log` | ◑ | — |  |
| `pve node ceph mds create` | — | — | deferred — provisions a Ceph metadata-server daemon on the node; not exercised live |
| `pve node ceph mds delete` | — | — | deferred — destroys a Ceph metadata-server daemon on the node; not exercised live |
| `pve node ceph mds list` | ◑ | — |  |
| `pve node ceph mgr create` | — | — | deferred — provisions a Ceph manager daemon on the node; not exercised live |
| `pve node ceph mgr delete` | — | — | deferred — destroys a Ceph manager daemon on the node; not exercised live |
| `pve node ceph mgr list` | ◑ | — |  |
| `pve node ceph mon create` | — | — | deferred — provisions a Ceph monitor daemon on the node; not exercised live |
| `pve node ceph mon delete` | — | — | deferred — destroys a Ceph monitor daemon on the node; not exercised live |
| `pve node ceph mon list` | ◑ | — |  |
| `pve node ceph osd create` | — | — | deferred — creates an OSD by wiping and consuming a block device; not exercised live |
| `pve node ceph osd delete` | — | — | deferred — destroys an OSD and optionally zaps its underlying volumes; not exercised live |
| `pve node ceph osd get` | ◑ | — |  |
| `pve node ceph osd in` | — | — | deferred — marks an OSD in, triggering cluster data movement; not exercised live |
| `pve node ceph osd list` | ◑ | — |  |
| `pve node ceph osd lv-info` | ◑ | — |  |
| `pve node ceph osd metadata` | ◑ | — |  |
| `pve node ceph osd out` | — | — | deferred — marks an OSD out, draining its data across the cluster; not exercised live |
| `pve node ceph osd scrub` | — | — | deferred — triggers an OSD scrub that adds cluster I/O load; not exercised live |
| `pve node ceph pool create` | — | — | deferred — creates a Ceph pool, consuming cluster capacity; not exercised live |
| `pve node ceph pool delete` | — | — | deferred — destroys a Ceph pool and permanently loses its data; not exercised live |
| `pve node ceph pool get` | ◑ | — |  |
| `pve node ceph pool list` | ◑ | — |  |
| `pve node ceph pool set` | — | — | deferred — reconfigures an existing Ceph pool's parameters; not exercised live |
| `pve node ceph pool status` | ◑ | — |  |
| `pve node ceph restart` | — | — | deferred — restarts Ceph services on the node — disruptive; not exercised live |
| `pve node ceph rules` | ◑ | — |  |
| `pve node ceph start` | — | — | deferred — starts Ceph services on the node — disruptive; not exercised live |
| `pve node ceph status` | ◑ | — |  |
| `pve node ceph stop` | — | — | deferred — stops Ceph services on the node — disruptive; not exercised live |
| `pve node cert acme delete` | — | — | deferred — removes the node's ACME certificate; not exercised live |
| `pve node cert acme list` | ◑ | — |  |
| `pve node cert acme order` | — | — | deferred — orders the node's ACME certificate (contacts Let's Encrypt); not exercised live |
| `pve node cert acme renew` | — | — | deferred — renews the node's ACME certificate (contacts Let's Encrypt); not exercised live |
| `pve node cert custom delete` | — | — | deferred — removes the node's custom API TLS certificate — could break TLS to the node; not exercised live |
| `pve node cert custom upload` | — | — | deferred — replaces the node's API TLS certificate — could break TLS to the node; not exercised live |
| `pve node cert list` | ◑ | — |  |
| `pve node config describe` | ✓ | — |  |
| `pve node config get` | ◑ | — |  |
| `pve node config set` | — | — | deferred — mutates node-level configuration (description, ACME, wake-on-LAN, ballooning target, startall delay); not exercised live; covered by unit tests |
| `pve node console` | — | — | deferred — opens a live SSH terminal aliased to `node shell`, so it cannot be driven head-less; not run live; covered by unit tests |
| `pve node disks create directory` | — | ✓ |  |
| `pve node disks create lvm` | — | ✓ |  |
| `pve node disks create lvmthin` | — | ✓ |  |
| `pve node disks create zfs` | — | ✓ |  |
| `pve node disks delete directory` | — | ✓ |  |
| `pve node disks delete lvm` | — | ✓ |  |
| `pve node disks delete lvmthin` | — | ✓ |  |
| `pve node disks delete zfs` | — | ✓ |  |
| `pve node disks get zfs` | ◑ | — |  |
| `pve node disks init-gpt` | — | ✓ |  |
| `pve node disks list` | ◑ | — |  |
| `pve node disks pools directory` | ◑ | — |  |
| `pve node disks pools lvm` | ◑ | — |  |
| `pve node disks pools lvmthin` | ◑ | — |  |
| `pve node disks pools zfs` | ◑ | — |  |
| `pve node disks smart` | ◑ | — |  |
| `pve node disks wipe` | — | — | deferred — BLOCKED: /nodes/{node}/disks/wipedisk is root@pam-only and rejects the API token ('user != root@pam'), like storage volume copy and cluster acme account; not invokable by the suite |
| `pve node dns get` | ◑ | ✓ |  |
| `pve node dns set` | — | ✓ |  |
| `pve node exec` | — | ✓ |  |
| `pve node execute` | — | — | n/a — runs arbitrary commands on the real host via the PVE API — security-sensitive; out of scope for automated e2e regardless of guarding |
| `pve node firewall log` | ◑ | — |  |
| `pve node firewall options describe` | ✓ | — |  |
| `pve node firewall options get` | ◑ | ✓ |  |
| `pve node firewall options set` | — | — | deferred — changes the host firewall policy — could cut the node off the network; not exercised live |
| `pve node firewall rules create` | — | ✓ |  |
| `pve node firewall rules delete` | — | ✓ |  |
| `pve node firewall rules get` | — | ✓ |  |
| `pve node firewall rules list` | ◑ | ✓ |  |
| `pve node firewall rules update` | — | ✓ |  |
| `pve node hardware mdev` | ◑ | — |  |
| `pve node hardware pci` | ◑ | — |  |
| `pve node hardware usb` | ◑ | — |  |
| `pve node hosts get` | ◑ | ✓ |  |
| `pve node hosts set` | — | ✓ |  |
| `pve node journal` | ◑ | — |  |
| `pve node list` | ✓ | — |  |
| `pve node migrateall` | — | — | deferred — migrates every guest off the node to a target (needs a second node); not exercised live; covered by unit tests |
| `pve node netstat` | ◑ | — |  |
| `pve node network apply` | — | — | deferred — reloads the staged host network configuration — could cut the node off the network; not exercised live |
| `pve node network create` | — | ✓ |  |
| `pve node network delete` | — | ✓ |  |
| `pve node network get` | ◑ | — |  |
| `pve node network list` | ◑ | — |  |
| `pve node network revert` | — | ✓ |  |
| `pve node network set` | — | ✓ |  |
| `pve node oci pull` | — | ✓ |  |
| `pve node oci tags` | — | ✓ |  |
| `pve node permissions effective` | ◑ | — |  |
| `pve node permissions grant` | — | — | deferred — grants ACL roles on the node's /nodes/{node} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve node permissions list` | ◑ | — |  |
| `pve node permissions revoke` | — | — | deferred — revokes ACL roles on the node's /nodes/{node} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve node query-url-metadata` | — | ✓ |  |
| `pve node reboot` | — | — | n/a — reboots the real host — would take the shared lab node offline; not automatable |
| `pve node replication get` | ◑ | — |  |
| `pve node replication list` | ◑ | — |  |
| `pve node replication log` | ◑ | — |  |
| `pve node replication run` | — | — | deferred — triggers an immediate replication sync to the target node (needs a configured job); not exercised live |
| `pve node replication status` | ◑ | — |  |
| `pve node report` | ◑ | — |  |
| `pve node rrddata` | ◑ | — |  |
| `pve node rsync` | — | ✓ |  |
| `pve node scan cifs` | — | ✓ |  |
| `pve node scan iscsi` | — | ✓ |  |
| `pve node scan lvm` | ◑ | — |  |
| `pve node scan lvmthin` | ◑ | — |  |
| `pve node scan nfs` | — | ✓ |  |
| `pve node scan pbs` | — | ✓ |  |
| `pve node scan zfs` | ◑ | — |  |
| `pve node services get` | ◑ | — |  |
| `pve node services list` | ◑ | — |  |
| `pve node services reload` | — | ✓ |  |
| `pve node services restart` | — | ✓ |  |
| `pve node services start` | — | ✓ |  |
| `pve node services state` | ◑ | — |  |
| `pve node services stop` | — | ✓ |  |
| `pve node shell` | — | — | deferred — opens a live SSH terminal on the node, so it cannot be driven head-less; not run live; covered by unit tests |
| `pve node shutdown` | — | — | n/a — shuts down the real host — would take the shared lab node offline; not automatable |
| `pve node spiceshell` | — | — | n/a — requests an interactive SPICE console-proxy ticket — not automatable head-less; covered by unit tests |
| `pve node ssh` | — | ✓ |  |
| `pve node startall` | — | ✓ |  |
| `pve node status` | ◑ | — |  |
| `pve node stopall` | — | ✓ |  |
| `pve node subscription delete` | — | ✓ |  |
| `pve node subscription get` | ◑ | — |  |
| `pve node subscription set` | — | — | deferred — sets the node's subscription key (changes licensing state); not exercised live; covered by unit tests |
| `pve node subscription update` | — | ✓ |  |
| `pve node suspendall` | — | ✓ |  |
| `pve node syslog` | ◑ | — |  |
| `pve node task list` | ◑ | — |  |
| `pve node task log` | ◑ | — |  |
| `pve node task status` | ◑ | — |  |
| `pve node task stop` | — | ✓ |  |
| `pve node task wait` | ◑ | — |  |
| `pve node termproxy` | — | — | n/a — requests an interactive websocket terminal-proxy ticket — not automatable head-less; covered by unit tests |
| `pve node time get` | ◑ | ✓ |  |
| `pve node time set` | — | ✓ |  |
| `pve node vncshell` | — | — | n/a — requests an interactive VNC console-proxy ticket — not automatable head-less; covered by unit tests |
| `pve node vzdump` | — | ✓ |  |
| `pve node vzdump defaults` | ◑ | — |  |
| `pve node vzdump extract-config` | ◑ | — |  |
| `pve node wakeonlan` | — | — | deferred — sends a Wake-on-LAN packet to power on another node — the API rejects waking the local node, and this is a single-node cluster, so there is no remote target; not exercised live; covered by unit tests |
| `pve pool create` | — | ✓ | error-contract checked |
| `pve pool delete` | — | ✓ |  |
| `pve pool get` | ◑ | — |  |
| `pve pool list` | ✓ | — |  |
| `pve pool permissions effective` | ◑ | — |  |
| `pve pool permissions grant` | — | — | deferred — grants ACL roles on the pool's singular /pool/{poolid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve pool permissions list` | ◑ | — |  |
| `pve pool permissions revoke` | — | — | deferred — revokes ACL roles on the pool's singular /pool/{poolid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve pool set` | — | ✓ |  |
| `pve qemu agent` | — | ✓ |  |
| `pve qemu agent exec` | — | ✓ |  |
| `pve qemu agent exec-status` | — | ✓ |  |
| `pve qemu agent file-read` | — | ✓ |  |
| `pve qemu agent file-write` | — | ✓ |  |
| `pve qemu agent set-user-password` | — | ✓ |  |
| `pve qemu clone` | — | ✓ |  |
| `pve qemu cloudinit dump` | — | ✓ |  |
| `pve qemu cloudinit pending` | ◑ | ✓ |  |
| `pve qemu cloudinit update` | — | ✓ |  |
| `pve qemu config describe` | ✓ | — |  |
| `pve qemu config get` | ◑ | ✓ |  |
| `pve qemu config pending` | — | ✓ |  |
| `pve qemu config set` | — | ✓ |  |
| `pve qemu console` | ◑ | ✓ |  |
| `pve qemu cpu list` | ✓ | — |  |
| `pve qemu cpu-flags` | ✓ | — |  |
| `pve qemu create` | — | ✓ |  |
| `pve qemu delete` | — | ✓ |  |
| `pve qemu disk move` | — | ✓ |  |
| `pve qemu disk resize` | — | ✓ |  |
| `pve qemu disk unlink` | — | ✓ |  |
| `pve qemu feature` | ◑ | — |  |
| `pve qemu firewall alias create` | — | ✓ |  |
| `pve qemu firewall alias delete` | — | ✓ |  |
| `pve qemu firewall alias get` | — | ✓ |  |
| `pve qemu firewall alias list` | — | ✓ |  |
| `pve qemu firewall alias update` | — | ✓ |  |
| `pve qemu firewall ipset add` | — | ✓ |  |
| `pve qemu firewall ipset create` | — | ✓ |  |
| `pve qemu firewall ipset delete` | — | ✓ |  |
| `pve qemu firewall ipset get-member` | — | ✓ |  |
| `pve qemu firewall ipset list` | — | ✓ |  |
| `pve qemu firewall ipset remove` | — | ✓ |  |
| `pve qemu firewall ipset update-member` | — | ✓ |  |
| `pve qemu firewall log` | ◑ | — |  |
| `pve qemu firewall options describe` | ✓ | — |  |
| `pve qemu firewall options get` | ◑ | ✓ |  |
| `pve qemu firewall options set` | — | ✓ |  |
| `pve qemu firewall refs` | ◑ | — |  |
| `pve qemu firewall rules create` | — | ✓ |  |
| `pve qemu firewall rules delete` | — | ✓ |  |
| `pve qemu firewall rules get` | — | ✓ |  |
| `pve qemu firewall rules list` | ◑ | ✓ |  |
| `pve qemu firewall rules update` | — | ✓ |  |
| `pve qemu hookscript get` | ◑ | — |  |
| `pve qemu hookscript set` | — | — | deferred — PVE restricts the hookscript config key to the root user and the suites run on an API token; the volume must also already exist on a snippets storage; covered by unit tests |
| `pve qemu hookscript unset` | — | — | deferred — PVE restricts the hookscript config key to the root user, including its deletion, and the suites run on an API token; covered by unit tests |
| `pve qemu list` | ✓ | — |  |
| `pve qemu machine list` | ✓ | — |  |
| `pve qemu metrics` | ◑ | — |  |
| `pve qemu migrate` | — | ✓ |  |
| `pve qemu migrate capabilities` | ✓ | — |  |
| `pve qemu migrate check` | ◑ | — |  |
| `pve qemu monitor` | — | ✓ |  |
| `pve qemu permissions effective` | ◑ | — |  |
| `pve qemu permissions grant` | — | — | deferred — grants ACL roles on the VM's /vms/{vmid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve qemu permissions list` | ◑ | — |  |
| `pve qemu permissions revoke` | — | — | deferred — revokes ACL roles on the VM's /vms/{vmid} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve qemu reboot` | — | · |  |
| `pve qemu remote-migrate` | — | — | deferred — migrates a VM to a different Proxmox VE cluster — requires two live clusters with shared or compatible storage; no rollback without manual intervention; not exercised live |
| `pve qemu reset` | — | ✓ |  |
| `pve qemu resume` | — | ✓ |  |
| `pve qemu rrd` | ◑ | — |  |
| `pve qemu security agent set` | — | ✓ |  |
| `pve qemu security agent show` | ◑ | — |  |
| `pve qemu security confidential clear` | — | ✓ |  |
| `pve qemu security confidential set` | — | ✓ |  |
| `pve qemu security confidential show` | ◑ | — |  |
| `pve qemu security cpu-flags describe` | ✓ | — |  |
| `pve qemu security cpu-flags set` | — | ✓ |  |
| `pve qemu security cpu-flags show` | ◑ | — |  |
| `pve qemu security list` | ◑ | — |  |
| `pve qemu security nic firewall` | — | ✓ |  |
| `pve qemu security nic show` | ◑ | ✓ |  |
| `pve qemu security protection disable` | — | ✓ |  |
| `pve qemu security protection enable` | — | ✓ |  |
| `pve qemu security secureboot enable` | — | ✓ |  |
| `pve qemu security secureboot show` | ◑ | ✓ |  |
| `pve qemu security show` | ◑ | ✓ |  |
| `pve qemu security tpm add` | — | ✓ |  |
| `pve qemu security tpm remove` | — | ✓ |  |
| `pve qemu security tpm show` | ◑ | ✓ |  |
| `pve qemu sendkey` | — | ✓ |  |
| `pve qemu shutdown` | — | ✓ |  |
| `pve qemu snapshot create` | — | ✓ | error-contract checked |
| `pve qemu snapshot delete` | — | ✓ |  |
| `pve qemu snapshot list` | ◑ | ✓ |  |
| `pve qemu snapshot rollback` | — | ✓ |  |
| `pve qemu snapshot show` | ◑ | ✓ |  |
| `pve qemu snapshot update` | — | ✓ |  |
| `pve qemu ssh` | — | — | n/a — opens an interactive SSH tunnel into a guest — not automatable head-less, same class as `node shell`/`node console`; covered by unit tests |
| `pve qemu start` | — | ✓ |  |
| `pve qemu status` | ◑ | ✓ |  |
| `pve qemu stop` | — | ✓ |  |
| `pve qemu suspend` | — | ✓ |  |
| `pve qemu template` | — | ✓ |  |
| `pve sdn apply` | — | ✓ |  |
| `pve sdn controller create` | — | ✓ |  |
| `pve sdn controller delete` | — | ✓ |  |
| `pve sdn controller get` | — | ✓ |  |
| `pve sdn controller list` | ✓ | — |  |
| `pve sdn controller set` | — | ✓ |  |
| `pve sdn dns create` | — | ✓ |  |
| `pve sdn dns delete` | — | ✓ |  |
| `pve sdn dns get` | — | ✓ |  |
| `pve sdn dns list` | ✓ | — |  |
| `pve sdn dns set` | — | ✓ |  |
| `pve sdn dry-run` | ◑ | — |  |
| `pve sdn fabric create` | — | ✓ |  |
| `pve sdn fabric delete` | — | ✓ |  |
| `pve sdn fabric get` | — | ✓ |  |
| `pve sdn fabric list` | ◑ | — |  |
| `pve sdn fabric list-all` | ◑ | — |  |
| `pve sdn fabric node create` | — | ✓ |  |
| `pve sdn fabric node delete` | — | ✓ |  |
| `pve sdn fabric node get` | — | ✓ |  |
| `pve sdn fabric node list` | ◑ | — |  |
| `pve sdn fabric node set` | — | ✓ |  |
| `pve sdn fabric set` | — | ✓ |  |
| `pve sdn ipam create` | — | ✓ |  |
| `pve sdn ipam delete` | — | ✓ |  |
| `pve sdn ipam get` | — | ✓ |  |
| `pve sdn ipam list` | ✓ | ✓ |  |
| `pve sdn ipam set` | — | — | deferred — the pve IPAM exposes no settable properties; the netbox/phpipam types validate a reachable external backend on create — covered by unit tests |
| `pve sdn ipam status` | ◑ | — |  |
| `pve sdn lock acquire` | — | ✓ |  |
| `pve sdn lock release` | — | ✓ |  |
| `pve sdn prefix-list create` | — | ✓ |  |
| `pve sdn prefix-list delete` | — | ✓ |  |
| `pve sdn prefix-list entry add` | — | ✓ |  |
| `pve sdn prefix-list entry delete` | — | ✓ |  |
| `pve sdn prefix-list entry get` | — | ✓ |  |
| `pve sdn prefix-list entry list` | — | ✓ |  |
| `pve sdn prefix-list entry set` | — | ✓ |  |
| `pve sdn prefix-list get` | — | ✓ |  |
| `pve sdn prefix-list list` | ◑ | — |  |
| `pve sdn prefix-list set` | — | ✓ |  |
| `pve sdn rollback` | — | — | deferred — discards ALL pending SDN changes cluster-wide; not exercised live; covered by unit tests |
| `pve sdn route-map entry add` | — | ✓ |  |
| `pve sdn route-map entry delete` | — | ✓ |  |
| `pve sdn route-map entry get` | — | ✓ |  |
| `pve sdn route-map entry list` | ◑ | — |  |
| `pve sdn route-map entry set` | — | ✓ |  |
| `pve sdn route-map get` | — | ✓ |  |
| `pve sdn route-map list` | ◑ | — |  |
| `pve sdn status fabrics interfaces` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `pve sdn status fabrics neighbors` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `pve sdn status fabrics routes` | — | — | deferred — requires applied FRR fabric backend not present in lab |
| `pve sdn status vnets mac-vrf` | — | ✓ |  |
| `pve sdn status zones bridges` | — | ✓ |  |
| `pve sdn status zones content` | — | ✓ |  |
| `pve sdn status zones get` | — | ✓ |  |
| `pve sdn status zones ip-vrf` | — | ✓ |  |
| `pve sdn subnet create` | — | ✓ |  |
| `pve sdn subnet delete` | — | ✓ |  |
| `pve sdn subnet list` | ◑ | — |  |
| `pve sdn subnet set` | — | ✓ |  |
| `pve sdn subnet show` | ◑ | — |  |
| `pve sdn vnet create` | — | ✓ |  |
| `pve sdn vnet delete` | — | ✓ |  |
| `pve sdn vnet firewall options describe` | ✓ | — |  |
| `pve sdn vnet firewall options get` | ◑ | ✓ |  |
| `pve sdn vnet firewall options set` | — | ✓ |  |
| `pve sdn vnet firewall rules create` | — | ✓ |  |
| `pve sdn vnet firewall rules delete` | — | ✓ |  |
| `pve sdn vnet firewall rules get` | — | ✓ |  |
| `pve sdn vnet firewall rules list` | ◑ | ✓ |  |
| `pve sdn vnet firewall rules set` | — | ✓ |  |
| `pve sdn vnet ips create` | — | ✓ |  |
| `pve sdn vnet ips delete` | — | ✓ |  |
| `pve sdn vnet ips set` | — | ✓ |  |
| `pve sdn vnet list` | ✓ | — |  |
| `pve sdn vnet permissions effective` | ◑ | — |  |
| `pve sdn vnet permissions grant` | — | — | deferred — grants ACL roles on the vnet's derived /sdn/zones/{zone}/{vnet} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve sdn vnet permissions list` | ◑ | — |  |
| `pve sdn vnet permissions revoke` | — | — | deferred — revokes ACL roles on the vnet's derived /sdn/zones/{zone}/{vnet} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve sdn vnet set` | — | ✓ |  |
| `pve sdn vnet show` | ◑ | — |  |
| `pve sdn zone create` | — | ✓ |  |
| `pve sdn zone delete` | — | ✓ |  |
| `pve sdn zone list` | ✓ | — |  |
| `pve sdn zone permissions effective` | ◑ | — |  |
| `pve sdn zone permissions grant` | — | — | deferred — grants ACL roles on the zone's /sdn/zones/{zone} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve sdn zone permissions list` | ◑ | — |  |
| `pve sdn zone permissions revoke` | — | — | deferred — revokes ACL roles on the zone's /sdn/zones/{zone} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve sdn zone set` | — | ✓ |  |
| `pve sdn zone show` | ◑ | — |  |
| `pve storage aplinfo download` | — | — | deferred — downloads a real appliance template tarball to a storage — bandwidth/storage-consuming; not exercised live; covered by unit tests |
| `pve storage aplinfo list` | ◑ | — |  |
| `pve storage content` | ◑ | — |  |
| `pve storage create` | — | ✓ |  |
| `pve storage delete` | — | ✓ |  |
| `pve storage describe` | ✓ | — |  |
| `pve storage download-url` | — | ✓ |  |
| `pve storage file-restore download` | — | — | deferred — extracts a file from a PBS snapshot — lab has no Proxmox Backup Server storage; not exercised live; covered by unit tests |
| `pve storage file-restore list` | — | — | deferred — browses files inside a PBS snapshot — lab has no Proxmox Backup Server storage; not exercised live; covered by unit tests |
| `pve storage get` | ◑ | ✓ |  |
| `pve storage identity` | ◑ | — |  |
| `pve storage import-metadata` | — | ✓ |  |
| `pve storage list` | ✓ | — |  |
| `pve storage node-list` | ◑ | — |  |
| `pve storage oci-pull` | — | — | deferred — pulls a real OCI image from a registry into a storage — needs registry egress and consumes storage; not exercised live from this tree; covered by unit tests |
| `pve storage permissions effective` | ◑ | — |  |
| `pve storage permissions grant` | — | — | deferred — grants ACL roles on the storage's /storage/{storage} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve storage permissions list` | ◑ | — |  |
| `pve storage permissions revoke` | — | — | deferred — revokes ACL roles on the storage's /storage/{storage} path; mutates cluster-wide ACLs, not wired into the mutate phase; covered by unit tests |
| `pve storage prune` | ◑ | ✓ |  |
| `pve storage rrd` | ◑ | — |  |
| `pve storage rrddata` | ◑ | — |  |
| `pve storage set` | — | ✓ |  |
| `pve storage status` | ◑ | — |  |
| `pve storage upload` | — | ✓ |  |
| `pve storage volume alloc` | — | ✓ |  |
| `pve storage volume copy` | — | — | deferred — copies a volume to a new target — the copy endpoint is restricted to root@pam and rejects API-token auth; not exercisable by the e2e suite — covered by unit tests |
| `pve storage volume delete` | — | ✓ |  |
| `pve storage volume get` | ◑ | ✓ |  |
| `pve storage volume set` | — | ✓ |  |
| `pve task cluster-list` | ✓ | — |  |
| `pve task list` | ✓ | — |  |
| `pve task log` | ◑ | — |  |
| `pve task status` | ◑ | — |  |
| `pve task stop` | — | ✓ |  |
| `pve task wait` | — | ✓ |  |

## `rsync`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `rsync` | — | ✓ |  |

## `ssh`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `ssh` | — | ✓ |  |

## `version`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `version` | ✓ | — |  |
| `version client` | ✓ | — |  |
| `version ping` | ◑ | — |  |

## Uncovered leaves

Leaves exercised by neither suite. These are genuine coverage gaps — candidates for read-only sweep checks (the `get`/`list`/`show` verbs) or isolated mutate-phase coverage (the `create`/`set`/`delete` verbs). Each is listed inline per tree for a compact gap view.

_None — every leaf is exercised or explicitly deferred._

## Running the suites

```bash
make test-e2e                  # all trees, read-only, against the `lab` context
make test-e2e TREES=qemu       # a subset
make test-e2e CONTEXT=prod     # a different configured context
make test-e2e PBS_CONTEXT=pbs-lab  # opt into the pbs tree (needs a `product: pbs` context)
make test-e2e PDM_CONTEXT=pdm-lab  # opt into the pdm tree (needs a `product: pdm` context)
scripts/e2e --list             # list trees and the isolation contract

make test-e2e-mutate           # read-only sweep + the destructive verb matrix
make test-lifecycle            # the destructive verb matrix only, against `lab`
scripts/e2e --mutate --vm-only # sweep + VM verb matrix (skip the container)
scripts/lifecycle --vm-only    # VM verb matrix only
scripts/lifecycle --ct-only    # container verb matrix only

make test-pbs-lifecycle        # the PBS verb matrix, against `pbs-e2e`
make test-pdm-lifecycle        # the PDM verb matrix, against `pdm-e2e`
```

Every suite skips gracefully (exit 0) when no context is configured; pass
`--strict` to fail instead. Each mutate phase prints a coverage table listing
every verb it drove and its result.

