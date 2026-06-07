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
  sweep against a configured target. Mutating operations are never executed;
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

| Tree | Leaves | e2e ✓ | e2e ◑ | mutate ✓ | mutate · | deferred / n/a | uncovered |
|------|-------:|------:|------:|---------:|---------:|---------------:|----------:|
| `access` | 39 | 9 | 8 | 25 | 0 | 0 | 3 |
| `api` | 11 | 8 | 0 | 3 | 0 | 0 | 0 |
| `cluster` | 157 | 42 | 12 | 96 | 5 | 17 | 6 |
| `init` | 1 | 1 | 0 | 0 | 0 | 0 | 0 |
| `lxc` | 48 | 2 | 13 | 38 | 0 | 1 | 0 |
| `node` | 138 | 1 | 59 | 15 | 0 | 35 | 32 |
| `pool` | 5 | 1 | 1 | 3 | 0 | 0 | 0 |
| `qemu` | 59 | 1 | 12 | 43 | 1 | 4 | 5 |
| `sdn` | 71 | 5 | 11 | 50 | 0 | 8 | 0 |
| `storage` | 21 | 1 | 8 | 9 | 0 | 6 | 0 |
| `task` | 4 | 1 | 1 | 2 | 0 | 0 | 0 |
| `version` | 2 | 2 | 0 | 0 | 0 | 0 | 0 |
| **Total** | **556** | **74** | **125** | **284** | **6** | **71** | **46** |

Leaf commands are counted from a walk of the built command tree (`pve <tree> … --help`); each `create`/`delete` and `get`/`set` verb is its own leaf. Of **556** leaves, **439** are exercised by at least one suite, **71** are deferred or n/a by design (irreversible, interactive, or environment-bound), and **46** are not yet exercised by either suite — see [Uncovered leaves](#uncovered-leaves).

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
| `access tfa create` | — | — | **uncovered** |
| `access tfa delete` | — | — | **uncovered** |
| `access tfa get` | ◑ | — |  |
| `access tfa get-entry` | ◑ | — |  |
| `access tfa list` | ✓ | — |  |
| `access tfa set` | — | — | **uncovered** |
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
| `api switch` | ✓ | — |  |
| `api target add` | ✓ | — |  |
| `api target remove` | ✓ | — |  |
| `api target show` | ✓ | — |  |
| `api targets` | ✓ | — |  |

## `cluster`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `cluster acme account create` | — | — | n/a — contacts the ACME certificate authority — never registered live on a shared lab |
| `cluster acme account delete` | — | — | **uncovered** |
| `cluster acme account get` | ◑ | — |  |
| `cluster acme account list` | ✓ | — |  |
| `cluster acme account set` | — | — | **uncovered** |
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
| `cluster bulk migrate` | — | — | help-only (parse smoke test) |
| `cluster bulk shutdown` | — | — | deferred — cluster-wide guest power and migration actions — affect every guest, not run live |
| `cluster bulk start` | — | — | help-only (parse smoke test) |
| `cluster bulk suspend` | — | — | help-only (parse smoke test) |
| `cluster ceph flags get` | ◑ | — |  |
| `cluster ceph flags list` | ◑ | — |  |
| `cluster ceph flags set` | — | — | deferred — toggles a cluster-wide Ceph OSD flag (e.g. noout/pause) — cluster-disruptive, not run live |
| `cluster ceph metadata` | ◑ | — |  |
| `cluster config apiversion` | ✓ | — |  |
| `cluster config join add` | — | — | **uncovered** |
| `cluster config join list` | ◑ | — |  |
| `cluster config nodes add` | — | — | n/a — changes cluster membership and quorum — too dangerous to exercise on a shared lab |
| `cluster config nodes delete` | — | — | **uncovered** |
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
| `cluster firewall alias list` | ✓ | ✓ |  |
| `cluster firewall alias update` | — | ✓ |  |
| `cluster firewall group create` | — | ✓ |  |
| `cluster firewall group delete` | — | ✓ |  |
| `cluster firewall group list` | ✓ | ✓ |  |
| `cluster firewall group rule-add` | — | ✓ |  |
| `cluster firewall group rule-delete` | — | ✓ |  |
| `cluster firewall group rule-update` | — | ✓ |  |
| `cluster firewall group rules` | — | ✓ |  |
| `cluster firewall ipset add` | — | ✓ |  |
| `cluster firewall ipset create` | — | ✓ |  |
| `cluster firewall ipset delete` | — | ✓ |  |
| `cluster firewall ipset list` | ✓ | ✓ |  |
| `cluster firewall ipset remove` | — | ✓ |  |
| `cluster firewall macros list` | ✓ | — |  |
| `cluster firewall options get` | ✓ | ✓ |  |
| `cluster firewall options set` | — | — | deferred — enables/changes the datacenter firewall policy cluster-wide — not exercised live |
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
| `cluster ha resource relocate` | — | — | **uncovered** |
| `cluster ha resource set` | — | ✓ |  |
| `cluster ha rule create` | — | ✓ |  |
| `cluster ha rule delete` | — | ✓ |  |
| `cluster ha rule get` | — | ✓ |  |
| `cluster ha rule list` | ✓ | ✓ |  |
| `cluster ha rule set` | — | ✓ |  |
| `cluster ha status arm` | — | — | **uncovered** |
| `cluster ha status current` | ✓ | — |  |
| `cluster ha status disarm` | — | — | deferred — toggles the cluster-wide HA stack — would disrupt every HA-managed resource on the lab |
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
| `cluster mapping pci create` | — | — | deferred — PCI mappings bind to a real device address validated against node hardware — covered by unit tests; dir mapping CRUD is covered live by `e2e --mutate` |
| `cluster mapping pci delete` | — | — | deferred — PCI mappings bind to a real device address validated against node hardware — covered by unit tests |
| `cluster mapping pci get` | — | — | deferred — PCI mappings bind to a real device address validated against node hardware — covered by unit tests |
| `cluster mapping pci list` | ✓ | — |  |
| `cluster mapping pci set` | — | — | deferred — PCI mappings bind to a real device address validated against node hardware — covered by unit tests |
| `cluster mapping usb create` | — | — | deferred — USB mappings bind to a real device address validated against node hardware — covered by unit tests; dir mapping CRUD is covered live by `e2e --mutate` |
| `cluster mapping usb delete` | — | — | deferred — USB mappings bind to a real device address validated against node hardware — covered by unit tests |
| `cluster mapping usb get` | — | — | deferred — USB mappings bind to a real device address validated against node hardware — covered by unit tests |
| `cluster mapping usb list` | ✓ | — |  |
| `cluster mapping usb set` | — | — | deferred — USB mappings bind to a real device address validated against node hardware — covered by unit tests |
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
| `cluster options get` | ✓ | ✓ |  |
| `cluster options set` | — | ✓ |  |
| `cluster replication create` | — | · |  |
| `cluster replication delete` | — | · |  |
| `cluster replication get` | — | · |  |
| `cluster replication list` | ✓ | ✓ |  |
| `cluster replication set` | — | · |  |
| `cluster resources` | ✓ | — |  |
| `cluster status` | ✓ | — |  |
| `cluster tasks` | ✓ | — |  |

## `init`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `init config` | ✓ | — |  |

## `lxc`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `lxc clone` | — | ✓ |  |
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
| `lxc firewall options get` | ◑ | ✓ |  |
| `lxc firewall options set` | — | ✓ |  |
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

## `node`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `node apt changelog` | ◑ | — |  |
| `node apt list` | ◑ | — |  |
| `node apt repositories add` | — | — | deferred — rewrites the node's APT repository configuration; not exercised live |
| `node apt repositories enable` | — | — | **uncovered** |
| `node apt repositories list` | ◑ | — |  |
| `node apt update` | — | — | deferred — refreshes the node's APT database (network I/O, apt state churn); not exercised live |
| `node apt versions` | ◑ | — |  |
| `node capabilities qemu cpu` | ◑ | — |  |
| `node capabilities qemu cpu-flags` | ◑ | — |  |
| `node capabilities qemu machines` | ◑ | — |  |
| `node capabilities qemu migration` | ◑ | — |  |
| `node ceph cfg` | ◑ | — |  |
| `node ceph fs create` | — | — | **uncovered** |
| `node ceph fs delete` | — | — | **uncovered** |
| `node ceph fs list` | ◑ | — |  |
| `node ceph init` | — | — | deferred — initializes a Ceph cluster configuration on the node — cluster-wide and destructive; not exercised live |
| `node ceph mds create` | — | — | **uncovered** |
| `node ceph mds delete` | — | — | **uncovered** |
| `node ceph mds list` | ◑ | — |  |
| `node ceph mgr create` | — | — | **uncovered** |
| `node ceph mgr delete` | — | — | **uncovered** |
| `node ceph mgr list` | ◑ | — |  |
| `node ceph mon create` | — | — | deferred — provisions or destroys Ceph monitor/MDS/MGR/filesystem daemons; not exercised live |
| `node ceph mon delete` | — | — | **uncovered** |
| `node ceph mon list` | ◑ | — |  |
| `node ceph osd create` | — | — | deferred — creates or destroys OSDs (wipes block devices) and moves cluster data; not exercised live |
| `node ceph osd delete` | — | — | **uncovered** |
| `node ceph osd get` | ◑ | — |  |
| `node ceph osd in` | — | — | **uncovered** |
| `node ceph osd list` | ◑ | — |  |
| `node ceph osd out` | — | — | **uncovered** |
| `node ceph osd scrub` | — | — | **uncovered** |
| `node ceph pool create` | — | — | deferred — creates, reconfigures, or destroys a Ceph pool (data loss on delete); not exercised live |
| `node ceph pool delete` | — | — | **uncovered** |
| `node ceph pool get` | ◑ | — |  |
| `node ceph pool list` | ◑ | — |  |
| `node ceph pool set` | — | — | **uncovered** |
| `node ceph pool status` | ◑ | — |  |
| `node ceph restart` | — | — | deferred — controls running Ceph services on the node — disruptive; not exercised live |
| `node ceph start` | — | — | **uncovered** |
| `node ceph status` | ◑ | — |  |
| `node ceph stop` | — | — | **uncovered** |
| `node cert acme delete` | — | — | **uncovered** |
| `node cert acme list` | ◑ | — |  |
| `node cert acme order` | — | — | deferred — orders, renews, or removes the node's ACME certificate (contacts Let's Encrypt); not exercised live |
| `node cert acme renew` | — | — | **uncovered** |
| `node cert custom delete` | — | — | **uncovered** |
| `node cert custom upload` | — | — | deferred — replaces or removes the node's API TLS certificate — could break TLS to the node; not exercised live |
| `node cert list` | ◑ | — |  |
| `node console` | — | — | **uncovered** |
| `node disks create directory` | — | — | **uncovered** |
| `node disks create lvm` | — | — | help-only (parse smoke test) |
| `node disks create lvmthin` | — | — | **uncovered** |
| `node disks create zfs` | — | — | **uncovered** |
| `node disks delete directory` | — | — | deferred — removes a mounted directory storage from the host — irreversible; not exercised live |
| `node disks delete lvm` | — | — | deferred — removes an LVM volume group from the host — irreversible; not exercised live |
| `node disks delete lvmthin` | — | — | deferred — removes an LVM thin pool from a VG — irreversible; not exercised live |
| `node disks delete zfs` | — | — | deferred — destroys a ZFS pool — irreversible, destroys all data on the pool; not exercised live |
| `node disks get zfs` | ◑ | — |  |
| `node disks init-gpt` | — | — | **uncovered** |
| `node disks list` | ◑ | — |  |
| `node disks ls directory` | ◑ | — |  |
| `node disks ls lvm` | ◑ | — |  |
| `node disks ls lvmthin` | ◑ | — |  |
| `node disks ls zfs` | ◑ | — |  |
| `node disks smart` | ◑ | — |  |
| `node disks wipe` | — | — | deferred — formats or wipes a physical disk — irreversible; not exercised live |
| `node dns get` | ◑ | ✓ |  |
| `node dns set` | — | ✓ |  |
| `node exec` | — | ✓ |  |
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
| `node hosts get` | ◑ | — |  |
| `node hosts set` | — | — | deferred — replaces the whole /etc/hosts file — could break host name resolution; not exercised live |
| `node journal` | ◑ | — |  |
| `node list` | ✓ | — |  |
| `node migrateall` | — | — | help-only (parse smoke test) |
| `node netstat` | ◑ | — |  |
| `node network apply` | — | — | deferred — reloads or discards the staged host network configuration — could cut the node off the network; not exercised live |
| `node network create` | — | — | deferred — edits a host network interface — could cut the node off the network; not exercised live |
| `node network delete` | — | — | **uncovered** |
| `node network get` | ◑ | — |  |
| `node network list` | ◑ | — |  |
| `node network revert` | — | — | **uncovered** |
| `node network set` | — | — | **uncovered** |
| `node oci pull` | — | — | n/a — downloads an OCI image into a storage — leaves an uncleanable artifact on shared lab storage; not exercised live |
| `node oci tags` | — | — | help-only (parse smoke test) |
| `node query-url-metadata` | — | — | deferred — fetches metadata from an external URL (needs outbound HTTP from the node); not exercised live to avoid a network-reachability dependency |
| `node replication list` | ◑ | — |  |
| `node replication log` | ◑ | — |  |
| `node replication run` | — | — | deferred — triggers an immediate replication sync to the target node (needs a configured job); not exercised live |
| `node replication status` | ◑ | — |  |
| `node report` | ◑ | — |  |
| `node rrddata` | ◑ | — |  |
| `node rsync` | — | ✓ |  |
| `node scan cifs` | — | — | deferred — probes a remote CIFS/SMB server for its shares (needs a server address and credentials); not exercised live |
| `node scan iscsi` | — | — | deferred — probes a remote iSCSI portal for its targets (needs a reachable portal address); not exercised live |
| `node scan lvm` | ◑ | — |  |
| `node scan lvmthin` | ◑ | — |  |
| `node scan nfs` | — | — | deferred — probes a remote NFS server for its exports (needs a reachable server address); not exercised live |
| `node scan pbs` | — | — | deferred — probes a Proxmox Backup Server for its datastores (needs a server address and credentials); not exercised live |
| `node scan zfs` | ◑ | — |  |
| `node services get` | ◑ | — |  |
| `node services list` | ◑ | — |  |
| `node services reload` | — | — | **uncovered** |
| `node services restart` | — | — | n/a — mutates real host daemons on a shared lab |
| `node services start` | — | — | **uncovered** |
| `node services state` | ◑ | — |  |
| `node services stop` | — | — | **uncovered** |
| `node shell` | — | — | n/a — interactive session; not automatable |
| `node ssh` | — | ✓ |  |
| `node startall` | — | — | help-only (parse smoke test) |
| `node status` | ◑ | — |  |
| `node stopall` | — | — | deferred — node-wide guest power and migration actions — affect every guest on the node, not run live |
| `node subscription delete` | — | — | **uncovered** |
| `node subscription get` | ◑ | — |  |
| `node subscription set` | — | — | n/a — changes the node's subscription/licensing state on a shared lab; not exercised live |
| `node subscription update` | — | — | **uncovered** |
| `node suspendall` | — | — | help-only (parse smoke test) |
| `node syslog` | ◑ | — |  |
| `node task list` | ◑ | — |  |
| `node task log` | ◑ | — |  |
| `node task stop` | — | ✓ |  |
| `node task wait` | ◑ | — |  |
| `node time get` | ◑ | ✓ |  |
| `node time set` | — | ✓ |  |
| `node vzdump` | — | ✓ |  |
| `node vzdump defaults` | ◑ | — |  |
| `node vzdump extract-config` | ◑ | — |  |
| `node wakeonlan` | — | — | n/a — sends a Wake-on-LAN packet to power on a node — affects real host power state, not run live |

## `pool`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `pool create` | — | ✓ | error-contract checked |
| `pool delete` | — | ✓ |  |
| `pool get` | ◑ | — |  |
| `pool list` | ✓ | — |  |
| `pool set` | — | ✓ |  |

## `qemu`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `qemu agent` | — | ✓ |  |
| `qemu agent exec` | — | — | **uncovered** |
| `qemu agent exec-status` | — | — | **uncovered** |
| `qemu agent file-read` | — | — | **uncovered** |
| `qemu agent file-write` | — | — | **uncovered** |
| `qemu agent set-user-password` | — | — | **uncovered** |
| `qemu clone` | — | ✓ |  |
| `qemu cloudinit dump` | — | ✓ |  |
| `qemu cloudinit pending` | ◑ | ✓ |  |
| `qemu cloudinit update` | — | ✓ |  |
| `qemu config get` | ◑ | ✓ |  |
| `qemu config pending` | — | ✓ |  |
| `qemu config set` | — | ✓ |  |
| `qemu console` | ◑ | ✓ |  |
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
| `qemu firewall options get` | ◑ | ✓ |  |
| `qemu firewall options set` | — | ✓ |  |
| `qemu firewall rules create` | — | ✓ |  |
| `qemu firewall rules delete` | — | ✓ |  |
| `qemu firewall rules get` | — | ✓ |  |
| `qemu firewall rules list` | ◑ | ✓ |  |
| `qemu firewall rules update` | — | ✓ |  |
| `qemu list` | ✓ | — |  |
| `qemu metrics` | ◑ | — |  |
| `qemu migrate` | — | ✓ |  |
| `qemu migrate check` | ◑ | — |  |
| `qemu monitor` | — | — | deferred — sends a raw QEMU monitor command to a running VM — even read-only commands require root and an active QEMU process; exercised live by `e2e --mutate` (soft-step: info status, which cannot change VM state) |
| `qemu reboot` | — | · |  |
| `qemu remote-migrate` | — | — | deferred — migrates a VM to a different Proxmox VE cluster — requires two live clusters with shared or compatible storage; no rollback without manual intervention; not exercised live |
| `qemu reset` | — | ✓ |  |
| `qemu resume` | — | ✓ |  |
| `qemu rrd` | ◑ | — |  |
| `qemu sendkey` | — | — | deferred — injects a key event into a running VM's console — requires a live guest process; a benign key (ret) is used, but the CI lab has no guaranteed running guest; not exercised live |
| `qemu shutdown` | — | ✓ |  |
| `qemu snapshot create` | — | ✓ | error-contract checked |
| `qemu snapshot delete` | — | ✓ |  |
| `qemu snapshot list` | ◑ | ✓ |  |
| `qemu snapshot rollback` | — | ✓ |  |
| `qemu snapshot show` | ◑ | — |  |
| `qemu snapshot update` | — | ✓ |  |
| `qemu start` | — | ✓ |  |
| `qemu status` | ◑ | ✓ |  |
| `qemu stop` | — | ✓ |  |
| `qemu suspend` | — | ✓ |  |
| `qemu template` | — | — | n/a — converts a VM into a template — irreversible, so it is never run on the shared lab (it would destroy the reusable isolated VM); covered by unit tests |

## `sdn`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `sdn apply` | — | ✓ |  |
| `sdn controller create` | — | ✓ |  |
| `sdn controller delete` | — | ✓ |  |
| `sdn controller get` | — | ✓ |  |
| `sdn controller list` | ✓ | — |  |
| `sdn controller set` | — | ✓ |  |
| `sdn dns create` | — | — | deferred — validates connectivity to an external DNS backend — covered by unit tests |
| `sdn dns delete` | — | — | deferred — needs an existing DNS provider (creatable only with a reachable external backend) — covered by unit tests |
| `sdn dns get` | — | — | deferred — needs an existing DNS provider (creatable only with a reachable external backend) — covered by unit tests |
| `sdn dns list` | ✓ | — |  |
| `sdn dns set` | — | — | deferred — needs an existing DNS provider (creatable only with a reachable external backend) — covered by unit tests |
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
| `sdn lock acquire` | — | — | deferred — acquires the global SDN config lock — requires a paired release and blocks all concurrent SDN writes; not exercised live |
| `sdn lock release` | — | — | deferred — releases the global SDN config lock — must follow acquire; not exercised live (paired with acquire, which is also deferred) |
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
| `sdn rollback` | — | — | n/a — discards ALL pending SDN changes cluster-wide — never run on shared lab |
| `sdn route-map entry add` | — | ✓ |  |
| `sdn route-map entry delete` | — | ✓ |  |
| `sdn route-map entry get` | — | ✓ |  |
| `sdn route-map entry list` | ◑ | — |  |
| `sdn route-map entry set` | — | ✓ |  |
| `sdn route-map get` | — | ✓ |  |
| `sdn route-map list` | ◑ | — |  |
| `sdn subnet create` | — | ✓ |  |
| `sdn subnet delete` | — | ✓ |  |
| `sdn subnet list` | ◑ | — |  |
| `sdn subnet set` | — | ✓ |  |
| `sdn vnet create` | — | ✓ |  |
| `sdn vnet delete` | — | ✓ |  |
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
| `sdn zone create` | — | ✓ |  |
| `sdn zone delete` | — | ✓ |  |
| `sdn zone list` | ✓ | — |  |
| `sdn zone set` | — | ✓ |  |

## `storage`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `storage content` | ◑ | — |  |
| `storage create` | — | ✓ |  |
| `storage delete` | — | ✓ |  |
| `storage download-url` | — | — | help-only (parse smoke test) |
| `storage file-restore download` | — | — | help-only (parse smoke test) |
| `storage file-restore list` | — | — | deferred — browses/extracts files from a PBS snapshot — lab has no Proxmox Backup Server storage; not exercised live |
| `storage get` | ◑ | ✓ |  |
| `storage identity` | ◑ | — |  |
| `storage import-metadata` | — | — | deferred — inspects an importable guest archive — lab has no import source; not exercised live |
| `storage list` | ✓ | — |  |
| `storage prune` | ◑ | ✓ |  |
| `storage rrd` | ◑ | — |  |
| `storage rrddata` | ◑ | — |  |
| `storage set` | — | ✓ |  |
| `storage status` | ◑ | — |  |
| `storage upload` | — | — | help-only (parse smoke test) |
| `storage volume alloc` | — | ✓ |  |
| `storage volume copy` | — | — | deferred — copies a volume to a new target — no CLI volume-delete verb yet to remove the copy; not exercised live |
| `storage volume delete` | — | ✓ |  |
| `storage volume get` | ◑ | ✓ |  |
| `storage volume set` | — | ✓ |  |

## `task`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `task list` | ✓ | — |  |
| `task log` | ◑ | — |  |
| `task stop` | — | ✓ |  |
| `task wait` | — | ✓ |  |

## `version`

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `version` | ✓ | — |  |
| `version client` | ✓ | — |  |

## Uncovered leaves

Leaves exercised by neither suite. These are genuine coverage gaps — candidates for read-only sweep checks (the `get`/`list`/`show` verbs) or isolated mutate-phase coverage (the `create`/`set`/`delete` verbs). Each is listed inline per tree for a compact gap view.

**`access`** (3) — `access tfa create`, `access tfa delete`, `access tfa set`

**`cluster`** (6) — `cluster acme account delete`, `cluster acme account set`, `cluster config join add`, `cluster config nodes delete`, `cluster ha resource relocate`, `cluster ha status arm`

**`node`** (32) — `node apt repositories enable`, `node ceph fs create`, `node ceph fs delete`, `node ceph mds create`, `node ceph mds delete`, `node ceph mgr create`, `node ceph mgr delete`, `node ceph mon delete`, `node ceph osd delete`, `node ceph osd in`, `node ceph osd out`, `node ceph osd scrub`, `node ceph pool delete`, `node ceph pool set`, `node ceph start`, `node ceph stop`, `node cert acme delete`, `node cert acme renew`, `node cert custom delete`, `node console`, `node disks create directory`, `node disks create lvmthin`, `node disks create zfs`, `node disks init-gpt`, `node network delete`, `node network revert`, `node network set`, `node services reload`, `node services start`, `node services stop`, `node subscription delete`, `node subscription update`

**`qemu`** (5) — `qemu agent exec`, `qemu agent exec-status`, `qemu agent file-read`, `qemu agent file-write`, `qemu agent set-user-password`

## Running the suites

```bash
make test-e2e                  # all trees, read-only, against the `lab` target
make test-e2e TREES=qemu       # a subset
make test-e2e TARGET=prod      # a different configured target
scripts/e2e --list             # list trees and the isolation contract

make test-e2e-mutate           # read-only sweep + the destructive verb matrix
make test-lifecycle            # the destructive verb matrix only, against `lab`
scripts/e2e --mutate --vm-only # sweep + VM verb matrix (skip the container)
scripts/lifecycle --vm-only    # VM verb matrix only
scripts/lifecycle --ct-only    # container verb matrix only
```

Both suites skip gracefully (exit 0) when the target is not configured; pass
`--strict` to fail instead. The mutate phase prints a per-guest coverage table
listing every verb it drove and its result.

