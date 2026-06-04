# pve

A comprehensive command-line interface for the Proxmox VE API, built on
[`pve-apiclient-go`](https://github.com/fivetwenty-io/pve-apiclient-go).

`pve` manages multiple named Proxmox targets, authenticates with API tokens or
password tickets, blocks on long-running tasks by default, and renders every
command as a table, plain text, JSON, or YAML.

## Features

- Multiple named targets in `~/.config/pve/config.yml`, switchable per command.

- Token and password (ticket + CSRF) authentication, with secrets resolved from
  environment variables, the system keychain, or literals.

- Structured output in four formats: `table` (default), `plain`, `json`, and
  `yaml` — JSON/YAML preserve native API types.

- Block-by-default on asynchronous tasks (VM/CT lifecycle, snapshots, services),
  with `--async` to return the task UPID immediately.

- Node administration over SSH and rsync, plus an interactive shell and remote
  exec.

- JSONL audit logs written to `~/.pve/logs/`, with secrets redacted.

- Semantic exit codes (0–7) for scripting.

## Installation

```bash
make build      # builds ./dist/pve with version ldflags
make install    # installs pve to $GOPATH/bin (or ~/go/bin)
```

Requires Go 1.26 or newer.

## Quick start

```bash
# 1. Add a target authenticated with an API token, and make it active.
pve api target lab add \
  --host pve.example.com \
  --username root@pam \
  --token automation=${PVE_TOKEN} \
  --switch

# 2. Use it.
pve cluster status
pve node list
pve --node pve1 qemu list
```

## Configuration

Configuration lives in `~/.config/pve/config.yml` (override with `--config`).
Files are written `0600` and directories `0700`, atomically.

```yaml
current-target: lab
default-output: table
targets:
  lab:
    host: pve.example.com
    port: 8006
    protocol: https
    realm: pam
    default-node: pve1
    auth:
      type: token
      username: root@pam
      token-id: automation
      secret: ${PVE_TOKEN}     # ${VAR}, $VAR, keychain:path, or a literal
    tls:
      insecure: false
      fingerprint: ""          # pin a hex SHA-256 cert fingerprint
      ca-cert: ""              # path to a PEM CA bundle for custom trust
```

### Secret resolution

The `auth.secret` value is resolved in three tiers:

- Environment reference: `${VAR}` or `$VAR` — read from the environment;
  `${VAR}` errors if unset.

- Keychain reference: `keychain:path` — read from the system keychain.

- Literal: any other value is used verbatim, with a one-time stderr warning so
  plaintext secrets in config are visible.

Password login stores a live `session` (ticket + CSRF + expiry) back into the
target; `pve api auth logout` wipes it.

## Authentication

```bash
# Token auth: store the token id + secret on the target (see Quick start).

# Password auth: obtain and persist a ticket + CSRF token.
pve api auth login --username root@pam
pve api auth status            # show the active identity and expiry
pve api auth logout            # invalidate and wipe the stored session
```

## Output and logging

Every command honors the global `--output/-o` flag (`table`, `plain`, `json`,
`yaml`) or the `PVE_OUTPUT` environment variable. JSON and YAML emit the full
API response with native types; tables show a curated column set.

```bash
pve node list -o json | jq '.[].node'
pve cluster resources -o yaml
pve qemu list --ascii          # ASCII-only table borders
```

Diagnostic logs are written as JSON Lines to
`~/.pve/logs/{command}[-{subcommand}]-{timestamp}.jsonl`. Authorization, cookie,
and CSRF headers and `password`/`token`/`secret` parameters are redacted.
Suppress log files with `--no-log`; raise verbosity with `--verbose`, `--debug`,
or `--trace`.

## Command overview

`pve` organizes the API into logical groups. VM and container operations take a
node via `--node`/`$PVE_NODE` (or a target's `default-node`); node administration
uses the `pve node` subtree.

| Group | Purpose | Sub-commands |
|-------|---------|--------------|
| `init` | Scaffold local CLI configuration | `config` |
| `api` | Targets and authentication (local config) | `targets`, `target <name> show\|add\|remove`, `switch`, `auth login\|logout\|status\|refresh\|set-token\|set-password` |
| `version` | Cluster API version and CLI build info | `version`, `version client` |
| `access` | Users, tokens, groups, roles, ACLs | `user` (with `user token`), `group`, `role`, `acl`, `permissions`, `password` |
| `cluster` | Cluster state | `status`, `resources`, `next-id`, `log`, `tasks` |
| `node` | Node administration and remote access | `list`, `status`, `ssh`, `rsync`, `shell`, `exec`, `console`, `services`, `task` |
| `qemu` | QEMU virtual machines | `list`, `status`, `create`, `start`, `stop`, `shutdown`, `reboot`, `reset`, `suspend`, `resume`, `delete`, `config`, `snapshot` |
| `lxc` | LXC containers | `list`, `status`, `create`, `template`, `start`, `stop`, `shutdown`, `reboot`, `suspend`, `resume`, `delete`, `config`, `snapshot` |
| `storage` | Cluster storage configuration | `list`, `get`, `content`, `create`, `set`, `delete` |
| `sdn` | Software-defined networking | `zone`, `vnet`, `subnet` (each `list\|create\|delete`), `apply` |
| `pool` | Resource pools | `list`, `get`, `create`, `set`, `delete` |
| `task` | Task inspection and control | `list`, `log`, `wait`, `stop` |

The top-level aliases `pve targets`, `pve target`, `pve switch`, and `pve auth`
resolve to the corresponding `api` sub-commands.

### Examples

```bash
# VM lifecycle (blocks until the task finishes).
pve --node pve1 qemu start 100
pve --node pve1 qemu shutdown 100 --timeout 60
pve --node pve1 qemu start 100 --async         # return the UPID immediately

# Snapshots.
pve --node pve1 qemu snapshot create 100 pre-upgrade --vmstate
pve --node pve1 qemu snapshot rollback 100 pre-upgrade

# Node access.
pve node ssh pve1 -- uptime
pve node rsync ./bundle/ pve1:/var/tmp/bundle/ --identity ~/.ssh/id_ed25519
pve node shell pve1

# Access control.
pve access user list
pve access user token create root@pam ci --privsep 1

# Tasks.
pve --node pve1 task list
pve --node pve1 task wait UPID:pve1:...
```

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Generic error |
| 2 | Bad arguments |
| 3 | Infrastructure / connection error |
| 4 | Authentication failure |
| 5 | Not found |
| 6 | Conflict (e.g. resource locked) |
| 7 | Two-factor authentication required |

## Development

```bash
make check            # fmt + vet + lint + unit tests (full quality gate)
make test             # unit tests
make test-race        # unit tests with the race detector
make coverage         # HTML + console coverage report
make build            # build ./dist/pve
make release          # cross-compile all platforms + checksums
make help             # list all targets
```

Each Makefile category delegates to a script under `scripts/` (`build`, `test`,
`fmt`, `lint`, `release`, `package`, `e2e`).

`scripts/e2e` is a live, read-only happy-path sweep of every command tree
against a configured target (default: `lab`). It runs the trees in parallel and
reports pass/fail/skip per check; mutating or destructive operations are never
executed — they are listed as deferred. Run it directly or via Make:

```bash
make test-e2e                  # all trees against the `lab` target
make test-e2e TREES=qemu       # a subset
make test-e2e TARGET=prod      # a different configured target
scripts/e2e --list             # list trees and the lab isolation contract
scripts/e2e qemu cluster -j 4  # named trees, four parallel workers
```

The sweep skips gracefully (exit 0) when the target is not configured; pass
`--strict` to fail instead. `make test-integration` runs the Go integration
tests (gated on the config file or `PVE_TEST_*`).

`scripts/lifecycle` (`make test-lifecycle`) is the destructive counterpart, and
`scripts/e2e --mutate` runs the read-only sweep and then this mutate phase in
one invocation. It provisions an isolated `pvecli` SDN (zone, vnet, and a
10.241.0.0/24 subnet off the host management network) and a `pve-cli` resource
pool, then drives a throwaway QEMU VM and an LXC container through **every**
mutating sub-command — the full power-state matrix
(`start`/`stop`/`shutdown`/`reboot`/`reset`/`suspend`/`resume`) plus
`snapshot create`/`rollback`/`delete` — recording each verb individually, and
tears everything down. Every created resource is tagged `pve-cli`, placed in the
`pve-cli` pool, and attached to the isolated SDN, so other efforts on a shared
lab are never disturbed. Teardown always runs, and a crashed prior run is swept
clean before the next provisions. Two verbs are environment-bound and recorded
as SKIP with their reason rather than run as failures: qemu `reboot` (a diskless
VM has no guest OS to ACPI-reboot — the verb is proven on the Alpine container)
and lxc `suspend`/`resume` (need working CRIU support on the host).

```bash
make test-e2e-mutate                      # read-only sweep + the destructive verb matrix
make test-lifecycle                       # the destructive verb matrix only, against `lab`
make test-lifecycle TARGET=prod
scripts/e2e --mutate --vm-only            # sweep + VM verb matrix (skip the container)
scripts/lifecycle --vm-only               # VM verb matrix only
scripts/lifecycle --ct-only               # container verb matrix only
```

See [`docs/test-coverage-matrix.md`](docs/test-coverage-matrix.md) for a
per-leaf-command map of e2e and mutate-phase coverage.

## Architecture

The binary entry point is `cmd/pve`; all logic lives under `internal/`:

- `internal/cli` — the cobra root, persistent flags, dependency wiring, and one
  package per command group under `internal/cli/<group>/`.

- `internal/apiclient` — a thin wrapper assembling the `pve-apiclient-go` service
  handles, UPID extraction, and task-wait helpers.

- `internal/config` — config types, loader, atomic writer, and secret resolver.

- `internal/output` — the `table`/`plain`/`json`/`yaml` renderer.

- `internal/logx` — the JSONL slog logger.

- `internal/exec`, `internal/nodeaddr` — shell-out runner and node-address
  resolution for SSH/rsync.

See [`docs/DESIGN.md`](docs/DESIGN.md) for the full design.
