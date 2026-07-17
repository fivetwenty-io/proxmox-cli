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
// outright), then the "user@host" destination. The remote command, if any,
// is appended by the caller.
func labGuestSSHArgs(f sshcmd.Flags, host string) []string {
	args := sshcmd.OptionArgs(&f)
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", guestConnectTimeoutSec),
		"-o", "StrictHostKeyChecking=accept-new",
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
		return res, fmt.Errorf("ssh %s@%s %q: %w (stderr: %s)", f.User, host, remoteCmd, err, strings.TrimSpace(stderr.String()))
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
// clustered), quorum state, vote counts, and whether a QDevice is currently
// registered.
type pvecmStatus struct {
	Clustered     bool
	ClusterName   string
	Quorate       bool
	ExpectedVotes int
	TotalVotes    int
	HasQdevice    bool
}

var (
	pvecmNameRE          = regexp.MustCompile(`(?m)^Name:\s*(\S+)`)
	pvecmQuorateRE       = regexp.MustCompile(`(?m)^Quorate:\s*(\S+)`)
	pvecmExpectedVotesRE = regexp.MustCompile(`(?m)^Expected votes:\s*(\d+)`)
	pvecmTotalVotesRE    = regexp.MustCompile(`(?m)^Total votes:\s*(\d+)`)
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
	// "Qdevice" appears both in the Flags line ("Flags: Quorate Qdevice")
	// and as a synthetic membership-list row once `pvecm qdevice setup` has
	// succeeded; either is sufficient evidence a QDevice is registered.
	st.HasQdevice = strings.Contains(output, "Qdevice")

	return st
}

// --- corosync-cfgtool -s parsing ------------------------------------------

var corosyncNodeStatusRE = regexp.MustCompile(`(?m)^\s*nodeid\s+\d+:\s*(\S+)`)

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
