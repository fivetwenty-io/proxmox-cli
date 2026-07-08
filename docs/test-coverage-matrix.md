# Test Coverage Matrix

> **Generated file — do not edit by hand.** Regenerate with
> `go build -o ./dist/pmx ./cmd/pmx && python3 scripts/coverage_matrix.py`.
> The classification is derived statically from the built command tree, the
> read-only sweep definitions in `scripts/e2e_lib/trees/*.py`, and the mutate
> phase in `scripts/e2e_lib/lifecycle.py`, so it stays correct as commands and
> tests change.

This document maps every invocable leaf command to its automated test coverage
across the two live suites:

- **e2e** (`scripts/e2e`, `make test-e2e`) — a read-only, parallel happy-path
  sweep against a configured context. Mutating operations are never executed;
  they are recorded as deferred. The `pbs` tree is opt-in: it runs only when
  `--pbs-context` (or `make test-e2e PBS_CONTEXT=…`) names a configured
  `product: pbs` context whose server is reachable, so all of its leaves are
  prerequisite-gated (◑).

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

- named or hostnamed with the `pmx-cli-` prefix,

- placed in the `pmx-cli` resource pool and tagged `pmx-cli`,

- attached to a dedicated `pmxcli` simple SDN zone and `pmxcli0` vnet on the
  `172.30.0.0/24` subnet, deliberately off the host management network.

Teardown runs in a `finally` block and is idempotent: a crashed prior run is
swept clean before the next provisions.

## Coverage summary

| Tree | Leaves | e2e ✓ | e2e ◑ | mutate ✓ | mutate · | deferred | n/a | uncovered |
|------|-------:|------:|------:|---------:|---------:|---------:|----:|----------:|
| `api` | 4 | 0 | 1 | 0 | 0 | 0 | 3 | 0 |
| `auth` | 7 | 3 | 1 | 3 | 0 | 0 | 0 | 0 |
| `context` | 9 | 8 | 0 | 0 | 0 | 0 | 1 | 0 |
| `init` | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 |
| `lxc` | 1 | 0 | 1 | 1 | 0 | 0 | 0 | 0 |
| `node` | 1 | 0 | 1 | 1 | 0 | 0 | 0 | 0 |
| `pbs` | 270 | 0 | 122 | 0 | 0 | 132 | 16 | 0 |
| `pve` | 667 | 0 | 0 | 201 | 4 | 0 | 0 | 462 |
| `qemu` | 2 | 1 | 0 | 2 | 0 | 0 | 0 | 0 |
| `rsync` | 1 | 0 | 0 | 1 | 0 | 0 | 0 | 0 |
| `ssh` | 1 | 0 | 0 | 1 | 0 | 0 | 0 | 0 |
| `version` | 3 | 2 | 1 | 0 | 0 | 0 | 0 | 0 |
| **Total** | **967** | **15** | **127** | **210** | **4** | **132** | **20** | **462** |

Leaf commands are counted from a walk of the built command tree (`pmx <tree> … --help`); each `create`/`delete` and `get`/`set` verb is its own leaf. Of **967** leaves, **353** are exercised by at least one live suite, **132** are deferred from the live suites (irreversible, interactive, or environment-bound — covered by unit tests), **20** are n/a by design, and **462** are not yet exercised by either suite — see [Uncovered leaves](#uncovered-leaves).

## `api`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `api delete` | — | — | n/a — raw write passthrough against the live PBS API — not automatable safely; covered by unit tests |
| `api get` | ◑ | — |  |
| `api post` | — | — | n/a — raw write passthrough against the live PBS API — not automatable safely; covered by unit tests |
| `api put` | — | — | n/a — raw write passthrough against the live PBS API — not automatable safely; covered by unit tests |

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
| `lxc migrate` | ◑ | ✓ |  |

## `node`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `node vzdump` | ◑ | ✓ |  |

## `pbs`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `pbs acl ls` | ◑ | — |  |
| `pbs acl update` | — | — | deferred — modifies the access control list; covered by unit tests |
| `pbs acme account add` | — | — | deferred — registers an account with a live certificate authority; covered by unit tests |
| `pbs acme account delete` | — | — | deferred — deactivates the account at the certificate authority; covered by unit tests |
| `pbs acme account ls` | ◑ | — |  |
| `pbs acme account show` | ◑ | — |  |
| `pbs acme account update` | — | — | deferred — updates the registration at the certificate authority; covered by unit tests |
| `pbs acme challenge-schema ls` | ◑ | — |  |
| `pbs acme directories ls` | ◑ | — |  |
| `pbs acme plugin add` | — | — | deferred — creates an ACME challenge plugin (stores API credentials); covered by unit tests |
| `pbs acme plugin delete` | — | — | deferred — removes an ACME challenge plugin; covered by unit tests |
| `pbs acme plugin ls` | ◑ | — |  |
| `pbs acme plugin show` | ◑ | — |  |
| `pbs acme plugin update` | — | — | deferred — modifies an ACME challenge plugin; covered by unit tests |
| `pbs acme tos show` | ◑ | — |  |
| `pbs datastore create` | — | — | deferred — creates a datastore (allocates a chunk store on disk); covered by unit tests |
| `pbs datastore delete` | — | — | deferred — removes a datastore definition; covered by unit tests |
| `pbs datastore ls` | ◑ | — |  |
| `pbs datastore rrd` | ◑ | — |  |
| `pbs datastore show` | ◑ | — |  |
| `pbs datastore status` | ◑ | — |  |
| `pbs datastore update` | — | — | deferred — modifies datastore configuration; covered by unit tests |
| `pbs datastore usage` | ◑ | — |  |
| `pbs encryption-key add` | — | — | deferred — creates a datastore encryption key; covered by unit tests |
| `pbs encryption-key delete` | — | — | deferred — removes a datastore encryption key; covered by unit tests |
| `pbs encryption-key ls` | ◑ | — |  |
| `pbs encryption-key toggle-archive` | — | — | n/a — flips the key's archive state on every call — not automatable idempotently; covered by unit tests |
| `pbs gc ls` | ◑ | — |  |
| `pbs gc run` | — | — | deferred — runs garbage collection, which deletes unreferenced chunks; covered by unit tests |
| `pbs gc status` | ◑ | — |  |
| `pbs group delete` | — | — | deferred — deletes an entire backup group and all its snapshots; covered by unit tests |
| `pbs group ls` | ◑ | — |  |
| `pbs group notes` | ◑ | — |  |
| `pbs metrics data` | ◑ | — |  |
| `pbs metrics influxdb-http add` | — | — | deferred — creates an influxdb-http metric server; covered by unit tests |
| `pbs metrics influxdb-http delete` | — | — | deferred — removes an influxdb-http metric server; covered by unit tests |
| `pbs metrics influxdb-http ls` | ◑ | — |  |
| `pbs metrics influxdb-http show` | ◑ | — |  |
| `pbs metrics influxdb-http update` | — | — | deferred — modifies an influxdb-http metric server; covered by unit tests |
| `pbs metrics influxdb-udp add` | — | — | deferred — creates an influxdb-udp metric server; covered by unit tests |
| `pbs metrics influxdb-udp delete` | — | — | deferred — removes an influxdb-udp metric server; covered by unit tests |
| `pbs metrics influxdb-udp ls` | ◑ | — |  |
| `pbs metrics influxdb-udp show` | ◑ | — |  |
| `pbs metrics influxdb-udp update` | — | — | deferred — modifies an influxdb-udp metric server; covered by unit tests |
| `pbs node apt changelog` | ◑ | — |  |
| `pbs node apt ls` | ◑ | — |  |
| `pbs node apt repo-add` | — | — | deferred — adds a package repository to the host; covered by unit tests |
| `pbs node apt repo-update` | — | — | deferred — enables or disables a package repository on the host; covered by unit tests |
| `pbs node apt repositories` | ◑ | — |  |
| `pbs node apt update` | — | — | deferred — refreshes the package index on the host; covered by unit tests |
| `pbs node apt versions` | ◑ | — |  |
| `pbs node certificates acme order` | — | — | deferred — orders a real certificate from the CA and replaces the server cert; covered by unit tests |
| `pbs node certificates acme renew` | — | — | deferred — renews the certificate at the CA and replaces the server cert; covered by unit tests |
| `pbs node certificates custom delete` | — | — | deferred — removes the custom TLS certificate; covered by unit tests |
| `pbs node certificates custom upload` | — | — | deferred — replaces the server's TLS certificate; covered by unit tests |
| `pbs node certificates info` | ◑ | — |  |
| `pbs node config show` | ◑ | — |  |
| `pbs node config update` | — | — | deferred — modifies host configuration; covered by unit tests |
| `pbs node disks directory create` | — | — | n/a — formats a physical disk of the real host into a directory datastore; covered by unit tests |
| `pbs node disks directory delete` | — | — | n/a — removes a directory mount backed by a physical disk of the real host; covered by unit tests |
| `pbs node disks directory ls` | ◑ | — |  |
| `pbs node disks initgpt` | — | — | n/a — writes a new GPT, destroying data on a physical disk of the real host; covered by unit tests |
| `pbs node disks ls` | ◑ | — |  |
| `pbs node disks smart` | ◑ | — |  |
| `pbs node disks wipe` | — | — | n/a — wipes a physical disk of the real host, destroying its data; covered by unit tests |
| `pbs node disks zfs create` | — | — | n/a — creates a zpool consuming physical disks of the real host; covered by unit tests |
| `pbs node disks zfs ls` | ◑ | — |  |
| `pbs node disks zfs show` | ◑ | — |  |
| `pbs node dns show` | ◑ | — |  |
| `pbs node dns update` | — | — | deferred — modifies host DNS configuration; covered by unit tests |
| `pbs node identity` | ◑ | — |  |
| `pbs node journal` | ◑ | — |  |
| `pbs node ls` | ◑ | — |  |
| `pbs node network apply` | — | — | deferred — applies staged host network changes; covered by unit tests |
| `pbs node network create` | — | — | deferred — changes host network configuration; covered by unit tests |
| `pbs node network delete` | — | — | deferred — changes host network configuration; covered by unit tests |
| `pbs node network ls` | ◑ | — |  |
| `pbs node network revert` | — | — | deferred — reverts staged host network changes; covered by unit tests |
| `pbs node network show` | ◑ | — |  |
| `pbs node network update` | — | — | deferred — changes host network configuration; covered by unit tests |
| `pbs node reboot` | — | — | n/a — reboots the real host; covered by unit tests |
| `pbs node report` | ◑ | — |  |
| `pbs node rrd` | ◑ | — |  |
| `pbs node services ls` | ◑ | — |  |
| `pbs node services reload` | — | — | deferred — reloads a PBS system service — disruptive to the server; covered by unit tests |
| `pbs node services restart` | — | — | deferred — restarts a PBS system service — disruptive to the server; covered by unit tests |
| `pbs node services show` | ◑ | — |  |
| `pbs node services start` | — | — | deferred — starts a PBS system service — disruptive to the server; covered by unit tests |
| `pbs node services state` | ◑ | — |  |
| `pbs node services stop` | — | — | deferred — stops a PBS system service — disruptive to the server; covered by unit tests |
| `pbs node shutdown` | — | — | n/a — shuts down the real host; covered by unit tests |
| `pbs node status` | ◑ | — |  |
| `pbs node subscription delete` | — | — | deferred — removes the subscription key; covered by unit tests |
| `pbs node subscription set` | — | — | deferred — registers a subscription key with the vendor; covered by unit tests |
| `pbs node subscription show` | ◑ | — |  |
| `pbs node subscription update` | — | — | deferred — re-checks the subscription with the vendor; covered by unit tests |
| `pbs node syslog` | ◑ | — |  |
| `pbs node tasks delete` | — | — | deferred — removes a task-log entry; covered by unit tests |
| `pbs node tasks log` | ◑ | — |  |
| `pbs node tasks ls` | ◑ | — |  |
| `pbs node tasks show` | ◑ | — |  |
| `pbs node time show` | ◑ | — |  |
| `pbs node time update` | — | — | deferred — modifies the host timezone; covered by unit tests |
| `pbs notification endpoint gotify add` | — | — | deferred — creates a gotify notification endpoint; covered by unit tests |
| `pbs notification endpoint gotify delete` | — | — | deferred — removes a gotify notification endpoint; covered by unit tests |
| `pbs notification endpoint gotify ls` | ◑ | — |  |
| `pbs notification endpoint gotify show` | ◑ | — |  |
| `pbs notification endpoint gotify update` | — | — | deferred — modifies a gotify notification endpoint; covered by unit tests |
| `pbs notification endpoint sendmail add` | — | — | deferred — creates a sendmail notification endpoint; covered by unit tests |
| `pbs notification endpoint sendmail delete` | — | — | deferred — removes a sendmail notification endpoint; covered by unit tests |
| `pbs notification endpoint sendmail ls` | ◑ | — |  |
| `pbs notification endpoint sendmail show` | ◑ | — |  |
| `pbs notification endpoint sendmail update` | — | — | deferred — modifies a sendmail notification endpoint; covered by unit tests |
| `pbs notification endpoint smtp add` | — | — | deferred — creates an smtp notification endpoint; covered by unit tests |
| `pbs notification endpoint smtp delete` | — | — | deferred — removes an smtp notification endpoint; covered by unit tests |
| `pbs notification endpoint smtp ls` | ◑ | — |  |
| `pbs notification endpoint smtp show` | ◑ | — |  |
| `pbs notification endpoint smtp update` | — | — | deferred — modifies an smtp notification endpoint; covered by unit tests |
| `pbs notification endpoint webhook add` | — | — | deferred — creates a webhook notification endpoint; covered by unit tests |
| `pbs notification endpoint webhook delete` | — | — | deferred — removes a webhook notification endpoint; covered by unit tests |
| `pbs notification endpoint webhook ls` | ◑ | — |  |
| `pbs notification endpoint webhook show` | ◑ | — |  |
| `pbs notification endpoint webhook update` | — | — | deferred — modifies a webhook notification endpoint; covered by unit tests |
| `pbs notification matcher add` | — | — | deferred — creates a notification matcher; covered by unit tests |
| `pbs notification matcher delete` | — | — | deferred — removes a notification matcher; covered by unit tests |
| `pbs notification matcher field-values ls` | ◑ | — |  |
| `pbs notification matcher fields ls` | ◑ | — |  |
| `pbs notification matcher ls` | ◑ | — |  |
| `pbs notification matcher show` | ◑ | — |  |
| `pbs notification matcher update` | — | — | deferred — modifies a notification matcher; covered by unit tests |
| `pbs notification target ls` | ◑ | — |  |
| `pbs notification target test` | — | — | n/a — sends a real notification through the live target — out of scope for the automated sweep; covered by unit tests |
| `pbs permission ls` | ◑ | — |  |
| `pbs prune job add` | — | — | deferred — creates a prune job; covered by unit tests |
| `pbs prune job delete` | — | — | deferred — removes a prune job; covered by unit tests |
| `pbs prune job ls` | ◑ | — |  |
| `pbs prune job run` | — | — | deferred — runs a configured prune job (deletes data); covered by unit tests |
| `pbs prune job show` | ◑ | — |  |
| `pbs prune job update` | — | — | deferred — modifies a prune job; covered by unit tests |
| `pbs prune run` | — | — | deferred — prunes snapshots by retention policy (deletes data); covered by unit tests |
| `pbs prune simulate` | ◑ | — |  |
| `pbs realm ad add` | — | — | deferred — adds an AD authentication realm; covered by unit tests |
| `pbs realm ad delete` | — | — | deferred — removes an AD realm; covered by unit tests |
| `pbs realm ad ls` | ◑ | — |  |
| `pbs realm ad show` | ◑ | — |  |
| `pbs realm ad update` | — | — | deferred — modifies an AD realm; covered by unit tests |
| `pbs realm ldap add` | — | — | deferred — adds an LDAP authentication realm; covered by unit tests |
| `pbs realm ldap delete` | — | — | deferred — removes an LDAP realm; covered by unit tests |
| `pbs realm ldap ls` | ◑ | — |  |
| `pbs realm ldap show` | ◑ | — |  |
| `pbs realm ldap update` | — | — | deferred — modifies an LDAP realm; covered by unit tests |
| `pbs realm ls` | ◑ | — |  |
| `pbs realm openid add` | — | — | deferred — adds an OpenID authentication realm; covered by unit tests |
| `pbs realm openid delete` | — | — | deferred — removes an OpenID realm; covered by unit tests |
| `pbs realm openid ls` | ◑ | — |  |
| `pbs realm openid show` | ◑ | — |  |
| `pbs realm openid update` | — | — | deferred — modifies an OpenID realm; covered by unit tests |
| `pbs realm pam show` | ◑ | — |  |
| `pbs realm pam update` | — | — | deferred — modifies the built-in PAM realm; covered by unit tests |
| `pbs realm pbs show` | ◑ | — |  |
| `pbs realm pbs update` | — | — | deferred — modifies the built-in PBS realm; covered by unit tests |
| `pbs realm sync` | — | — | deferred — runs a realm sync task that can create or update users; covered by unit tests |
| `pbs remote add` | — | — | deferred — adds a remote PBS connection (stores credentials); covered by unit tests |
| `pbs remote delete` | — | — | deferred — removes a remote PBS connection; covered by unit tests |
| `pbs remote ls` | ◑ | — |  |
| `pbs remote scan groups` | ◑ | — |  |
| `pbs remote scan ls` | ◑ | — |  |
| `pbs remote scan namespaces` | ◑ | — |  |
| `pbs remote show` | ◑ | — |  |
| `pbs remote update` | — | — | deferred — modifies a remote PBS connection; covered by unit tests |
| `pbs role ls` | ◑ | — |  |
| `pbs snapshot delete` | — | — | deferred — deletes a backup snapshot; covered by unit tests |
| `pbs snapshot files` | ◑ | — |  |
| `pbs snapshot ls` | ◑ | — |  |
| `pbs snapshot notes` | ◑ | — |  |
| `pbs snapshot protect` | — | — | deferred — sets the protected flag on a snapshot; covered by unit tests |
| `pbs snapshot show` | ◑ | — |  |
| `pbs snapshot unprotect` | — | — | deferred — clears the protected flag on a snapshot; covered by unit tests |
| `pbs status datastore-usage` | ◑ | — |  |
| `pbs sync job add` | — | — | deferred — creates a sync job; covered by unit tests |
| `pbs sync job delete` | — | — | deferred — removes a sync job; covered by unit tests |
| `pbs sync job ls` | ◑ | — |  |
| `pbs sync job run` | — | — | deferred — runs a configured sync job (transfers data); covered by unit tests |
| `pbs sync job show` | ◑ | — |  |
| `pbs sync job update` | — | — | deferred — modifies a sync job; covered by unit tests |
| `pbs sync ls` | ◑ | — |  |
| `pbs sync pull` | — | — | deferred — transfers backup data into a local datastore; covered by unit tests |
| `pbs sync push` | — | — | deferred — transfers backup data to a remote; covered by unit tests |
| `pbs tape backup` | — | — | deferred — runs a tape backup, writing datastore contents to tape; covered by unit tests |
| `pbs tape changer add` | — | — | deferred — adds a tape changer definition; covered by unit tests |
| `pbs tape changer delete` | — | — | deferred — removes a tape changer definition; covered by unit tests |
| `pbs tape changer ls` | ◑ | — |  |
| `pbs tape changer scan` | ◑ | — |  |
| `pbs tape changer show` | ◑ | — |  |
| `pbs tape changer status` | ◑ | — |  |
| `pbs tape changer transfer` | — | — | deferred — moves tape library hardware (transfers media between slots); covered by unit tests |
| `pbs tape changer update` | — | — | deferred — modifies a tape changer definition; covered by unit tests |
| `pbs tape drive add` | — | — | deferred — adds a tape drive definition; covered by unit tests |
| `pbs tape drive barcode-label` | — | — | n/a — labels every unlabelled tape in the changer, overwriting media headers — not automatable; covered by unit tests |
| `pbs tape drive cartridge-memory` | ◑ | — |  |
| `pbs tape drive catalog` | — | — | deferred — reads the whole loaded tape to rebuild its catalog (long, drive-locking); covered by unit tests |
| `pbs tape drive clean` | — | — | deferred — runs a drive cleaning cycle with a cleaning cartridge; covered by unit tests |
| `pbs tape drive delete` | — | — | deferred — removes a tape drive definition; covered by unit tests |
| `pbs tape drive eject` | — | — | deferred — ejects the loaded tape from the drive; covered by unit tests |
| `pbs tape drive export` | — | — | deferred — moves tape library hardware (exports media to the IE slot); covered by unit tests |
| `pbs tape drive format` | — | — | n/a — formats (erases) the loaded tape, destroying media contents — not automatable; covered by unit tests |
| `pbs tape drive inventory` | — | — | deferred — moves tape library hardware (loads each tape to read labels); covered by unit tests |
| `pbs tape drive label` | — | — | n/a — writes a new label to the loaded tape, destroying its contents — not automatable; covered by unit tests |
| `pbs tape drive load-media` | — | — | deferred — moves tape library hardware (loads a tape into the drive); covered by unit tests |
| `pbs tape drive load-slot` | — | — | deferred — moves tape library hardware (loads from a slot); covered by unit tests |
| `pbs tape drive ls` | ◑ | — |  |
| `pbs tape drive read-label` | ◑ | — |  |
| `pbs tape drive restore-key` | — | — | n/a — prompts for the encryption-key password interactively; covered by unit tests |
| `pbs tape drive rewind` | — | — | deferred — rewinds the loaded tape; covered by unit tests |
| `pbs tape drive scan` | ◑ | — |  |
| `pbs tape drive show` | ◑ | — |  |
| `pbs tape drive status` | ◑ | — |  |
| `pbs tape drive unload` | — | — | deferred — moves tape library hardware (unloads the drive); covered by unit tests |
| `pbs tape drive update` | — | — | deferred — modifies a tape drive definition; covered by unit tests |
| `pbs tape drive update-inventory` | — | — | deferred — moves tape library hardware (re-reads every tape label); covered by unit tests |
| `pbs tape drive volume-statistics` | ◑ | — |  |
| `pbs tape job add` | — | — | deferred — creates a tape backup job; covered by unit tests |
| `pbs tape job delete` | — | — | deferred — removes a tape backup job; covered by unit tests |
| `pbs tape job ls` | ◑ | — |  |
| `pbs tape job run` | — | — | deferred — runs a tape backup job, writing to tape; covered by unit tests |
| `pbs tape job show` | ◑ | — |  |
| `pbs tape job status` | ◑ | — |  |
| `pbs tape job update` | — | — | deferred — modifies a tape backup job; covered by unit tests |
| `pbs tape key add` | — | — | deferred — creates a tape encryption key; covered by unit tests |
| `pbs tape key delete` | — | — | deferred — removes a tape encryption key; covered by unit tests |
| `pbs tape key ls` | ◑ | — |  |
| `pbs tape key show` | ◑ | — |  |
| `pbs tape key update` | — | — | deferred — modifies a tape encryption key; covered by unit tests |
| `pbs tape media content` | ◑ | — |  |
| `pbs tape media destroy` | — | — | n/a — destroys all data on a tape medium — not automatable; covered by unit tests |
| `pbs tape media ls` | ◑ | — |  |
| `pbs tape media move` | — | — | deferred — moves tape library hardware (relocates a tape); covered by unit tests |
| `pbs tape media set-status` | — | — | deferred — changes a tape medium's status flag; covered by unit tests |
| `pbs tape media sets` | ◑ | — |  |
| `pbs tape pool add` | — | — | deferred — creates a media pool; covered by unit tests |
| `pbs tape pool delete` | — | — | deferred — removes a media pool; covered by unit tests |
| `pbs tape pool ls` | ◑ | — |  |
| `pbs tape pool show` | ◑ | — |  |
| `pbs tape pool update` | — | — | deferred — modifies a media pool; covered by unit tests |
| `pbs tape restore` | — | — | deferred — restores from tape into a datastore; covered by unit tests |
| `pbs traffic add` | — | — | deferred — creates a traffic-control rule; covered by unit tests |
| `pbs traffic current` | ◑ | — |  |
| `pbs traffic delete` | — | — | deferred — removes a traffic-control rule; covered by unit tests |
| `pbs traffic ls` | ◑ | — |  |
| `pbs traffic show` | ◑ | — |  |
| `pbs traffic update` | — | — | deferred — modifies a traffic-control rule; covered by unit tests |
| `pbs user add` | — | — | deferred — creates a user; covered by unit tests |
| `pbs user delete` | — | — | deferred — removes a user; covered by unit tests |
| `pbs user ls` | ◑ | — |  |
| `pbs user passwd` | — | — | n/a — prompts for the new password interactively; covered by unit tests |
| `pbs user show` | ◑ | — |  |
| `pbs user token add` | — | — | n/a — creates a credential and prints a once-only secret — out of scope for the automated sweep; covered by unit tests |
| `pbs user token delete` | — | — | deferred — removes an API token; covered by unit tests |
| `pbs user token ls` | ◑ | — |  |
| `pbs user token show` | ◑ | — |  |
| `pbs user token update` | — | — | deferred — modifies an API token; covered by unit tests |
| `pbs user unlock-tfa` | — | — | deferred — resets a user's second factors; covered by unit tests |
| `pbs user update` | — | — | deferred — modifies a user; covered by unit tests |
| `pbs verify job add` | — | — | deferred — creates a verify job; covered by unit tests |
| `pbs verify job delete` | — | — | deferred — removes a verify job; covered by unit tests |
| `pbs verify job ls` | ◑ | — |  |
| `pbs verify job run` | — | — | deferred — runs a configured verify job (long, IO-heavy); covered by unit tests |
| `pbs verify job show` | ◑ | — |  |
| `pbs verify job update` | — | — | deferred — modifies a verify job; covered by unit tests |
| `pbs verify run` | — | — | deferred — runs a datastore verification task (long, IO-heavy); covered by unit tests |

## `pve`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `pve access acl list` | — | — | **uncovered** |
| `pve access acl set` | — | ✓ |  |
| `pve access domain create` | — | ✓ |  |
| `pve access domain delete` | — | ✓ |  |
| `pve access domain get` | — | ✓ |  |
| `pve access domain list` | — | — | **uncovered** |
| `pve access domain set` | — | ✓ |  |
| `pve access domain sync` | — | ✓ |  |
| `pve access group create` | — | — | **uncovered** |
| `pve access group delete` | — | — | **uncovered** |
| `pve access group get` | — | — | **uncovered** |
| `pve access group list` | — | — | **uncovered** |
| `pve access group set` | — | — | **uncovered** |
| `pve access openid list` | — | — | **uncovered** |
| `pve access password set` | — | — | **uncovered** |
| `pve access permissions` | — | — | **uncovered** |
| `pve access role create` | — | ✓ |  |
| `pve access role delete` | — | ✓ |  |
| `pve access role get` | — | ✓ |  |
| `pve access role list` | — | — | **uncovered** |
| `pve access role set` | — | ✓ |  |
| `pve access tfa create` | — | ✓ |  |
| `pve access tfa delete` | — | ✓ |  |
| `pve access tfa get` | — | — | **uncovered** |
| `pve access tfa get-entry` | — | — | **uncovered** |
| `pve access tfa list` | — | — | **uncovered** |
| `pve access tfa set` | — | ✓ |  |
| `pve access tfa types` | — | — | **uncovered** |
| `pve access tfa unlock` | — | ✓ |  |
| `pve access user create` | — | ✓ |  |
| `pve access user delete` | — | — | **uncovered** |
| `pve access user get` | — | ✓ |  |
| `pve access user list` | — | — | **uncovered** |
| `pve access user set` | — | ✓ |  |
| `pve access user token create` | — | ✓ |  |
| `pve access user token delete` | — | — | **uncovered** |
| `pve access user token get` | — | ✓ |  |
| `pve access user token list` | — | ✓ |  |
| `pve access user token set` | — | ✓ |  |
| `pve cluster acme account create` | — | — | **uncovered** |
| `pve cluster acme account delete` | — | — | **uncovered** |
| `pve cluster acme account get` | — | — | **uncovered** |
| `pve cluster acme account list` | — | — | **uncovered** |
| `pve cluster acme account set` | — | — | **uncovered** |
| `pve cluster acme challenge-schema` | — | — | **uncovered** |
| `pve cluster acme directories` | — | — | **uncovered** |
| `pve cluster acme plugin create` | — | ✓ |  |
| `pve cluster acme plugin delete` | — | — | **uncovered** |
| `pve cluster acme plugin get` | — | ✓ |  |
| `pve cluster acme plugin list` | — | ✓ |  |
| `pve cluster acme plugin set` | — | ✓ |  |
| `pve cluster backup create` | — | ✓ |  |
| `pve cluster backup delete` | — | ✓ |  |
| `pve cluster backup get` | — | ✓ |  |
| `pve cluster backup included-volumes` | — | — | **uncovered** |
| `pve cluster backup list` | — | ✓ |  |
| `pve cluster backup set` | — | ✓ |  |
| `pve cluster backup-info not-backed-up` | — | — | **uncovered** |
| `pve cluster bulk migrate` | — | — | **uncovered** |
| `pve cluster bulk shutdown` | — | ✓ |  |
| `pve cluster bulk start` | — | ✓ |  |
| `pve cluster bulk suspend` | — | ✓ |  |
| `pve cluster ceph flags get` | — | — | **uncovered** |
| `pve cluster ceph flags list` | — | — | **uncovered** |
| `pve cluster ceph flags set` | — | — | **uncovered** |
| `pve cluster ceph flags set-all` | — | — | **uncovered** |
| `pve cluster ceph metadata` | — | — | **uncovered** |
| `pve cluster ceph status` | — | — | **uncovered** |
| `pve cluster config apiversion` | — | — | **uncovered** |
| `pve cluster config create` | — | — | **uncovered** |
| `pve cluster config join add` | — | — | **uncovered** |
| `pve cluster config join list` | — | — | **uncovered** |
| `pve cluster config nodes add` | — | — | **uncovered** |
| `pve cluster config nodes delete` | — | — | **uncovered** |
| `pve cluster config nodes list` | — | — | **uncovered** |
| `pve cluster config qdevice` | — | — | **uncovered** |
| `pve cluster config totem` | — | — | **uncovered** |
| `pve cluster cpu-model create` | — | ✓ |  |
| `pve cluster cpu-model delete` | — | ✓ |  |
| `pve cluster cpu-model get` | — | ✓ |  |
| `pve cluster cpu-model list` | — | ✓ |  |
| `pve cluster cpu-model set` | — | ✓ |  |
| `pve cluster firewall alias create` | — | — | **uncovered** |
| `pve cluster firewall alias delete` | — | — | **uncovered** |
| `pve cluster firewall alias get` | — | — | **uncovered** |
| `pve cluster firewall alias list` | — | — | **uncovered** |
| `pve cluster firewall alias update` | — | — | **uncovered** |
| `pve cluster firewall group create` | — | ✓ |  |
| `pve cluster firewall group delete` | — | ✓ |  |
| `pve cluster firewall group get` | — | — | **uncovered** |
| `pve cluster firewall group list` | — | ✓ |  |
| `pve cluster firewall group rule-add` | — | ✓ |  |
| `pve cluster firewall group rule-delete` | — | ✓ |  |
| `pve cluster firewall group rule-update` | — | ✓ |  |
| `pve cluster firewall group rules` | — | ✓ |  |
| `pve cluster firewall ipset add` | — | — | **uncovered** |
| `pve cluster firewall ipset create` | — | — | **uncovered** |
| `pve cluster firewall ipset delete` | — | — | **uncovered** |
| `pve cluster firewall ipset get` | — | — | **uncovered** |
| `pve cluster firewall ipset list` | — | — | **uncovered** |
| `pve cluster firewall ipset remove` | — | — | **uncovered** |
| `pve cluster firewall ipset update` | — | ✓ |  |
| `pve cluster firewall macros list` | — | — | **uncovered** |
| `pve cluster firewall options describe` | — | — | **uncovered** |
| `pve cluster firewall options get` | — | — | **uncovered** |
| `pve cluster firewall options set` | — | — | **uncovered** |
| `pve cluster firewall refs list` | — | — | **uncovered** |
| `pve cluster firewall rules create` | — | — | **uncovered** |
| `pve cluster firewall rules delete` | — | — | **uncovered** |
| `pve cluster firewall rules get` | — | — | **uncovered** |
| `pve cluster firewall rules list` | — | — | **uncovered** |
| `pve cluster firewall rules update` | — | — | **uncovered** |
| `pve cluster ha group create` | — | ✓ |  |
| `pve cluster ha group delete` | — | ✓ |  |
| `pve cluster ha group get` | — | ✓ |  |
| `pve cluster ha group list` | — | ✓ |  |
| `pve cluster ha group set` | — | ✓ |  |
| `pve cluster ha resource create` | — | ✓ |  |
| `pve cluster ha resource delete` | — | ✓ |  |
| `pve cluster ha resource get` | — | ✓ |  |
| `pve cluster ha resource list` | — | ✓ |  |
| `pve cluster ha resource migrate` | — | · |  |
| `pve cluster ha resource relocate` | — | — | **uncovered** |
| `pve cluster ha resource set` | — | ✓ |  |
| `pve cluster ha rule create` | — | ✓ |  |
| `pve cluster ha rule delete` | — | ✓ |  |
| `pve cluster ha rule get` | — | ✓ |  |
| `pve cluster ha rule list` | — | ✓ |  |
| `pve cluster ha rule set` | — | ✓ |  |
| `pve cluster ha status arm` | — | — | **uncovered** |
| `pve cluster ha status current` | — | — | **uncovered** |
| `pve cluster ha status disarm` | — | — | **uncovered** |
| `pve cluster ha status manager` | — | — | **uncovered** |
| `pve cluster jobs realm-sync create` | — | ✓ |  |
| `pve cluster jobs realm-sync delete` | — | ✓ |  |
| `pve cluster jobs realm-sync get` | — | ✓ |  |
| `pve cluster jobs realm-sync list` | — | ✓ |  |
| `pve cluster jobs realm-sync set` | — | ✓ |  |
| `pve cluster jobs schedule-analyze` | — | — | **uncovered** |
| `pve cluster log` | — | — | **uncovered** |
| `pve cluster mapping dir create` | — | ✓ |  |
| `pve cluster mapping dir delete` | — | ✓ |  |
| `pve cluster mapping dir get` | — | ✓ |  |
| `pve cluster mapping dir list` | — | ✓ |  |
| `pve cluster mapping dir set` | — | ✓ |  |
| `pve cluster mapping pci create` | — | ✓ |  |
| `pve cluster mapping pci delete` | — | ✓ |  |
| `pve cluster mapping pci get` | — | ✓ |  |
| `pve cluster mapping pci list` | — | — | **uncovered** |
| `pve cluster mapping pci set` | — | ✓ |  |
| `pve cluster mapping usb create` | — | ✓ |  |
| `pve cluster mapping usb delete` | — | ✓ |  |
| `pve cluster mapping usb get` | — | ✓ |  |
| `pve cluster mapping usb list` | — | — | **uncovered** |
| `pve cluster mapping usb set` | — | ✓ |  |
| `pve cluster metrics export` | — | — | **uncovered** |
| `pve cluster metrics server create` | — | ✓ |  |
| `pve cluster metrics server delete` | — | ✓ |  |
| `pve cluster metrics server get` | — | ✓ |  |
| `pve cluster metrics server list` | — | ✓ |  |
| `pve cluster metrics server set` | — | ✓ |  |
| `pve cluster next-id` | — | — | **uncovered** |
| `pve cluster notifications endpoints` | — | — | **uncovered** |
| `pve cluster notifications gotify create` | — | ✓ |  |
| `pve cluster notifications gotify delete` | — | ✓ |  |
| `pve cluster notifications gotify get` | — | ✓ |  |
| `pve cluster notifications gotify list` | — | ✓ |  |
| `pve cluster notifications gotify set` | — | ✓ |  |
| `pve cluster notifications matcher create` | — | ✓ |  |
| `pve cluster notifications matcher delete` | — | ✓ |  |
| `pve cluster notifications matcher get` | — | ✓ |  |
| `pve cluster notifications matcher list` | — | — | **uncovered** |
| `pve cluster notifications matcher set` | — | ✓ |  |
| `pve cluster notifications matcher-field-values` | — | — | **uncovered** |
| `pve cluster notifications matcher-fields` | — | — | **uncovered** |
| `pve cluster notifications sendmail create` | — | ✓ |  |
| `pve cluster notifications sendmail delete` | — | ✓ |  |
| `pve cluster notifications sendmail get` | — | ✓ |  |
| `pve cluster notifications sendmail list` | — | ✓ |  |
| `pve cluster notifications sendmail set` | — | ✓ |  |
| `pve cluster notifications smtp create` | — | ✓ |  |
| `pve cluster notifications smtp delete` | — | ✓ |  |
| `pve cluster notifications smtp get` | — | ✓ |  |
| `pve cluster notifications smtp list` | — | ✓ |  |
| `pve cluster notifications smtp set` | — | ✓ |  |
| `pve cluster notifications targets` | — | ✓ |  |
| `pve cluster notifications targets-test` | — | ✓ |  |
| `pve cluster notifications webhook create` | — | ✓ |  |
| `pve cluster notifications webhook delete` | — | ✓ |  |
| `pve cluster notifications webhook get` | — | ✓ |  |
| `pve cluster notifications webhook list` | — | ✓ |  |
| `pve cluster notifications webhook set` | — | ✓ |  |
| `pve cluster options describe` | — | — | **uncovered** |
| `pve cluster options get` | — | — | **uncovered** |
| `pve cluster options set` | — | — | **uncovered** |
| `pve cluster qemu cpu-flags` | — | — | **uncovered** |
| `pve cluster replication create` | — | · |  |
| `pve cluster replication delete` | — | · |  |
| `pve cluster replication get` | — | — | **uncovered** |
| `pve cluster replication list` | — | — | **uncovered** |
| `pve cluster replication set` | — | · |  |
| `pve cluster resources` | — | — | **uncovered** |
| `pve cluster status` | — | — | **uncovered** |
| `pve cluster tasks` | — | — | **uncovered** |
| `pve lxc clone` | — | — | **uncovered** |
| `pve lxc config describe` | — | — | **uncovered** |
| `pve lxc config get` | — | — | **uncovered** |
| `pve lxc config pending` | — | — | **uncovered** |
| `pve lxc config set` | — | — | **uncovered** |
| `pve lxc console` | — | — | **uncovered** |
| `pve lxc create` | — | — | **uncovered** |
| `pve lxc delete` | — | — | **uncovered** |
| `pve lxc disk move` | — | — | **uncovered** |
| `pve lxc disk resize` | — | — | **uncovered** |
| `pve lxc feature` | — | — | **uncovered** |
| `pve lxc firewall alias create` | — | — | **uncovered** |
| `pve lxc firewall alias delete` | — | — | **uncovered** |
| `pve lxc firewall alias get` | — | — | **uncovered** |
| `pve lxc firewall alias list` | — | — | **uncovered** |
| `pve lxc firewall alias update` | — | — | **uncovered** |
| `pve lxc firewall ipset add` | — | — | **uncovered** |
| `pve lxc firewall ipset create` | — | — | **uncovered** |
| `pve lxc firewall ipset delete` | — | — | **uncovered** |
| `pve lxc firewall ipset get-member` | — | — | **uncovered** |
| `pve lxc firewall ipset list` | — | — | **uncovered** |
| `pve lxc firewall ipset remove` | — | — | **uncovered** |
| `pve lxc firewall ipset update-member` | — | — | **uncovered** |
| `pve lxc firewall log` | — | — | **uncovered** |
| `pve lxc firewall options describe` | — | — | **uncovered** |
| `pve lxc firewall options get` | — | — | **uncovered** |
| `pve lxc firewall options set` | — | — | **uncovered** |
| `pve lxc firewall refs` | — | — | **uncovered** |
| `pve lxc firewall rules create` | — | — | **uncovered** |
| `pve lxc firewall rules delete` | — | — | **uncovered** |
| `pve lxc firewall rules get` | — | — | **uncovered** |
| `pve lxc firewall rules list` | — | — | **uncovered** |
| `pve lxc firewall rules update` | — | — | **uncovered** |
| `pve lxc interfaces` | — | — | **uncovered** |
| `pve lxc list` | — | — | **uncovered** |
| `pve lxc metrics` | — | — | **uncovered** |
| `pve lxc migrate check` | — | — | **uncovered** |
| `pve lxc permissions effective` | — | — | **uncovered** |
| `pve lxc permissions grant` | — | — | **uncovered** |
| `pve lxc permissions list` | — | — | **uncovered** |
| `pve lxc permissions revoke` | — | — | **uncovered** |
| `pve lxc reboot` | — | — | **uncovered** |
| `pve lxc remote-migrate` | — | — | **uncovered** |
| `pve lxc resume` | — | — | **uncovered** |
| `pve lxc rrd` | — | — | **uncovered** |
| `pve lxc security caps add` | — | — | **uncovered** |
| `pve lxc security caps describe` | — | — | **uncovered** |
| `pve lxc security caps remove` | — | — | **uncovered** |
| `pve lxc security caps reset` | — | — | **uncovered** |
| `pve lxc security caps set` | — | — | **uncovered** |
| `pve lxc security caps show` | — | — | **uncovered** |
| `pve lxc security features set` | — | — | **uncovered** |
| `pve lxc security features show` | — | — | **uncovered** |
| `pve lxc security list` | — | — | **uncovered** |
| `pve lxc security show` | — | — | **uncovered** |
| `pve lxc shutdown` | — | — | **uncovered** |
| `pve lxc snapshot create` | — | — | **uncovered** |
| `pve lxc snapshot delete` | — | — | **uncovered** |
| `pve lxc snapshot list` | — | — | **uncovered** |
| `pve lxc snapshot rollback` | — | — | **uncovered** |
| `pve lxc snapshot show` | — | — | **uncovered** |
| `pve lxc snapshot update` | — | — | **uncovered** |
| `pve lxc start` | — | — | **uncovered** |
| `pve lxc status` | — | — | **uncovered** |
| `pve lxc stop` | — | — | **uncovered** |
| `pve lxc suspend` | — | — | **uncovered** |
| `pve lxc template download` | — | ✓ |  |
| `pve lxc template list` | — | — | **uncovered** |
| `pve lxc to-template` | — | — | **uncovered** |
| `pve node apt changelog` | — | — | **uncovered** |
| `pve node apt list` | — | — | **uncovered** |
| `pve node apt repositories add` | — | — | **uncovered** |
| `pve node apt repositories enable` | — | — | **uncovered** |
| `pve node apt repositories list` | — | — | **uncovered** |
| `pve node apt templates download` | — | — | **uncovered** |
| `pve node apt templates list` | — | — | **uncovered** |
| `pve node apt update` | — | — | **uncovered** |
| `pve node apt versions` | — | — | **uncovered** |
| `pve node capabilities qemu cpu` | — | — | **uncovered** |
| `pve node capabilities qemu cpu-flags` | — | — | **uncovered** |
| `pve node capabilities qemu machines` | — | — | **uncovered** |
| `pve node capabilities qemu migration` | — | — | **uncovered** |
| `pve node ceph cfg db` | — | — | **uncovered** |
| `pve node ceph cfg index` | — | — | **uncovered** |
| `pve node ceph cfg raw` | — | — | **uncovered** |
| `pve node ceph cfg value` | — | — | **uncovered** |
| `pve node ceph cmd-safety` | — | — | **uncovered** |
| `pve node ceph crush` | — | — | **uncovered** |
| `pve node ceph fs create` | — | — | **uncovered** |
| `pve node ceph fs delete` | — | — | **uncovered** |
| `pve node ceph fs list` | — | — | **uncovered** |
| `pve node ceph init` | — | — | **uncovered** |
| `pve node ceph log` | — | — | **uncovered** |
| `pve node ceph mds create` | — | — | **uncovered** |
| `pve node ceph mds delete` | — | — | **uncovered** |
| `pve node ceph mds list` | — | — | **uncovered** |
| `pve node ceph mgr create` | — | — | **uncovered** |
| `pve node ceph mgr delete` | — | — | **uncovered** |
| `pve node ceph mgr list` | — | — | **uncovered** |
| `pve node ceph mon create` | — | — | **uncovered** |
| `pve node ceph mon delete` | — | — | **uncovered** |
| `pve node ceph mon list` | — | — | **uncovered** |
| `pve node ceph osd create` | — | — | **uncovered** |
| `pve node ceph osd delete` | — | — | **uncovered** |
| `pve node ceph osd get` | — | — | **uncovered** |
| `pve node ceph osd in` | — | — | **uncovered** |
| `pve node ceph osd list` | — | — | **uncovered** |
| `pve node ceph osd lv-info` | — | — | **uncovered** |
| `pve node ceph osd metadata` | — | — | **uncovered** |
| `pve node ceph osd out` | — | — | **uncovered** |
| `pve node ceph osd scrub` | — | — | **uncovered** |
| `pve node ceph pool create` | — | — | **uncovered** |
| `pve node ceph pool delete` | — | — | **uncovered** |
| `pve node ceph pool get` | — | — | **uncovered** |
| `pve node ceph pool list` | — | — | **uncovered** |
| `pve node ceph pool set` | — | — | **uncovered** |
| `pve node ceph pool status` | — | — | **uncovered** |
| `pve node ceph restart` | — | — | **uncovered** |
| `pve node ceph rules` | — | — | **uncovered** |
| `pve node ceph start` | — | — | **uncovered** |
| `pve node ceph status` | — | — | **uncovered** |
| `pve node ceph stop` | — | — | **uncovered** |
| `pve node cert acme delete` | — | — | **uncovered** |
| `pve node cert acme list` | — | — | **uncovered** |
| `pve node cert acme order` | — | — | **uncovered** |
| `pve node cert acme renew` | — | — | **uncovered** |
| `pve node cert custom delete` | — | — | **uncovered** |
| `pve node cert custom upload` | — | — | **uncovered** |
| `pve node cert list` | — | — | **uncovered** |
| `pve node config describe` | — | — | **uncovered** |
| `pve node config get` | — | — | **uncovered** |
| `pve node config set` | — | — | **uncovered** |
| `pve node console` | — | — | **uncovered** |
| `pve node disks create directory` | — | ✓ |  |
| `pve node disks create lvm` | — | ✓ |  |
| `pve node disks create lvmthin` | — | ✓ |  |
| `pve node disks create zfs` | — | ✓ |  |
| `pve node disks delete directory` | — | ✓ |  |
| `pve node disks delete lvm` | — | ✓ |  |
| `pve node disks delete lvmthin` | — | ✓ |  |
| `pve node disks delete zfs` | — | ✓ |  |
| `pve node disks get zfs` | — | — | **uncovered** |
| `pve node disks init-gpt` | — | ✓ |  |
| `pve node disks list` | — | — | **uncovered** |
| `pve node disks ls directory` | — | — | **uncovered** |
| `pve node disks ls lvm` | — | — | **uncovered** |
| `pve node disks ls lvmthin` | — | — | **uncovered** |
| `pve node disks ls zfs` | — | — | **uncovered** |
| `pve node disks smart` | — | — | **uncovered** |
| `pve node disks wipe` | — | — | **uncovered** |
| `pve node dns get` | — | — | **uncovered** |
| `pve node dns set` | — | — | **uncovered** |
| `pve node exec` | — | — | **uncovered** |
| `pve node execute` | — | — | **uncovered** |
| `pve node firewall log` | — | — | **uncovered** |
| `pve node firewall options describe` | — | — | **uncovered** |
| `pve node firewall options get` | — | — | **uncovered** |
| `pve node firewall options set` | — | — | **uncovered** |
| `pve node firewall rules create` | — | — | **uncovered** |
| `pve node firewall rules delete` | — | — | **uncovered** |
| `pve node firewall rules get` | — | — | **uncovered** |
| `pve node firewall rules list` | — | — | **uncovered** |
| `pve node firewall rules update` | — | — | **uncovered** |
| `pve node hardware mdev` | — | — | **uncovered** |
| `pve node hardware pci` | — | — | **uncovered** |
| `pve node hardware usb` | — | — | **uncovered** |
| `pve node hosts get` | — | ✓ |  |
| `pve node hosts set` | — | ✓ |  |
| `pve node journal` | — | — | **uncovered** |
| `pve node list` | — | — | **uncovered** |
| `pve node migrateall` | — | — | **uncovered** |
| `pve node netstat` | — | — | **uncovered** |
| `pve node network apply` | — | — | **uncovered** |
| `pve node network create` | — | — | **uncovered** |
| `pve node network delete` | — | — | **uncovered** |
| `pve node network get` | — | — | **uncovered** |
| `pve node network list` | — | — | **uncovered** |
| `pve node network revert` | — | — | **uncovered** |
| `pve node network set` | — | ✓ |  |
| `pve node oci pull` | — | ✓ |  |
| `pve node oci tags` | — | ✓ |  |
| `pve node permissions effective` | — | — | **uncovered** |
| `pve node permissions grant` | — | — | **uncovered** |
| `pve node permissions list` | — | — | **uncovered** |
| `pve node permissions revoke` | — | — | **uncovered** |
| `pve node query-url-metadata` | — | ✓ |  |
| `pve node reboot` | — | — | **uncovered** |
| `pve node replication get` | — | — | **uncovered** |
| `pve node replication list` | — | — | **uncovered** |
| `pve node replication log` | — | — | **uncovered** |
| `pve node replication run` | — | — | **uncovered** |
| `pve node replication status` | — | — | **uncovered** |
| `pve node report` | — | — | **uncovered** |
| `pve node rrddata` | — | — | **uncovered** |
| `pve node rsync` | — | — | **uncovered** |
| `pve node scan cifs` | — | ✓ |  |
| `pve node scan iscsi` | — | ✓ |  |
| `pve node scan lvm` | — | — | **uncovered** |
| `pve node scan lvmthin` | — | — | **uncovered** |
| `pve node scan nfs` | — | ✓ |  |
| `pve node scan pbs` | — | ✓ |  |
| `pve node scan zfs` | — | — | **uncovered** |
| `pve node services get` | — | — | **uncovered** |
| `pve node services list` | — | — | **uncovered** |
| `pve node services reload` | — | — | **uncovered** |
| `pve node services restart` | — | — | **uncovered** |
| `pve node services start` | — | — | **uncovered** |
| `pve node services state` | — | — | **uncovered** |
| `pve node services stop` | — | — | **uncovered** |
| `pve node shell` | — | — | **uncovered** |
| `pve node shutdown` | — | — | **uncovered** |
| `pve node spiceshell` | — | — | **uncovered** |
| `pve node ssh` | — | — | **uncovered** |
| `pve node startall` | — | ✓ |  |
| `pve node status` | — | — | **uncovered** |
| `pve node stopall` | — | ✓ |  |
| `pve node subscription delete` | — | — | **uncovered** |
| `pve node subscription get` | — | — | **uncovered** |
| `pve node subscription set` | — | — | **uncovered** |
| `pve node subscription update` | — | — | **uncovered** |
| `pve node suspendall` | — | ✓ |  |
| `pve node syslog` | — | — | **uncovered** |
| `pve node task list` | — | — | **uncovered** |
| `pve node task log` | — | — | **uncovered** |
| `pve node task status` | — | — | **uncovered** |
| `pve node task stop` | — | — | **uncovered** |
| `pve node task wait` | — | — | **uncovered** |
| `pve node termproxy` | — | — | **uncovered** |
| `pve node time get` | — | ✓ |  |
| `pve node time set` | — | ✓ |  |
| `pve node vncshell` | — | — | **uncovered** |
| `pve node vzdump defaults` | — | — | **uncovered** |
| `pve node vzdump extract-config` | — | — | **uncovered** |
| `pve node wakeonlan` | — | — | **uncovered** |
| `pve pool create` | — | — | **uncovered** |
| `pve pool delete` | — | — | **uncovered** |
| `pve pool get` | — | — | **uncovered** |
| `pve pool list` | — | — | **uncovered** |
| `pve pool permissions effective` | — | — | **uncovered** |
| `pve pool permissions grant` | — | — | **uncovered** |
| `pve pool permissions list` | — | — | **uncovered** |
| `pve pool permissions revoke` | — | — | **uncovered** |
| `pve pool set` | — | — | **uncovered** |
| `pve pool show` | — | — | **uncovered** |
| `pve qemu agent exec` | — | — | **uncovered** |
| `pve qemu agent exec-status` | — | — | **uncovered** |
| `pve qemu agent file-read` | — | — | **uncovered** |
| `pve qemu agent file-write` | — | — | **uncovered** |
| `pve qemu agent set-user-password` | — | — | **uncovered** |
| `pve qemu clone` | — | — | **uncovered** |
| `pve qemu cloudinit dump` | — | ✓ |  |
| `pve qemu cloudinit pending` | — | ✓ |  |
| `pve qemu cloudinit update` | — | ✓ |  |
| `pve qemu config describe` | — | — | **uncovered** |
| `pve qemu config get` | — | — | **uncovered** |
| `pve qemu config pending` | — | — | **uncovered** |
| `pve qemu config set` | — | — | **uncovered** |
| `pve qemu console` | — | — | **uncovered** |
| `pve qemu cpu list` | — | — | **uncovered** |
| `pve qemu cpu-flags` | — | — | **uncovered** |
| `pve qemu create` | — | — | **uncovered** |
| `pve qemu delete` | — | — | **uncovered** |
| `pve qemu disk move` | — | — | **uncovered** |
| `pve qemu disk resize` | — | — | **uncovered** |
| `pve qemu disk unlink` | — | ✓ |  |
| `pve qemu feature` | — | — | **uncovered** |
| `pve qemu firewall alias create` | — | — | **uncovered** |
| `pve qemu firewall alias delete` | — | — | **uncovered** |
| `pve qemu firewall alias get` | — | — | **uncovered** |
| `pve qemu firewall alias list` | — | — | **uncovered** |
| `pve qemu firewall alias update` | — | — | **uncovered** |
| `pve qemu firewall ipset add` | — | — | **uncovered** |
| `pve qemu firewall ipset create` | — | — | **uncovered** |
| `pve qemu firewall ipset delete` | — | — | **uncovered** |
| `pve qemu firewall ipset get-member` | — | — | **uncovered** |
| `pve qemu firewall ipset list` | — | — | **uncovered** |
| `pve qemu firewall ipset remove` | — | — | **uncovered** |
| `pve qemu firewall ipset update-member` | — | — | **uncovered** |
| `pve qemu firewall log` | — | — | **uncovered** |
| `pve qemu firewall options describe` | — | — | **uncovered** |
| `pve qemu firewall options get` | — | — | **uncovered** |
| `pve qemu firewall options set` | — | — | **uncovered** |
| `pve qemu firewall refs` | — | — | **uncovered** |
| `pve qemu firewall rules create` | — | — | **uncovered** |
| `pve qemu firewall rules delete` | — | — | **uncovered** |
| `pve qemu firewall rules get` | — | — | **uncovered** |
| `pve qemu firewall rules list` | — | — | **uncovered** |
| `pve qemu firewall rules update` | — | — | **uncovered** |
| `pve qemu list` | — | — | **uncovered** |
| `pve qemu machine list` | — | — | **uncovered** |
| `pve qemu metrics` | — | — | **uncovered** |
| `pve qemu migrate capabilities` | — | — | **uncovered** |
| `pve qemu migrate check` | — | — | **uncovered** |
| `pve qemu monitor` | — | ✓ |  |
| `pve qemu permissions effective` | — | — | **uncovered** |
| `pve qemu permissions grant` | — | — | **uncovered** |
| `pve qemu permissions list` | — | — | **uncovered** |
| `pve qemu permissions revoke` | — | — | **uncovered** |
| `pve qemu reboot` | — | — | **uncovered** |
| `pve qemu remote-migrate` | — | — | **uncovered** |
| `pve qemu reset` | — | — | **uncovered** |
| `pve qemu resume` | — | — | **uncovered** |
| `pve qemu rrd` | — | — | **uncovered** |
| `pve qemu security agent set` | — | — | **uncovered** |
| `pve qemu security agent show` | — | — | **uncovered** |
| `pve qemu security confidential clear` | — | — | **uncovered** |
| `pve qemu security confidential set` | — | — | **uncovered** |
| `pve qemu security confidential show` | — | — | **uncovered** |
| `pve qemu security cpu-flags describe` | — | — | **uncovered** |
| `pve qemu security cpu-flags set` | — | — | **uncovered** |
| `pve qemu security cpu-flags show` | — | — | **uncovered** |
| `pve qemu security list` | — | — | **uncovered** |
| `pve qemu security nic firewall` | — | — | **uncovered** |
| `pve qemu security nic show` | — | — | **uncovered** |
| `pve qemu security protection disable` | — | — | **uncovered** |
| `pve qemu security protection enable` | — | — | **uncovered** |
| `pve qemu security secureboot enable` | — | — | **uncovered** |
| `pve qemu security secureboot show` | — | — | **uncovered** |
| `pve qemu security show` | — | — | **uncovered** |
| `pve qemu security tpm add` | — | — | **uncovered** |
| `pve qemu security tpm remove` | — | — | **uncovered** |
| `pve qemu security tpm show` | — | — | **uncovered** |
| `pve qemu sendkey` | — | ✓ |  |
| `pve qemu shutdown` | — | — | **uncovered** |
| `pve qemu snapshot create` | — | — | **uncovered** |
| `pve qemu snapshot delete` | — | — | **uncovered** |
| `pve qemu snapshot list` | — | — | **uncovered** |
| `pve qemu snapshot rollback` | — | — | **uncovered** |
| `pve qemu snapshot show` | — | — | **uncovered** |
| `pve qemu snapshot update` | — | — | **uncovered** |
| `pve qemu ssh` | — | — | **uncovered** |
| `pve qemu start` | — | — | **uncovered** |
| `pve qemu status` | — | — | **uncovered** |
| `pve qemu stop` | — | — | **uncovered** |
| `pve qemu suspend` | — | — | **uncovered** |
| `pve qemu template` | — | ✓ |  |
| `pve sdn apply` | — | ✓ |  |
| `pve sdn controller create` | — | ✓ |  |
| `pve sdn controller delete` | — | ✓ |  |
| `pve sdn controller get` | — | ✓ |  |
| `pve sdn controller list` | — | — | **uncovered** |
| `pve sdn controller set` | — | ✓ |  |
| `pve sdn dns create` | — | ✓ |  |
| `pve sdn dns delete` | — | ✓ |  |
| `pve sdn dns get` | — | — | **uncovered** |
| `pve sdn dns list` | — | — | **uncovered** |
| `pve sdn dns set` | — | — | **uncovered** |
| `pve sdn dry-run` | — | — | **uncovered** |
| `pve sdn fabric create` | — | ✓ |  |
| `pve sdn fabric delete` | — | ✓ |  |
| `pve sdn fabric get` | — | ✓ |  |
| `pve sdn fabric list` | — | — | **uncovered** |
| `pve sdn fabric list-all` | — | — | **uncovered** |
| `pve sdn fabric node create` | — | ✓ |  |
| `pve sdn fabric node delete` | — | ✓ |  |
| `pve sdn fabric node get` | — | ✓ |  |
| `pve sdn fabric node list` | — | — | **uncovered** |
| `pve sdn fabric node set` | — | ✓ |  |
| `pve sdn fabric set` | — | ✓ |  |
| `pve sdn ipam create` | — | ✓ |  |
| `pve sdn ipam delete` | — | ✓ |  |
| `pve sdn ipam get` | — | ✓ |  |
| `pve sdn ipam list` | — | ✓ |  |
| `pve sdn ipam set` | — | — | **uncovered** |
| `pve sdn ipam status` | — | — | **uncovered** |
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
| `pve sdn prefix-list list` | — | — | **uncovered** |
| `pve sdn prefix-list set` | — | ✓ |  |
| `pve sdn rollback` | — | — | **uncovered** |
| `pve sdn route-map entry add` | — | ✓ |  |
| `pve sdn route-map entry delete` | — | ✓ |  |
| `pve sdn route-map entry get` | — | ✓ |  |
| `pve sdn route-map entry list` | — | — | **uncovered** |
| `pve sdn route-map entry set` | — | ✓ |  |
| `pve sdn route-map get` | — | ✓ |  |
| `pve sdn route-map list` | — | — | **uncovered** |
| `pve sdn status fabrics interfaces` | — | — | **uncovered** |
| `pve sdn status fabrics neighbors` | — | — | **uncovered** |
| `pve sdn status fabrics routes` | — | — | **uncovered** |
| `pve sdn status vnets mac-vrf` | — | ✓ |  |
| `pve sdn status zones bridges` | — | ✓ |  |
| `pve sdn status zones content` | — | ✓ |  |
| `pve sdn status zones get` | — | ✓ |  |
| `pve sdn status zones ip-vrf` | — | ✓ |  |
| `pve sdn subnet create` | — | ✓ |  |
| `pve sdn subnet delete` | — | ✓ |  |
| `pve sdn subnet list` | — | — | **uncovered** |
| `pve sdn subnet set` | — | ✓ |  |
| `pve sdn subnet show` | — | — | **uncovered** |
| `pve sdn vnet create` | — | ✓ |  |
| `pve sdn vnet delete` | — | ✓ |  |
| `pve sdn vnet firewall options describe` | — | — | **uncovered** |
| `pve sdn vnet firewall options get` | — | — | **uncovered** |
| `pve sdn vnet firewall options set` | — | — | **uncovered** |
| `pve sdn vnet firewall rules create` | — | — | **uncovered** |
| `pve sdn vnet firewall rules delete` | — | — | **uncovered** |
| `pve sdn vnet firewall rules get` | — | — | **uncovered** |
| `pve sdn vnet firewall rules list` | — | — | **uncovered** |
| `pve sdn vnet firewall rules set` | — | — | **uncovered** |
| `pve sdn vnet ips create` | — | ✓ |  |
| `pve sdn vnet ips delete` | — | ✓ |  |
| `pve sdn vnet ips set` | — | ✓ |  |
| `pve sdn vnet list` | — | — | **uncovered** |
| `pve sdn vnet permissions effective` | — | — | **uncovered** |
| `pve sdn vnet permissions grant` | — | — | **uncovered** |
| `pve sdn vnet permissions list` | — | — | **uncovered** |
| `pve sdn vnet permissions revoke` | — | — | **uncovered** |
| `pve sdn vnet set` | — | ✓ |  |
| `pve sdn vnet show` | — | — | **uncovered** |
| `pve sdn zone create` | — | ✓ |  |
| `pve sdn zone delete` | — | ✓ |  |
| `pve sdn zone list` | — | — | **uncovered** |
| `pve sdn zone permissions effective` | — | — | **uncovered** |
| `pve sdn zone permissions grant` | — | — | **uncovered** |
| `pve sdn zone permissions list` | — | — | **uncovered** |
| `pve sdn zone permissions revoke` | — | — | **uncovered** |
| `pve sdn zone set` | — | ✓ |  |
| `pve sdn zone show` | — | — | **uncovered** |
| `pve storage aplinfo download` | — | — | **uncovered** |
| `pve storage aplinfo list` | — | — | **uncovered** |
| `pve storage content` | — | — | **uncovered** |
| `pve storage create` | — | — | **uncovered** |
| `pve storage delete` | — | — | **uncovered** |
| `pve storage describe` | — | — | **uncovered** |
| `pve storage download-url` | — | ✓ |  |
| `pve storage file-restore download` | — | — | **uncovered** |
| `pve storage file-restore list` | — | — | **uncovered** |
| `pve storage get` | — | — | **uncovered** |
| `pve storage identity` | — | — | **uncovered** |
| `pve storage import-metadata` | — | ✓ |  |
| `pve storage list` | — | — | **uncovered** |
| `pve storage node-list` | — | — | **uncovered** |
| `pve storage oci-pull` | — | — | **uncovered** |
| `pve storage permissions effective` | — | — | **uncovered** |
| `pve storage permissions grant` | — | — | **uncovered** |
| `pve storage permissions list` | — | — | **uncovered** |
| `pve storage permissions revoke` | — | — | **uncovered** |
| `pve storage prune` | — | ✓ |  |
| `pve storage rrd` | — | — | **uncovered** |
| `pve storage rrddata` | — | — | **uncovered** |
| `pve storage set` | — | — | **uncovered** |
| `pve storage status` | — | — | **uncovered** |
| `pve storage upload` | — | — | **uncovered** |
| `pve storage volume alloc` | — | ✓ |  |
| `pve storage volume copy` | — | — | **uncovered** |
| `pve storage volume delete` | — | ✓ |  |
| `pve storage volume get` | — | ✓ |  |
| `pve storage volume set` | — | ✓ |  |
| `pve task cluster-list` | — | — | **uncovered** |
| `pve task list` | — | — | **uncovered** |
| `pve task log` | — | — | **uncovered** |
| `pve task status` | — | — | **uncovered** |
| `pve task stop` | — | — | **uncovered** |
| `pve task wait` | — | — | **uncovered** |

## `qemu`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `qemu agent` | — | ✓ |  |
| `qemu migrate` | ✓ | ✓ |  |

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

**`pve`** (462) — `pve access acl list`, `pve access domain list`, `pve access group create`, `pve access group delete`, `pve access group get`, `pve access group list`, `pve access group set`, `pve access openid list`, `pve access password set`, `pve access permissions`, `pve access role list`, `pve access tfa get`, `pve access tfa get-entry`, `pve access tfa list`, `pve access tfa types`, `pve access user delete`, `pve access user list`, `pve access user token delete`, `pve cluster acme account create`, `pve cluster acme account delete`, `pve cluster acme account get`, `pve cluster acme account list`, `pve cluster acme account set`, `pve cluster acme challenge-schema`, `pve cluster acme directories`, `pve cluster acme plugin delete`, `pve cluster backup included-volumes`, `pve cluster backup-info not-backed-up`, `pve cluster bulk migrate`, `pve cluster ceph flags get`, `pve cluster ceph flags list`, `pve cluster ceph flags set`, `pve cluster ceph flags set-all`, `pve cluster ceph metadata`, `pve cluster ceph status`, `pve cluster config apiversion`, `pve cluster config create`, `pve cluster config join add`, `pve cluster config join list`, `pve cluster config nodes add`, `pve cluster config nodes delete`, `pve cluster config nodes list`, `pve cluster config qdevice`, `pve cluster config totem`, `pve cluster firewall alias create`, `pve cluster firewall alias delete`, `pve cluster firewall alias get`, `pve cluster firewall alias list`, `pve cluster firewall alias update`, `pve cluster firewall group get`, `pve cluster firewall ipset add`, `pve cluster firewall ipset create`, `pve cluster firewall ipset delete`, `pve cluster firewall ipset get`, `pve cluster firewall ipset list`, `pve cluster firewall ipset remove`, `pve cluster firewall macros list`, `pve cluster firewall options describe`, `pve cluster firewall options get`, `pve cluster firewall options set`, `pve cluster firewall refs list`, `pve cluster firewall rules create`, `pve cluster firewall rules delete`, `pve cluster firewall rules get`, `pve cluster firewall rules list`, `pve cluster firewall rules update`, `pve cluster ha resource relocate`, `pve cluster ha status arm`, `pve cluster ha status current`, `pve cluster ha status disarm`, `pve cluster ha status manager`, `pve cluster jobs schedule-analyze`, `pve cluster log`, `pve cluster mapping pci list`, `pve cluster mapping usb list`, `pve cluster metrics export`, `pve cluster next-id`, `pve cluster notifications endpoints`, `pve cluster notifications matcher list`, `pve cluster notifications matcher-field-values`, `pve cluster notifications matcher-fields`, `pve cluster options describe`, `pve cluster options get`, `pve cluster options set`, `pve cluster qemu cpu-flags`, `pve cluster replication get`, `pve cluster replication list`, `pve cluster resources`, `pve cluster status`, `pve cluster tasks`, `pve lxc clone`, `pve lxc config describe`, `pve lxc config get`, `pve lxc config pending`, `pve lxc config set`, `pve lxc console`, `pve lxc create`, `pve lxc delete`, `pve lxc disk move`, `pve lxc disk resize`, `pve lxc feature`, `pve lxc firewall alias create`, `pve lxc firewall alias delete`, `pve lxc firewall alias get`, `pve lxc firewall alias list`, `pve lxc firewall alias update`, `pve lxc firewall ipset add`, `pve lxc firewall ipset create`, `pve lxc firewall ipset delete`, `pve lxc firewall ipset get-member`, `pve lxc firewall ipset list`, `pve lxc firewall ipset remove`, `pve lxc firewall ipset update-member`, `pve lxc firewall log`, `pve lxc firewall options describe`, `pve lxc firewall options get`, `pve lxc firewall options set`, `pve lxc firewall refs`, `pve lxc firewall rules create`, `pve lxc firewall rules delete`, `pve lxc firewall rules get`, `pve lxc firewall rules list`, `pve lxc firewall rules update`, `pve lxc interfaces`, `pve lxc list`, `pve lxc metrics`, `pve lxc migrate check`, `pve lxc permissions effective`, `pve lxc permissions grant`, `pve lxc permissions list`, `pve lxc permissions revoke`, `pve lxc reboot`, `pve lxc remote-migrate`, `pve lxc resume`, `pve lxc rrd`, `pve lxc security caps add`, `pve lxc security caps describe`, `pve lxc security caps remove`, `pve lxc security caps reset`, `pve lxc security caps set`, `pve lxc security caps show`, `pve lxc security features set`, `pve lxc security features show`, `pve lxc security list`, `pve lxc security show`, `pve lxc shutdown`, `pve lxc snapshot create`, `pve lxc snapshot delete`, `pve lxc snapshot list`, `pve lxc snapshot rollback`, `pve lxc snapshot show`, `pve lxc snapshot update`, `pve lxc start`, `pve lxc status`, `pve lxc stop`, `pve lxc suspend`, `pve lxc template list`, `pve lxc to-template`, `pve node apt changelog`, `pve node apt list`, `pve node apt repositories add`, `pve node apt repositories enable`, `pve node apt repositories list`, `pve node apt templates download`, `pve node apt templates list`, `pve node apt update`, `pve node apt versions`, `pve node capabilities qemu cpu`, `pve node capabilities qemu cpu-flags`, `pve node capabilities qemu machines`, `pve node capabilities qemu migration`, `pve node ceph cfg db`, `pve node ceph cfg index`, `pve node ceph cfg raw`, `pve node ceph cfg value`, `pve node ceph cmd-safety`, `pve node ceph crush`, `pve node ceph fs create`, `pve node ceph fs delete`, `pve node ceph fs list`, `pve node ceph init`, `pve node ceph log`, `pve node ceph mds create`, `pve node ceph mds delete`, `pve node ceph mds list`, `pve node ceph mgr create`, `pve node ceph mgr delete`, `pve node ceph mgr list`, `pve node ceph mon create`, `pve node ceph mon delete`, `pve node ceph mon list`, `pve node ceph osd create`, `pve node ceph osd delete`, `pve node ceph osd get`, `pve node ceph osd in`, `pve node ceph osd list`, `pve node ceph osd lv-info`, `pve node ceph osd metadata`, `pve node ceph osd out`, `pve node ceph osd scrub`, `pve node ceph pool create`, `pve node ceph pool delete`, `pve node ceph pool get`, `pve node ceph pool list`, `pve node ceph pool set`, `pve node ceph pool status`, `pve node ceph restart`, `pve node ceph rules`, `pve node ceph start`, `pve node ceph status`, `pve node ceph stop`, `pve node cert acme delete`, `pve node cert acme list`, `pve node cert acme order`, `pve node cert acme renew`, `pve node cert custom delete`, `pve node cert custom upload`, `pve node cert list`, `pve node config describe`, `pve node config get`, `pve node config set`, `pve node console`, `pve node disks get zfs`, `pve node disks list`, `pve node disks ls directory`, `pve node disks ls lvm`, `pve node disks ls lvmthin`, `pve node disks ls zfs`, `pve node disks smart`, `pve node disks wipe`, `pve node dns get`, `pve node dns set`, `pve node exec`, `pve node execute`, `pve node firewall log`, `pve node firewall options describe`, `pve node firewall options get`, `pve node firewall options set`, `pve node firewall rules create`, `pve node firewall rules delete`, `pve node firewall rules get`, `pve node firewall rules list`, `pve node firewall rules update`, `pve node hardware mdev`, `pve node hardware pci`, `pve node hardware usb`, `pve node journal`, `pve node list`, `pve node migrateall`, `pve node netstat`, `pve node network apply`, `pve node network create`, `pve node network delete`, `pve node network get`, `pve node network list`, `pve node network revert`, `pve node permissions effective`, `pve node permissions grant`, `pve node permissions list`, `pve node permissions revoke`, `pve node reboot`, `pve node replication get`, `pve node replication list`, `pve node replication log`, `pve node replication run`, `pve node replication status`, `pve node report`, `pve node rrddata`, `pve node rsync`, `pve node scan lvm`, `pve node scan lvmthin`, `pve node scan zfs`, `pve node services get`, `pve node services list`, `pve node services reload`, `pve node services restart`, `pve node services start`, `pve node services state`, `pve node services stop`, `pve node shell`, `pve node shutdown`, `pve node spiceshell`, `pve node ssh`, `pve node status`, `pve node subscription delete`, `pve node subscription get`, `pve node subscription set`, `pve node subscription update`, `pve node syslog`, `pve node task list`, `pve node task log`, `pve node task status`, `pve node task stop`, `pve node task wait`, `pve node termproxy`, `pve node vncshell`, `pve node vzdump defaults`, `pve node vzdump extract-config`, `pve node wakeonlan`, `pve pool create`, `pve pool delete`, `pve pool get`, `pve pool list`, `pve pool permissions effective`, `pve pool permissions grant`, `pve pool permissions list`, `pve pool permissions revoke`, `pve pool set`, `pve pool show`, `pve qemu agent exec`, `pve qemu agent exec-status`, `pve qemu agent file-read`, `pve qemu agent file-write`, `pve qemu agent set-user-password`, `pve qemu clone`, `pve qemu config describe`, `pve qemu config get`, `pve qemu config pending`, `pve qemu config set`, `pve qemu console`, `pve qemu cpu list`, `pve qemu cpu-flags`, `pve qemu create`, `pve qemu delete`, `pve qemu disk move`, `pve qemu disk resize`, `pve qemu feature`, `pve qemu firewall alias create`, `pve qemu firewall alias delete`, `pve qemu firewall alias get`, `pve qemu firewall alias list`, `pve qemu firewall alias update`, `pve qemu firewall ipset add`, `pve qemu firewall ipset create`, `pve qemu firewall ipset delete`, `pve qemu firewall ipset get-member`, `pve qemu firewall ipset list`, `pve qemu firewall ipset remove`, `pve qemu firewall ipset update-member`, `pve qemu firewall log`, `pve qemu firewall options describe`, `pve qemu firewall options get`, `pve qemu firewall options set`, `pve qemu firewall refs`, `pve qemu firewall rules create`, `pve qemu firewall rules delete`, `pve qemu firewall rules get`, `pve qemu firewall rules list`, `pve qemu firewall rules update`, `pve qemu list`, `pve qemu machine list`, `pve qemu metrics`, `pve qemu migrate capabilities`, `pve qemu migrate check`, `pve qemu permissions effective`, `pve qemu permissions grant`, `pve qemu permissions list`, `pve qemu permissions revoke`, `pve qemu reboot`, `pve qemu remote-migrate`, `pve qemu reset`, `pve qemu resume`, `pve qemu rrd`, `pve qemu security agent set`, `pve qemu security agent show`, `pve qemu security confidential clear`, `pve qemu security confidential set`, `pve qemu security confidential show`, `pve qemu security cpu-flags describe`, `pve qemu security cpu-flags set`, `pve qemu security cpu-flags show`, `pve qemu security list`, `pve qemu security nic firewall`, `pve qemu security nic show`, `pve qemu security protection disable`, `pve qemu security protection enable`, `pve qemu security secureboot enable`, `pve qemu security secureboot show`, `pve qemu security show`, `pve qemu security tpm add`, `pve qemu security tpm remove`, `pve qemu security tpm show`, `pve qemu shutdown`, `pve qemu snapshot create`, `pve qemu snapshot delete`, `pve qemu snapshot list`, `pve qemu snapshot rollback`, `pve qemu snapshot show`, `pve qemu snapshot update`, `pve qemu ssh`, `pve qemu start`, `pve qemu status`, `pve qemu stop`, `pve qemu suspend`, `pve sdn controller list`, `pve sdn dns get`, `pve sdn dns list`, `pve sdn dns set`, `pve sdn dry-run`, `pve sdn fabric list`, `pve sdn fabric list-all`, `pve sdn fabric node list`, `pve sdn ipam set`, `pve sdn ipam status`, `pve sdn prefix-list list`, `pve sdn rollback`, `pve sdn route-map entry list`, `pve sdn route-map list`, `pve sdn status fabrics interfaces`, `pve sdn status fabrics neighbors`, `pve sdn status fabrics routes`, `pve sdn subnet list`, `pve sdn subnet show`, `pve sdn vnet firewall options describe`, `pve sdn vnet firewall options get`, `pve sdn vnet firewall options set`, `pve sdn vnet firewall rules create`, `pve sdn vnet firewall rules delete`, `pve sdn vnet firewall rules get`, `pve sdn vnet firewall rules list`, `pve sdn vnet firewall rules set`, `pve sdn vnet list`, `pve sdn vnet permissions effective`, `pve sdn vnet permissions grant`, `pve sdn vnet permissions list`, `pve sdn vnet permissions revoke`, `pve sdn vnet show`, `pve sdn zone list`, `pve sdn zone permissions effective`, `pve sdn zone permissions grant`, `pve sdn zone permissions list`, `pve sdn zone permissions revoke`, `pve sdn zone show`, `pve storage aplinfo download`, `pve storage aplinfo list`, `pve storage content`, `pve storage create`, `pve storage delete`, `pve storage describe`, `pve storage file-restore download`, `pve storage file-restore list`, `pve storage get`, `pve storage identity`, `pve storage list`, `pve storage node-list`, `pve storage oci-pull`, `pve storage permissions effective`, `pve storage permissions grant`, `pve storage permissions list`, `pve storage permissions revoke`, `pve storage rrd`, `pve storage rrddata`, `pve storage set`, `pve storage status`, `pve storage upload`, `pve storage volume copy`, `pve task cluster-list`, `pve task list`, `pve task log`, `pve task status`, `pve task stop`, `pve task wait`

## Running the suites

```bash
make test-e2e                  # all trees, read-only, against the `lab` context
make test-e2e TREES=qemu       # a subset
make test-e2e CONTEXT=prod     # a different configured context
make test-e2e PBS_CONTEXT=pbs-lab  # opt into the pbs tree (needs a `product: pbs` context)
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

