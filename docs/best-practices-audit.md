# pmx CLI — Proxmox Operational Best-Practices Audit

Date: 2026-07-16

Scope: the pmx CLI command tree under `internal/cli/`, audited for whether its
defaults, flag semantics, and inline guidance match Proxmox VE and PBS
recommended operational practice. No Go code was modified as part of this audit.

> **Remediation status (2026-07-16):** this document records the pre-fix state.
> All seven LOW-RISK FIX items (F1–F7) were applied the same day, in the same
> change set: `--yes` gates on `lxc snapshot delete` and on `snapshot rollback`
> for both guest types, firewall-enable lockout warnings at all four levels,
> and the F4–F7 help-text corrections. The three RECOMMENDATION items (R1–R3)
> were decided (all as soft, non-blocking behaviors) and shipped the same day:
> R1 firewall-enable inbound-allow-rule scan (`e0b7879`), R2 inquorate warning
> before HA/SDN mutations (`4d9859c`), R3 `--protected` retention guidance
> (`a009782`). All 22 findings are closed; no items remain open.

Method: each command's flag defaults and `RunE` behavior were compared against
Proxmox recommended practice and classified as one of:

- GOOD — already best practice.

- LOW-RISK FIX — change a default, add a warning, or add an example, with little
  or no compatibility impact.

- RECOMMENDATION — a behavior change that needs a user decision.

A structural note that shapes every finding below: the CLI registers flag
defaults with cobra but forwards each value to the API **only when the flag was
explicitly set** (`fl.Changed(...)`). So a registered default is almost always
cosmetic (it appears in `--help`) rather than a value that is actually sent. When
a flag is omitted, the Proxmox **server-side** default applies. This is verified
in, for example, `internal/cli/node/vzdump.go:73-165` and
`internal/cli/cluster/firewall.go:1296-1319`.

## Summary Table

| # | Area | Finding | Class | Evidence |
|---|------|---------|-------|----------|
| G1 | Backup | vzdump `--mode` omitted → server default `snapshot` (least downtime) | GOOD | node/vzdump.go:201 |
| G2 | Backup | vzdump `--compress` omitted → server default (zstd) | GOOD | node/vzdump.go:202 |
| G3 | Security | SSH strict host-key checking on by default; insecure only via opt-in `--no-strict` | GOOD | internal/sshcmd/sshcmd.go:28,42-44 |
| G4 | Access | API token `--privsep` defaults true; strong one-time-secret help | GOOD | access/token.go:156,119-123 |
| G5 | Access | ACL `--propagate` defaults true (matches PVE GUI) | GOOD | access/acl.go:142 |
| G6 | Lifecycle | Clone `--full` defaults false → linked clone for templates, documented | GOOD | qemu/clone.go:100,37-40; lxc/clone.go:94 |
| G7 | Migration | qemu migrate refuses running VM without `--online`; `--migration-type` secure | GOOD | qemu/migrate.go:89-93,155-156 |
| G8 | Snapshot | qemu snapshot `--vmstate` (RAM) defaults false; live snapshot is opt-in | GOOD | qemu/snapshot.go:143 |
| G9 | Safety | Destructive deletes gated by `--yes` across most verbs | GOOD | see G9 detail |
| G10 | Backup | Storage prune requires ≥1 `--keep-*`, offers `--dry-run`, gates delete on `--yes` | GOOD | storage/prune.go:104-125 |
| G11 | Lifecycle | stop vs shutdown semantics documented; stop steers to shutdown; timeouts 300s | GOOD | qemu/lifecycle.go:146-150; lxc/lifecycle.go:85-89 |
| G12 | SDN | SDN rollback and lock-release gated by `--yes` with cluster-wide warnings | GOOD | sdn/dryrun.go:57-67; sdn/lock.go:79-81 |
| F1 | Safety | LXC snapshot delete has no `--yes` gate (qemu does) | LOW-RISK FIX | lxc/snapshot.go:127-128 |
| F2 | Safety | Snapshot rollback (qemu + lxc) discards all changes but is not `--yes`-gated | LOW-RISK FIX | qemu/snapshot.go:307-329; lxc/snapshot.go:149-171 |
| F3 | Firewall | Enabling firewall (DROP input policy) prints no lockout warning | LOW-RISK FIX | cluster/firewall.go:1296-1298; node/firewall.go:586-588 |
| F4 | Cloud-init | `--cipassword` help does not steer toward SSH keys | LOW-RISK FIX | qemu/create.go:407; qemu/config.go:511 |
| F5 | SDN | `sdn apply` help does not surface `sdn dry-run` or note it reloads all nodes | LOW-RISK FIX | sdn/apply.go:27-32 |
| F6 | Backup | No example for a `{{guestname}}` notes-template | LOW-RISK FIX | node/vzdump.go:207; cluster/backup.go:197 |
| F7 | Backup | vzdump vs cluster-backup registered defaults differ for `--zstd`/`--ionice` | LOW-RISK FIX | node/vzdump.go:210,222; cluster/backup.go:211,222 |
| R1 | Firewall | Pre-flight check or confirm for an inbound admin allow rule before enable | RECOMMENDATION | cluster/firewall.go:1287-1326 |
| R2 | Cluster | No quorum pre-check surfaced for quorum-sensitive ops | RECOMMENDATION | cluster/ha_resources.go; sdn/apply.go |
| R3 | Backup | No guidance/example for `--protected` on long-term-retention backups | RECOMMENDATION | node/vzdump.go:205; cluster/backup.go:204 |

Counts: 12 GOOD, 7 LOW-RISK FIX, 3 RECOMMENDATION.

## Backups (vzdump, cluster backup jobs, PBS/storage prune)

### GOOD

- vzdump `--mode` is registered empty and only forwarded when set
  (`node/vzdump.go:201`, and the `fl.Changed("mode")` guard at
  `node/vzdump.go:79-81`). An omitted mode therefore uses the Proxmox server
  default of `snapshot`, which is the recommended least-downtime mode. Same for
  the cluster backup job at `cluster/backup.go:189`.

- vzdump `--compress` is likewise omitted-by-default (`node/vzdump.go:202`), so
  the server default (zstd on current PVE) applies — the recommended balance of
  speed and ratio.

- Storage prune will not run without an explicit retention option — it errors if
  no `--keep-*` (or `--keep-all`) is given (`storage/prune.go:104-106`), offers a
  `--dry-run` preview (`storage/prune.go:108-121`), and refuses the real delete
  without `--yes` (`storage/prune.go:123-125`). PBS `prune simulate` is hardcoded
  to a dry run (`pbs/prune.go:229`). This matches the PBS practice of always
  reviewing a prune plan before deleting.

### LOW-RISK FIX

- F6 — Notes-template example. Both vzdump (`node/vzdump.go:207`) and the cluster
  backup job (`cluster/backup.go:197`) document `--notes-template` with
  `{{guestname}}`, `{{node}}`, and `{{vmid}}`, but neither shows an example.
  Proxmox recommends a `{{guestname}}` notes-template so backups are
  identifiable in the storage/PBS UI. Adding one example line to the command's
  `Example` is non-breaking guidance.

- F7 — Registered-default drift. `--zstd` is registered as `1` in vzdump
  (`node/vzdump.go:222`) but `0` in the cluster backup job
  (`cluster/backup.go:222`), and `--ionice` is `7` versus `0`
  (`node/vzdump.go:210` vs `cluster/backup.go:211`), with differing help text
  ("0 for all available cores" vs "0 uses half the cores"). Because both values
  are only sent when the flag is changed, this is cosmetic, but the divergent
  help text is confusing. Aligning the descriptions (and ideally the registered
  default) is a doc-only fix.

### RECOMMENDATION

- R3 — Protected-backup guidance. `--protected` exists on both backup paths
  (`node/vzdump.go:205`, `cluster/backup.go:204`) but there is no example or note
  that protected backups are excluded from prune/retention, which is the intended
  use for long-term or compliance copies. Consider an example; behavior itself is
  fine.

## Guest Lifecycle (stop/shutdown, migration, clone)

### GOOD

- stop vs shutdown is clearly separated and correctly steered. qemu `stop` is
  documented as a hard power-off that says "Prefer 'qemu shutdown' for a graceful
  stop" (`qemu/lifecycle.go:146-150`); `shutdown` is graceful ACPI with a
  `--force-stop` fallback (`qemu/lifecycle.go:224-228`). LXC mirrors this
  (`lxc/lifecycle.go:85-89`, `164-167`). Timeouts default to 300s across
  start/stop/shutdown/reboot.

- Clone defaults to a linked clone for templates: `--full` is a bool defaulting
  false and only sent when set (`qemu/clone.go:100,65-67`; `lxc/clone.go:94`),
  with help explaining "a linked clone is created when the source is a template;
  pass --full to force a full disk copy" (`qemu/clone.go:37-40`). This matches the
  PVE default and is the storage-efficient choice.

- qemu migration is safe-by-default: a running VM is refused without `--online`
  unless `--force` is also given (documented at `qemu/migrate.go:89-93`),
  `--with-local-disks` requires `--online` (`qemu/migrate.go:149-150`), and
  `--migration-type` defaults to secure (`qemu/migrate.go:155-156`). LXC migrate
  correctly exposes `--restart` for the restart-migration path
  (`lxc/migrate.go:84`) since containers cannot be live-migrated.

- Cross-cluster remote migration is gated by `--yes` and warns it is
  irreversible when `--delete` is set (`qemu/remote_migrate.go:48-53,91-92`;
  `lxc/remote_migrate.go:54-59`).

### LOW-RISK FIX

- F2 — Snapshot rollback is not confirmation-gated. Both `qemu snapshot rollback`
  (`qemu/snapshot.go:307-329`) and `lxc snapshot rollback`
  (`lxc/snapshot.go:149-171`) describe themselves as "discarding any changes made
  since" the snapshot, yet neither requires `--yes` — unlike every delete verb in
  the CLI. Rollback is arguably more destructive than delete because it silently
  throws away all state since the snapshot. Adding a `--yes` gate (matching
  `qemu snapshot delete` at `qemu/snapshot.go:171-173`) closes the gap. Caveat:
  this changes behavior for scripts that call rollback without a flag; if that is
  a concern, a stderr warning is the strictly non-breaking alternative.

## Storage and Snapshots

### GOOD

- Volume deletion is gated: `storage volume delete` refuses without `--yes`
  (`storage/volume.go:165-167`), and the help/examples show it
  (`storage/volume.go:152-157`). `storage content` is list-only (no delete path).

- qemu snapshot RAM capture is opt-in: `--vmstate` defaults false
  (`qemu/snapshot.go:143`), matching the PVE default where a memory snapshot is
  the exception, not the rule.

- Content-type guidance is present in `storage content` help, naming iso, backup,
  images, snippets, and vztmpl (`storage/content.go:35-39,89`).

### LOW-RISK FIX

- F1 — LXC snapshot delete has no `--yes` gate. `qemu snapshot delete` refuses
  without `--yes` (`qemu/snapshot.go:171-173`) and `storage volume delete` does
  too, but `lxc snapshot delete` deletes directly with only a `--force` flag and
  no confirmation (`lxc/snapshot.go:103-128`). This is almost certainly an
  oversight; adding the same `--yes` gate makes the container path consistent
  with its qemu sibling. Same compatibility caveat as F2.

## Security (SSH, firewall, tokens)

### GOOD

- SSH host-key verification is secure by default. The shared `sshcmd` package
  leaves strict host-key checking on and only emits
  `-o StrictHostKeyChecking=no` when the user passes `--no-strict`
  (`internal/sshcmd/sshcmd.go:28,42-44`). There is no `InsecureIgnoreHostKey`,
  no `UserKnownHostsFile=/dev/null`, and the non-interactive path uses
  `BatchMode=yes` so it fails rather than blindly trusts
  (`internal/sshcmd/sshcmd.go:59-64`).

- API token privilege separation is on by default: `--privsep` defaults true
  (`access/token.go:156`), and the help explains the one-time secret and that a
  privsep token needs its own ACLs (`access/token.go:119-123`). ACL `--propagate`
  defaults true (`access/acl.go:142`), matching the PVE GUI.

### LOW-RISK FIX

- F3 — Firewall enable lockout warning. Enabling the firewall while the default
  input policy is DROP can lock out SSH and the web UI, but none of the four
  enable paths warns or confirms: cluster (`cluster/firewall.go:1296-1298`, and
  `--enable` even registers a default of `1`), node
  (`node/firewall.go:586-588`), qemu (`qemu/firewall.go`), and lxc
  (`lxc/firewall.go`). A stderr warning on enable — reminding the operator that
  the default input policy is DROP and to ensure a management allow rule exists
  first — is non-breaking and directly matches Proxmox's own firewall-enable
  caution.

- F4 — Cloud-init password guidance. `--cipassword` help is just "cloud-init:
  password for the default user" (`qemu/create.go:407`, `qemu/config.go:511`),
  with no note that the password is stored in the guest config (readable by
  anyone with VM.Audit) and that SSH keys (`--sshkeys`) are preferred. Adding
  that steer to the help text is a non-breaking security improvement.

### RECOMMENDATION

- R1 — Firewall enable pre-flight. Beyond a warning (F3), consider a pre-flight
  check that an inbound allow rule for the admin/SSH source exists, or an
  interactive confirmation, before enabling a DROP-policy firewall. This is a
  behavior change and needs a decision.

## Cluster, HA, and SDN

### GOOD

- SDN rollback warns it discards every pending edit cluster-wide and is gated by
  `--yes` (`sdn/dryrun.go:57-67`); SDN lock release is gated too
  (`sdn/lock.go:79-81`). A read-only `sdn dry-run` command exists to preview
  staged changes (`sdn/dryrun.go:17-42`).

- HA resource create/set impose no client-side state defaults — `--state`,
  `--max-restart`, and `--max-relocate` are only sent when set
  (`cluster/ha_resources.go:181-201`), deferring to PVE server defaults, and
  delete is `--yes`-gated (`cluster/ha_resources.go:294-296`).

### LOW-RISK FIX

- F5 — SDN apply guidance. `sdn apply` commits pending SDN config and reloads
  every cluster node, but that fact lives only in the `Long` text
  (`sdn/apply.go:27-32`) and the help never points to `sdn dry-run` as the
  review step. Adding a "see also `sdn dry-run`" example and an explicit note that
  apply reloads all nodes is non-breaking and mirrors the recommended
  review-then-apply flow.

### RECOMMENDATION

- R2 — Quorum-sensitive operations. Neither the HA resource commands
  (`cluster/ha_resources.go`) nor `sdn apply` (`sdn/apply.go`) surface a quorum
  check. On a partitioned or inquorate cluster these operations can fail or
  behave unexpectedly. Consider surfacing quorum status (or a warning) before
  quorum-sensitive changes. This is a behavior change and needs a decision.

## G9 detail — deletes already gated by `--yes`

For reference, the following destructive verbs already refuse to proceed without
`--yes`, which is the correct baseline:

- Backup job delete — `cluster/backup.go:524-526`.

- PBS prune job delete — `pbs/prune.go:689-691`.

- Storage volume delete — `storage/volume.go:165-167`.

- Storage prune (real run) — `storage/prune.go:123-125`.

- qemu snapshot delete — `qemu/snapshot.go:171-173`.

- HA resource delete — `cluster/ha_resources.go:294-296`.

- Cross-cluster remote migration — `qemu/remote_migrate.go:48-53`,
  `lxc/remote_migrate.go:54-59`.

- SDN rollback and lock release — `sdn/dryrun.go:64-67`, `sdn/lock.go:79-81`.

- Access user/role/token/group/domain/tfa deletes — `access/access.go:20-32`.

The two gaps in this otherwise-consistent pattern are LXC snapshot delete (F1)
and snapshot rollback for both guest types (F2).
