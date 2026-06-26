# API Coverage Audit

This report compares every `pve` subtree command against the Proxmox VE API it
wraps. It identifies flags, parameters, and subcommands the underlying API
supports but the CLI does not yet expose.

Three sources were compared for each command:

1. The Proxmox VE API specification (`apidoc.json`), which is the source of
   truth for every endpoint, method, and parameter.
2. The `pve-apiclient-go` Go binding, generated from that specification.
3. The `pve` CLI commands and the flags they define.

> **Remediation note (2026-06-26).** All gaps identified in this audit have since
> been remediated in the CLI. Specifically: appliance and container template
> management (`pve storage`), single-task status (`pve task`), OIDC login
> (`pve api auth login --oidc`), SDN fabric redistribute and interfaces flags
> plus per-node SDN status views, storage OCI pull and upload checksums, QEMU
> convenience and discovery flags, node config/ceph/replication reads and
> console-proxy and execute commands, cluster firewall single-item reads and
> bulk/cpu-flags previews, the LXC container-to-template command, and pool
> modernization to non-deprecated endpoints. The client pin was bumped to
> v3.2.10. The gap analysis below stands as the historical record.

## Audit date and versions

- Audited: 2026-06-26
- CLI: current `main` (`internal/cli/`)
- API client referenced by the CLI: `pve-apiclient-go` v3.2.8
- API client checkout used for the audit: v3.2.10
- Specification: `pve-apiclient-go/_data/apidoc.json`

## The short version

The CLI is in excellent shape. Across all ten subtrees, no command is missing a
core create, update, or delete verb, and the common parameters on those verbs are
already exposed. Most of what remains is read-only convenience, deprecated
parameters that Proxmox itself discourages, or internal endpoints that have no
place in an operator CLI.

Two stand out.

First, the Go client has **zero coverage gaps**. Every endpoint in the
specification has a corresponding method in `pve-apiclient-go`. None of the gaps
below require a client change — every remediation is CLI wiring against methods
that already exist. No client release is needed; the only client-related task is
the routine version bump described below.

Second, only three gaps rise to high severity, each a single contained piece of
work:

- `pve storage` cannot list or download appliance and container templates (the
  `pveam` equivalent).
- `pve task` cannot read a single task's status in one shot.
- `pve api auth` cannot complete an OpenID Connect login, even though Proxmox
  supports OIDC realms and the client exposes the handshake.

## Coverage at a glance

| Subtree | Leaf commands | Endpoint coverage | Missing params | Missing subcommands | Client gaps | High-severity gaps |
|---|---|---|---|---|---|---|
| access | 39 | 42 / 48 | 3 (all deprecated) | 4 (auth primitives) | 0 | 0 |
| api | 7 | login params 7 / 7 | 0 | 1 (OIDC login) | 0 | 1 |
| cluster | ~189 | 160 / 181 | 4 (backup, legacy) | 21 (12 are index GETs) | 0 | 0 |
| lxc | 51 | 38 / 43 | 2 (`unused[n]`) | 1 (template convert) | 0 | 0 |
| node | ~95 | ~140 | 2 (`location`, `mailnotification`) | ~24 (~11 medium) | 0 | 0 |
| pool | 5 | 7 / 7 | 0 | 0 | 0 | 0 |
| qemu | ~60 | create ~88 / 91 | minor only | ~5 (internal) | 0 | 0 |
| sdn | 72 | 36 / 38 | 2 (`redistribute`, `interfaces`) | 2 + 12 node views | 0 | 0 |
| storage | 22 | 24 / 27 | 2 (upload checksum) | 2 (aplinfo, oci pull) | 0 | 1 |
| task | 5 | 4 / 5 | 0 | 1 (status) | 0 | 1 |

"Endpoint coverage" counts endpoints reached by at least one CLI command and
excludes pure directory-index endpoints where noted. Parameter counts exclude
path and context values (such as node and VM ID) that the CLI supplies
positionally.

## High-severity gaps

These three block real operator workflows. Each maps to a client method that
already exists.

### Storage cannot manage appliance and container templates

`pve storage` exposes no command for the appliance index. Proxmox uses this index
(the `pveam` workflow) to list available LXC and appliance templates and to
download one onto a storage. Both endpoints are wrapped by the client but unwired
in the CLI:

- `GET /nodes/{node}/aplinfo` lists available templates (`nodes.ListAplinfo`).
- `POST /nodes/{node}/aplinfo` downloads a template to a storage, taking
  `storage` and `template` (`nodes.CreateAplinfo`).

Suggested shape: `pve storage aplinfo list` and `pve storage aplinfo download`.

### Task status cannot be read without blocking

`pve task` can list tasks, stream a log, wait for completion, and stop a task,
but it cannot fetch a single task's current status in one call. Today the only
way to learn a task's exit status is `pve task wait`, which blocks until the task
finishes. The status endpoint and its typed response already exist
(`GET /nodes/{node}/tasks/{upid}/status`, `nodes.ListTasksStatus`).

Suggested shape: `pve task status <upid>`.

### OpenID Connect login is not supported

`pve api auth login` covers password, API token, and TOTP/two-factor
authentication, but not OIDC. Proxmox supports OpenID Connect realms, and the
client exposes both handshake calls (`CreateOpenidAuthUrl` and
`CreateOpenidLogin`), yet no CLI command drives them. An operator on an
OIDC-only realm cannot log in.

Suggested shape: `pve api auth login --oidc`, which would request the
authorization URL, accept the pasted redirect, and complete the login.

## Medium-severity gaps

These add coverage without blocking any core workflow.

### sdn — fabric configuration is incomplete

Two parameters on the SDN fabric commands have no flag, which prevents fully
defining a non-WireGuard fabric from the CLI:

- `fabric create` and `fabric set` are missing `redistribute`, which controls
  BGP and OSPF route redistribution.
- `fabric node create` and `fabric node set` are missing `interfaces`, the
  per-node interface list that OpenFabric and OSPF nodes require.

Both struct fields already exist as `[]json.RawMessage`, so the work is a CLI
flag plus a decision on the value shape that operators will type. Separately, twelve
read-only `GET /nodes/{node}/sdn/...` status views (applied zones, vnets, fabric
neighbors, and routes) are unexposed; they are the only way to inspect applied
per-node SDN state and verify an `apply`.

### storage — OCI pull and upload checksums

- `POST /nodes/{node}/storage/{storage}/oci-registry-pull` pulls an OCI
  container image and is entirely unwired (`nodes.CreateStorageOciRegistryPull`).
- `pve storage upload` cannot send `checksum` or `checksum-algorithm`, so
  uploads cannot be integrity-checked even though `pve storage download-url`
  already supports both. The asymmetry is worth closing for consistency.

### qemu — convenience flags and discovery

Configuration coverage is otherwise near-complete (`create` and `config set`
each expose roughly ninety flags). The gaps are:

- `autostart` is missing on `create` and `config set`.
- The `--cdrom` convenience flag is missing; the workaround is
  `--ide 2=...,media=cdrom`.
- `agent set-user-password` is missing `crypted`, so a pre-hashed password
  cannot be set.
- There is no cluster-wide VM listing (`pve qemu list` is per-node only) and no
  helper to discover valid `--cpu` or `--machine` values.

### node — reads, console proxies, and one config field

`node` is the most complete subtree audited; no create or update verb is missing.
The medium items are mostly read-only or proxy endpoints:

- `config set` is missing the `location` field.
- No `task status` read, no single `replication get`.
- Ceph log, CRUSH rules, CRUSH map, and the config-database views
  (`cfg db`, `cfg raw`, `cfg value`) are unexposed; only the `cfg` index is
  wired.
- The console and proxy verbs (`termproxy`, `vncshell`, `spiceshell`) and the
  batch `execute` endpoint are unexposed.

### cluster — single-item reads

Every write verb is present and fully parameterised. The gaps are read-only:

- No get-single for firewall rules, aliases, group rules, or ipset CIDRs (only
  list).
- No `ha rules get`.
- `GET /cluster/bulk-action/guest` (preview the guests a bulk action would
  affect) and `GET /cluster/qemu/cpu-flags` (supported CPU flags) are unexposed.

### lxc — convert a container to a template

There is no dedicated command to convert an existing container into a template
(`POST /nodes/{node}/lxc/{vmid}/template`, `nodes.CreateLxcTemplate`). The
`pve lxc template` name is already taken by the appliance-download wrapper, and
the only current path is `pve lxc config set --template`.

### access — none

`access` is the best-covered namespace. Every non-deprecated parameter on every
wired command is exposed, and there are no medium-severity gaps.

## Low-severity gaps

Safe to leave open or document rather than build.

- **Deprecated parameters.** Several commands omit parameters that Proxmox itself
  has deprecated, where a supported replacement is already exposed:
  - `access domain sync` omits `full` and `purge` (replaced by
    `--remove-vanished`); `access domain create/set` omits `secure` (replaced by
    the exposed `--mode`).
  - `cluster backup create/set` omits `dow`, `starttime`, and `mailnotification`
    (replaced by `--schedule` and `--notification-mode`); it also omits `quiet`.
  - `node vzdump` omits `mailnotification` (replaced by `--notification-mode`).
  These are best handled by a short note in the help text, not new flags.
- **Restore-only fields.** `lxc create` and `lxc config set` omit `unused[n]`,
  which is meaningful only during restore.
- **Migration internals.** `qemu start` and `qemu stop` omit incoming-migration
  fields (`migration_network`, `migration_type`, `targetstorage`,
  `with-conntrack-state`, `migratedfrom`) that the migration commands set
  internally.
- **Index endpoints.** Across `cluster` (12), `node` (several), `sdn` (2), and
  others, bare directory-index GET endpoints are unexposed. Their data is already
  reachable through child commands.
- **Internal and websocket endpoints.** `dbus-vmstate`, `mtunnel`,
  `vncwebsocket`, `vncticket`, and the access ticket primitives are transport or
  session internals, not operator commands.
- **Auth flow documentation.** The `--otp` and `--tfa-challenge` two-step
  challenge sequence on `api auth login` would benefit from a documentation
  example.

## The deprecated-endpoint case in pool

`pve pool` is parameter-complete: all five commands expose every parameter their
endpoints accept, and there are no missing verbs. One structural note: `get`,
`set`, and `delete` route through the deprecated
`/pools/{poolid}` endpoint variants rather than the current `/pools` forms that
accept a pool ID as a query parameter. As a result, nested resource pools
(introduced in Proxmox VE 8.x) are not supported. The non-deprecated client
methods exist but are unused. This is a medium-term modernisation rather than a
coverage gap.

## The client is complete; only the version pin lags

No namespace turned up a single endpoint missing from `pve-apiclient-go`. In the
two largest namespaces the counts match exactly: the specification lists 181
non-SDN cluster endpoints and the client exposes 181; every SDN endpoint, every
node-management endpoint, and every access endpoint likewise has a method. The
client is generated from the specification, and the generation is current.

The one client-side action is housekeeping: the CLI pins `pve-apiclient-go`
v3.2.8 while v3.2.10 is current. The newer tags carry bug fixes (large-integer
parameter encoding, cache, stream, pool, auth, and batch fixes) but no coverage
that the audit depends on. Bumping the pin is worth doing regardless of the gaps
in this report.

## Suggested order of work

When remediation proceeds, a reasonable sequence:

1. **High severity, CLI only.** Add `pve storage aplinfo` (list and download),
   `pve task status`, and `pve api auth login --oidc`.
2. **Medium, completeness.** SDN fabric `redistribute` and `interfaces`; storage
   `oci-registry-pull` and upload checksums; the qemu convenience and discovery
   flags; the lxc template-convert command.
3. **Medium, read-only surface.** The single-item reads in `cluster` and `node`,
   the SDN per-node status views, and the node ceph and console-proxy endpoints.
4. **Housekeeping.** Bump the `pve-apiclient-go` pin from v3.2.8 to v3.2.10, and
   modernise `pool` onto the non-deprecated endpoints to gain nested-pool
   support.
5. **Documentation.** Note the deprecated parameters and their replacements in
   help text rather than adding flags for them.

Because every gap is CLI-side, none of this work requires a new client release.
