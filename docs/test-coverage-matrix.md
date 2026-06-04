# Test Coverage Matrix

This document maps every command-tree leaf command to its automated test
coverage across the two live suites:

- **e2e** (`scripts/e2e`, `make test-e2e`) — a read-only, parallel happy-path
  sweep of all command trees against a configured target. Mutating or
  destructive operations are never executed; they are recorded as deferred.

- **lifecycle / mutate** (`scripts/lifecycle`, `make test-lifecycle`, or
  `scripts/e2e --mutate`) — the destructive counterpart. It provisions an
  isolated `pvecli` SDN and a `pve-cli` resource pool, then drives **every**
  mutating sub-command across the trees on resources created for the purpose,
  recording each verb individually, and tears everything down:

  - a throwaway QEMU VM and an LXC container through the full power-state matrix
    (`start`/`stop`/`shutdown`/`reboot`/`reset`/`suspend`/`resume`), `snapshot
    create`/`rollback`/`delete`, and `config set`/`pending`;
  - `pool set` on the provisioned pool, `task wait` on a real `--async` UPID,
    and `task stop`/`node task stop` aborting a deterministic server-side
    `qmshutdown` task spawned by `qemu shutdown --async`;
  - an isolated `pve-cli-probe` access block: user/group `create`/`delete`,
    `user token create`/`delete`, `acl set` (grant + revoke) on the `pve-cli`
    pool path, and `password set` (on the throwaway user, never root);
  - a node-restricted `pve-cli-store` `dir` storage `create`/`set`/`delete`;
  - the SSH-gated node `exec`/`ssh`/`rsync` verbs (SKIP if the host is
    unreachable).

  `scripts/e2e --mutate` runs the read-only sweep and then this mutate phase in
  one invocation. The `api` config verbs (`target add`/`remove`, `switch`) are
  exercised by the read-only sweep itself against a throwaway scratch `--config`
  file, so they never touch the real config or the configured target.

The two suites are complementary: e2e proves read paths work and never mutates;
the mutate phase proves the mutating paths work within an isolation contract
that shields other lab efforts. A few verbs are environment-bound rather than
CLI-bound and are recorded as SKIP with their reason: qemu `reboot` (a diskless
VM has no guest OS to ACPI-reboot — the `reboot` verb itself is proven live on
the Alpine container, and qemu `reset` covers the in-place restart path), lxc
`suspend`/`resume` (need working CRIU checkpoint support on the host), and
`access password set` (PVE blocks `/access/password` when the target
authenticates with an API token; PASSes on a password-auth target).

## Legend

- **e2e ✓** — exercised by the read-only sweep on every run.

- **e2e ◑** — exercised by the sweep only when prerequisite inventory exists
  (e.g. a VM, user, or vnet is present); otherwise the check is skipped (a skip
  still passes, exit 0).

- **mutate ✓** — exercised live by the mutate phase (`scripts/e2e --mutate` /
  `scripts/lifecycle`) on a purpose-built isolated guest.

- **mutate ·** — driven by the mutate phase but recorded as SKIP because the
  host/guest can't complete it (the reason is recorded); not a CLI gap.

- **—** — not applicable to that suite. A mutating verb shows `—` in the `e2e`
  column because the read-only sweep never runs mutations (it lists them in its
  deferred section); a read-only leaf shows `—` in the `mutate` column.

- **deferred** — a genuine coverage gap: nothing exercises this leaf yet. It
  appears in the column of the suite that would own it (e.g. `mutate` for a
  mutating verb), with the reason in the notes.

- **n/a** — no automated coverage by design (interactive, host-mutating, or out
  of scope — e.g. `node shell`/`console`, `node services` control, `api auth
  login`).

## Isolation contract

Every resource the lifecycle suite creates is shielded from other lab efforts:

- named or hostnamed with the `pve-cli-` prefix,

- placed in the `pve-cli` resource pool and tagged `pve-cli`,

- attached to a dedicated `pvecli` simple SDN zone and `pvecli0` vnet on the
  `10.241.0.0/24` subnet, off the host management network.

Teardown runs in a `finally` block and is idempotent: a crashed prior run is
swept clean before the next provisions.

## Coverage summary

| Tree | Leaf commands | e2e ✓ | e2e ◑ | mutate ✓ | mutate · | deferred / n/a |
|------|--------------:|------:|------:|---------:|---------:|---------------:|
| `version` | 2 | 2 | 0 | 0 | 0 | 0 |
| `cluster` | 5 | 5 | 0 | 0 | 0 | 0 |
| `node` | 17 | 1 | 6 | 4 | 0 | 6 |
| `qemu` | 18 | 1 | 3 | 13 | 1 | 0 |
| `lxc` | 18 | 2 | 3 | 11 | 2 | 0 |
| `storage` | 6 | 1 | 2 | 3 | 0 | 0 |
| `sdn` | 10 | 2 | 1 | 7 | 0 | 0 |
| `pool` | 5 | 1 | 1 | 3 | 0 | 0 |
| `task` | 4 | 1 | 1 | 2 | 0 | 0 |
| `access` | 21 | 5 | 5 | 10 | 1 | 0 |
| `api` | 11 | 8 | 0 | 3 | 0 | 0 |
| `init` | 1 | 1 | 0 | 0 | 0 | 0 |
| **Total** | **118** | **30** | **22** | **56** | **4** | **6** |

Counts are by distinct invocable leaf command, verified against a walk of the
built command tree (`pve <tree> ... --help`). Each `create`/`delete` and
`get`/`set` verb counts as its own leaf — they are not collapsed into a single
row. `mutate ·` is a verb the mutate phase drives but the host can't complete
(recorded as SKIP with its reason). For `qemu` and `lxc`, every power-state,
snapshot, and `config set`/`pending` verb is mutate-covered.

The 6 remaining `deferred`/`n/a` entries are now all `n/a` (by design) — there
are zero genuine deferred gaps:

- **n/a (by design):** `node shell`/`console` (interactive PTY) and `node services
  start`/`stop`/`restart`/`reload` (mutate shared-host daemons).

The former task-control gaps are closed: `task stop` and `node task stop` are
driven live by the mutate phase against a deterministic `qmshutdown` task
(spawned by `qemu shutdown --timeout 30 --async`, which returns its UPID
immediately while the task waits out the timeout), and `node task wait` is swept
read-only against an already-finished UPID from `node task list`, so it returns
immediately without hanging.

Note there are two distinct task trees: the top-level `task` (covered) and the
per-node `node task` sub-group; `list`/`log` are swept read-only, `wait` against
a finished UPID, and `stop` is exercised live by the mutate phase.

Beyond per-leaf reachability, the sweep also runs assertions that go past
`exit 0`: schema checks on the high-value reads (`qemu`/`lxc`/`node status`,
`storage get`, `access permissions`), set→read-back round-trips on every
mutating `set`, a four-format renderer smoke test (`table`/`plain`/`json`/`yaml`),
and a dedicated `negative` tree of error-contract checks (bad input must fail
with a non-zero exit and a message). These are test depth, not new leaves, so
they do not change the counts above.

## `version`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `version` | ✓ | — | asserts `version`/`release` present in response |
| `version client` | ✓ | — | CLI build info |

## `cluster`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `cluster status` | ✓ | — | asserts JSON array |
| `cluster resources` | ✓ | — | asserts JSON array |
| `cluster next-id` | ✓ | (✓) | read-only check; lifecycle also calls it to pick a free VMID/CTID |
| `cluster log` | ✓ | — | `--max 5` |
| `cluster tasks` | ✓ | — | recent tasks |

## `node`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `node list` | ✓ | — | asserts non-empty array |
| `node status` | ◑ | — | node as positional; skipped if no node discovered |
| `node services list` | ◑ | — | skipped if no node discovered |
| `node services get` | ◑ | — | first service from the list; skipped if none |
| `node task list` | ◑ | — | node as positional |
| `node task log` | ◑ | — | first UPID from `node task list`; skipped if none |
| `node task wait` | ◑ | — | swept against an already-finished UPID from `node task list` (returns immediately, no hang); skipped if no task exists |
| `node task stop` | — | ✓ | aborts a deterministic `qmshutdown` task spawned by `qemu shutdown --async` (positional `<node> <upid>` form) |
| `node exec` | — | ✓ | `exec <node> -- true`; SSH-gated (SKIP if host unreachable) |
| `node ssh` | — | ✓ | `ssh <node> -- true`; SSH-gated |
| `node rsync` | — | ✓ | pulls a `/tmp` scratch file back from the host; SSH-gated |
| `node shell` | — | n/a | interactive PTY; not automatable |
| `node console` | — | n/a | interactive PTY; not automatable |
| `node services start` | — | n/a | mutates real host daemons on a shared lab; out of scope |
| `node services stop` | — | n/a | mutates real host daemons on a shared lab; out of scope |
| `node services restart` | — | n/a | mutates real host daemons on a shared lab; out of scope |
| `node services reload` | — | n/a | mutates real host daemons on a shared lab; out of scope |

`node status`, `services list`, `services get`, and `task list` are all
conditional on a discovered node, hence ◑. The e2e summary counts `node status`
and `task list` as the always-attempted pair once a node exists. The `node task`
sub-group duplicates the top-level `task` verbs; `node task list`/`log` are now
swept, and `node status` additionally asserts its response carries the expected
keys (`memory`, `pveversion`). `node task wait` is swept against a finished UPID
(returns immediately), and `node task stop` is exercised live by the mutate
phase, so neither is deferred any longer.

## `qemu`

Every power-state and snapshot verb is driven live by the mutate phase on a
purpose-built diskless VM (`scsi0 local-lvm:1`, 512 MB, `ostype l26`, NIC on the
isolated `pvecli0` vnet, pooled/tagged `pve-cli`). Sequenced through a valid
state machine: create → start → suspend → resume → reset → stop → start →
snapshot create → shutdown → snapshot rollback → snapshot delete → delete.

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `qemu list` | ✓ | — | asserts JSON array |
| `qemu status` | ◑ | ✓ | e2e: first VM in list (asserts `status`+`vmid` keys); mutate: throwaway VM |
| `qemu config get` | ◑ | ✓ | e2e: first VM in list (skipped if none); mutate: reads back the cloud-init flags set at create |
| `qemu snapshot list` | ◑ | ✓ | e2e read-only; mutate lists its own snapshot |
| `qemu create` | — | ✓ | isolation: pool/tag `pve-cli`, NIC on `pvecli0`; drives `--sockets`/`--boot` plus a cloud-init flag group (`--ciuser`/`--citype`/`--ipconfig0`/`--searchdomain`/`--nameserver`) round-tripped via `config get` |
| `qemu start` | — | ✓ | power state (run twice: initial + pre-shutdown) |
| `qemu stop` | — | ✓ | hard power off from running |
| `qemu shutdown` | — | ✓ | `--timeout 10 --force-stop` → deterministic without a guest OS |
| `qemu reboot` | — | · | diskless VM has no guest OS to ACPI-reboot; covered live on lxc, restart path covered by `reset` |
| `qemu reset` | — | ✓ | hard reset, stays running |
| `qemu suspend` | — | ✓ | pause (suspend to RAM); no guest needed |
| `qemu resume` | — | ✓ | unpause |
| `qemu snapshot create` | — | ✓ | disk snapshot (no `--vmstate`) |
| `qemu snapshot rollback` | — | ✓ | run while stopped (snapshot carries no RAM state) |
| `qemu snapshot delete` | — | ✓ | requires `--yes` |
| `qemu delete` | — | ✓ | `--yes --purge --destroy-unreferenced-disks` |
| `qemu config set` | — | ✓ | sets `--description` on the running VM |
| `qemu config pending` | — | ✓ | reads the pending config diff after the set |

## `lxc`

Every power-state and snapshot verb is driven live by the mutate phase on a
purpose-built Alpine container (`vztmpl` from `local`, `rootfs local-lvm:1`, 256
MB, unprivileged, static IP `10.241.0.50/24` on the isolated net). Sequence:
create → start → suspend → resume → reboot → stop → start → snapshot create →
shutdown → snapshot rollback → snapshot delete → delete.

| Leaf | e2e | mutate | Notes |
|------|-----|--------|-------|
| `lxc list` | ✓ | — | asserts JSON array |
| `lxc template list` | ✓ | ✓ | e2e read-only; mutate uses it to find Alpine |
| `lxc status` | ◑ | ✓ | e2e: first CT (asserts `status`+`vmid` keys); mutate: throwaway CT (exercises PVEFloat PSI decode) |
| `lxc config get` | ◑ | — | first CT in list; skipped if none |
| `lxc snapshot list` | ◑ | ✓ | |
| `lxc template download` | — | ✓ | downloads Alpine to `local` if absent |
| `lxc create` | — | ✓ | isolation: unprivileged, static IP on `pvecli0`; drives `--swap` for flag breadth |
| `lxc start` | — | ✓ | power state (run twice: initial + pre-shutdown) |
| `lxc stop` | — | ✓ | immediate stop from running |
| `lxc shutdown` | — | ✓ | `--timeout 30 --force-stop`; Alpine init handles it |
| `lxc reboot` | — | ✓ | graceful; Alpine init handles it (also covers qemu's skipped reboot) |
| `lxc suspend` | — | · | needs working CRIU checkpoint support on the host; SKIP otherwise |
| `lxc resume` | — | · | only runs if suspend succeeded |
| `lxc snapshot create` | — | ✓ | |
| `lxc snapshot rollback` | — | ✓ | newly implemented (`lxc snapshot rollback`); run while stopped |
| `lxc snapshot delete` | — | ✓ | no `--yes` required (unlike qemu) |
| `lxc delete` | — | ✓ | `--yes --force --purge` |
| `lxc config set` | — | ✓ | sets `--description` on the running container |

## `storage`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `storage list` | ✓ | — | asserts JSON array |
| `storage get` | ◑ | — | first storage in list (asserts `storage`+`type` keys); skipped if none |
| `storage content` | ◑ | (✓) | e2e: first storage + discovered node; lifecycle queries `vztmpl` content |
| `storage create` | — | ✓ | isolated `pve-cli-store` `dir` storage, `--nodes` the test node only |
| `storage set` | — | ✓ | updates `--content` on the isolated storage |
| `storage delete` | — | ✓ | tears down the isolated storage (`--yes`) |

## `sdn`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `sdn zone list` | ✓ | — | asserts JSON array |
| `sdn vnet list` | ✓ | (✓) | e2e read-only; lifecycle reads it during teardown |
| `sdn subnet list` | ◑ | (✓) | e2e: first vnet; lifecycle reads subnet ids for teardown |
| `sdn zone create` | — | ✓ | provisions `pvecli` simple zone |
| `sdn zone delete` | — | ✓ | teardown |
| `sdn vnet create` | — | ✓ | provisions `pvecli0` |
| `sdn vnet delete` | — | ✓ | teardown |
| `sdn subnet create` | — | ✓ | `10.241.0.0/24` with gateway |
| `sdn subnet delete` | — | ✓ | teardown by subnet **id** (`pvecli-10.241.0.0-24`), not CIDR |
| `sdn apply` | — | ✓ | reloads network config; run on both provision and teardown |

## `pool`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `pool list` | ✓ | — | asserts JSON array |
| `pool get` | ◑ | — | first pool in list; skipped if none |
| `pool create` | — | ✓ | provisions the `pve-cli` pool |
| `pool set` | — | ✓ | sets `--comment` on the provisioned `pve-cli` pool |
| `pool delete` | — | ✓ | teardown |

## `task`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `task list` | ✓ | — | node as positional; asserts JSON array |
| `task log` | ◑ | — | first UPID from the list; skipped if none |
| `task wait` | — | ✓ | waits on a real UPID from an `--async` VM start |
| `task stop` | — | ✓ | aborts a deterministic `qmshutdown` task spawned by `qemu shutdown --async` (top-level form, node via `--node`) |

## `access`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `access user list` | ✓ | — | asserts JSON array |
| `access role list` | ✓ | — | asserts JSON array |
| `access group list` | ✓ | — | asserts JSON array |
| `access acl list` | ✓ | — | asserts JSON array |
| `access permissions` | ✓ | — | self permissions |
| `access user get` | ◑ | ✓ | sweep: first user in list; mutate: read-back of the probe user after `user set` |
| `access user token list` | ◑ | — | first user in list |
| `access role get` | ◑ | — | first role in list |
| `access group get` | ◑ | ✓ | sweep: first group in list; mutate: read-back after `group set` |
| `access user token get` | ◑ | ✓ | sweep: first token (skipped if none); mutate: read-back after `token set` |
| `access user create` | — | ✓ | isolated `pve-cli-probe@pve` |
| `access user delete` | — | ✓ | deleted in teardown |
| `access user set` | — | ✓ | sets `--comment` on the probe user; read back via `user get` |
| `access group create` | — | ✓ | isolated `pve-cli-probe` group |
| `access group delete` | — | ✓ | deleted in teardown |
| `access group set` | — | ✓ | sets `--comment` on the probe group; read back via `group get` |
| `access user token create` | — | ✓ | token `e2e`; the plaintext secret VALUE is never logged or parsed |
| `access user token delete` | — | ✓ | revoked in teardown |
| `access user token set` | — | ✓ | sets `--comment` on the probe token; read back via `token get` |
| `access acl set` | — | ✓ | grants then revokes `PVEAuditor` on `/pool/pve-cli` |
| `access password set` | — | · | driven on the probe user, but recorded SKIP when the target uses API-token auth (PVE blocks `/access/password` for token auth); PASSes on a password-auth target |

There is no `access role create`/`delete` verb — role management is read-only in
the CLI, so it is not a leaf and not a gap. Every `access` leaf is now covered:
the read-only `get` verbs in the sweep, and the create/delete/`set`/token/acl
verbs live in the isolated `pve-cli-probe` lifecycle block. Each `set` reads its
value back to prove the mutation took. `permissions` additionally asserts the
response is a `/`-rooted permission tree, not merely valid JSON.

## `api`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `api targets` | ✓ | — | asserts the configured target is listed |
| `api target <name> show` | ✓ | — | |
| `api auth status` | ✓ | — | active identity + expiry |
| `api target add` | ✓ | — | run against a scratch `--config` file; real config untouched |
| `api switch` | ✓ | — | switches between two scratch-config targets |
| `api target remove` | ✓ | — | removes the scratch target; asserts it is gone |
| `api auth login` | — | ✓ | live: throwaway pve-realm user + scratch `--config`; session asserted present via `auth status` |
| `api auth logout` | — | ✓ | live: invalidates the ticket server-side; session asserted cleared |
| `api auth refresh` | — | ✓ | live: re-obtains the ticket for the password target |
| `api auth set-token` | ✓ | — | scratch `--config`; reads back via `auth status` and asserts auth-type=token + token-id |
| `api auth set-password` | ✓ | — | scratch `--config`; reads back via `auth status` and asserts auth-type=password + username |

`api auth set-token` and `api auth set-password` mutate *local config only*. Like
`target add`/`remove`/`switch`, they are exercised against a throwaway scratch
`--config` file without touching the real config, and each is read back through
`auth status` to confirm the write round-trips. `login`/`logout`/`refresh` mutate
a stored *session*, so they stay deferred in the read-only sweep but are driven
live by the mutate phase: it creates a throwaway `pve-cli-authprobe@pve` user
(NEVER root) with an initial password, points a scratch `--config` target at the
same host, then `login` → `refresh` → `logout`, asserting via `auth status` that
the session appears and is then cleared. The real config, the configured target,
and its session are never touched, so the suite returns to the original identity
automatically.

## `init`

| Leaf | e2e | mutate | Notes |
|------|-----|-----------|-------|
| `init config` | ✓ | — | run against a throwaway `--config` path; asserts the file is written, so the real `~/.config/pve/config.yml` is never touched |

## Gaps and deferred work

- **QEMU/LXC power-state + snapshot breadth — closed.** The mutate phase now
  drives the full power-state matrix (`start`/`stop`/`shutdown`/`reboot`/
  `reset`/`suspend`/`resume`) and `snapshot create`/`rollback`/`delete` on both
  a VM and a container. The only verbs not given a live PASS are qemu `reboot`
  (no guest OS on the diskless VM — proven on the lxc container instead) and lxc
  `suspend`/`resume` (depend on host CRIU support); both are recorded as SKIP
  with their reason rather than silently dropped.

- **VM/CT config mutation — closed.** `qemu config set`/`pending` and `lxc
  config set` are exercised live on the throwaway guests.

- **Storage / access / api mutations — closed.** `storage create/set/delete` run
  against an isolated node-restricted `pve-cli-store`; the full `access`
  create/delete/`set`/token/acl/password matrix runs against an isolated
  `pve-cli-probe` user/group/token with an ACL scoped to the `pve-cli` pool path,
  each `set` read back to prove it took; the `api target add/remove/switch` and
  `auth set-token/set-password` config verbs run against a throwaway scratch
  `--config` file in the read-only sweep, read back through `auth status`. The
  session verbs `api auth login`/`refresh`/`logout` are driven live by the mutate
  phase against a throwaway `pve-cli-authprobe@pve` user and a scratch
  `--config`, so the real config and session are never touched.

- **Node host operations.** `exec`, `ssh`, and `rsync` are exercised live but
  SSH-gated: the mutate phase probes reachability and records SKIP if the host
  is unreachable, so they never hard-fail. `shell`, `console`, and service
  control remain out of scope (interactive PTY or real host-daemon mutation).

- **Task control — closed.** `qemu shutdown --timeout 30 --async` spawns a
  server-side `qmshutdown` task that waits the full timeout for an ACPI
  power-off the diskless VM can never deliver, and returns its UPID
  immediately — a safe (aborting it leaves the VM running), deterministic,
  isolated task. The mutate phase aborts it through both `task stop` (top-level,
  node via `--node`) and `node task stop` (positional `<node> <upid>`).
  `node task wait` needs no running task at all: it parses the node from the
  UPID, so the read-only sweep waits on an already-finished UPID from `node task
  list` and returns immediately. No genuine deferred gaps remain; the only
  uncovered leaves are `n/a` by design (interactive PTYs and host-daemon
  control).

- **Stored-session auth — closed.** `api auth login`/`refresh`/`logout` are
  driven live by the mutate phase: it creates a throwaway `pve-cli-authprobe@pve`
  user (NEVER root) with an initial password, points a scratch `--config` target
  at the same host, and runs `login` → `refresh` → `logout`, asserting through
  `auth status` that the session is established and then cleared. Because the
  ticket lives only in the scratch config, the real config, the configured
  target, and its session are never touched.

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
