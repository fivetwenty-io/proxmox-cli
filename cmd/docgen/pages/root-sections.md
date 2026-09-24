# root-sections

This file is never rendered on its own; injectRootSections (see main.go) drops
everything through the first ".SH" macro that md2man emits below (the
title-block preamble triggered by this leading heading) and splices only the
ENVIRONMENT / FILES / EXIT STATUS sections into each root page.

# ENVIRONMENT

**PMX_API_CA_CERT**
: Path to a PEM CA certificate file that replaces the context's trust settings
  (see **--api-ca-cert**).

**PMX_API_CONNECT_TIMEOUT**
: Bound on TCP connection setup, such as **5s** (see
  **--api-connect-timeout**).

**PMX_API_ENDPOINT**
: API endpoint override for one invocation, as **[scheme://]host[:port]**
  (see **--api-endpoint**).

**PMX_API_FINGERPRINT**
: SHA-256 certificate pin that replaces the context's trust settings (see
  **--api-fingerprint**).

**PMX_API_JUMP**
: ssh bastion chain for the API connection, or **none** to dial direct (see
  **--api-jump**).

**PMX_API_PROXY**
: Proxy URL for the API connection, or **none** to disable a configured
  proxy. Unlike the flag, it may carry the proxy's user name and password
  (see **--api-proxy**).

**PMX_API_REQUEST_TIMEOUT**
: Bound on each attempt of an API request, such as **30s** (see
  **--api-request-timeout**).

**PMX_API_TLS_HANDSHAKE_TIMEOUT**
: Bound on the TLS handshake, such as **10s** (see
  **--api-tls-handshake-timeout**).

**PMX_CONTEXT**
: Context name override, taking precedence over current-context in the config
  file (see **--context**).

**PMX_NODE**
: Default Proxmox node name (see **--node**).

**PMX_OUTPUT**
: Default output format: table, ascii, plain, json, or yaml (see **--output**).

**XDG_CONFIG_HOME**
: Base directory for the configuration file; defaults to **~/.config** when unset.

Each of the eight API connection variables above prints a **note:** line on
standard error when it overrides the context. The flag named in each entry
outranks its variable, and the variable outranks the context, as
**pmx-config(5)** describes under CONNECTION OVERRIDES.

# FILES

**~/.config/pmx/config.yml**
: YAML configuration: contexts, current-context, and defaults. The
  **auth.secret** field for a context may reference an environment variable
  with **${VAR}** or **$VAR** instead of storing a plaintext secret. See
  **pmx-config(5)**.

**~/.pmx/logs/**
: Per-invocation JSONL logs, nested under per-command directories by
  default (configurable via the **log** key in **pmx-config(5)**); every
  file opens with an invocation audit record and closes with an exit
  record. Suppress with **--no-log**; delete old files with
  **pmx logs prune** or the **log.retention** config key.

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
