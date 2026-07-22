package lab

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// This file holds the SSH transport `pmx lab cluster`/`qdevice`/`sdn`/`nfs`
// share: every one of those verbs runs shell commands (pvecm, corosync-
// cfgtool, pvesm, pvesh, dpkg/apt-get) directly against a lab guest's own
// mgmt IP, never against the outer PVE API (deps.API), and never against the
// pmx context's own host the way quota.go's ssh usage does. This is a
// genuinely new transport pattern (multi-node lab plan §4.5): quota.go SSHes
// to the pmx context host (sm-0), which is already a trusted, long-lived
// target; the verbs in this file SSH into freshly-provisioned, ephemeral
// guest VMs whose host key is never pre-seeded into a known_hosts file, so
// every call here must accept an unseen host key non-interactively
// (StrictHostKeyChecking=accept-new) rather than either prompting (which a
// scripted, non-interactive invocation can never answer) or refusing
// outright (which would make every guest unreachable on its first-ever SSH
// connection).

// guestConnectTimeoutSec bounds how long a single guest ssh connection
// attempt may hang before failing, matching sshcmd.DefaultConnectTimeoutSec's
// role for context-host connections.
const guestConnectTimeoutSec = sshcmd.DefaultConnectTimeoutSec

// labGuestSSHFlags builds the ssh connection flags (user/port/identity) for
// SSHing into a lab guest, using deps.Ctx.SSH as the source of overrides on
// top of the same root/22 compiled-in defaults runQuotaSet uses — the same
// SSH key/user pmx already trusts for the context host is the one every lab
// guest's root account trusts too (both provisioned from the same answer.toml
// key material). Returns an error when deps.Ctx is nil, since there is no
// other source of SSH connection defaults to fall back to.
func labGuestSSHFlags(deps *cli.Deps) (sshcmd.Flags, error) {
	if deps.Ctx == nil {
		return sshcmd.Flags{}, fmt.Errorf(
			"this command requires an active pmx context to resolve ssh connection defaults; select one with --context/-c")
	}

	f := sshcmd.Flags{User: "root", Port: 22}
	if deps.Ctx.SSH.User != "" {
		f.User = deps.Ctx.SSH.User
	}
	if deps.Ctx.SSH.Port != 0 {
		f.Port = deps.Ctx.SSH.Port
	}
	if deps.Ctx.SSH.Identity != "" {
		f.Identity = deps.Ctx.SSH.Identity
	}
	return f, nil
}

// labGuestSSHArgs builds the full ssh argv (options + destination) for
// connecting to a lab guest at host: sshcmd.OptionArgs(f) plus BatchMode=yes
// and a bounded ConnectTimeout (so a scripted call never prompts or hangs
// indefinitely, mirroring sshcmd.BatchOptionArgs), plus
// StrictHostKeyChecking=accept-new (silently trusting a guest's host key on
// its first connection, rather than either prompting — impossible
// non-interactively — or refusing every never-before-seen lab guest
// outright), plus ForwardAgent=no, then the "user@host" destination. The
// remote command, if any, is appended by the caller.
//
// ForwardAgent=no (live pve-cpi-az2 finding): every guest-side command this
// package runs must authenticate as the GUEST's own /root/.ssh/id_rsa —
// nothing else. Without this override, an operator whose own ~/.ssh/config
// sets `ForwardAgent yes` (a common `Host *` default) gets their local
// ssh-agent forwarded straight into the guest session; any ssh call the
// guest itself then makes (notably `pvecm add`'s own ssh-fallback join path,
// and this package's own clusterSeedJoinTrust preflight — see cluster.go)
// offers every key in that forwarded agent before ever trying the guest's
// own key, and a large keyring (dozens of unrelated operator keys) blows
// through the remote sshd's MaxAuthTries and gets disconnected with no
// output on either stream: "Received disconnect ...: Too many
// authentication failures". A command-line -o always overrides a matching
// ssh_config directive (command-line options take precedence over config
// files), so this is sufficient regardless of the operator's own
// ~/.ssh/config. Deliberately NOT paired with -o IdentityAgent=none: that
// option only changes which agent socket THIS OUTER ssh call (operator ->
// guest) uses to authenticate itself, which is unrelated to the forwarding
// bug above (which is about an agent socket appearing *inside* the guest
// session) and would risk breaking an operator whose key material lives
// only in an agent (e.g. a hardware token) rather than an on-disk identity
// file.
func labGuestSSHArgs(f sshcmd.Flags, host string) []string {
	args := sshcmd.OptionArgs(&f)
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", guestConnectTimeoutSec),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ForwardAgent=no",
	)
	args = append(args, sshcmd.Dest(&f, host))
	return args
}

// guestSSHResult holds the outcome of one runGuestSSH call: both output
// streams (so a caller can surface stderr in an error message even when the
// command "succeeded" with exit 0 but printed a warning) and the resolved
// exit code (0 when err is nil).
type guestSSHResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// runGuestSSH runs remoteCmd (already a fully-formed remote shell command
// line, e.g. "pvecm status" or "apt-get update && apt-get install -y
// corosync-qnetd") against host via ssh, using deps.Runner (so tests can
// substitute exec.Fake()) and the connection flags labGuestSSHFlags
// resolves. remoteCmd is passed as a single ssh argv element: ssh itself
// concatenates every argument after the destination with a single space
// before handing the result to the remote shell, so a single element
// containing shell metacharacters (&&, quotes) is handled identically to
// passing the same text split across several argv elements — this file
// always uses one element per call for clarity. Returns the captured
// stdout/stderr and the resolved exit code; err is non-nil only for a
// transport-level failure (a *exec.ExitError from a non-zero remote exit
// still comes back as err, but callers that need to distinguish "connected
// but the remote command failed" from "could not connect at all" should use
// exec.ExitCodeOf(err) — this is exactly what the idempotency probes in this
// package do to tell "not yet clustered" from "ssh to the node itself is
// broken").
//
// A non-nil err is always wrapped in exec.CapturedError: this call wires
// ssh's stdout/stderr to in-memory buffers (never to the real terminal), so
// unlike an interactive/pass-through ssh call, nothing here has ever been
// shown to the user — the top-level error handler (internal/cli.Execute)
// must print it rather than assume, as it does for *exec.ExitError from a
// pass-through call, that the child already displayed its own diagnostics
// (see internal/exec.CapturedError's doc comment for the live failure this
// prevents: a swallowed "Too many authentication failures" that exited 255
// with zero output on either stream).
func runGuestSSH(deps *cli.Deps, host, remoteCmd string) (guestSSHResult, error) {
	f, ferr := labGuestSSHFlags(deps)
	if ferr != nil {
		return guestSSHResult{}, ferr
	}

	argv := labGuestSSHArgs(f, host)
	argv = append(argv, remoteCmd)

	var stdout, stderr bytes.Buffer
	err := deps.Runner.Run("ssh", argv, nil, nil, &stdout, &stderr)

	res := guestSSHResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		res.ExitCode = exec.ExitCodeOf(err)
		wrapped := fmt.Errorf("ssh %s@%s %q: %w (stderr: %s)", f.User, host, remoteCmd, err, strings.TrimSpace(stderr.String()))
		return res, exec.NewCapturedError(wrapped)
	}
	return res, nil
}

// guestCommandTransportFailed reports whether err represents a failure to
// even reach the remote host/command (a non-*exec.ExitError, e.g. ssh itself
// could not be found, or the process could not be started) as opposed to the
// remote command running and exiting non-zero (*exec.ExitError — the
// expected shape of "not yet clustered", "package not installed", etc. probe
// failures this package treats as meaningful, not fatal, signals). Every
// idempotency probe in this package calls this before deciding whether a
// non-nil probe error means "state not yet reached" (safe to proceed) or
// "something is actually broken" (must abort).
//
// Accepted convention and tradeoff (M3-R05): a plain non-zero exit is
// treated as "state not yet present" REGARDLESS of the actual reason —
// "not found" (`pvesh get` on a missing zone/storage entry, `pvecm status`
// on an unclustered node, `dpkg -s` on a missing package) and every other
// non-zero exit (e.g. pmxcfs not yet ready, a transient pvesh error) both
// take the same "proceed to the mutating step" branch. This is broader than
// strictly "not found," but deliberately not narrowed to specific known
// error text: PVE's CLI tools do not expose a stable, parseable
// not-found-vs-other-failure distinction across `pvesh`/`pvecm`/`dpkg`, so
// pattern-matching stderr text would be brittle across PVE versions. The
// accepted risk is that a transient, non-"not found" failure here causes an
// unnecessary create/add/setup attempt — which then fails loudly on ITS OWN
// non-zero exit (propagated as a real error, not silently swallowed), so
// this never corrupts state; it only means a transient probe hiccup surfaces
// one step later, on the mutating call, rather than immediately on the
// probe itself.
func guestCommandTransportFailed(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	return !errors.As(err, &exitErr)
}

// --- pvecm status parsing -------------------------------------------------

// pvecmStatus is the subset of `pvecm status`'s plain-text output this
// package parses: whether the node is clustered at all, the cluster name (if
// clustered), quorum state, vote counts, the live corosync membership node
// count, and whether a QDevice is currently registered.
type pvecmStatus struct {
	Clustered     bool
	ClusterName   string
	Quorate       bool
	ExpectedVotes int
	TotalVotes    int
	// NodeCount is the "Nodes:" field of the Quorum information section —
	// the number of real cluster NODES currently in the corosync
	// membership, distinct from ExpectedVotes/TotalVotes (which also
	// include the QDevice's vote when one is registered). `pmx lab scale`
	// uses this as the ground-truth "current node count" for its delta
	// computation and re-run idempotency (see scaleCurrentMembership) — VM
	// shell existence alone cannot distinguish "joined" from "created but
	// never joined."
	NodeCount  int
	HasQdevice bool
}

var (
	pvecmNameRE          = regexp.MustCompile(`(?m)^Name:\s*(\S+)`)
	pvecmQuorateRE       = regexp.MustCompile(`(?m)^Quorate:\s*(\S+)`)
	pvecmExpectedVotesRE = regexp.MustCompile(`(?m)^Expected votes:\s*(\d+)`)
	pvecmTotalVotesRE    = regexp.MustCompile(`(?m)^Total votes:\s*(\d+)`)
	pvecmNodesRE         = regexp.MustCompile(`(?m)^Nodes:\s*(\d+)`)
)

// parsePvecmStatus parses the plain-text output of `pvecm status` (the only
// form PVE's pvecm exposes; there is no --output-format json for this
// subcommand). A node that is not part of any cluster prints an error to
// stderr and exits non-zero rather than emitting this format at all, so
// Clustered is derived from the presence of the "Cluster information"
// section header, not merely from a successful parse.
func parsePvecmStatus(output string) pvecmStatus {
	var st pvecmStatus

	st.Clustered = strings.Contains(output, "Cluster information")
	if !st.Clustered {
		return st
	}

	if m := pvecmNameRE.FindStringSubmatch(output); m != nil {
		st.ClusterName = m[1]
	}
	if m := pvecmQuorateRE.FindStringSubmatch(output); m != nil {
		st.Quorate = strings.EqualFold(m[1], "Yes")
	}
	if m := pvecmExpectedVotesRE.FindStringSubmatch(output); m != nil {
		st.ExpectedVotes, _ = strconv.Atoi(m[1])
	}
	if m := pvecmTotalVotesRE.FindStringSubmatch(output); m != nil {
		st.TotalVotes, _ = strconv.Atoi(m[1])
	}
	if m := pvecmNodesRE.FindStringSubmatch(output); m != nil {
		st.NodeCount, _ = strconv.Atoi(m[1])
	}
	// "Qdevice" appears both in the Flags line ("Flags: Quorate Qdevice")
	// and as a synthetic membership-list row once `pvecm qdevice setup` has
	// succeeded; either is sufficient evidence a QDevice is registered.
	st.HasQdevice = strings.Contains(output, "Qdevice")

	return st
}

// --- corosync-cfgtool -s parsing ------------------------------------------

// corosyncNodeStatusRE matches a "nodeid" status line in `corosync-cfgtool
// -s` output in either shape corosync is known to emit it in: the live
// PVE 9.2/corosync 3.x knet-transport shape, where the label itself carries
// a trailing colon and the id/status pair is tab-separated ("nodeid:
// 1:\tlocalhost"), and the previously-assumed shape with no colon on the
// label and space-separated fields ("nodeid 1: connected"). The `:?` after
// "nodeid" and `\s+` (matching either spaces or tabs) before the captured
// status cover both without needing two separate patterns. (M-COROLINK: the
// unmatched live shape previously made parseCorosyncLinks return (false,
// nil) unconditionally — clusterWaitForJoin and scaleValidateNode then timed
// out waiting for "all corosync links up" on an already-healthy cluster, and
// `pmx lab cluster status` always reported "no link status parsed".)
var corosyncNodeStatusRE = regexp.MustCompile(`(?m)^\s*nodeid:?\s+\d+:\s*(\S+)`)

// parseCorosyncLinks parses the plain-text output of `corosync-cfgtool -s`
// and reports whether every reported link status is either "connected" (a
// live peer) or "localhost" (the node's own loopback entry for itself) —
// i.e. no peer link reports "disconnected" or anything else. A link report
// with zero matched status lines is treated as NOT all-up (rather than
// vacuously true), since that shape means the output could not be parsed at
// all (e.g. corosync-cfgtool itself failed) and this function has no
// evidence of a healthy link.
func parseCorosyncLinks(output string) (allUp bool, statuses []string) {
	matches := corosyncNodeStatusRE.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return false, nil
	}

	allUp = true
	for _, m := range matches {
		status := m[1]
		statuses = append(statuses, status)
		if status != "connected" && status != "localhost" {
			allUp = false
		}
	}
	return allUp, statuses
}
