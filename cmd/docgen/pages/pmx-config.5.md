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
  of config.yml; the lab schema (see **LAB KEYS**) has no password field of
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

**log**
: A mapping of JSONL command-log preferences with three optional keys.
  **log.layout** selects where log files are written under **~/.pmx/logs**:
  **nested** (the default) writes each command's log into a per-command
  directory tree named after the full command path with a bare timestamp
  filename (for example
  **~/.pmx/logs/pve/storage/volume/copy/20260714-132051.jsonl**), while
  **flat** writes every log directly into **~/.pmx/logs** with the command
  path encoded in the filename (for example
  **~/.pmx/logs/pve-storage-volume-copy-20260714-132051.jsonl**). The
  **PMX_LOG_LAYOUT** environment variable overrides this key; any value
  other than **flat** means **nested**. **log.level** sets the minimum
  record level written to the log: one of **trace**, **debug**, **info**
  (the default), **warn**, or **error**. The **PMX_LOG_LEVEL** environment
  variable overrides this key, and the **--debug**, **--verbose**, and
  **--trace** flags force debug-level logging regardless of it.
  **--no-log** suppresses log files entirely. **log.retention** sets the
  number of days to keep log files: when positive it supplies the default
  cutoff for **pmx logs prune** and enables an automatic equivalent prune
  (including removal of empty log files and emptied directories) at most
  once per 24 hours after a command completes. Unset, zero, or negative
  disables both.

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

  1. **${VAR}**: read environment variable **VAR** (any name you choose);
     an unset variable is a hard error.
  2. **$VAR**: read environment variable **VAR** only when it looks like a
     valid variable name *and* is actually set; otherwise the whole string
     falls through to rule 4, so a literal secret that happens to start with
     **$** is not silently misread as an env lookup.
  3. **keychain:service** or **keychain:service/account**: look up a
     generic password in the macOS login keychain (macOS only; on other
     platforms this form errors, telling the operator to use **${VAR}**
     instead).
  4. Anything else: used verbatim as a plaintext literal. pmx emits a
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
  pmx auth now verifies a context's tls.ca-cert the way every other command does, so an authentication call against a server whose certificate chains only to a system root fails where it used to succeed.

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
  **ssh.user**, **ssh.port**, and **ssh.identity** configure the Proxmox
  node's ssh login only. The API bastion named by **ssh.jump** takes its user
  and port from the hop string and its key from **~/.ssh/config** or the
  agent, exactly as **pmx ssh -J** does, so none of the three reaches the
  bastion.

**ssh.user**
: Default SSH login user for this context. Falls back to **root** when
  unset.

**ssh.port**
: Default SSH port for this context. Falls back to **22** when unset.

**ssh.identity**
: Path to a default SSH private key (identity) file for this context.
  Unset by default, meaning the SSH client's own key discovery is used.

**ssh.jump**
: Default jump host to tunnel through, as **[user@]host[:port]**, or a
  comma-separated chain. Passed to ssh as **-J**. Unset by default, meaning a
  direct connection; the literal **none** means the same, for the API
  connection and for **pmx ssh** and **pmx rsync** alike. Set this when the
  context's nodes are not routable from where **pmx** runs: every ssh-based
  command picks it up, including the **pmx lab** verbs that reach lab guests
  on their own SDN mgmt IPs, which are reachable only from inside the
  context's network. Override per invocation with **-J/--jump**
  (**--ssh-jump** on **pmx rsync**).

  The Proxmox API connection tunnels through it too, so a context whose **host** is reachable only from the bastion works without any further setup. **pmx** runs **ssh -W** for each API connection, which means the same keys, agent, **known_hosts**, and **~/.ssh/config** the ssh transport already uses apply unchanged. TLS is still negotiated against the context's **host** at the far end, so certificate verification, a pinned **fingerprint**, and **tofu** all behave exactly as they do on a direct connection.

  Each hop is written as **[user@]host[:port]** or **ssh://[user@]host[:port]**, and whitespace around a hop is ignored. A host is a name, an IPv4 address, or a bracketed IPv6 literal such as **[2001:db8::1]:22**. A name may use letters, digits, **_**, **.**, and **-**, so **~/.ssh/config** aliases such as **my_bastion** work. A bare IPv6 literal without brackets is accepted only as the last plain hop, where no port can follow it. A user is made of letters, digits, **.**, **_**, **-**, and **@**. A plain hop splits at its last **@**, so **alice@corp.example@bastion** logs in to **bastion** as **alice@corp.example**. An **ssh://** hop splits at its first **@**, as OpenSSH's own parser does, so an **@** inside that form's user is written **%40**, as in **ssh://alice%40corp.example@bastion:2222**. pmx checks the chain without resolving or dialling anything, and it rejects a hop that carries a shell metacharacter, a control character, or a leading **-**, because OpenSSH releases before 10.3 run **-J** through **/bin/sh**. Every API command fails on a rejected chain, and **pmx context add**, **pmx context update**, and **pmx context edit** refuse to save one. Each of them reports the chain as **ssh.jump "<chain>" is not valid:** followed by the reason.

  The API bastion hop runs with BatchMode=yes and without a terminal, so it cannot prompt for a password, a second factor, or an unknown host key; run ssh <bastion> once to accept its host key before the first API command.

  pmx passes **BatchMode=yes** and a **ConnectTimeout** taken from **timeout.connect** to the last hop only. Intermediate hops of a chain take **BatchMode** and **ConnectTimeout** from **~/.ssh/config** rather than from pmx, so set both there for every bastion. The ssh child runs without **DISPLAY** and **WAYLAND_DISPLAY** in its environment, so no OpenSSH version, including one older than 8.4, can open a graphical askpass for any hop. A security key on the bastion hop, such as an **ed25519-sk** key, gets no touch prompt under pmx, because the ssh child has no terminal and no display. Load such a key into **ssh-agent**, whose own notifier still works.

  Every API connection starts its own ssh process, so a verb that fans out over many connections authenticates to the bastion once per connection. Setting **ControlMaster auto** with a **ControlPath** and a **ControlPersist** interval for the bastion host in **~/.ssh/config** turns that into one authentication per session.

  A failing bastion costs one attempt per client wherever the first-byte timer is armed (see **timeout.tls-handshake**), because pmx blocks new dials through that client for ten seconds after the failure instead of starting ssh again. A **pmx context validate --all --connect** sweep through a bastion that rejects the key therefore makes one failed attempt per context.

  The jump host resolves **host** itself, since it is the end that opens the forwarded connection. A name that resolves only on your workstation (a **/etc/hosts** entry, or a split-DNS or MagicDNS name the bastion does not share) leaves the API hanging until the request times out, while ssh-based commands to the same context still work. Give **host** an address the jump host can reach when the two do not share a resolver. A SOCKS5 proxy resolves the target name at the proxy instead, so a **socks5h://** URL in **proxy.url** is an alternative to giving **host** an address the bastion can resolve.

  **-J/--jump** overrides the ssh transport for one invocation, and **--api-jump** overrides the API connection's bastion. When neither flag is given, both connections use **ssh.jump**. **--api-jump none** dials the API direct even when the context sets a bastion. See **CONNECTION OVERRIDES** below.

**proxy**
: Outbound proxy for this context's Proxmox API connection. Optional; omitting
  it connects direct. The block covers the API connection only, and
  **pmx ssh**, **pmx rsync**, and the other ssh-based commands never use it.
  The block must be a mapping, so a scalar such as
  **proxy: socks5h://proxy:1080** fails the load with **proxy must be a
  mapping, e.g. proxy: {url: socks5h://host:1080}**. When **ssh.jump** is also
  set, the bastion reaches the proxy and the proxy reaches **host**, so the
  proxy must be reachable from the bastion.

**proxy.url**
: The proxy to send API requests through. pmx accepts three schemes, which are
  **socks5://**, **socks5h://**, and **http://**, and an **https://** proxy is
  not accepted in this release. The URL must include a host and must not carry
  credentials, which belong in **proxy.username** and **proxy.password**. It
  names the proxy by scheme, host, and port alone, so it must not carry a path
  other than a bare **/**, a query, or a fragment either.
  Unset by default, meaning a direct connection. An **http://** proxy carries
  an **https** connection through a CONNECT tunnel, so TLS still runs end to
  end between pmx and **host**. A **protocol: http** context has no TLS to
  protect it. An **http://** proxy then receives every API request in clear
  text, including the API credentials in its headers, and a SOCKS5 proxy can
  read the same clear text as it relays it. A bad value is reported with one
  of these messages, in which the URL is shown with any credential masked:

  - **proxy.url <url> is not a valid URL**
  - **proxy.url <url> must use scheme socks5, socks5h, or http**
  - **proxy.url <url> must include a host**
  - **proxy.url <url> must use a port from 1 to 65535**
  - **proxy.url <url> must not carry a path, a query, or a fragment; a password
    that contains a reserved character such as /, ?, or # must be
    percent-encoded**
  - **proxy.url <url> must not embed credentials; use proxy.username and
    proxy.password**
  - **proxy.url and proxy.from-env are both set; use one or the other**

**proxy.username**
: User name presented to the proxy, either for SOCKS5 username authentication
  or in the **Proxy-Authorization** header that an **http://** proxy receives.
  Unset by default. It requires **proxy.url**, and setting it without one
  fails with **proxy.username is set but proxy.url is empty**.

**proxy.password**
: Password for **proxy.username**, resolved with the same precedence as
  **auth.secret**, which accepts **${VAR}**, **$VAR**,
  **keychain:service/account**, or a literal that triggers the same one-time
  warning. pmx resolves it only when it builds the connection, never while it
  reads the file. It requires **proxy.url** and **proxy.username**, and the
  messages for a missing one are **proxy.password is set but proxy.url is
  empty** and **proxy.password is set without proxy.username**. The credential
  crosses the network in clear text to a **socks5** or **http** proxy, so
  anyone who can watch the path between pmx and the proxy can read it.

**proxy.from-env**
: When **true**, pmx honours **HTTPS_PROXY** (or **HTTP_PROXY** for a
  **protocol: http** context) and **NO_PROXY** for this context, as Go's
  standard library reads them. **ALL_PROXY** is not honoured. Defaults to
  **false**, and while it is off pmx ignores those variables whatever the
  shell exports. It cannot be combined with **proxy.url**.
  **--api-proxy-from-env** turns it on for one invocation, and
  **--api-proxy-from-env=false** overrides a context that enables it.
  pmx context validate --connect no longer honours HTTPS_PROXY on its own; set proxy.from-env: true on the context, or pass --api-proxy-from-env, to route the probe through the proxy environment.

**timeout**
: Bounds on this context's API transport. Optional; each key left unset uses
  its built-in default. The block must be a mapping, so a scalar such as
  **timeout: 30s** fails the load with **timeout must be a mapping, e.g.
  timeout: {connect: 5s}**. Each value is a Go duration such as **5s**,
  **500ms**, or **1m30s**, and it must be greater than zero. A bad value fails
  with **timeout.connect "<value>" is not a duration (e.g. 5s, 500ms)** or
  **timeout.connect must be greater than zero**, with the key's own name in
  place of **timeout.connect**. To return a key to its default, remove it, or
  pass an empty value to the matching
  **--timeout-connect**, **--timeout-tls-handshake**, or **--timeout-request**
  flag of **pmx context update**.

**timeout.connect**
: Bound on TCP connection setup. Defaults to **5s**. Behind a proxy it covers
  only the connect to the proxy. Through a bastion it also becomes ssh's
  **ConnectTimeout** for the last hop, and it is added to the handshake bound,
  as **timeout.tls-handshake** describes. The client library's connect and
  handshake bounds have one-second granularity, so pmx rounds both up to whole
  seconds, and **500ms** behaves as **1s**.

**timeout.tls-handshake**
: Bound on the TLS handshake. Defaults to **10s**, and it rounds up to whole
  seconds as **timeout.connect** does. When the API connection goes through a
  bastion, pmx arms its own first-byte timer on every https route and on an
  http route behind a SOCKS proxy. The timer runs for **timeout.connect** plus
  this value, and pmx never lets that sum fall below one second. The request
  bound then caps the timer at that bound minus the lesser of one second and a
  quarter of it, and a capped timer can fall below one second. When the timer
  fires before the first byte arrives, pmx ends the ssh child and reports the
  bastion as unresponsive. The transport's own handshake bound through a
  bastion is the uncapped sum plus one second, which keeps it past the timer.
  A **protocol: http** jump route without a SOCKS proxy arms no timer, so a
  slow response there is never cut off.

**timeout.request**
: Bound on one API request. Defaults to **30s**, and it is not rounded. The
  bound applies per attempt, and it covers the whole body of an upload or a
  file-restore download, so a large transfer over a slow link needs a larger
  value. An idempotent request can take up to four attempts plus about six
  seconds of backoff, so a SOCKS proxy that accepts the connection and then
  stalls can hold one request for about two minutes at the thirty-second
  default. Through a bastion the request bound also caps the first-byte timer,
  so a short request bound shortens the bastion's time to answer. A request
  bound of five seconds leaves the bastion four seconds, two seconds leaves it
  one and a half, and one second leaves it 750 milliseconds.
  **pmx context validate --connect** keeps its own five-second probe bound
  unless this key, **--api-request-timeout**, or **PMX_API_REQUEST_TIMEOUT**
  sets the value.

# CONNECTION OVERRIDES

Nine root flags override a context's API connection for one invocation, and
eight environment variables mirror all of them except
**--api-proxy-from-env**. They change only the API connection. **pmx ssh**,
**pmx rsync**, and the other ssh-based commands keep reading the context's
**ssh** block and their own **-J/--jump**. An override is never written to the
configuration file, so use **pmx context update** to store a setting.

Connection settings resolve flag > environment variable > context config > built-in default.

On a command that connects, a flag or a variable given an empty value counts
as unset. Every variable that overrides a context prints one **note:** line on
standard error, once per invocation, such as **note: $PMX_API_JUMP
(admin@bastion.example.com) overrides the bastion of context "lab"**. A flag
prints no note, because a flag on the command line is never ambient. A command
that never connects refuses these flags with a message such as **--api-jump
has no effect on pmx context ls**, while the variables stay silent there.

The live probe of **pmx context validate --connect** reports the route each context takes.
pmx context validate --connect prints a VIA column between REACHABLE and PRODUCT, so a script that reads the table by column position should use --output json instead.

**--api-endpoint**
: Replaces the context's **host**, and its **port** and **protocol** when the
  value carries them, as **[scheme://]host[:port]**. A component the value
  omits keeps the context's value. The scheme must be **https** or **http**,
  and the host may be a bracketed IPv6 literal such as **[2001:db8::1]:8006**.
  The value must not carry credentials, a path, a query, or a fragment. An
  override cannot turn an **https** context into **http**, so set **protocol:
  http** on the context when that is intended. Its environment variable is
  **PMX_API_ENDPOINT**.

**--api-jump**
: Replaces **ssh.jump** for the API connection, with the same hop grammar. The
  value **none** dials direct even when the context sets a bastion. Its
  environment variable is **PMX_API_JUMP**.

**--api-proxy**
: Replaces **proxy.url**, with the same scheme, host, port, and path rules. The
  value **none** disables a configured proxy, including one that
  **proxy.from-env** selects. The flag must not carry credentials, which would
  show in the process list, so pmx refuses any userinfo in it, a bare user
  name included. An override URL never picks up the context's
  **proxy.username** and **proxy.password**, so a proxy that needs a password
  for one invocation takes it from the flag's environment variable,
  **PMX_API_PROXY**, whose URL may carry a user name and password. Any
  reserved character in that password must be percent-encoded, such as
  **%2F** for **/**. An unescaped **/**, **?**, or **#** ends the host early, so pmx
  refuses the URL rather than dial the wrong proxy.

**--api-proxy-from-env**
: Turns **proxy.from-env** on for one invocation, and
  **--api-proxy-from-env=false** turns off a context's **proxy.from-env**. It
  conflicts with a proxy URL from **--api-proxy** or **PMX_API_PROXY**. It has
  no environment variable, so the environment alone can never switch on an
  ambient proxy.

**--api-ca-cert**
: Verifies the server against the PEM CA certificate file at this path for one
  invocation. It replaces the context's whole trust mode, meaning
  **tls.insecure**, **tls.fingerprint**, **tls.ca-cert**, and **tls.tofu**,
  and it cannot be combined with **--insecure** or **--api-fingerprint**. Its
  environment variable is **PMX_API_CA_CERT**.

**--api-fingerprint**
: Pins the server's certificate to this SHA-256 fingerprint, written as 32
  colon-separated hex pairs, for one invocation. It replaces the context's
  whole trust mode, so it never prompts, and it cannot be combined with
  **--insecure** or **--api-ca-cert**. Its environment variable is
  **PMX_API_FINGERPRINT**.

**--api-connect-timeout**
: Replaces **timeout.connect**, under the same duration rules. Its environment
  variable is **PMX_API_CONNECT_TIMEOUT**.

**--api-tls-handshake-timeout**
: Replaces **timeout.tls-handshake**, under the same duration rules. Its
  environment variable is **PMX_API_TLS_HANDSHAKE_TIMEOUT**.

**--api-request-timeout**
: Replaces **timeout.request**, under the same duration rules. Its environment
  variable is **PMX_API_REQUEST_TIMEOUT**.

Five limits apply to the overrides and to the routes they select.

When the client library cannot reach a proxy, it retries an idempotent
request, which can then take up to four attempts and about six seconds of
backoff before the command fails. A bastion failure ends after one attempt per
client wherever the first-byte timer is armed, and it blocks new dials through
that client for ten seconds, so a failing bastion sees one authentication per
client rather than one per retry.

Under **--api-endpoint**, the OpenID login of **pmx auth login --oidc** keeps
the context's stored endpoint as its redirect URL, so a tunnelled login needs
no extra flag, and **--redirect-url** still overrides it.

On Windows the bastion needs the OpenSSH client, **ssh.exe**, on **PATH**. Its
connection has no deadlines, and closing it waits for ssh to exit or be
killed. An intermediate hop without **BatchMode** in **~/.ssh/config**
can still stall on a prompt until the first-byte timer fires. When that timer
fires, pmx ends **ssh.exe** at once, but an intermediate **ssh.exe** that a
**-J** chain started survives until its own connection ends.

A pinned **tls.fingerprint** keeps applying under **--api-endpoint**, so a
tunnel to the same node works, and another node fails the handshake and needs
**--api-fingerprint**. Trust on first use cannot accept anything new for that
invocation, so a tunnel through another address to a **tls.tofu** context
needs **--api-fingerprint** too. A certificate that the context already
trusted for the same host name still verifies.

A saving verb such as **pmx context select** or **pmx auth login**, run by an
older pmx against a file this version wrote, drops the **proxy** and
**timeout** blocks from every context, including any proxy credential
reference. Upgrade every copy of pmx that shares one configuration file at the
same time.

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
   (**<labs_dir>/*.yaml**); it is the same code path, not a special case.

A lab's name is its **name** key when set, else (for an inline entry) its
map key, else (for a file) the file's basename with **.yaml** stripped. A
name that resolves from two different sources (two inline entries, an
inline entry and a file, or two files) is a hard configuration error
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
field in **LAB KEYS**, without requiring **labs_dir** to already be set. A
field that would contradict the example's own shape (**network.zone_peers**,
which belongs to a **vxlan** zone, while the example is a **simple** one)
is documented in a comment instead of being rendered as a key.

# LAB KEYS

Each lab block, whether inline under **labs** or the top level of a
**labs_dir**/**include** file, has the following keys. Defaults noted below
are the lab schema's own zero-value behavior; **pmx lab config add**
additionally applies its own fleet-wide starting values (vcpu 16, memory
32-96 GB, 64 GB OS disk, 400 GB data disk, 480 GB refquota, pool **tank**,
mode **nested**, role **PVEVMUser**, zone **labs** of type **simple**)
before any **--flag** override, and
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

**network.zone_name**
: SDN zone this lab's vnet lives in. Defaults to **labs**, the shared
  outer zone; the zone is not fixed by the tool, and two labs may sit in
  different zones. **pmx lab create** and **pmx lab net apply** create the
  zone when it is absent and otherwise update only the fields that have
  drifted (MTU, peers, node membership).

**network.zone_type**
: PVE SDN zone plugin used when this lab's zone is created. Defaults to
  **simple**; **vxlan** is the other type the lab verbs model. Only
  consulted when the zone does not yet exist; an existing zone keeps its
  live type.

**network.zone_peers**
: Underlay peer list for a **vxlan** zone. Ignored when the effective
  **network.zone_type** is **simple**, whose plugin has no peers field.

**network.vxlan_tag**
: VXLAN tag assigned to the vnet. Must be unique across every lab on a
  given fleet; **pmx lab config add** requires this to be set and > 0. It
  is omitted from the vnet's create and update calls when the zone is a
  **simple** zone: that plugin has no vnet-level tag concept and PVE
  rejects the parameter outright.

**network.cidr**
: Overall subnet CIDR allocated to the lab (for example
  **10.108.0.0/16**). Required by **pmx lab config add**. The address
  plan is validated against this CIDR: **pmx lab create** and **pmx lab
  config add** reject a lab whose **network.mgmt.subnet**,
  **network.mgmt.host_ip**, **network.mgmt.gateway**, or
  **network.bosh_bloc** falls outside it.

**network.ipv6**
: Whether the lab is dual-stack. Defaults to **true**: every addressed
  vnet gets an IPv6 subnet alongside its IPv4 one (**pmx lab create**,
  **pmx lab net apply**, **pmx lab sdn vlan apply**), and the nested
  nodes and QDevice VM get management IPv6 addresses (**pmx lab hostnet
  apply**, **pmx lab qdevice add**, and, for the nodes a transition
  brings into the cluster, **pmx lab scale** and **pmx lab cluster
  join**, which reconcile it against the lab's own nested context). Set
  to **false** to keep the lab
  IPv4-only; disabling it later stops further IPv6 provisioning but
  never deletes anything already created. Cluster formation, NFS, and
  pmx's own ssh transport stay on IPv4 either way.

**network.cidr6**
: The lab's IPv6 block, a prefix of **/48** or wider (its first /48 is
  used). Omitted means a stable RFC 4193 ULA /48 derived from
  **network.cidr** (the same lab file always derives the same block,
  and labs with different IPv4 CIDRs can never collide), so most labs
  never set this. Per-role /64s are carved from the block by fixed
  subnet IDs: management (gateway **::1**, node *i* at **::a**+*i*, the
  QDevice at **::f**, mirroring the IPv4 offsets), one per
  **network.vnets** entry (overridable per entry with its own
  **cidr6**; an override must be **/112** or wider so the derived **::1**
  gateway lands inside it), and one per nested client-VLAN vnet. The
  carving scheme fits at most 16 **network.vnets** entries; validation
  rejects more while IPv6 is enabled. Rejected when **network.ipv6** is
  **false**.

**network.snat6**
: Whether the lab's IPv6 subnets are masqueraded for egress. Defaults to
  **false**: a lab's IPv6 is ULA-internal unless egress is asked for.
  With **true**, **pmx lab create** and **pmx lab net apply** set the
  SDN subnet's **snat** flag on every IPv6 subnet they ensure (never on
  the IPv4 one). PVE only renders subnet SNAT on a **simple** zone, so
  validation rejects the combination with any other **network.zone_type**,
  and **pmx lab net apply** re-checks it even for a hand-edited lab file
  that never passed through **pmx lab config add**. Rendering further
  depends on the PVE host having an IPv6 route of its own: PVE resolves the
  egress interface by routing to 2001:4860:4860::8888 and, failing that,
  logs *interface for SNAT could not be resolved* and renders no rule, so
  **net apply** warns when the target node declares no IPv6 gateway
  anywhere. Like every other IPv6 field, turning it back off stops
  provisioning it and never strips the flag from a subnet that already
  carries it.

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

**storage.osd_disks.count**
: Number of extra whole-raw-device disks (**scsi2**, **scsi3**, ...)
  attached to every node VM for nested Ceph OSDs, 0 through 8. Requires
  **storage.controller: virtio-scsi-single**, because OSD disks are emitted with
  **iothread=1**, which PVE rejects on any other SCSI controller. Zero or
  unset means none, today's shape. Overridable per node with
  **topology.node_overrides.<n>.osd_disk_count**.

**storage.osd_disks.size_gb**
: Size in gigabytes of each OSD disk. Required (and must be positive) when
  **storage.osd_disks.count** is set. Every disk is emitted identically, as
  **discard=on,iothread=1,ssd=1,backup=0,serial=osdN** (N starting at 0),
  and PBS never backs one up. The serial is how **pmx lab ceph osd** picks
  each disk out of the node's own disk listing, which is stable where a
  **/dev/sdX** kernel name is not; the OSD is then created on whatever device
  path that listing reports. Note that the serial does not name the disk's
  **/dev/disk/by-id** symlink: on PVE 9.2 that link is keyed off the drive
  name, and the serial surfaces only as the **ID_SCSI_SERIAL** udev property
  that **lsblk** and the disk listing report. On an already-running
  VM, **discard** and **ssd** only take effect after a full power-off and
  power-on (a reboot is not enough), though this never matters for
  **pmx lab create**, since the VM has not booted yet. OSD disks raise the
  *default* **storage.refquota_gb** derivation (count × size_gb, added per
  node) but never augment an explicit **storage.refquota_gb**: the operator
  who pins one owns the whole number. Overridable per node with
  **topology.node_overrides.<n>.osd_disk_gb**.

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

**topology.nodes**
: Number of PVE node VMs in the lab's nested cluster, 1 through 5 (node
  indexes 0 through nodes-1). Zero or unset defaults to 1, today's
  single-node shape, unchanged. **pmx lab config add --nodes N** and
  **pmx lab create --nodes N** both set this.

**topology.qdevice**
: QDevice tie-breaker policy for the nested cluster: **auto** (the default
  when empty) adds a QDevice VM only when the effective **topology.nodes**
  is even; **never** never adds one, regardless of node count. No other
  value is valid. **pmx lab config add --qdevice auto|never** and
  **pmx lab create --qdevice auto|never** both set this.

**topology.node_overrides.<n>**
: Optional per-node sizing overrides, keyed by 0-based node index (0
  through 4, and must be below the lab's own effective **topology.nodes**;
  index 3 is invalid for a 2-node lab). A node index absent from this map
  uses the lab's own Compute/Storage values (which are themselves layered
  over the sizing profile the effective node count selects: 16 vCPU /
  32-128 GB memory / 64 GB OS disk / 400 GB data disk for a single-node
  lab, 8 vCPU / 16-48 GB / 64 GB / 200 GB for a multi-node one). Within a
  present entry, a zero-valued field falls through the same way; there is
  no sizing dimension for which zero is a meaningful override. Keys:

  - **vcpu**: vCPU count for this node.
  - **memory_min_gb** / **memory_max_gb**: guaranteed/ballooned memory
    for this node.
  - **os_disk_gb** / **data_disk_gb**: OS/data disk size for this node.
  - **osd_disk_count** / **osd_disk_gb**: this node's
    **storage.osd_disks.count**/**size_gb**.

# PERMISSIONS

config.yml is checked for group- or world-accessible permission bits
(**mode & 0077 != 0**) whenever **default_user_password** is set to a
non-empty value: loading the config then fails with a message naming the
file and telling the operator to **chmod 0600** it. This check runs only
when **default_user_password** is present (a config file with no lab
password configured is not stat'd or rejected on this basis) because that
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

**proxy.password** is redacted the same way **auth.secret** is.
**pmx context show** prints a literal value as a fixed mask and shows an
environment-variable or keychain reference as it is, since a reference reveals
nothing on its own. A **$NAME** reference whose variable is unset is masked
too, because pmx would use it as a literal password. Every message, note, and
log record that prints a proxy URL masks any credential embedded in it, and
the resolved proxy password never appears in any of them.

# EXAMPLE

A config with three contexts: a PVE lab cluster reached over token auth with a
pinned certificate fingerprint, a PBS host reached over token auth with
TLS Trust-On-First-Use enabled, and a remote PVE node that sits behind a
bastion and a SOCKS5 proxy with raised timeouts. Every secret is an
environment-variable or keychain reference; the variable names are chosen
by the operator and are not fixed by pmx. Every auth block is written in
block style, because a flow mapping such as **auth: {secret: ${TOKEN}}** is
not valid YAML unless the reference is quoted.

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

  remote:
    host: pve1.internal
    product: pve
    auth:
      type: token
      username: automation@pve
      token-id: cli
      secret: ${REMOTE_PVE_TOKEN}
    ssh:
      jump: admin@bastion.example.com
    proxy:
      url: socks5h://proxy.dmz.example.com:1080
      username: pmx
      password: keychain:pmx-proxy/remote
    timeout:
      connect: 10s
      tls-handshake: 20s
      request: 120s
```

With this file, **pmx --context backup datastore ls** talks to the PBS host
using **$BACKUP_PBS_TOKEN**, while a bare **pmx node ls** (no **--context**)
uses **lab**, the value of **current-context**, over its pinned certificate.

Context **remote** takes two hops. pmx runs **ssh -W** through
**admin@bastion.example.com** to reach the proxy, and the proxy then connects
to **pve1.internal**. The proxy must therefore be reachable from the bastion.
Because the URL uses the **socks5h** scheme, the proxy resolves
**pve1.internal** itself. The raised
timeouts give the longer route more room, and they set the first-byte timer
through the bastion to 30 seconds, the connect bound plus the handshake bound.

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
      zone_name: labs
      zone_type: simple
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

A third example is a multi-node lab for nested Ceph: three node VMs, no
QDevice (three is already odd), and two 100 GB raw OSD disks per node on
top of the OS and data disks.

```yaml
labs:
  ceph:
    mode: nested
    owner: wayne@pve
    topology:
      nodes: 3
      qdevice: never
    network:
      vnet_id: ceph
      vxlan_tag: 5015
      cidr: 10.252.0.0/16
    compute:
      vcpu: 8
      memory:
        min_gb: 24
        max_gb: 48
    storage:
      pool: tank
      os_disk_gb: 64
      data_disk_gb: 100
      refquota_gb: 1150
      controller: virtio-scsi-single
      osd_disks:
        count: 2
        size_gb: 100
```

**storage.controller: virtio-scsi-single** is mandatory here:
**storage.osd_disks.count** above 0 fails validation without it.
**storage.refquota_gb** is set explicitly to 1150 (roughly 364 GB
provisioned per node for OS, data, and OSD disks, times 3 nodes, plus EFI
and slack) rather than left for the *default* derivation (node-count ×
264 GB, plus every node's OSD disk total) that **pmx lab create** and
**pmx lab config add** apply automatically when **storage.refquota_gb** is
omitted; an explicit value like this one is never augmented further by
OSD disk size.

```bash
pmx lab config add ceph --vxlan-tag 5015 --cidr 10.252.0.0/16 \
  --nodes 3 --qdevice never --osd-disks 2 --osd-disk-gb 100 \
  --vcpu 8 --memory-max-gb 48 --data-disk-gb 100 --refquota-gb 1150
```

scaffolds the topology and OSD disk shape above in one step: **config add**'s
schema default for **storage.controller** is already **virtio-scsi-single**,
so **--osd-disks** needs no separate controller flag. There is no
**--memory-min-gb** flag, so **compute.memory.min_gb** still comes out at the
schema default (32); edit it in the scaffolded file directly for a value
other than that, such as the 24 shown above.

# SEE ALSO

**pmx(1)**, **pmx-context(1)**, **pmx-context-add(1)**, **pmx-init(1)**,
**pmx-init-config(1)**, **pmx-auth(1)**, **pmx-lab(1)**,
**pmx-lab-config(1)**, **pmx-lab-config-init(1)**, **pmx-lab-config-add(1)**,
**pmx-lab-config-show(1)**, **pmx-lab-access-grant(1)**,
**pmx-context-update(1)**, **pmx-context-validate(1)**, **ssh(1)**,
**ssh_config(5)**, **ssh-agent(1)**
