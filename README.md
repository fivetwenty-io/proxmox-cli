# pmx

A comprehensive command-line interface for the Proxmox VE, Proxmox Backup
Server, and Proxmox Datacenter Manager APIs, built on
[`proxmox-apiclient-go`](https://github.com/fivetwenty-io/proxmox-apiclient-go).

`pmx` manages multiple named Proxmox contexts, authenticates with API tokens or
password tickets, blocks on long-running tasks by default, and renders every
command as a table (Unicode or ASCII borders), plain text, JSON, or YAML.

## Features

- Multiple named contexts in `~/.config/pmx/config.yml`, switchable per command
  with `pmx context select` or the `--context` flag.

- Token and password (ticket + CSRF) authentication, with secrets resolved from
  environment variables, the system keychain, or literals.

- Structured output in four formats: `table` (default), `plain`, `json`, and
  `yaml` — JSON/YAML preserve native API types.

- Block-by-default on asynchronous tasks (VM/CT lifecycle, snapshots, services),
  with `--async` to return the task UPID immediately.

- Node administration over SSH and rsync, plus an interactive shell and remote
  exec.

- Proxmox Backup Server support: contexts with `product: pbs` drive the full
  `pmx pbs` command group — datastores, snapshots, sync/prune/GC/verify jobs,
  access control, tape backup, and a raw API passthrough.

- Proxmox Datacenter Manager support: contexts with `product: pdm` drive the
  full `pmx pdm` command group — managed-remote directory, aggregated
  resources/SDN/Ceph, subscription pool management, access control, the
  instance's own node/config administration, automated installation, and
  proxied PVE/PBS remote operations.

- Persona-aware binary: run it as `pmx` for the combined tree, or as `pve`/
  `pbs`/`pdm` (a symlink to the same binary) to hoist that product's commands
  straight onto the root — see [Personas](#personas).

- JSONL audit logs written to `~/.pmx/logs/`, with secrets redacted.

- Semantic exit codes (0–7) for scripting.

## Installation

### Homebrew (macOS and Linux)

```bash
brew install --cask fivetwenty-io/tap/pmx
```

This installs `pmx` plus the `pve`/`pbs`/`pdm` persona symlinks, man pages
for every command tree, and shell completions for `pmx` (Homebrew casks only
support one completion script per shell, so the persona binaries do not get
their own — see [Download a release archive](#download-a-release-archive) or
[Download a `.deb` or `.rpm` package](#download-a-deb-or-rpm-package) for
per-persona completions). The macOS binaries are signed with an Apple
Developer ID and notarized, so Gatekeeper accepts them on first launch even
though Homebrew quarantines cask artifacts. Maintainers: the tap lives in the
separate
[`fivetwenty-io/homebrew-tap`](https://github.com/fivetwenty-io/homebrew-tap)
GitHub repository, and each release updates the cask there via the
`HOMEBREW_TAP_GITHUB_TOKEN` repository secret on `proxmox-cli`.

### Download a release archive

Pre-built archives are published on the [Releases page](https://github.com/fivetwenty-io/proxmox-cli/releases)
for `darwin/arm64`, `linux/amd64`, `linux/arm64`, `freebsd/amd64`,
`freebsd/arm64`, and `windows/amd64`. The `darwin` binaries are signed and
notarized.

```bash
# Pick your platform; example: macOS Apple Silicon.
VERSION=1.0.0
PLATFORM=darwin_arm64   # or linux_amd64, linux_arm64, freebsd_amd64, freebsd_arm64
BASE="https://github.com/fivetwenty-io/proxmox-cli/releases/download/v${VERSION}"

curl -fsSLO "${BASE}/pmx_${VERSION}_${PLATFORM}.tar.gz"
curl -fsSLO "${BASE}/pmx_${VERSION}_SHA256SUMS"

# Verify the checksum, then extract.
shasum -a 256 -c "pmx_${VERSION}_SHA256SUMS" --ignore-missing
tar -xzf "pmx_${VERSION}_${PLATFORM}.tar.gz"
```

Each archive contains the `pmx` binary, `README.md`, `LICENSE`, and a
`share/` tree with man pages for every command tree (`pmx`, `pve`, `pbs`,
`pdm`) and shell completions for `pmx` and each persona (mirroring what `make
install` lays down under a prefix — see below):

```
pmx
README.md
LICENSE
share/man/man1/*.1
share/man/man5/*.5
share/bash-completion/completions/{pmx,pve,pbs,pdm}.bash
share/zsh/site-functions/_{pmx,pve,pbs,pdm}
share/fish/vendor_completions.d/{pmx,pve,pbs,pdm}.fish
```

Install the binary and, optionally, the `share/` tree:

```bash
sudo install pmx /usr/local/bin/pmx
sudo cp -r share /usr/local/   # man pages + completions (optional)
```

Windows users download `pmx_${VERSION}_windows_amd64.zip`, verify against the
`SHA256SUMS` file, and place `pmx.exe` on their `PATH`.

Release archives ship only the `pmx` binary; create the `pve`/`pbs`/`pdm`
persona symlinks yourself if you want them (see [Personas](#personas)):

```bash
sudo ln -s pmx /usr/local/bin/pve
sudo ln -s pmx /usr/local/bin/pbs
sudo ln -s pmx /usr/local/bin/pdm
```

### Download a `.deb` or `.rpm` package

Debian/Ubuntu and Fedora/RHEL packages are published alongside the archives
on the [Releases page](https://github.com/fivetwenty-io/proxmox-cli/releases)
for `linux/amd64` and `linux/arm64`, named
`pmx_<version>_linux_<arch>.deb`/`.rpm`. Each package installs `pmx` to
`/usr/bin/pmx`, the `pve`/`pbs`/`pdm` persona symlinks, man pages (gzipped)
under `/usr/share/man/`, shell completions for `pmx` and each persona under
`/usr/share/{bash-completion,zsh,fish}/`, and `LICENSE` under
`/usr/share/doc/pmx/` — no separate symlink or `share/` setup step needed.

```bash
# Debian/Ubuntu
curl -fsSLO "https://github.com/fivetwenty-io/proxmox-cli/releases/download/v${VERSION}/pmx_${VERSION}_linux_amd64.deb"
sudo apt install ./pmx_${VERSION}_linux_amd64.deb

# Fedora/RHEL
curl -fsSLO "https://github.com/fivetwenty-io/proxmox-cli/releases/download/v${VERSION}/pmx_${VERSION}_linux_amd64.rpm"
sudo dnf install ./pmx_${VERSION}_linux_amd64.rpm
```

### Install with `go install`

```bash
go install github.com/fivetwenty-io/proxmox-cli/cmd/pmx@latest
```

`go install` places only the `pmx` binary; it does not create persona
symlinks, man pages, or shell completions.

### Build from source

```bash
make build      # builds ./dist/pmx (+ pve/pbs/pdm symlinks) with version ldflags
make install    # installs pmx + pve/pbs/pdm symlinks, man pages, and completions
```

Requires Go 1.26 or newer.

`make install` follows FHS/GNU conventions and defaults to `PREFIX=/usr/local`
(so it typically needs `sudo`):

```bash
sudo make install                        # /usr/local/{bin,share/man,share/*-completion*}
make install PREFIX=$HOME/.local         # per-user prefix, no sudo
make install DESTDIR=/stage PREFIX=/usr  # staged install, for packagers
make uninstall                           # remove everything the matching install placed
```

`DESTDIR` and `PREFIX` combine (`$(DESTDIR)$(PREFIX)/...`) and both `make
install` and `make uninstall` must be run with the same values to stay in
sync. For a quick local dev loop with no `sudo` and no man pages, use:

```bash
make install-user   # copies pmx + pve/pbs/pdm symlinks to $GOPATH/bin (or ~/go/bin)
```

### Man pages

Every command tree ships a man page, generated from the same command
metadata as `--help`: `man pmx` (root, and the same page for every subcommand
prefixed with `pmx-`, e.g. `man pmx-pve-qemu-start`), `man pve` (the `pve`
persona tree, e.g. `man pve-qemu-start`), and likewise for `pbs`/`pdm`, plus
`man pmx-config` (the `~/.config/pmx/config.yml` format, man section 5).
`make install` installs and gzips all of them; to generate them locally
without installing:

```bash
make man   # writes roff pages to ./dist/man/man1 and ./dist/man/man5
```

## Quick start

```bash
# 1. Add a context authenticated with an API token, and make it active.
#    The Proxmox token id "root@pam!automation" maps to --username + --token-id.
pmx context add lab \
  --host pve.example.com \
  --username root@pam \
  --token-id automation \
  --secret ${PMX_TOKEN} \
  --select

# 2. Use it. (Proxmox VE commands live under `pve` — see Personas below;
#    this is the same as running the `pve` binary/symlink without the prefix.)
pmx pve cluster status
pmx pve node list
pmx pve --node pve1 qemu list
```

The same flow works for the other products — pass `--product pbs` or
`--product pdm` to `pmx context add` (the default port follows the product:
8006 PVE, 8007 PBS, 8443 PDM) and use the matching command group or binary:

```bash
pmx context add backup --product pbs --host pbs.example.com \
  --username root@pam --token-id automation
pmx auth set-token --context backup --token-id automation --secret ${PBS_TOKEN}
pmx pbs datastore ls --context backup
```

## Personas

`pmx` inspects how it was invoked (`argv[0]`, with any `.exe` suffix
stripped) to decide which command surface to expose:

- Invoked as `pmx` (or anything else — `go run`/`go test` temp binary names,
  etc.): the combined tree. Proxmox VE resource groups live under
  `pmx pve`, Proxmox Backup Server groups under `pmx pbs`, Proxmox
  Datacenter Manager groups under `pmx pdm`, and the shared commands below
  sit at the root.

- Invoked as `pve`: the Proxmox VE groups (`cluster`, `qemu`, `lxc`, `node`,
  `storage`, `sdn`, `pool`, `access`, `task`) are hoisted directly onto the
  root — `pve node ls` instead of `pmx pve node ls`.

- Invoked as `pbs`: the Proxmox Backup Server groups (`datastore`,
  `snapshot`, `sync`, and the rest of the [Proxmox Backup
  Server](#proxmox-backup-server-pmx-pbs) subtree) are hoisted the same way
  — `pbs datastore ls` instead of `pmx pbs datastore ls`.

- Invoked as `pdm`: the Proxmox Datacenter Manager groups (`remote`,
  `resource`, `sdn`, `ceph`, `subscription`, `user`, `token`, `acl`, `role`,
  `permission`, `tfa`, `realm`, `config`, `node`, `auto-install`, and the
  proxied `pbs`/`pve` remote-operation subtrees — see [Proxmox Datacenter
  Manager](#proxmox-datacenter-manager-pmx-pdm)) are hoisted the same way —
  `pdm remote ls` instead of `pmx pdm remote ls`. Under this persona `pve`
  and `pbs` at the root are the PDM-proxied remote-operation groups, not the
  native `pve`/`pbs` persona trees.

`pve`, `pbs`, and `pdm` are ordinary symlinks (or copies, on platforms
without symlinks) to the same `pmx` binary. `make build`/`make install`
create them automatically; set them up by hand after a release-archive
install with:

```bash
sudo ln -s pmx /usr/local/bin/pve
sudo ln -s pmx /usr/local/bin/pbs
sudo ln -s pmx /usr/local/bin/pdm
```

Every persona — `pmx`, `pve`, `pbs`, and `pdm` — exposes the same top-level
commands, since these operate on the active context rather than a specific
product:

| Command | Purpose |
|---------|---------|
| `context` (alias `ctx`) | Manage named contexts (local config only) |
| `init` | Scaffold local CLI configuration |
| `auth` | Authenticate against the active context: `login`, `logout`, `status`, `refresh`, `whoami`, `set-token`, `set-password` |
| `version` | Active context's server API version; `version client` for CLI build info; `version ping` for a PBS reachability check |
| `ssh` | Open an SSH session to a resolved node |
| `rsync` | Sync files to/from a resolved node over SSH |
| `api` | Raw `get`/`post`/`put`/`delete` passthrough against the active context |

### Breaking change

Earlier `pmx` releases exposed the Proxmox VE resource groups (`cluster`,
`qemu`, `lxc`, `node`, `storage`, `sdn`, `pool`, `access`, `task`) flat at
the root. They now live under `pmx pve` (or at the root of the `pve`
binary):

```bash
# Before              # Now
pmx node list          pmx pve node list        # or: pve node list
pmx qemu start 100     pmx pve qemu start 100    # or: pve qemu start 100
```

`pmx api` also changed meaning. It used to be the authentication command;
that's now the canonical `pmx auth` (unchanged behavior), and `pmx api` is
the raw API passthrough described above. `pmx pbs version`, `pmx pbs api`,
and `pmx pbs ping` moved out of the `pbs` subtree onto the shared root
commands above: use `pmx version`, `pmx api`, and `pmx version ping`
against a `product: pbs` context (or, via the `pbs` binary: `pbs version`,
`pbs api`, `pbs version ping`).

## Configuration

Configuration lives in `~/.config/pmx/config.yml` (override with `--config`).
Files are written `0600` and directories `0700`, atomically.

```yaml
current-context: lab
default-output: table
contexts:
  lab:
    host: pve.example.com
    port: 8006
    protocol: https
    product: pve               # pve (default), pbs, or pdm — selects the API dialect
    realm: pam
    default-node: pve1
    default-output: table      # per-context output format override
    auth:
      type: token
      username: root@pam
      token-id: automation
      secret: ${PMX_TOKEN}     # ${VAR}, $VAR, keychain:path, or a literal
    tls:
      insecure: false
      fingerprint: ""          # pin a hex SHA-256 cert fingerprint
      ca-cert: ""              # path to a PEM CA bundle for custom trust
      tofu: false              # opt-in Trust-On-First-Use cert pinning
    ssh:
      user: root                   # default -l/--user for `pmx ssh`/`pmx rsync`
      port: 22                     # default -p/--port
      identity: ~/.ssh/id_ed25519  # default -i/--identity
```

Configs written by an earlier version of `pmx` use `targets:` and
`current-target:`. Run `scripts/migrate-config.py` (or
`python3 scripts/migrate-config.py`) to rename those keys in place. The script
is idempotent and supports `--dry-run`.

### TLS trust

By default `pmx` verifies the server certificate against the system CA bundle
(or `tls.ca-cert` / `tls.fingerprint` / `tls.insecure` if the context sets
them). For a self-signed lab or homelab node, set `tls.tofu: true` (or pass
`--tofu` to `pmx context add`) to opt into Trust-On-First-Use pinning, the
same model SSH uses for `known_hosts`. `pmx context copy` carries the
setting over from the source context automatically.

- On an interactive terminal, an unrecognized certificate is shown to you as a
  host + fingerprint prompt; answering `y`/`yes` accepts it for that context
  only, and the accepted fingerprint is cached per context under
  `~/.config/pmx/fingerprints/<context>.json`. Later connections to the same
  context trust that fingerprint without prompting again.

- On a non-interactive run (scripts, CI, piped input), an unrecognized
  certificate is always rejected outright — no prompt, no blocking read on
  stdin. Only fingerprints already cached (or explicitly set via
  `tls.fingerprint`) are trusted.

`tls.tofu` is ignored when `tls.insecure` is set, since that already disables
certificate verification. `tofu` is not a substitute for a real CA-signed
certificate in production; it exists to make self-signed lab nodes usable
without `--insecure`.

### Secret resolution

The `auth.secret` value is resolved in three tiers:

- Environment reference: `${VAR}` or `$VAR` — read from the environment;
  `${VAR}` errors if unset.

- Keychain reference: `keychain:path` — read from the system keychain.

- Literal: any other value is used verbatim, with a one-time stderr warning so
  plaintext secrets in config are visible.

Password login stores a live `session` (ticket + CSRF + expiry) back into the
target; `pmx auth logout` wipes it.

## Contexts

`pmx context` (alias: `pmx ctx`) manages the named Proxmox contexts stored in
the config file — each targeting one product: Proxmox VE, Proxmox Backup
Server, or Proxmox Datacenter Manager. All verbs operate on the local config
and never contact a Proxmox API, except `validate --connect`, which probes
the configured endpoint live.

```bash
# Add a context. (Or paste the full token id: --token-id 'root@pam!automation')
pmx context add lab \
  --host pve.example.com --username root@pam \
  --token-id automation --secret ${PMX_TOKEN} --select

# List all contexts (* marks the active one).
pmx context ls

# Show one context (secrets are redacted).
pmx context show lab

# Switch the active context.
pmx ctx select prod
pmx ctx select -         # toggle back to the previous context

# Return to the previous context explicitly.
pmx context previous

# Copy a context to a new name.
pmx context copy lab staging --select

# Rename a context (current/previous pointers follow).
pmx context rename lab lab-old

# List only one product's contexts.
pmx context ls --product pbs

# Edit a context in $EDITOR.
pmx context edit lab

# Remove a context.
pmx context rm old-lab

# Validate one or all contexts (structural checks; add --connect to probe live).
pmx context validate lab
pmx context validate --all
pmx context validate lab --connect
```

The active context is resolved in this order:

1. `--context/-c` flag on any command.
2. `$PMX_CONTEXT` environment variable.
3. `current-context:` in the config file.

Per-context `default-node` and `default-output` fields supply defaults for
`--node` and `--output`. The full resolution order for both fields is:
explicit flag > environment variable (`$PMX_NODE` / `$PMX_OUTPUT`) > context default > built-in default (`""` / `table`).

Each context targets one product. The default, `product: pve`, is a Proxmox VE
API endpoint (port 8006); `product: pbs` marks the context as a Proxmox Backup
Server (port 8007 unless `--port` is given) and enables the `pmx pbs` command
group for it — see
[Proxmox Backup Server](#proxmox-backup-server-pmx-pbs) below; `product: pdm`
marks the context as a Proxmox Datacenter Manager (port 8443 unless `--port`
is given) and enables the `pmx pdm` command group for it — see
[Proxmox Datacenter Manager](#proxmox-datacenter-manager-pmx-pdm) below.

```bash
# Add a PBS context (defaults to port 8007), then attach the token secret.
pmx context add backup \
  --product pbs \
  --host pbs.example.com --username root@pam \
  --token-id automation --select
pmx auth set-token --context backup --token-id automation --secret ${PBS_TOKEN}

# Add a PDM context (defaults to port 8443).
pmx context add dcmgr \
  --product pdm --host pdm.example.com \
  --username root@pam --token-id automation --select
pmx auth set-token --context dcmgr --token-id automation --secret ${PDM_TOKEN}
```

Passing `--secret ${VAR}` directly to `context add` also works; prefer an
env or `keychain:` reference over a literal, which lands in shell history
and the config file in plaintext.

Per-context `ssh.user`, `ssh.port`, and `ssh.identity` fields supply defaults
for `pmx ssh`/`pmx rsync` (and `pmx pve node ssh`/`pmx pve node rsync`):
explicit flag (`-l`/`-p`/`-i` on `pmx ssh`, `--ssh-user`/`--ssh-port`/
`--ssh-identity` on `pmx rsync`) > context `ssh.*` > built-in default
(`root` / `22` / no identity file).

## Authentication

```bash
# Token auth: store the token id + secret on the context (see Quick start).

# Password auth: obtain and persist a ticket + CSRF token.
pmx auth login --username root@pam
pmx auth status            # show the active identity and expiry
pmx auth logout            # invalidate and wipe the stored session

# Two-factor auth (TOTP): first login returns a TFA challenge; re-issue with the
# one-time password and the signed challenge the server returned.
pmx auth login --username root@pam --otp 123456 --tfa-challenge <signed-challenge>

# OIDC login (interactive): prints the authorization URL; open it in a browser,
# authenticate, then paste the full redirect URL at the prompt.
pmx auth login --oidc --realm myoidc

# OIDC login (non-interactive): supply the authorization code and state directly,
# e.g. from a script that completes the browser step out of band.
pmx auth login --oidc --realm myoidc --code <auth-code> --state <state>
```

`auth` is a shared, product-neutral command: `login`, `refresh`, and `whoami` all work
with any context targeting Proxmox VE (PVE), Proxmox Backup Server (PBS), or
Proxmox Datacenter Manager (PDM). The subcommands `status`, `set-token`,
`set-password`, and `logout` also work with any context. The `--otp` flag (one-time password for TOTP-based
two-factor authentication) is PVE-only; PBS and PDM contexts use `--tfa-challenge`
instead. OIDC login via `--oidc` works with all products. The client library handles
per-product API token wire format differences (PVE uses `=` as separator, PBS/PDM use
`:`) automatically — users configure tokens the same way for all products.

## Output and logging

Every command honors the global `--output/-o` flag (`table`, `ascii`, `plain`,
`json`, `yaml`) or the `PMX_OUTPUT` environment variable. JSON and YAML emit the
full API response with native types; tables show a curated column set, and
`ascii` renders the same tables with ASCII-only borders.

```bash
pmx pve node list -o json | jq '.[].node'
pmx pve cluster resources -o yaml
pmx pve qemu list -o ascii         # ASCII-only table borders
```

Diagnostic logs are written as JSON Lines to
`~/.pmx/logs/{command}/{subcommand…}/{timestamp}.jsonl` (for example
`~/.pmx/logs/pve/storage/volume/copy/20260714-132051.jsonl`). Set
`log.layout: flat` in the config file (or `PMX_LOG_LAYOUT=flat`) for the
single-directory `{command}[-{subcommand}]-{timestamp}.jsonl` layout instead.
Authorization, cookie, and CSRF headers and `password`/`token`/`secret`
parameters are redacted. The minimum recorded level defaults to `info`;
set `log.level` (`trace`, `debug`, `info`, `warn`, `error`) or
`PMX_LOG_LEVEL` to change it. Suppress log files with `--no-log`; raise
verbosity with `--verbose`, `--debug`, or `--trace`.

`pmx --version` (or `-v`) prints the CLI's own build information — release tag,
short commit, build date, Go toolchain, and OS/arch — without contacting any
server. `make build` and release binaries inject these via ldflags; ad-hoc
`go build` binaries report a dev version with unknown commit. The same
information is available as a command (with `-o json`/`-o yaml` support) via
`pmx version client`.

## Command overview

`pmx` organizes the API into logical groups. VM and container operations take a
node via `--node`/`$PMX_NODE` (or the context's `default-node`); node administration
uses the `pmx pve node` subtree. The commands below are shared across every
persona (see [Personas](#personas)); everything else lives under `pmx pve`
(Proxmox VE), `pmx pbs` (Proxmox Backup Server, `product: pbs` contexts), or
`pmx pdm` (Proxmox Datacenter Manager, `product: pdm` contexts).

| Group | Purpose | Sub-commands |
|-------|---------|--------------|
| `init` | Scaffold local CLI configuration | `config` |
| `context` | Named contexts (local config) | `add`, `ls`, `show`, `select`, `previous`, `rm`, `copy`, `edit`, `validate` |
| `auth` | Authenticate against the active context | `login`, `logout`, `status`, `refresh`, `whoami`, `set-token`, `set-password` |
| `version` | Active context's server API version and CLI build info | `version`, `version client`, `version ping` (PBS only) |
| `rsync` | Top-level `rsync` wrapper: sync files to/from a resolved node over SSH (`node:path` operands) | `--ssh-user`, `--ssh-port`, `--ssh-identity`, `--ssh-agent`, `--no-strict` |
| `ssh` | Top-level `ssh` wrapper: open an SSH session to a resolved node | `-l/--user`, `-i/--identity`, `-p/--port`, `-A/--agent`, `--no-strict` |
| `api` | Raw API passthrough against the active context | `get`, `post`, `put`, `delete` |

The top-level alias `pmx ctx` resolves to `pmx context`.

### Proxmox VE (`pmx pve`)

| Group | Purpose | Sub-commands |
|-------|---------|--------------|
| `access` | Users, tokens, groups, roles, ACLs | `user` (with `user token`), `group`, `role`, `acl`, `permissions`, `password` |
| `cluster` | Cluster state | `status`, `resources`, `next-id`, `log`, `tasks` |
| `node` | Node administration and remote access | `list`, `status`, `ssh`, `rsync`, `shell`, `exec`, `console`, `services`, `task`, `permissions` |
| `qemu` | QEMU virtual machines | `list`, `status`, `create`, `start`, `stop`, `shutdown`, `reboot`, `reset`, `suspend`, `resume`, `delete`, `config`, `snapshot`, `security`, `permissions` |
| `lxc` | LXC containers | `list`, `status`, `create`, `template`, `start`, `stop`, `shutdown`, `reboot`, `suspend`, `resume`, `delete`, `config`, `snapshot`, `security`, `permissions` |
| `storage` | Cluster storage configuration | `list`, `get`, `content`, `create`, `set`, `delete`, `upload`, `permissions` |
| `sdn` | Software-defined networking | `zone`, `vnet` (each `list\|show\|create\|delete\|permissions`), `subnet` (`list\|show\|create\|delete`), `apply` |
| `pool` | Resource pools | `list`, `get`, `show`, `create`, `set`, `delete`, `permissions` |
| `task` | Task inspection and control | `list`, `log`, `wait`, `stop` |

`pmx pbs` — Proxmox Backup Server (contexts with `product: pbs`) — is documented
separately: see [Proxmox Backup Server](#proxmox-backup-server-pmx-pbs) below.
`pmx pdm` — Proxmox Datacenter Manager (contexts with `product: pdm`) — is
documented separately: see [Proxmox Datacenter
Manager](#proxmox-datacenter-manager-pmx-pdm) below.

### Examples

Shown as run via the `pve` binary/symlink; drop the leading `pve` and prefix
`pmx pve` instead if you're invoking the plain `pmx` binary (e.g.
`pmx pve --node pve1 qemu start 100`).

```bash
# VM lifecycle (blocks until the task finishes).
pve --node pve1 qemu start 100
pve --node pve1 qemu shutdown 100 --timeout 60
pve --node pve1 qemu start 100 --async         # return the UPID immediately

# Snapshots.
pve --node pve1 qemu snapshot create 100 pre-upgrade --vmstate
pve --node pve1 qemu snapshot rollback 100 pre-upgrade --yes

# Node access.
pve node ssh pve1 -- uptime
pve node rsync ./bundle/ pve1:/var/tmp/bundle/ --identity ~/.ssh/id_ed25519
pve node shell pve1

# Top-level ssh/rsync (same node resolver as above, no "node" prefix; shared —
# use the pmx binary, not pve/pbs, since these commands never take a "pve"/"pbs" prefix).
pmx ssh pve1
pmx ssh -c prod pve1 uptime
pmx ssh pve1 -L 8080:localhost:80 -N
pmx ssh pve1 -- ls -la
pmx rsync -c prod -avz --delete ./site/ pve1:/var/www/

# Access control.
pve access user list
pve access user token create root@pam ci --privsep 1

# Tasks.
pve --node pve1 task list
pve --node pve1 task wait UPID:pve1:...
```

### Escape hatches and small additions

`qemu create`, `qemu config set`, `lxc create`, and `lxc config set` all
accept a repeatable `--set KEY=VALUE` flag, an escape hatch that sends an
arbitrary config option straight to the API, verbatim, for options with no
dedicated flag yet; a `--set` key that collides with a dedicated flag passed
in the same invocation is rejected rather than silently overwritten.

`pmx pve qemu firewall alias get <vmid> <name>` and `pmx pve qemu firewall
ipset get-member <vmid> <name> <cidr>` (with `pmx pve lxc` equivalents) read a
single firewall alias or IP set member by name, alongside the existing `list`
verbs. `pmx pve qemu migrate capabilities` reports the node's QEMU
live-migration feature support — the same data as `pmx pve node capabilities
qemu migration`.

### Cloud-init snippets over SSH

The PVE upload API cannot upload snippets: the endpoint's content enum is
`iso|vztmpl|import`, so custom cloud-init files referenced by `--cicustom`
normally have to be copied onto snippet storage by hand. This is a
long-standing upstream gap
([Proxmox Bugzilla #2208](https://bugzilla.proxmox.com/show_bug.cgi?id=2208)).

As a workaround, `pmx pve storage upload --content snippets` streams the file
over SSH into the storage's `snippets/` directory instead of calling the
upload API. It requires:

- a path-backed storage (`dir`, `nfs`, `cifs`, ...) with the `snippets`
  content type enabled (`pmx pve storage set local --content iso,vztmpl,snippets`)

- SSH access to the node (root by default; `-l`/`-i`/`-p` and the context's
  `ssh:` block apply, same as `pmx ssh`)

```bash
# Push a custom cloud-init user-data snippet, then wire it to a VM.
pmx pve --node pve1 storage upload local --file ./user-data.yaml --content snippets
pmx pve --node pve1 qemu config set 100 --cicustom "user=local:snippets/user-data.yaml"
pmx pve --node pve1 qemu cloudinit update 100
```

`--checksum` is not supported in this mode, and no PVE task is created — the
transfer is a plain SSH stream. Once the upstream API grows a snippets content
type, the SSH path can be retired in favor of the normal upload endpoint.

## Proxmox Backup Server (`pmx pbs`)

`pmx pbs` manages a Proxmox Backup Server through its own API. It requires the
active context (or `--context`) to have `product: pbs`; PVE commands reject PBS
contexts and vice versa, so a mixed fleet is a matter of switching contexts.
Everything else works exactly like the PVE side: the same output formats,
`--async` on task-producing verbs, JSONL logging, and exit codes.

| Group | Purpose | Sub-commands |
|-------|---------|--------------|
| `datastore` | Datastore configuration and usage | `ls`, `show`, `create`, `update`, `delete`, `status`, `usage`, `rrd` |
| `snapshot` | Backup snapshots in a datastore | `ls`, `show`, `files`, `delete`, `protect`, `unprotect`, `notes` |
| `group` | Backup groups | `ls`, `delete`, `notes` |
| `prune` | Prune jobs and one-shot prune runs | `run`, `simulate`, `job` (CRUD + `run`) |
| `gc` | Garbage collection | `run`, `status`, `ls` |
| `verify` | Verification jobs and one-shot runs | `run`, `job` (CRUD + `run`) |
| `sync` | Sync jobs and one-shot pull/push | `ls`, `job` (CRUD + `run`), `pull`, `push` |
| `remote` | Remote PBS instances for sync | `ls`, `show`, `add`, `update`, `delete`, `scan` |
| `traffic` | Traffic-control rules | `ls`, `show`, `add`, `update`, `delete`, `current` |
| `node` | Node administration | `ls`, `status`, `reboot`, `shutdown`, `rrd`, `report`, `syslog`, `journal`, `dns`, `time`, `config`, `subscription`, `identity`, `tasks`, `services`, `apt`, `disks`, `network`, `certificates` |
| `user` | Users and API tokens | `ls`, `show`, `add`, `update`, `delete`, `unlock-tfa`, `passwd`, `token` |
| `acl` | Access control list | `ls`, `update` |
| `role` | Roles (fixed set) | `ls` |
| `permission` | Effective permissions | `ls` |
| `realm` | Authentication realms | `ls`, `sync`, `ad`, `ldap`, `openid`, `pam`, `pbs` |
| `metrics` | Metric servers and metric data | `influxdb-http`, `influxdb-udp`, `data` |
| `notification` | Notification endpoints, matchers, targets | `endpoint`, `matcher`, `target` |
| `acme` | ACME accounts and plugins | `account`, `plugin`, `challenge-schema`, `directories`, `tos` |
| `tape` | Tape backup | `drive`, `changer`, `media`, `pool`, `key`, `job`, `backup`, `restore` |
| `encryption-key` | Datastore encryption keys | `ls`, `add`, `delete`, `toggle-archive` |
| `status` | Server-wide status | `datastore-usage` |

The PBS version, a reachability check, and a raw API passthrough are shared
root commands (present under every persona — see [Personas](#personas)),
not part of this subtree: `pmx version` (or `pbs version`), `pmx version
ping` (or `pbs version ping`), and `pmx api get\|post\|put\|delete` (or
`pbs api ...`), each against a `product: pbs` context.

```bash
# Datastores and their contents.
pmx -c backup pbs datastore ls
pmx -c backup pbs snapshot ls --store tank
pmx -c backup pbs snapshot delete --store tank vm/100/2026-07-01T02:00:00Z

# Jobs: one-shot runs and configured schedules.
pmx -c backup pbs gc run --store tank
pmx -c backup pbs prune simulate vm/100 --store tank --keep-last 3
pmx -c backup pbs sync pull --store tank --remote offsite --remote-store tank
pmx -c backup pbs verify job add nightly --store tank --schedule daily

# Tape.
pmx -c backup pbs tape drive ls
pmx -c backup pbs tape backup --store tank --pool weekly --drive lto9

# Anything without a dedicated verb (shared "api" command, not nested under "pbs").
pmx -c backup api get /admin/datastore/tank/status
```

## Proxmox Datacenter Manager (`pmx pdm`)

`pmx pdm` manages a Proxmox Datacenter Manager (PDM) instance through its own
API. It requires the active context (or `--context`) to have `product: pdm`;
PVE and PBS commands reject PDM contexts and vice versa, so a mixed fleet is a
matter of switching contexts. Everything else works exactly like the PVE and
PBS sides: the same output formats, `--async` on task-producing verbs, JSONL
logging, and exit codes.

A Proxmox Datacenter Manager instance itself manages a fleet of PVE and PBS
remotes. The `pdm pve` and `pdm pbs` groups proxy operations against those
managed remotes — they are distinct from, and nested under, the top-level
`pmx pve`/`pmx pbs` command trees, which talk directly to a PVE/PBS context
instead of through a PDM instance.

| Group | Purpose | Sub-commands |
|-------|---------|--------------|
| `remote` | Manage remotes registered with this Proxmox Datacenter Manager | `ls`, `show`, `add`, `update`, `delete`, `version`, `probe-certificate`, `rrddata`, `task`, `updates`, `metric-collection` |
| `resource` | List and inspect aggregated resources across managed remotes | `ls`, `location-info`, `status`, `subscription`, `top-entities` |
| `sdn` | Inspect and manage aggregated SDN configuration | `controller`, `vnet`, `zone` |
| `ceph` | Inspect Ceph clusters registered with managed remotes | `ls`, `status`, `summary`, `flags`, `fs`, `mds`, `mgr`, `mon`, `osd-tree`, `pools` |
| `subscription` | Manage the subscription key pool and remote subscription status | `key` (`ls`, `show`, `add`, `delete`, `assign`, `unassign`), `node-status`, `check`, `adopt-key`, `adopt-all`, `auto-assign`, `bulk-assign`, `apply-pending`, `clear-pending`, `queue-clear`, `revert-pending-clear` |
| `user` | Manage Proxmox Datacenter Manager users | `ls`, `show`, `add`, `update`, `delete` |
| `token` | Manage a user's API tokens | `ls`, `show`, `add`, `update`, `delete` |
| `acl` | Manage the Proxmox Datacenter Manager access control list | `ls`, `update` |
| `role` | List Proxmox Datacenter Manager roles | `ls` |
| `permission` | Show effective permissions | `ls` |
| `tfa` | Manage user two-factor authentication entries | `ls`, `show`, `update`, `delete` |
| `realm` | Manage authentication realms | `ls`, `sync`, `ad`, `ldap`, `openid`, `pam`, `pdm` |
| `config` | Manage this instance's own host configuration | `acme`, `certificate`, `notes`, `view`, `webauthn` |
| `node` | Administer this instance's own node(s) | `ls`, `status`, `reboot`, `shutdown`, `config`, `dns`, `time`, `journal`, `syslog`, `report`, `rrddata`, `network`, `apt`, `certificate`, `task`, `sdn`, `subscription` |
| `auto-install` | Manage automated installations, prepared answers, and tokens | `installation`, `prepared`, `token` |
| `pbs` | Proxy operations against managed PBS remotes | `remote`, `scan`, `probe-tls`, `realms`, `status`, `rrddata`, `datastore`, `node`, `task` |
| `pve` | Proxy operations against managed PVE remotes | `remote`, `scan`, `probe-tls`, `realms`, `options`, `updates`, `cluster`, `firewall`, `node`, `storage`, `task`, `qemu`, `lxc` |

The PDM version, a reachability check, and a raw API passthrough are shared
root commands (present under every persona — see [Personas](#personas)), not
part of this subtree: `pmx version` (or `pdm version`) and `pmx api
get\|post\|put\|delete` (or `pdm api ...`), each against a `product: pdm`
context.

```bash
# Managed remotes and aggregated resources.
pmx -c dcmgr pdm remote ls
pmx -c dcmgr pdm resource ls --resource-type qemu
pmx -c dcmgr pdm resource top-entities --timeframe hour

# Subscription pool management.
pmx -c dcmgr pdm subscription key ls
pmx -c dcmgr pdm subscription auto-assign

# Proxied PVE remote operations (through the PDM instance, not a direct PVE context).
pmx -c dcmgr pdm pve remote ls
pmx -c dcmgr pdm pve qemu ls pve-remote-1
pmx -c dcmgr pdm pve node status pve-remote-1 node1

# Proxied PBS remote operations.
pmx -c dcmgr pdm pbs datastore ls pbs-remote-1

# This instance's own node/config administration.
pmx -c dcmgr pdm node ls
pmx -c dcmgr pdm config acme account ls

# Anything without a dedicated verb (shared "api" command, not nested under "pdm").
pmx -c dcmgr api get /remotes
```

## VM security (`pmx pve qemu security`)

`pmx pve qemu security` inspects and hardens the layered security posture of a
QEMU VM: the protection flag, Secure Boot / EFI and TPM state, confidential
computing (AMD SEV / Intel TDX), security-relevant CPU flags, the guest
agent configuration, and per-NIC firewall coverage. Every command uses only
the PVE config and firewall APIs — no ssh.

```bash
pmx pve qemu security show 100              # full posture: protection, boot chain, TPM, confidential, agent, NIC firewall
pmx pve qemu security list                  # cluster-wide audit table (risky VMs flagged '!' first)
pmx pve qemu security agent show 100        # agent= sub-options at their effective value
pmx pve qemu security secureboot show 100   # bios/efidisk0 and the derived Secure Boot posture
pmx pve qemu security tpm show 100          # tpmstate0 presence, volume, and version
pmx pve qemu security confidential show 100 # amd-sev / intel-tdx configuration, if any
pmx pve qemu security cpu-flags show 100    # the 13-flag PVE security-relevant catalog, per-VM state
pmx pve qemu security cpu-flags describe    # the same catalog offline, with mitigation notes
pmx pve qemu security nic show 100          # per-NIC model, bridge, VLAN, firewall, link-down
```

Every mutating `security` sub-command, except `protection enable`/`disable`
(which apply immediately, with no digest race to close), reads the VM's
current configuration first and sends that digest back with its update; an
explicit `--digest` overrides this. Those same sub-commands also accept
`--restart`, which reboots a running VM after a successful change; without
it, the change is pending until the VM's next stop/start or reboot.

### Hardening examples

```bash
# Guest agent: enable communication, keep freeze-fs (snapshot consistency) on.
pmx pve qemu security agent set 100 --enabled --type virtio --restart

# Secure Boot: allocate a pre-enrolled EFI vars disk (OVMF + Secure Boot keys).
pmx pve qemu security secureboot enable 100 --storage local-lvm --restart

# TPM: add a 2.0 state device (Windows 11 requires it); remove destroys sealed keys.
pmx pve qemu security tpm add 100 --storage local-lvm
pmx pve qemu security tpm remove 100 --force

# Confidential computing: AMD SEV-SNP without hypervisor debug access.
pmx pve qemu security confidential set 100 --sev snp --sev-no-debug
pmx pve qemu security confidential clear 100

# CPU flags: enable the Spectre/MDS mitigations; disabling one needs --force.
pmx pve qemu security cpu-flags set 100 --enable spec-ctrl,ssbd,md-clear
pmx pve qemu security cpu-flags set 100 --disable spec-ctrl --force

# Per-NIC firewall: turn coverage on for every configured NIC.
pmx pve qemu security nic firewall 100 --on --all

# Protection flag: block destroy/disk removal, then allow it again.
pmx pve qemu security protection enable 100
pmx pve qemu security protection disable 100
```

There is deliberately no `secureboot disable`: the real Secure Boot on/off
switch lives in the EFI variables themselves, flipped from the guest's own
firmware setup menu. `secureboot enable` refuses to touch an existing
`efidisk0` unless you pass `--recreate`, since replacing it discards every
enrolled key and boot entry; pass `--recreate` (optionally with a different
`--storage`) to allocate a fresh vars disk, moving the old one to
`unused[n]`.

## Container security (`pmx pve lxc security`)

`pmx pve lxc security` inspects and hardens the layered security posture of an LXC
container: its privilege level, its `features=` flags, and the low-level Linux
capability whitelist. Containers created through `pmx pve lxc create` are
**unprivileged by default** — the container's `root` is mapped to an unprivileged
host UID — which is the safe baseline. The `security` commands help you keep that
baseline and grant only the capabilities a workload actually needs.

All the read verbs use only the API and need no SSH:

```bash
pmx pve lxc security show 105          # full posture: privilege, features, caps, raw lxc.* keys
pmx pve lxc security list              # cluster-wide audit table (privileged CTs flagged '!' first)
pmx pve lxc security caps show 105     # the configured lxc.cap.keep / lxc.cap.drop lists
pmx pve lxc security caps describe     # offline catalog of every capability and the presets
pmx pve lxc security features show 105 # nesting, keyctl, fuse, mknod, force_rw_sys, mount
```

### Tuning features

`pmx pve lxc security features set` edits the container's `features=` option with
structured per-feature flags. It is a read-merge-write over the config API, so
only the flags you pass change and the rest are left untouched:

```bash
pmx pve lxc security features set 105 --nesting --keyctl
pmx pve lxc security features set 105 --mount 'nfs;cifs'
pmx pve lxc security features set 105 --reset            # clear features= entirely
```

`keyctl` (unprivileged only) is needed by some Docker workloads but breaks
systemd-networkd, `mknod` is experimental, and mounting loop or NFS filesystems
widens the attack surface. Feature changes apply on the next container start.

> `pmx pve lxc security features` is **not** `pmx pve lxc feature`. The latter is an
> unrelated, read-only probe of whether a container supports a snapshot, clone,
> or copy operation.

### Capability whitelist workflow

PVE grants a broad default capability set. To lock a container down to only what
it needs, replace that set with an explicit keep-list and iterate:

```bash
# 1. Confirm the container is unprivileged. A keep-list on a privileged CT is far
#    weaker, because its mapped root already has host-level reach.
pmx pve lxc security show 105

# 2. Start from a preset (or an explicit --keep list). A keep-list writes
#    lxc.cap.keep and drops every capability not named.
pmx pve lxc security caps set 105 --preset minimal --restart
#    or: pmx pve lxc security caps set 105 --keep chown,setuid,setgid,kill --restart

# 3. Verify what the running container actually holds (decodes /proc/1/status).
pmx pve lxc security caps show 105 --effective

# 4. If the workload is missing a capability, grant one, restart, and retest.
#    Add narrowly rather than widening back toward the defaults.
pmx pve lxc security caps add 105 net_bind_service --restart
pmx pve lxc security caps show 105 --effective

# Revoke a single capability, or restore PVE's defaults entirely.
pmx pve lxc security caps remove 105 net_raw --restart
pmx pve lxc security caps reset 105 --restart
```

Without `--restart`, each mutation prints the manual restart command instead; the
changes only take effect on the next container start.

## Object permissions

Every object tree with its own ACL path — `qemu`, `lxc`, `storage`, `pool`,
`node`, `sdn zone`, and `sdn vnet` — carries a `permissions` sub-command
that derives that path automatically, so day-to-day ACL work never
requires typing PVE's path grammar (`/vms/100`, `/pool/lab`,
`/sdn/zones/dmz/vnet0`, and so on) by hand.

```bash
# Grant a role on a VM, addressed by vmid or name.
pmx pve qemu permissions grant 100 --roles PVEVMAdmin --users alice@pve

# List ACL entries on a storage, including entries inherited from its
# ancestor paths (/, /storage).
pmx pve storage permissions list local-lvm --inherited
```

Every tree exposes the same four verbs: `list` (`--inherited` also
includes entries from ancestor paths), `effective` (`--userid` checks
another user or token, which needs `Sys.Audit` on `/access`), and `grant`/
`revoke` (both need `Permissions.Modify` on the derived path, take
`--roles` plus at least one of `--users`, `--groups`, or `--tokens`
comma-separated, and accept `--no-propagate` to withhold the default
propagation to sub-paths). `pmx pve pool permissions` derives PVE's singular
`/pool/{poolid}` ACL path automatically, even though the `pool` object
tree and its own sub-commands are plural — a mismatch the command handles
so the operator never has to remember it.

These commands are thin, path-deriving wrappers: for any ACL path outside
a single object, `pmx pve access acl` and `pmx pve access permissions`
remain the general-purpose commands, and both still accept an arbitrary
`--path`.

The keep-mode presets are:

| Preset | Capabilities | For |
|--------|--------------|-----|
| `minimal` | `chown`, `dac_override`, `fowner`, `setuid`, `setgid`, `kill` | a bare init or single-service container |
| `systemd` | `minimal` plus `setpcap` | a systemd-based distribution |
| `network` | `systemd` plus `net_bind_service`, `net_raw` | privileged ports or raw sockets |

Granting a dangerous capability (`sys_admin`, `sys_module`, `sys_rawio`,
`sys_boot`, or `sys_time`) breaks the container isolation boundary and can
compromise the host, so `caps set` and `caps add` refuse these unless you pass
`--force`, which proceeds but still prints a warning.

PVE exposes no API for `lxc.cap.keep` / `lxc.cap.drop`, so the capability
**mutations** (`caps set`, `add`, `remove`, and `reset`) and the `caps show
--effective` probe reach the node over SSH and require a **root SSH login**
(`/etc/pve` is only writable by root). They honor the context's `ssh.*` defaults
and the standard `-l`/`-i`/`-p`/`-A`/`--no-strict` flags. Each edit is serialized
against `pct`'s own config lock, guarded by an optimistic checksum, and validated
with `pct config`, rolling back automatically if the result would not parse. The
read verbs above need only the API.

## Lab environments (`pmx lab`)

`pmx lab` manages per-member nested lab environments running inside a
Proxmox VE cluster. Each lab is a self-contained slice of the cluster: its
own SDN vnet and subnet (carved out of a single shared VXLAN zone,
`labsvxlan`), a VM, storage derived from that VM's disks, a resource pool,
a pve-realm user's access grant on that pool, and a ZFS `refquota` on the
lab's dataset. `pmx lab` is only available when the binary runs as `pmx`
(or an unrecognized `argv[0]`) — it is not hoisted onto the `pve`, `pbs`,
or `pdm` persona roots the way `pve`'s own groups are (see
[Personas](#personas)).

Labs are config-driven, resolved from three keys in `~/.config/pmx/config.yml`:
an inline `labs:` map, an `include:` list of glob patterns, and a `labs_dir:`
directory of one-lab-per-file YAML (sugar for one more `include:` glob).
Every mutating verb accepts flags that override individual resolved fields
for that single invocation, without touching the underlying config.

```yaml
labs_dir: labs.d/
default_user_password: changeme-example   # bootstrap password for new grantees; setting it requires config.yml to be mode 0600

labs:
  wayne:
    network:
      vnet_id: wayne
      vxlan_tag: 5001
      cidr: 10.108.0.0/16
      mgmt:
        gateway: 10.108.0.1
    compute:
      vcpu: 16
      memory:
        min_gb: 32
        max_gb: 96
    storage:
      data_disk_gb: 400
      refquota_gb: 480
    access:
      pool: lab-wayne
      role: PVEVMUser
```

`drgao`'s lab would instead live as its own file under `labs_dir` (e.g.
`labs.d/drgao.yaml`), written with:

```bash
pmx lab config init                                          # scaffold labs_dir/ with a commented example.yaml
pmx lab config add drgao --vxlan-tag 5002 --cidr 10.109.0.0/16
pmx lab config show drgao                                    # resolved lab + provenance (which file it came from)
```

`pmx lab config add` never rewrites `config.yml`; it only ever writes a new
file under `labs_dir`, so hand-written comments in `config.yml` are never
lost to a struct-marshal round trip. See `man 5 pmx-config` for the full
lab schema.

### Command walkthrough

```bash
# Create the lab: SDN zone/vnet/subnet, storage, resource pool, and VM, in
# that order, skipping anything already in place. This stages the SDN
# changes but does not commit them — pending zone/vnet/subnet changes are
# only committed by `pmx lab net apply` (or `pmx pve sdn apply`).
pmx lab create wayne --node sm-0
pmx lab net apply wayne             # always previews the pending SDN changeset first, then commits it

# Inspect it.
pmx lab status wayne
pmx lab list

# Grant wayne@pve access to their own lab's pool (creates the pool, the
# user, and the role along the way if any of them is missing).
pmx lab access grant wayne wayne@pve

# There is no Proxmox VE API for ZFS dataset properties, so quota set runs
# `zfs set refquota=...` on the lab host over ssh instead.
pmx lab quota set wayne --refquota-gb 600

# Tear it down. --purge additionally removes the resource pool and storage
# definition; without it only the VM is stopped and deleted.
pmx lab destroy wayne --yes
```

Every mutating verb (`create`, `destroy`, `net apply`, `access grant`,
`quota set`, `start`, `stop`) supports `--dry-run` to preview its effect
without mutating anything, and every one of them refuses to act on a
lab whose resolved identifiers collide with a protected production
resource. `pmx lab list`/`status`/`start`/`stop` join each configured lab
against its live VM by resource-pool membership, since labs carry no
stored VMID in config.

`pmx lab create` and `pmx lab config add` also validate the lab's address
plan up front: `network.mgmt.subnet`, `network.mgmt.host_ip`,
`network.mgmt.gateway`, and `network.bosh_bloc` must all fall inside
`network.cidr`. Note that `network.mgmt.subnet` is an address-plan
reservation, not the lab host's interface prefix — the host's interface
must be addressed with the lab CIDR's own prefix length (`host_ip/16` for a
`/16` lab, even when the management subnet is a `/24`), because a narrower
interface prefix makes the host route replies to on-link guests via the
gateway, which drops them as out-of-state. `pmx lab status` decodes each
guest interface's prefix from the guest agent and adds a `NETWORK_WARNING`
row when an in-CIDR interface is narrower than the lab CIDR.

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

Once `pmx ssh`/`pmx rsync` (or `pmx pve node ssh`/`pmx pve node rsync`) hands
off to the child process, `pmx` exits with that child's own exit code, verbatim,
instead of one of the codes above — `ssh` uses 255 for a connection/auth
failure, `rsync` uses 23/24 for a partial transfer, and so on. A child exit
code of 2 is indistinguishable from this table's `Bad arguments` (2): the
child's code always wins once it has actually run.

## Development

```bash
make check            # fmt + vet + lint + unit tests (full quality gate)
make test             # unit tests
make test-race        # unit tests with the race detector
make coverage         # HTML + console coverage report
make build            # build ./dist/pmx
make release          # cross-compile all platforms + checksums
make help             # list all targets
```

Each Makefile category delegates to a script under `scripts/` (`build`, `test`,
`fmt`, `lint`, `release`, `package`, `e2e`).

`scripts/e2e` is a live, read-only happy-path sweep of every command tree
against a configured context (default: `lab`). It runs the trees in parallel and
reports pass/fail/skip per check; mutating or destructive operations are never
executed — they are listed as deferred. The `pbs` and `pdm` trees are opt-in:
`pbs` sweeps the `pmx pbs` group against a separate `product: pbs` context
named via `--pbs-context` (or `PBS_CONTEXT=`/`$PMX_E2E_PBS_CONTEXT`), and `pdm`
sweeps the `pmx pdm` group against a separate `product: pdm` context named via
`--pdm-context` (or `PDM_CONTEXT=`/`$PMX_E2E_PDM_CONTEXT`); each skips when its
opt-in is absent or its server is unreachable. Run it directly or via Make:

```bash
make test-e2e                   # all trees against the `lab` context
make test-e2e TREES=qemu        # a subset
make test-e2e CONTEXT=prod      # a different configured context
make test-e2e PBS_CONTEXT=pbs-lab  # opt into the pbs tree (needs a PBS server)
make test-e2e PDM_CONTEXT=pdm-lab  # opt into the pdm tree (needs a PDM server)
scripts/e2e --list              # list trees and the lab isolation contract
scripts/e2e qemu cluster -j 4   # named trees, four parallel workers
```

The sweep skips gracefully (exit 0) when the context is not configured; pass
`--strict` to fail instead. `make test-integration` runs the Go integration
tests (gated on the config file or `PMX_TEST_*`).

`scripts/stack` provisions the PBS and PDM servers those opt-in trees need,
as guests on the lab cluster itself: `scripts/stack init` writes a commented
`config/stack.toml`, `make stack-up` clones a Debian cloud-init template per
enabled product, installs the product from its Proxmox apt repository,
provisions an API token, and creates the matching `pbs-e2e`/`pdm-e2e`
contexts; `make test-e2e-stack` then runs the full sweep with those contexts
wired in, and `make stack-down` destroys the guests again. See
[`docs/e2e-stack.md`](docs/e2e-stack.md) for details.

`scripts/lifecycle` (`make test-lifecycle`) is the destructive counterpart, and
`scripts/e2e --mutate` runs the read-only sweep and then this mutate phase in
one invocation. It provisions an isolated `pmxcli` SDN (zone, vnet, and a
10.241.0.0/24 subnet off the host management network) and a `pmx-cli` resource
pool, then drives a throwaway QEMU VM and an LXC container through **every**
mutating sub-command — the full power-state matrix
(`start`/`stop`/`shutdown`/`reboot`/`reset`/`suspend`/`resume`) plus
`snapshot create`/`rollback`/`delete` — recording each verb individually, and
tears everything down. Every created resource is tagged `pmx-cli`, placed in the
`pmx-cli` pool, and attached to the isolated SDN, so other efforts on a shared
lab are never disturbed. Teardown always runs, and a crashed prior run is swept
clean before the next provisions. Two verbs are environment-bound and recorded
as SKIP with their reason rather than run as failures: qemu `reboot` (a diskless
VM has no guest OS to ACPI-reboot — the verb is proven on the Alpine container)
and lxc `suspend`/`resume` (need working CRIU support on the host).

```bash
make test-e2e-mutate                       # read-only sweep + the destructive verb matrix
make test-lifecycle                        # the destructive verb matrix only, against `lab`
make test-lifecycle CONTEXT=prod
scripts/e2e --mutate --vm-only             # sweep + VM verb matrix (skip the container)
scripts/lifecycle --vm-only                # VM verb matrix only
scripts/lifecycle --ct-only                # container verb matrix only
```

See [`docs/test-coverage-matrix.md`](docs/test-coverage-matrix.md) for a
per-leaf-command map of e2e and mutate-phase coverage.

## Architecture

The binary entry point is `cmd/pmx`; all logic lives under `internal/`:

- `internal/cli` — the cobra root, persistent flags, dependency wiring, and one
  package per command group under `internal/cli/<group>/`.

- `internal/apiclient` — a thin wrapper assembling the `proxmox-apiclient-go`
  service handles (PVE and PBS), UPID extraction, and task-wait helpers.

- `internal/config` — config types, loader, atomic writer, and secret resolver.

- `internal/output` — the `table`/`plain`/`json`/`yaml` renderer.

- `internal/logx` — the JSONL slog logger.

- `internal/exec`, `internal/nodeaddr` — shell-out runner and node-address
  resolution for SSH/rsync.

See [`docs/DESIGN.md`](docs/DESIGN.md) for the full design.
