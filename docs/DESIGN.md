# pmx-cli Architecture

## Overview

`pmx` is a Cobra-based CLI that wraps the generated
[`proxmox-apiclient-go`](https://github.com/fivetwenty-io/proxmox-apiclient-go) v3
client. The binary entry point is `cmd/pmx/main.go`; all logic lives under
`internal/`.

## Package layout

```
cmd/pmx/           — binary entry point; calls cli.Execute()
internal/
  cli/             — Cobra root, persistent flags, dependency wiring
    context/       — pmx context / pmx ctx command group
    api/           — pmx api (raw request) and pmx auth command groups
    access/        — pmx pve access subtree
    cluster/       — pmx pve cluster subtree
    node/          — pmx pve node subtree
    qemu/          — pmx pve qemu subtree
    lxc/           — pmx pve lxc subtree
    sdn/           — pmx pve sdn subtree
    storage/       — pmx pve storage subtree
    pool/          — pmx pve pool subtree
    task/          — pmx pve task subtree
    version/       — pmx version subtree
    initcmd/       — pmx init subtree
    lab/           — pmx lab subtree: config-driven nested lab lifecycle
                     (create/destroy/list/status/start/stop/net/access/quota/config);
                     available only under the pmx persona, not pve/pbs/pdm
  apiclient/       — thin wrapper: service handles, UPID extraction, task-wait
  config/          — Config types, loader, atomic writer, secret resolver
  output/          — table/plain/json/yaml renderer
  logx/            — JSONL slog audit logger
  exec/            — shell-out runner (SSH, rsync)
  nodeaddr/        — node address resolution
```

## Contexts

A **context** is a named bundle of connection and authentication settings for
one Proxmox endpoint of a single product: Proxmox VE (PVE), Proxmox Backup Server
(PBS), or Proxmox Datacenter Manager (PDM). The config file at `~/.config/pmx/config.yml`
stores all contexts under the `contexts:` key.

```yaml
current-context: lab
previous-context: prod
contexts:
  lab:
    host: pve.example.com
    port: 8006
    protocol: https
    product: pve
    realm: pam
    default-node: pve1
    default-output: table
    auth:
      type: token
      username: root@pam
      token-id: automation
      secret: ${PMX_TOKEN}
    tls:
      insecure: false
      fingerprint: ""
      ca-cert: ""
  prod:
    host: pbs.example.com
    port: 8007
    product: pbs
    ...
```

Each context carries a `product:` field (`pve`, `pbs`, or `pdm`; empty means
`pve`). The default port follows the product: 8006 (PVE), 8007 (PBS), 8443
(PDM).

### Selection precedence

The active context is resolved in this order on every command invocation:

1. `--context/-c` flag (highest priority).
2. `$PMX_CONTEXT` environment variable.
3. `current-context:` in the config file.

If none of these resolves to a configured context, the command exits with
"no context specified" and suggests `pmx context select`.

### previous-context mechanism

`pmx context select <name>` writes the outgoing `current-context` into
`previous-context` before updating `current-context`. This allows:

- `pmx context previous` — swap back in one step.
- `pmx context select -` — the same swap via the `-` shorthand.

`previous-context` is cleared when a stale reference is detected (the named
context was removed); the error message guides the operator to run
`pmx context select <name>` again.

### Per-context defaults

Each context may carry `default-node` and `default-output` values. These
supply the defaults for `--node` and `--output` on every command run under
that context. The resolution order for both fields is:

1. Explicit flag (`--node` / `--output`).
2. Environment variable (`$PMX_NODE` / `$PMX_OUTPUT`).
3. Context `default-node` / `default-output`.
4. Built-in global default (`""` for node, `table` for output).

A value at a higher tier always wins. In particular, `$PMX_NODE` and
`$PMX_OUTPUT` outrank per-context defaults.

### Context validation

`pmx context validate [<name>] [--all]` runs structural checks against one or
all contexts without contacting any Proxmox API:

- `host` is present.
- `auth.type` is `token` or `password`; required sub-fields are set.
- `port`, if non-zero, is in `[1, 65535]`.
- `protocol`, if set, is `https` or `http`.
- `default-output`, if set, is one of `table`, `ascii`, `plain`, `json`, `yaml`.
- `fingerprint`, if set, matches the `XX:XX:…:XX` hex SHA-256 pattern (32 pairs).

Network connectivity is not checked in v1 (`--connect` is reserved for a future
release). Exit status is 0 when all validated contexts pass, 1 when any fail.

### Secret resolution

`auth.secret` is resolved through three tiers at connection time:

- `${VAR}` or `$VAR` — read from the environment; `${VAR}` errors if unset.
- `keychain:path` — read from the system keychain.
- Literal — used verbatim, with a one-time stderr warning.

Password login persists a live session (ticket + CSRF + expiry) back into the
context entry; `pmx auth logout` invalidates and removes it.

## Dependency wiring

`internal/cli/root.go` registers a `PersistentPreRunE` hook that:

1. Loads the config file.
2. Resolves the active context name (flag → env → config).
3. Constructs a `*cli.Deps` value (config, context, output renderer, logger).
4. Stores it in the command's annotation map via `cli.SetDeps`.

Sub-commands retrieve deps with `cli.GetDeps(cmd)`. Commands annotated
`noClient: true` (all `pmx context` and `pmx auth` verbs) skip API-client
construction; they operate only on the local config file.

## Output rendering

Every command returns an `output.Result` value (headers + rows for tables,
`Raw` for JSON/YAML, `Message` for plain text). The renderer selected by
`--output/-o` (or `$PMX_OUTPUT`, or the context's `default-output`) formats
the result to stdout. JSON and YAML preserve native API types.

## Audit logging

All commands write a JSONL log to `~/.pmx/logs/`. Authorization, cookie, CSRF,
`password`, `token`, and `secret` fields are redacted before writing. Pass
`--no-log` to suppress the log file; `--verbose`, `--debug`, or `--trace` raise
the slog level.

## Asynchronous tasks

Commands that trigger PVE background tasks (VM/CT lifecycle, snapshots,
storage operations) block by default until the task reaches a terminal state,
then exit with the appropriate semantic exit code. Pass `--async` to return the
task UPID immediately instead.

## Semantic exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Generic error |
| 2 | Bad arguments |
| 3 | Infrastructure / connection error |
| 4 | Authentication failure |
| 5 | Not found |
| 6 | Conflict (resource locked) |
| 7 | Two-factor authentication required |
