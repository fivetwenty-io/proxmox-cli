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

The same file can also describe one or more **labs**: nested lab
environments used by **pmx lab** and its sub-commands. See **LAB
CONFIGURATION** and **LAB KEYS** below for the **labs**, **labs_dir**,
**include**, and **default_user_password** top-level keys, and the full
schema of a lab block.

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

**default_user_password**
: Password assigned to a lab owner's pve-realm user account when **pmx lab
  access grant** finds that the user does not already exist. Optional; when
  unset, **pmx lab access grant** refuses to create a missing user and tells
  the operator to either set this key or create the account manually with
  **pmx pve access user create**. This key lives only here, at the top level
  of config.yml — the lab schema (see **LAB KEYS**) has no password field of
  its own, so a per-lab file under **labs_dir** or named by **include** can
  never carry this secret even if it is shared or committed separately from
  config.yml. Setting this key imposes a stricter file-mode requirement on
  config.yml than **auth.secret** alone does; see **PERMISSIONS** below. No
  command ever prints this value; see **SECRET REDACTION** below.

**labs_dir**
: Path to a directory of **<name>.yaml** files, each holding one lab
  definition in the bare single-lab form described under **LAB FILES**
  below. A relative path is resolved against the directory containing
  config.yml itself, not the current working directory. Optional; every
  ***.yaml** file directly inside this directory is merged into the
  resolved lab set alongside any inline **labs** entries and any
  **include** globs (see **LAB CONFIGURATION**). **pmx lab config init** and
  **pmx lab config add** use this key, when set, as the directory they
  write new lab files into; when it is unset they use **labs.d** resolved
  against config.yml's directory, and print the **labs_dir:** line to add
  by hand rather than writing it into config.yml themselves.

**include**
: A list of glob patterns for additional per-lab YAML files to merge into
  the resolved lab set, alongside **labs_dir** and inline **labs** entries.
  Optional. Each pattern is expanded independently; a relative pattern is
  resolved against config.yml's own directory, an absolute pattern is used
  as-is, and a pattern matching zero files is not an error. Each matched
  file is parsed as a bare single-lab document (see **LAB FILES**), never as
  a nested **labs:** map.

**labs**
: A map from lab name to a lab block (see **LAB KEYS**), defined inline in
  config.yml itself rather than in a separate file. Optional; useful for a
  lab an operator wants to keep alongside the contexts in one file. An
  entry's **name** key defaults to its map key when omitted. See **LAB
  CONFIGURATION** for how this map combines with **labs_dir** and
  **include**.

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

# LAB CONFIGURATION

**pmx lab** and its sub-commands (**pmx lab create**, **pmx lab start**,
**pmx lab access grant**, and the rest) resolve their target's definition
from a single flat map of lab name to lab block, built by merging three
sources every time a lab verb runs:

1. Inline **labs** entries in config.yml itself.
2. Every file matched by a pattern in **include** (relative patterns
   resolved against config.yml's directory).
3. Every **<name>.yaml** file directly inside **labs_dir**, which is
   pure sugar for one more **include** pattern
   (**<labs_dir>/*.yaml**) — the same code path, not a special case.

A lab's name is its **name** key when set, else (for an inline entry) its
map key, else (for a file) the file's basename with **.yaml** stripped. A
name that resolves from two different sources — two inline entries, an
inline entry and a file, or two files — is a hard configuration error
naming both locations (for example **config.yml (inline)** and the
conflicting file's path); the merge never silently prefers one definition
over the other. **pmx lab config show <name>** reports which of these
sources a given lab resolved from.

Each file matched by **include** or **labs_dir** is parsed as a bare
single-lab YAML document: the file's top level IS the lab block described
under **LAB KEYS**, never a **labs:**-wrapped map copy-pasted from
config.yml. Two checks reject a malformed file outright rather than
silently accepting a hollow definition:

- An empty file, a whitespace/comment-only file, or an explicit **{}**
  is a hard error naming the file, rather than a lab silently created
  with every field at its zero value.
- The file is decoded strictly: any key not part of the lab schema
  (a typo such as **vxlan_tg**, or a stray **labs:** wrapper) is a hard
  error naming the file and the offending key, rather than being dropped
  silently.

**pmx lab config add <name>** is the normal way to create one of these
files: it writes a new, fully-commented **<labs_dir>/<name>.yaml** built
from schema defaults plus any flags given, and refuses to overwrite an
existing file, or to write a name that already resolves via config.yml,
unless **--force**. It never rewrites config.yml itself, so any comments an
operator has added there are preserved. **pmx lab config init** scaffolds
**<labs_dir>/example.yaml**, a fully-commented reference covering every
field in **LAB KEYS**, without requiring **labs_dir** to already be set.

# LAB KEYS

Each lab block, whether inline under **labs** or the top level of a
**labs_dir**/**include** file, has the following keys. Defaults noted below
are the lab schema's own zero-value behavior; **pmx lab config add**
additionally applies its own fleet-wide starting values (vcpu 16, memory
32-96 GB, 64 GB OS disk, 400 GB data disk, 480 GB refquota, pool **tank**,
mode **nested**, role **PVEVMUser**) before any **--flag** override, and
those are documented on **pmx-lab-config-add(1)**, not repeated here.

**name**
: Display name of the lab. Defaults to the lab's map key (inline) or
  filename stem (file-based) when omitted; **pmx lab config add** and
  **pmx lab config init** always write it explicitly.

**mode**
: How the lab is realized: **nested** (VM-in-VM; the only mode implemented
  today) or **hardware** (bare metal, reserved for future use).

**owner**
: The pve user this lab is assigned to, as **user@realm** (for example
  **wayne@pve**). Empty or **~** means no owner.

**network.vnet_id**
: SDN vnet identifier. Must be 1-8 alphanumeric characters with no hyphen;
  this format is enforced by **pmx lab config add**, not by the loader
  itself, so a hand-edited file with an invalid ID is only caught when a
  lab verb that provisions SDN state runs.

**network.vnet_alias**
: Human-readable label for the vnet.

**network.vxlan_tag**
: VXLAN tag assigned to the vnet. Must be unique across every lab on a
  given fleet; **pmx lab config add** requires this to be set and > 0.

**network.cidr**
: Overall subnet CIDR allocated to the lab (for example
  **10.108.0.0/16**). Required by **pmx lab config add**. The address
  plan is validated against this CIDR: **pmx lab create** and **pmx lab
  config add** reject a lab whose **network.mgmt.subnet**,
  **network.mgmt.host_ip**, **network.mgmt.gateway**, or
  **network.bosh_bloc** falls outside it.

**network.mgmt.subnet**
: Management subnet CIDR: an address-plan reservation within
  **network.cidr** marking which slice is set aside for management-plane
  hosts. It is NOT an interface prefix: the lab host's interface must be
  addressed with **network.cidr**'s own prefix length (e.g. host_ip/16
  for a /16 lab, even when this subnet is a /24). A narrower interface
  prefix makes the host route replies to on-link guests in the wider
  CIDR via the gateway, which drops them as out-of-state; **pmx lab
  status** flags such interfaces with a NETWORK_WARNING row.

**network.mgmt.host_ip**
: Management-plane IP address of the lab host. Must fall inside
  **network.cidr**.

**network.mgmt.gateway**
: Gateway address for the management subnet. Must fall inside
  **network.cidr**.

**network.bosh_bloc**
: Subnet range reserved for BOSH-deployed VMs inside the lab. Must fall
  inside **network.cidr**.

**network.mtu**
: MTU for the vnet. **pmx lab config add** writes **1450**.

**compute.vcpu**
: Number of virtual CPUs assigned to the lab's VM. Must be > 0.

**compute.cpu_type**
: QEMU CPU model presented to the guest (for example **host**).

**compute.numa**
: Whether NUMA topology awareness is enabled for the VM.

**compute.machine**
: QEMU machine type (for example **q35**).

**compute.firmware**
: VM firmware: **ovmf** for UEFI, **seabios** for legacy BIOS.

**compute.memory.min_gb**
: Minimum (guaranteed) memory for the VM, in gigabytes.

**compute.memory.max_gb**
: Maximum (ballooned) memory for the VM, in gigabytes. Must be > 0.

**storage.pool**
: Base ZFS pool name the lab's storage identifiers are derived from.
  Defaults to **tank** when empty. Every lab verb that touches storage
  derives two identifiers from this single base, so both always agree on
  which pool a lab's disks live on:

  - the PVE **storage.cfg** identifier, **<pool>-lab-<name>**;
  - the raw ZFS dataset path, **<pool>/labs/<name>**, which is the same
    path **pmx lab quota set** targets over ssh.

  For example, **storage.pool: tank** on a lab named **wayne** yields
  storage ID **tank-lab-wayne** and dataset **tank/labs/wayne**.

**storage.os_disk_gb**
: Size of the OS disk, in gigabytes. Must be > 0.

**storage.data_disk_gb**
: Size of the data disk, in gigabytes. Must be > 0.

**storage.refquota_gb**
: ZFS refquota enforced on the lab's dataset, in gigabytes. Must be > 0.

**storage.controller**
: Disk controller type (for example **virtio-scsi-single**).

**storage.iothread**
: Whether a dedicated I/O thread is enabled for the disk.

**storage.discard**
: Whether discard/TRIM passthrough is enabled for the disk.

**storage.ssd**
: Whether the disk is marked SSD-backed to the guest.

**dns.zone**
: DNS zone name associated with the lab (for example
  **wayne.lab.example.com**). No **pmx lab config add** flag sets this yet;
  it, and **network.mgmt**, are left for the operator to fill in by hand.

**provisioning.mode**
: Guest provisioning method (for example **answer-toml**).

**provisioning.answer_template**
: Path to the answer-file template used to provision the guest.

**provisioning.ssh_keys**
: List of SSH public keys injected into the guest. Empty list by default.

**access.realm**
: pve authentication realm the owner is granted access under (for example
  **pve**).

**access.pool**
: pve resource pool the lab's access grant is scoped to. Defaults to
  **lab-<name>** when empty; every lab verb that resolves a pool (create,
  destroy, access grant, start, stop) derives the same default, so omitting
  this key is safe as long as it stays omitted everywhere for that lab.

**access.role**
: pve role granted to the owner on **access.pool**. When empty,
  **pmx lab access grant** falls back to **--role** if given, else
  **PMXAdmin** (created automatically if it does not already exist); any
  other role named here or via **--role** must already exist on the target.

# PERMISSIONS

config.yml is checked for group- or world-accessible permission bits
(**mode & 0077 != 0**) whenever **default_user_password** is set to a
non-empty value: loading the config then fails with a message naming the
file and telling the operator to **chmod 0600** it. This check runs only
when **default_user_password** is present — a config file with no lab
password configured is not stat'd or rejected on this basis — because that
key is the one place in this file a plaintext bootstrap password for newly
created lab users can live. Files written by **pmx lab config init** and
**pmx lab config add** under **labs_dir** are themselves written at mode
0600 as a matter of course, even though the lab schema itself never carries
this or any other secret.

# SECRET REDACTION

**default_user_password** is never printed by any command. **pmx lab
access grant** (and its **--dry-run** preview) shows only a fixed
**<redacted>** placeholder in the plan line describing user creation, never
the configured value, and prints nothing about the password at all when
**default_user_password** is unset or the target user already exists. No
other lab command reads or displays this key.

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

A second example adds one inline lab, **wayne**, plus **labs_dir** for any
further labs kept as separate files. **default_user_password** is set here
only as an illustration; a real config.yml holding it must be mode 0600
(see **PERMISSIONS**), and the value shown is an obvious placeholder, never
a real password.

```yaml
current-context: lab
default-output: table
default_user_password: "changeme-example"
labs_dir: labs.d

contexts:
  lab:
    host: pve1.example.com
    product: pve
    auth:
      type: token
      username: automation@pve
      token-id: cli
      secret: ${LAB_PVE_TOKEN}

labs:
  wayne:
    mode: nested
    owner: wayne@pve
    network:
      vnet_id: wayne
      vnet_alias: lab-wayne
      vxlan_tag: 5001
      cidr: 10.108.0.0/16
      mgmt:
        subnet: 10.108.0.0/24
        host_ip: 10.108.0.10
        gateway: 10.108.0.1
      bosh_bloc: 10.108.16.0/20
      mtu: 1450
    compute:
      vcpu: 16
      cpu_type: host
      numa: true
      machine: q35
      firmware: ovmf
      memory:
        min_gb: 32
        max_gb: 96
    storage:
      pool: tank
      os_disk_gb: 64
      data_disk_gb: 400
      refquota_gb: 480
      controller: virtio-scsi-single
      iothread: true
      discard: true
      ssd: true
    dns:
      zone: wayne.lab.example.com
    provisioning:
      mode: answer-toml
      answer_template: templates/answer.toml.tmpl
      ssh_keys:
        - "~/.ssh/wayne-lab.pub"
    access:
      realm: pve
      pool: lab-wayne
      role: PMXAdmin
```

With **storage.pool: tank**, lab **wayne** derives PVE storage ID
**tank-lab-wayne** and ZFS dataset **tank/labs/wayne**. Any further lab
written with **pmx lab config add <name>** lands under **labs.d/** as
**<name>.yaml**, merged in alongside **wayne** at load time; a name
collision between an inline lab and a **labs_dir** file is rejected at
load time rather than silently resolved.

# SEE ALSO

**pmx(1)**, **pmx-context(1)**, **pmx-context-add(1)**, **pmx-init(1)**,
**pmx-init-config(1)**, **pmx-auth(1)**, **pmx-lab(1)**,
**pmx-lab-config(1)**, **pmx-lab-config-init(1)**, **pmx-lab-config-add(1)**,
**pmx-lab-config-show(1)**, **pmx-lab-access-grant(1)**
