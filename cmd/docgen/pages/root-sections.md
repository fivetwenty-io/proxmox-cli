# root-sections

This file is never rendered on its own; injectRootSections (see main.go) drops
everything through the first ".SH" macro that md2man emits below (the
title-block preamble triggered by this leading heading) and splices only the
ENVIRONMENT / FILES / EXIT STATUS sections into each root page.

# ENVIRONMENT

**PMX_CONTEXT**
: Context name override, taking precedence over current-context in the config
  file (see **--context**).

**PMX_NODE**
: Default Proxmox node name (see **--node**).

**PMX_OUTPUT**
: Default output format: table, ascii, plain, json, or yaml (see **--output**).

**XDG_CONFIG_HOME**
: Base directory for the configuration file; defaults to **~/.config** when unset.

# FILES

**~/.config/pmx/config.yml**
: YAML configuration: contexts, current-context, and defaults. The
  **auth.secret** field for a context may reference an environment variable
  with **${VAR}** or **$VAR** instead of storing a plaintext secret. See
  **pmx-config(5)**.

**~/.pmx/logs/**
: Per-invocation JSONL logs, nested under per-command directories by
  default (configurable via the **log** key in **pmx-config(5)**);
  suppress with **--no-log**.

# EXIT STATUS

**0**
: Success.

**1**
: Generic or unclassified error.

**2**
: Invalid arguments or parameter validation failure.

**3**
: Infrastructure error: connection, TLS, or timeout reaching the API.

**4**
: Authentication or authorization failure.

**5**
: Requested resource not found.

**6**
: Resource conflict: already exists, locked, or in use.

**7**
: Two-factor authentication required.
