# pmx-config.5

This file is never rendered on its own; run's man5 renderer (see main.go)
drops everything through the first ".SH" macro that md2man emits below (the
title-block preamble triggered by this leading heading) and prepends its own
hand-built .TH line so the section number, date, and version stay consistent
with the man1 pages.

# NAME

pmx-config - configuration file format for pmx(1), pve(1), pbs(1), and pdm(1)

# SYNOPSIS

**$XDG_CONFIG_HOME/pmx/config.yml**, defaulting to **~/.config/pmx/config.yml**

# DESCRIPTION

pmx and its persona binaries (**pve(1)**, **pbs(1)**, **pdm(1)**) read a
single YAML configuration file that holds one or more named **contexts**,
each describing a Proxmox VE, Proxmox Backup Server, or Proxmox Datacenter
Manager API endpoint plus the credentials used to reach it. The file is
looked up at **$XDG_CONFIG_HOME/pmx/config.yml**, or **~/.config/pmx/config.yml**
when **XDG_CONFIG_HOME** is unset; **--config** overrides the path for a
single invocation. A missing file is not an error: it is treated as an empty
configuration with no contexts, so a first run always needs either
**pmx init config** to scaffold a commented template or **pmx context add**
to write one context directly.

The file is written with mode 0600 wherever pmx writes it (**pmx init config**,
**pmx context add**, **pmx context edit**, and password-login session
updates), since a context's **auth.secret** may hold plaintext credentials.
Values are read with the strict 3-tier precedence described under
**CONTEXT KEYS** below (environment-variable reference, keychain reference,
or literal), so a shared or version-controlled config need not itself hold a
plaintext secret.

# TOP-LEVEL KEYS

**current-context**
: Name of the context used when neither **--context**/**-c** nor
  **PMX_CONTEXT** is given. Must match a key under **contexts**.

**previous-context**
: Name of the last active context, maintained automatically by
  **pmx context select** and consulted by **pmx context previous**. Not
  normally hand-edited.

**default-output**
: Default output format for every command when a context does not override
  it and **--output**/**-o** is not given: one of **table**, **ascii**,
  **plain**, **json**, or **yaml**.

**contexts**
: A map from context name to a context block (see **CONTEXT KEYS**). The map
  key is an arbitrary label chosen by the operator (for example **lab**,
  **prod-pbs**, or **dc1**) and is what **--context**, **current-context**,
  and **previous-context** refer to; it is never derived from the host name.

# CONTEXT KEYS

Each entry under **contexts** is a mapping with the following keys.

**host**
: Hostname or IP address of the API endpoint. Required. For a PVE cluster,
  any cluster member works; the API redirects internally as needed.

**port**
: HTTPS (or HTTP) API port. If omitted, the default is chosen from
  **product**: **8006** for **pve**, **8007** for **pbs**, and **8443** for
  **pdm**.

**protocol**
: Connection scheme: **https** (default) or **http**. Only use **http**
  against a trusted, non-production endpoint.

**realm**
: Authentication realm used to qualify **auth.username** (for example
  **pam**, **pve**, or an LDAP/OIDC realm configured on the server). Defaults
  to **pam** when omitted.

**default-node**
: Node name substituted when **--node** is not given on the command line.
  Optional; most commands that need a node also accept **--node** directly.

**default-output**
: Per-context override of the top-level **default-output**. Optional.

**product**
: Which Proxmox product this context targets: **pve**, **pbs**, or **pdm**.
  An empty or omitted value means **pve**, which keeps configuration files
  written before **product** existed working unchanged.

**auth**
: Credential block for this context, described below. Required.

**auth.type**
: Authentication method: **token** (recommended) or **password**.

**auth.username**
: The **user@realm** identity the token or password belongs to (for example
  **root@pam** or **automation@pve**). Required for both **token** and
  **password** auth; for token auth it supplies the **user@realm** portion of
  the API token header, so a token context without it fails at the server
  with an authentication error rather than at config-parse time. Never
  include the **!token-id** suffix here.

**auth.token-id**
: The API token's name only (the part after **!** in
  **user@realm!token-id**), used when **auth.type** is **token**. Never
  include **@** or **!** in this field; a value containing either usually
  means the full **user@realm!token-id** string was pasted into the wrong
  field.

**auth.secret**
: The token value or password. Never a fixed variable name: pmx does not
  read a hardcoded environment variable for this. Instead, the string
  written here is resolved at connection time with the following
  precedence:

  1. **${VAR}** — read environment variable **VAR** (any name you choose);
     an unset variable is a hard error.
  2. **$VAR** — read environment variable **VAR** only when it looks like a
     valid variable name *and* is actually set; otherwise the whole string
     falls through to rule 4, so a literal secret that happens to start with
     **$** is not silently misread as an env lookup.
  3. **keychain:service** or **keychain:service/account** — look up a
     generic password in the macOS login keychain (macOS only; on other
     platforms this form errors, telling the operator to use **${VAR}**
     instead).
  4. Anything else — used verbatim as a plaintext literal. pmx emits a
     one-time warning to stderr the first time a literal secret is resolved,
     since committing this file then leaks the credential.

**auth.session**
: Ticket, CSRF token, and expiry timestamp cached after a successful
  password login (**auth.session.ticket**, **auth.session.csrf**,
  **auth.session.expires-at**). Written and cleared automatically by
  **pmx auth login** and **pmx auth logout**; not normally hand-edited, and
  absent entirely for token-auth contexts.

**tls**
: TLS verification settings for this context. Optional; omitting it keeps
  the default of full certificate-chain verification.

**tls.insecure**
: When **true**, disables TLS certificate verification entirely. Defaults to
  **false**. Intended for lab endpoints with a self-signed certificate you
  cannot otherwise pin; it also disables **tls.tofu** since there is nothing
  left to pin.

**tls.fingerprint**
: A pinned certificate fingerprint: 32 colon-separated hex byte pairs
  (SHA-256), matching the format Proxmox VE itself displays. When set, the
  connection succeeds only if the server presents this exact fingerprint,
  independent of the system trust store.

**tls.ca-cert**
: Path to a PEM-encoded CA certificate file used to verify the server
  instead of (or in addition to) the system trust store. Useful for an
  internal CA.

**tls.tofu**
: When **true**, enables Trust-On-First-Use fingerprint pinning: on an
  interactive terminal, an unrecognized certificate is shown to the operator
  (host and fingerprint) for a one-time accept/reject decision, and an
  accepted fingerprint is then persisted to this context so later
  connections do not prompt again. A non-interactive invocation always
  rejects an unrecognized certificate outright rather than prompting.
  Defaults to **false**, and is ignored entirely when **tls.insecure** is
  **true**.

**ssh**
: Per-context defaults for **pmx ssh** and **pmx rsync**. Optional; any
  field left unset falls back to that command's own compiled-in default
  rather than to a zero value.

**ssh.user**
: Default SSH login user for this context. Falls back to **root** when
  unset.

**ssh.port**
: Default SSH port for this context. Falls back to **22** when unset.

**ssh.identity**
: Path to a default SSH private key (identity) file for this context.
  Unset by default, meaning the SSH client's own key discovery is used.

# EXAMPLE

A config with two contexts: a PVE lab cluster reached over token auth with a
pinned certificate fingerprint, and a PBS host reached over token auth with
TLS Trust-On-First-Use enabled. Both secrets are environment-variable
references; the variable names are chosen by the operator and are not fixed
by pmx.

```yaml
current-context: lab
default-output: table

contexts:
  lab:
    host: pve1.example.com
    port: 8006
    protocol: https
    realm: pam
    default-node: pve1
    product: pve
    auth:
      type: token
      username: automation@pve
      token-id: cli
      secret: ${LAB_PVE_TOKEN}
    tls:
      fingerprint: "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
    ssh:
      user: root
      port: 22

  backup:
    host: pbs1.example.com
    protocol: https
    realm: pam
    product: pbs
    auth:
      type: token
      username: automation@pbs
      token-id: cli
      secret: ${BACKUP_PBS_TOKEN}
    tls:
      tofu: true
```

With this file, **pmx --context backup datastore ls** talks to the PBS host
using **$BACKUP_PBS_TOKEN**, while a bare **pmx node ls** (no **--context**)
uses **lab**, the value of **current-context**, over its pinned certificate.

# SEE ALSO

**pmx(1)**, **pmx-context(1)**, **pmx-context-add(1)**, **pmx-init(1)**,
**pmx-init-config(1)**, **pmx-auth(1)**
