# pmx CLI — Code Quality Audit

Date: 2026-07-31

Scope: the whole Go module at HEAD (~115k LOC non-test, 41 packages), scored
against the Go merge-gate checklist: formatting and style, package and API
design, readability, error handling, testing, concurrency and context,
performance, security, observability, and CI gates.

Method: six parallel adversarial review passes (one per checklist area), then
an independent verification round instructed to refute rather than confirm.
Every claim was checked against the source and, where it concerned runtime
behavior, against a running binary or a purpose-built harness.

The verification round earned its keep twice. It found the credential-in-logs
disclosure below, which the first pass had graded MEDIUM as "logging hygiene"
and cited only against a deliberately-misused passthrough flag; tracing it to
`node scan pbs`, where `--password` is mandatory, moved it to the most serious
finding here. And it corrected an over-reach in the opposite direction: `pmx
version` was reported as failing when it should print help, but it reports the
connected server's version and genuinely needs a client, so it was left alone.

Baseline before any change: `go build`, `go test`, `go test -race`, `go vet`,
`staticcheck`, and `golangci-lint` were all clean, and remained clean after
every commit. The defects below are the ones those tools cannot see.

## Closed

Twenty-seven commits, each with regression tests where behavior changed.
Grouped by what was actually wrong.

### Values reaching a remote root shell unvalidated

`lab.Name` was charset-checked before mutating verbs ran, and the comment
explaining why named dataset paths as the reason. The other half of those same
dataset paths was not checked. `storage.pool` flows through `zfsDatasetPath`
into `zfs set` / `zfs create` command lines that `quota set`, `create`, and
`nfs attach` hand to ssh, which joins its trailing argv with spaces and
evaluates the result in the remote root login shell.

The nested SDN identifiers had the same gap, with a sharper edge: the struct
docs asserted a charset constraint, and `ValidateNestedNetwork` delegated it in
a comment to a wrapper that turned out to be a pure passthrough. The outer vnet
IDs were validated against exactly the pattern the nested ones were missing.
`vnets[].alias` is documented free text, so it is shell-quoted at the point of
use rather than restricted.

`sdn vlan apply` now runs the coherence gate itself instead of assuming
`lab config add` wrote the config, so a hand-written lab cannot bypass it.

### Signals: an uninterruptible ssh, and an orphaned one

The parent shielded itself from SIGINT with `signal.Ignore` so it could survive
^C long enough to read the child's exit status. The comment claimed the
inherited `SIG_IGN` was harmless because ssh installs its own handlers. It does
not — ssh guards the install on the inherited disposition
(`if ssh_signal(SIGINT, SIG_IGN) != SIG_IGN`), so it kept the inherited
`SIG_IGN` and never installed a handler.

Every scripted ssh — `node exec`, `lab create`, `cluster join`, `quota set`,
snippet upload — was uninterruptible from the invoking terminal, by ^C or ^\,
with no way to abort a wrong destructive remote command. rsync was unaffected;
it installs unconditionally.

Reproduced two ways before the fix: a harness mirroring the runner (ssh child
survived SIGINT indefinitely; without the shield it died in 2s), and the same
asymmetry in a plain `/bin/sh` child, which is now the regression test.

Two adjacent defects shared that one design decision. A signal directed at pmx
alone (`kill <pid>`) never reaches the process group, so it killed pmx and left
ssh running a remote command unsupervised — now relayed. And the root context
was `context.Background()`, so nothing could observe cancellation: the
task-wait poll, the lab SSH wait, and the SDK's retry backoff all select on
`ctx.Done()` that could never fire, and ^C killed the process before the exit
audit record, the log close, or the retention prune ran.

Verified end-to-end against a real hung `node exec`: the command now exits on
^C, its ssh child exits with it, no orphan remains, and the exit record is
written.

### Waits with no wall-clock bound

`ConnectTimeout` bounds connection establishment only. A peer that goes silent
without a RST or FIN left ssh blocked indefinitely, and pmx blocked behind it.
That is an expected outcome here, not an exotic fault: `pvecm add` restarts
corosync, which reconfigures the very management network the ssh session
carrying that command travels over. Keepalives now bound an established
session for both scripted argv builders; interactive paths are untouched.

`clusterWaitForJoin` compounded it — no context, and its documented one-minute
ceiling counted sleep time only, while each of its 30 iterations made two
unbounded ssh calls. It now takes a context and checks it per attempt.

### An error read as a fact

`scaleNodeStillMember` tolerated any non-transport error from `pvecm status`
and then searched the resulting empty output for the node's IP. An inquorate or
corosync-down node 0 exits non-zero with no output, which is indistinguishable
from "the node already left" — so a failed `pvecm delnode` was taken as
idempotent success and the still-joined node's VM was destroyed underneath the
cluster. The neighbouring "could not confirm membership" error also dropped the
confirmation error it named.

### Data at risk of silent loss or disclosure

The snippet upload redirected `cat` straight onto its destination, which the
remote shell truncates on open: an interrupted upload left the previous snippet
truncated with no rollback, and a snippet is a cloud-init or hookscript the
next guest boot consumes. It now streams to a sibling temp file and renames,
matching `internal/nodefile`.

`security -i` consumes a backslash as an escape and still exits 0, so storing
`Domain\pass` left `Domainpass` in the keychain and reported success. Measured
against a throwaway keychain: quotes, `$`, and backticks round-trip; only
backslash does not.

`context edit` wrote the whole context — `Auth.Secret` included, possibly an
inline literal — to `$TMPDIR`, and two failure paths deliberately preserve that
file. Nothing ever removed it. It now lives in the 0700 config directory.

Every failure path in `parseOIDCRedirect` embedded the full redirect URL, whose
query carries the single-use authorization code, in errors that reach the JSONL
log. The function had no tests; the disclosure assertion added with the fix
immediately caught a second leak, where `url.Parse`'s own error re-embedded the
URL that the outer message had just redacted.

### A diagnostic that argued for disabling security

`context validate --connect` built its TLS config from `tls.insecure` alone. It
ignored `tls.fingerprint` — which is how a Proxmox context is normally trusted,
since the certificates are self-signed and `pmx lab` pins one automatically —
so a healthy pinned context was reported `unreachable: x509: certificate is not
trusted`, with a non-zero exit. It also ignored `--insecure`, unlike every
other verb, and skipped the warning both other verification-disabling paths
emit. The one lever that made validate pass was setting `tls.insecure`, which
then disabled verification for every real API call that context made.

Confirmed live against the lab context: `unreachable` before, `yes / match
(pve)` after. The pin runs in `VerifyConnection`, not `VerifyPeerCertificate`,
so a resumed session cannot slip past it.

### A credential written to disk in cleartext

The SDK encodes GET and DELETE parameters into the request URL and logs that
URL verbatim. Four typed commands take a `--password` and issue one of those
methods — `pve node scan pbs`, where the flag is **required**, `pve node scan
cifs`, and the two `access tfa delete` verbs including the PDM twin — so the
credential was written to the log file in the clear.

Captured against a local endpoint: the password appeared three times per
invocation, in `http.request`, in `http.response`, and in pmx's own exit
record. Two of those are logged at error level, so lowering `log.level` did not
suppress it, and per the retention item below the file is kept indefinitely.
The wrapped error also printed the full URL to stderr, putting the credential
into terminal scrollback and CI job output.

`redact.QueryParams` now masks the value of any query parameter whose key looks
like a credential — reusing the marker list the audit-arg redaction already
applies — at the log adapter, the exit record, and the stderr path.
Non-sensitive parameters are preserved so the error stays diagnosable.

This one was found by the verification round, not the first review pass, and
graded MEDIUM ("logging hygiene") before it was traced to a command whose
password flag is mandatory.

### Tasks that finished with warnings reported clean success

The SDK returns a `WARNINGS: N` task as a success with `Status.Warned` set, and
all three wait helpers discarded it across 33 call sites, so a vzdump that
skipped a guest printed "Backup completed" and exited 0. The helpers now report
the UPID and exit status on stderr. The exit code is deliberately unchanged —
whether a warning is a failure is a contract question for existing scripts, and
making the outcome visible does not require answering it.

### Observability

Shell completion was dispatched through the same `PersistentPreRunE` as a real
command. The client build was already skipped for it, but only after the log
file was opened and the invocation record written, so every tab press created a
JSONL file under a `__complete` directory. The live tree on this workstation
had reached **112,580 files / 407 MB**, 12,300 of them under 1 KB.

Log filenames carry one-second granularity and are opened `O_APPEND`, so
invocations starting within the same second shared a file with no way to tell
their records apart — six concurrent runs produced one file whose invocation
and exit records could not be paired to a run. Every record now carries the
pid.

A bare grouping command (`pmx pve`, `pmx pbs`, `pmx context`) resolved the
context secret and shelled out to the keychain purely to print help, and failed
with a credential error instead of helping when that lookup failed.
`RequireSubcommands` had made those commands runnable so a stray positional
exits non-zero, which also subjected them to client construction; they are now
annotated `noClient`. `pmx version` was reported as part of this and is
deliberately unchanged — it reports the connected server's version, so it does
need a client.

The audit record also carried the context name but nothing about what that
context pointed at. Contexts get renamed, repointed, and copied between
machines, so a log read months later could not answer the first question an
audit trail exists for: which machine did this mutation reach. It now records
host, port, product, and username — never the secret, which the test asserts
against every record a run produces.

### Table and JSON disagreed on the order of the same list

Most PBS `ls` commands sort their table rows, but across twenty-two files they
passed the *unsorted* API response to `Raw`, so `-o json` and `-o yaml` emitted
whatever order the server happened to return. A table reader and a JSON
consumer looking at the same command saw different orderings, and nothing tied
a raw object to the row above it.

The package already had the fix — `cli.DecodePairedRows` keeps each decoded
entry with the raw object it came from, and `pbs/remote.go` plus all of `pdm/`
were using it. Thirty-six sites were converted to it, thirty-four of which
sort, so rows and raw entries are now appended in one loop from one sorted
slice and cannot drift apart. The helper that made the split possible,
`decodeRawList`, is deleted rather than left as a footgun; its last two
callers, `node ls` and `drive cartridge-memory`, do not sort and were converted
with it. PDM was checked for the same defect and has none — no PDM list sorts.

Ordering is unchanged for tables and now matches it for JSON; no sort keys were
added or altered. The regression test serves each list in reverse of its sorted
order with a marker field the row struct does not declare, so it fails both if
the order is wrong and if a raw object is paired with the wrong row. It covers
one command per decode shape, including the site that strips secrets from the
raw objects, and all eight cases fail against the pre-fix code.

### Failures that named neither their cause nor their cost

Three commands lost the one fact an operator needed to act.

`nodeaddr.Resolve` falls back to the node name when the cluster-status call
fails. The fallback is deliberate, but the name is rarely a real hostname, so
an expired token surfaced as `ssh: Could not resolve hostname pve-1` — a DNS
error naming neither the API call nor the credential behind it. The cause is
now reported on the log and stderr before falling back. The empty-list and
node-not-found fallbacks stay silent: a single-node install returns no cluster
status at all, and warning on the ordinary case teaches operators to ignore the
warning that matters.

`pmx lab context sync` rotates the lab token by removing it and minting a new
one. PVE returns a token value exactly once, so from the mint onwards the
stored secret is dead — yet the keychain store, the config write, and the
end-to-end probe could each fail with an error that never mentioned it, leaving
a broken context and a message that read like a transient. Every post-mint
failure now says the token was already rotated, and the fingerprint and
hostname are fetched *before* the mint, since neither depends on the token and
running them after only widened the window.

The container security audit returned the first config-read error, so one
unreadable container out of hundreds produced no report at all, while the VM
twin degraded that row and carried on. They now behave alike. That audit's row
struct also had no exported fields, so `-o json` and `-o yaml` rendered a list
of empty objects.

### Audits that took one round trip per guest

Both security audits are N+1 by nature — one cluster-resources scan, then one
config read per guest — and both ran the reads back to back. They now fan out
eight at a time: well inside what a default pveproxy accepts, where unbounded
would point hundreds of simultaneous connections at a single pvedaemon. Rows
are written by index rather than appended as reads finish, so the report stays
in VMID order and can be diffed against yesterday's run.

The fan-out is proved rather than asserted: the fake server records how many
reads are in flight, and the test fails when the cap is lowered to one.

### A prune that kept the tree's skeleton, and shouted when run twice

Two findings surfaced while covering `internal/logx/prune.go`'s error branches,
both from tests written to exercise paths nothing had reached.

A directory only entered the prune's bookkeeping by having a child walked, so a
directory left empty by an earlier run survived every run after it — the log
tree kept its skeleton forever, which is part of how the 407 MB tree above got
its 112,580 entries.

And two invocations can pass the daily sentinel gate at once, after which the
walk lstats entries the other prune has already removed. Only `os.Remove`
tolerated that; the walk itself reported it, so a routine concurrent prune
printed a list of `no such file or directory`. Writing that test also turned up
a filesystem detail worth recording: on darwin, two concurrent `unlink` calls
on one path can *both* report success, so per-call deletion counts cannot be
exact under concurrency, and the test asserts bounds rather than a split.

### Two names for one thing

`migrate` names its storage mapping `--targetstorage` and `remote-migrate`
names the same thing `--target-storage`, each mirroring the API parameter it
carries. Mirroring the API keeps a flag findable from the Proxmox
documentation, but an operator who learned one spelling got "unknown flag" from
the neighbouring command. Both spellings now resolve to the same flag through
pflag's name normaliser, so help and completion still show one name each.

Likewise `template`: `lxc` had to call the conversion `to-template` because
`lxc template` is the appliance-download group, so one word meant a destructive
verb for VMs and a noun group for containers. `qemu template` now answers to
`to-template` too, and both commands say in their help why the other name
exists.

### Warnings, on the operator's terms

A task can finish with a `WARNINGS: N` exit status — a vzdump that skipped an
unreachable guest. The warning reaches stderr, and the command still exits 0.

Whether that is a failure depends on the fleet, and scripts already branch on
the current codes, so it is opt-in rather than a changed default:
`--warnings-as-errors`, `PMX_WARNINGS_AS_ERRORS`, or `warnings-as-errors` in
the config file. Such a task exits 8, not the generic 1 — the work was done, so
a script treating it like a command that failed to run would retry what already
happened.

### Config keys that no setting matches

`config.Load` is permissive on purpose: a config carrying a key this binary
does not know must still load, or the operator cannot run the CLI at all. The
cost is that a typo is indistinguishable from silence — `fingerprnt` under a
context means TLS pinning is simply not happening.

`pmx context validate` now re-reads the file strictly and names each unmatched
key by its full dotted path. It does not affect the exit status: a config
shared with a newer pmx legitimately carries keys this build has never heard
of, and failing on those would make the verb unusable exactly when it is most
needed.

## Deferred, with reasons

One item remains open by choice.

- **Log retention is disabled by default** (`internal/config/config.go`). This
  is the direct cause of the 407 MB tree above. The completion fix removed the
  largest contributor, and the prune fixes above stop the tree keeping its
  skeleton, but the default is still off. Turning retention on by default would
  start deleting an operator's existing logs on upgrade. That is a destructive
  default change and belongs to the maintainer, not to a hardening pass;
  `pmx logs prune` and `log.retention` already exist for anyone who wants it.

Everything else previously listed here has since been closed and is described
above.

### CI gates

CI ran `go test ./...` but never the race detector, though the repo ships a
`make test-race` target for it. And because the job runs only on Linux, every
`//go:build darwin` file was excluded from every check — including
`internal/config/secrets_keychain_darwin.go`, the whole macOS keychain
credential backend, which this audit itself modified.

Demonstrated by appending a deliberate type error to that file: the old
Linux-only pipeline built clean and would have shipped it; the added
`GOOS=darwin` build-and-vet step fails on it. Cross-compiling costs seconds and
needs no macOS runner.

### Tests that could not fail

Two tests wrapped `root.Execute()` in `if err != nil { t.Skipf(...) }`,
attributing any failure to an "absent lab env". Both use a self-contained temp
config with an inline secret and a host nothing ever dials, and client
construction is lazy — as one of the comments states outright. There was no
environmental failure mode to absorb, so the only thing those branches could
ever catch was a regression, which they converted into a green SKIP. Both now
assert.

## Verdict

**CHANGES REQUIRED → PASS** for everything remediated above. The full gate
block — `go test`, `go test -race`, `go vet`, `staticcheck`, `golangci-lint`,
`gosec` — is green. `gosec` reports 26 findings against a pre-audit 25: the one
addition is `internal/config/unknownkeys.go` reading the config path, the same
already-reviewed G304 pattern as `config.Load` two lines of which it mirrors,
carrying the same annotation.

One item is deferred rather than closed, because changing it would delete an
operator's existing data on upgrade. That is the maintainer's call, not a
hardening pass's.
