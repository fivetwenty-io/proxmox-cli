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

Nineteen commits, each with regression tests where behavior changed. Grouped
by what was actually wrong.

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

## Deferred, with reasons

These are real, and deliberately not changed here.

- **Log retention is disabled by default** (`internal/config/config.go`). This
  is the direct cause of the 407 MB tree above, and the completion fix removes
  the largest contributor but not the default. Turning retention on by default
  would start deleting an operator's existing logs on upgrade. That is a
  destructive default change and belongs to the maintainer, not to a hardening
  pass. `pmx logs prune` already exists for it.

- **Whether a `WARNINGS: N` task should exit non-zero.** The warning is now
  reported (above), but the exit code is still 0. Changing it is a contract
  decision for scripts already depending on the current codes.

- **Config is parsed non-strictly, so a misspelled key is silently ignored**
  (`internal/config/loader.go`). Switching `Load` to strict parsing would make
  every existing config carrying an unknown key fail to load, which could leave
  an operator unable to run the CLI at all. The better shape, if wanted: keep
  `Load` permissive and add a strict re-parse reporting unknown keys inside
  `pmx context validate`, which is already the "tell me what's wrong with my
  config" verb.

- **`nodeaddr.Resolve` swallows the cluster-status error** and falls back to
  the symbolic node name, so an expired token surfaces as `ssh: Could not
  resolve hostname pve-1`. The fallback itself is deliberate and documented;
  making the cause visible needs a logger the function does not currently have.

- **`labMintToken` deletes the old token before five more fallible steps**
  (`internal/cli/lab/labcontext.go`), and no error mentions the rotation
  already happened. The guard above it means the old secret is usually already
  known-bad, which is why this is not higher: hoisting the two independent
  fetches above the mint and naming the rotation in post-mint errors would
  close it.

- **N+1 sequential config reads in the lxc/qemu security audits**, and the
  related divergence where the lxc twin aborts on one unreadable container
  while the qemu twin degrades gracefully. Worth fixing; a behavior-shaping
  change large enough to want its own review.

- **Cross-persona duplication** (pbs/pdm near-identical command bodies, two
  names for one helper, `--targetstorage` vs `--target-storage`, `template`
  meaning opposite things for VMs and containers). Genuine maintainability
  debt, and the user-visible flag naming items are breaking changes.

- **Remaining test-suite gaps.** The two CI gates and the two self-disabling
  tests are closed (above). Still open: `internal/logx/prune.go`'s deletion
  error branches are unexercised, `cmd/pmx` has no test files, and 32 tests
  assert only `require.NoError` with no value assertion. Worth a dedicated pass
  with `go-testing-analysis`.

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
`gosec` — is green, and `gosec` is back to its pre-audit baseline with no new
findings introduced. The deferred items are listed above rather than closed
because each one either destroys operator data, changes a documented contract,
or is large enough to deserve its own review.
