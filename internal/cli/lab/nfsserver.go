package lab

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

// This file implements the SERVER-side half of `pmx lab nfs attach`: the ZFS
// dataset/export objects on the outer PVE node hosting tank/nfs (sm-0 today)
// that nfs.go's existing pvesm-add loop has always assumed already exist.
// Historically those objects were provisioned by hand-running
// scripts/60-nfs-service (lab repo), whose fixed 10-member NFS_LAB_MEMBERS
// array deliberately excludes az2-style/new-topology labs (RULE D-CLI: live
// provisioning goes through pmx; when a live step has no pmx coverage,
// extend pmx-cli, not the hand-run script). Every convention here mirrors
// that script's build_lab_rw_sharenfs/build_shared_iso_sharenfs/ensure_zfs_prop
// shapes exactly, verified live against sm-0, so a lab attached through pmx
// converges to identical dataset/export state as one built by the script.
//
// `nfs detach` deliberately does NOT undo any of this: removing a ZFS
// dataset or export is a data-loss operation this package never performs
// implicitly (see newNfsDetachCmd's Long text).

// nfsServerRootDataset is the ZFS dataset path backing nfsExportRoot's NFS
// export paths, derived from it (never hand-duplicated) so the two
// constants can never drift apart: nfsExportRoot is "/tank/nfs" (a
// filesystem/export path, leading slash), nfsServerRootDataset is
// "tank/nfs" (a ZFS dataset name, no leading slash — ZFS dataset names are
// pool/dataset paths, not filesystem paths).
var nfsServerRootDataset = strings.TrimPrefix(nfsExportRoot, "/")

// nfsServerLabRwSharenfsOpts are the fixed sharenfs options
// scripts/60-nfs-service's build_lab_rw_sharenfs applies to every lab's
// images/backup export, after the "rw=@<mgmt-/24>" ACL clause: no_root_squash
// (guest root needs real root on its own disk images/backups),
// no_subtree_check (the export root IS the dataset root, so subtree
// checking has nothing to protect and only adds latency/spurious denials),
// sec=sys (AUTH_SYS, this platform's only supported NFS auth mode).
const nfsServerLabRwSharenfsOpts = "no_root_squash,no_subtree_check,sec=sys"

// nfsLabRwSharenfs renders one lab's images/backup sharenfs value:
// "rw=@<mgmtCIDR>,no_root_squash,no_subtree_check,sec=sys" — byte-identical
// to scripts/60-nfs-service's build_lab_rw_sharenfs output for the same
// subnet, so a lab attached via pmx and one built by the script converge to
// the same live sharenfs value.
func nfsLabRwSharenfs(mgmtCIDR string) string {
	return fmt.Sprintf("rw=@%s,%s", mgmtCIDR, nfsServerLabRwSharenfsOpts)
}

// nfsServerEnsurePlan is the resolved, host-independent description of the
// server-side phase runNfsAttach performs before its existing pvesm-add
// loop: every dataset path, the desired sharenfs values, and the resolved
// quota, all computed once per invocation from lab so every step (both the
// --dry-run preview and the real ensure phase) reads off identical values.
type nfsServerEnsurePlan struct {
	labName          string
	labDataset       string // tank/nfs/labs/<lab> — the quota parent
	imagesDataset    string // tank/nfs/labs/<lab>/images
	backupDataset    string // tank/nfs/labs/<lab>/backup
	sharedIsoDataset string // tank/nfs/shared/iso
	mgmtCIDR         string // this lab's mgmt /24, e.g. "10.254.0.0/24"
	quotaGB          int    // EffectiveNFSQuotaGB(lab)
}

// buildNfsServerEnsurePlan resolves lab's server-side ensure plan. The only
// failure mode is an unresolvable mgmt /24 (labMgmtCIDR) — every other field
// is a pure string/int derivation from already-validated lab data.
func buildNfsServerEnsurePlan(lab *config.Lab) (nfsServerEnsurePlan, error) {
	mgmtCIDR, err := labMgmtCIDR(lab.Network)
	if err != nil {
		return nfsServerEnsurePlan{}, fmt.Errorf("resolve lab %q's mgmt /24 for NFS export ACLs: %w", lab.Name, err)
	}

	return nfsServerEnsurePlan{
		labName:          lab.Name,
		labDataset:       fmt.Sprintf("%s/labs/%s", nfsServerRootDataset, lab.Name),
		imagesDataset:    fmt.Sprintf("%s/labs/%s/images", nfsServerRootDataset, lab.Name),
		backupDataset:    fmt.Sprintf("%s/labs/%s/backup", nfsServerRootDataset, lab.Name),
		sharedIsoDataset: fmt.Sprintf("%s/shared/iso", nfsServerRootDataset),
		mgmtCIDR:         mgmtCIDR,
		quotaGB:          config.EffectiveNFSQuotaGB(lab),
	}, nil
}

// nfsServerDryRunSteps renders plan's server-side phase as STEP/STATUS
// preview rows, computable entirely from plan (no ssh, no probing which
// objects already exist) — matching the existing pvesm-add preview's own
// "would run" contract (nfsAddCommand under --dry-run). The shared-iso step
// is the one exception whose eventual literal command cannot be known ahead
// of time (it depends on the export's live ro= list, only readable over
// ssh); its row states the ensured OUTCOME instead of a literal command.
func nfsServerDryRunSteps(plan nfsServerEnsurePlan) [][]string {
	rw := nfsLabRwSharenfs(plan.mgmtCIDR)
	return [][]string{
		{fmt.Sprintf("zfs create -p -o recordsize=128K %s", plan.imagesDataset), "would run (if missing)"},
		{fmt.Sprintf("zfs create -p -o recordsize=1M %s", plan.backupDataset), "would run (if missing)"},
		{fmt.Sprintf("zfs set quota=%dG %s", plan.quotaGB, plan.labDataset), "would run (if different)"},
		{fmt.Sprintf("zfs set sharenfs=%s %s", rw, plan.imagesDataset), "would run (if different)"},
		{fmt.Sprintf("zfs set sharenfs=%s %s", rw, plan.backupDataset), "would run (if different)"},
		{fmt.Sprintf("ensure %s's sharenfs ro= list includes @%s", plan.sharedIsoDataset, plan.mgmtCIDR), "would run (if missing)"},
	}
}

// --- generic ssh-borne zfs primitives --------------------------------------
//
// Every helper below shells out via deps.Runner using createDatasetSSHFlags/
// createDatasetSSHArgs (create.go) — the identical ssh connection/argv
// convention buildCreatePlan's own dataset-ensure step uses, so both call
// sites resolve the exact same user/port/identity against the exact same
// resolved node host. Unlike create.go's dataset helpers, every error path
// here is wrapped with exec.NewCapturedError: this file's ssh calls always
// capture stdout/stderr into in-memory buffers (never pass them through to
// the real terminal), so the top-level error handler (internal/cli.Execute)
// must be told explicitly to print the wrapped diagnostic rather than assume
// (as it does for a pass-through *exec.ExitError) that the child already
// displayed it — the same contract guestssh.go's runGuestSSH follows.

// nfsZfsDatasetEnsureExists probes whether dataset exists on host via
// createZfsDatasetExists (create.go's own three-outcome exit-code contract:
// exit 0 exists, ssh transport failure aborts loudly, any other nonzero
// exit means the remote host was reached and zfs itself reported absence),
// creating it with "-o recordsize=<recordsize>" when absent. Returns
// whether a create actually ran, so callers can render an accurate
// "created"/"already exists" status row.
func nfsZfsDatasetEnsureExists(deps *cli.Deps, host, dataset, recordsize string) (created bool, err error) {
	exists, perr := createZfsDatasetExists(deps, host, dataset)
	if perr != nil {
		return false, exec.NewCapturedError(perr)
	}
	if exists {
		return false, nil
	}

	f := createDatasetSSHFlags(deps)
	args := []string{"zfs", "create", "-p"}
	if recordsize != "" {
		args = append(args, "-o", fmt.Sprintf("recordsize=%s", recordsize))
	}
	args = append(args, dataset)
	argv := createDatasetSSHArgs(f, host, args)

	var stdout, stderr bytes.Buffer
	if cerr := deps.Runner.Run("ssh", argv, nil, nil, &stdout, &stderr); cerr != nil {
		wrapped := fmt.Errorf(
			"create zfs dataset %q via ssh %s@%s (exit %d): %w (stderr: %s)",
			dataset, f.User, host, exec.ExitCodeOf(cerr), cerr, strings.TrimSpace(stderr.String()))
		return false, exec.NewCapturedError(wrapped)
	}
	return true, nil
}

// nfsZfsGetProp runs "zfs get -H -o value <prop> <dataset>" over ssh via
// deps.Runner and returns the trimmed value. Called only against a dataset
// this phase has already confirmed exists (or just created), so unlike a
// dataset-existence probe, ANY nonzero exit here — ssh transport failure or
// the remote "zfs get" itself failing — is treated as an error: there is no
// valid "absent" reading for a property read to fall back to.
func nfsZfsGetProp(deps *cli.Deps, host, dataset, prop string) (string, error) {
	f := createDatasetSSHFlags(deps)
	argv := createDatasetSSHArgs(f, host, []string{"zfs", "get", "-H", "-o", "value", prop, dataset})

	var stdout, stderr bytes.Buffer
	if err := deps.Runner.Run("ssh", argv, nil, nil, &stdout, &stderr); err != nil {
		wrapped := fmt.Errorf(
			"read zfs property %q of %q via ssh %s@%s (exit %d): %w (stderr: %s)",
			prop, dataset, f.User, host, exec.ExitCodeOf(err), err, strings.TrimSpace(stderr.String()))
		return "", exec.NewCapturedError(wrapped)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// nfsZfsSetProp runs "zfs set <prop>=<value> <dataset>" over ssh via
// deps.Runner.
func nfsZfsSetProp(deps *cli.Deps, host, dataset, prop, value string) error {
	f := createDatasetSSHFlags(deps)
	argv := createDatasetSSHArgs(f, host, []string{"zfs", "set", fmt.Sprintf("%s=%s", prop, value), dataset})

	var stdout, stderr bytes.Buffer
	if err := deps.Runner.Run("ssh", argv, nil, nil, &stdout, &stderr); err != nil {
		wrapped := fmt.Errorf(
			"set zfs property %s=%s on %q via ssh %s@%s (exit %d): %w (stderr: %s)",
			prop, value, dataset, f.User, host, exec.ExitCodeOf(err), err, strings.TrimSpace(stderr.String()))
		return exec.NewCapturedError(wrapped)
	}
	return nil
}

// nfsZfsEnsureProp reads dataset's current prop value and, only when it
// differs from want, sets it — the same read-then-skip-or-set idempotency
// convention scripts/60-nfs-service's own ensure_zfs_prop uses, which this
// phase supersedes for labs that script's fixed member list never covers.
// Returns whether a mutation actually occurred, so callers can render an
// accurate "set"/"already correct" status row.
func nfsZfsEnsureProp(deps *cli.Deps, host, dataset, prop, want string) (changed bool, err error) {
	current, gerr := nfsZfsGetProp(deps, host, dataset, prop)
	if gerr != nil {
		return false, gerr
	}
	if current == want {
		return false, nil
	}
	if serr := nfsZfsSetProp(deps, host, dataset, prop, want); serr != nil {
		return false, serr
	}
	return true, nil
}

// --- shared/iso ro= subnet-list parsing ------------------------------------

// nfsSharedIsoSharenfsRE matches the shared/iso dataset's sharenfs value
// shape this phase understands: "ro=<colon-separated @subnet list>[,<rest>]",
// where <rest> (typically ",sec=sys" — scripts/60-nfs-service's
// build_shared_iso_sharenfs) is captured but never interpreted, only
// preserved byte-for-byte on append (nfsSharedIsoAppendSubnet): this phase
// must never synthesize a fresh sharenfs value from nothing (see
// nfsSharedIsoContainsSubnet's doc comment) since it cannot know every OTHER
// lab's subnet, only insert this lab's into whatever list already exists.
var nfsSharedIsoSharenfsRE = regexp.MustCompile(`^ro=([^,]*)(,.*)?$`)

// nfsSharedIsoUnrecognizedShape is returned by nfsSharedIsoContainsSubnet/
// nfsSharedIsoAppendSubnet when current does not match nfsSharedIsoSharenfsRE
// (e.g. sharenfs is "off"/"-", or has been hand-edited into some other
// shape) — the caller must refuse loudly rather than guess, per the shared
// ISO dataset's ensure-step contract (see nfsServerEnsure).
var nfsSharedIsoUnrecognizedShape = fmt.Errorf(
	"sharenfs value is not a recognized \"ro=@subnet[:@subnet...][,...]\" shape")

// nfsSharedIsoContainsSubnet reports whether current (the shared/iso
// dataset's live sharenfs value) already lists "@mgmtCIDR" in its ro=
// colon-separated subnet list. err is non-nil only when current does not
// match the expected "ro=..." shape at all (nfsSharedIsoUnrecognizedShape) —
// this phase never falls back to constructing a value from scratch, since
// it has no knowledge of every other lab's subnet already present in a
// hand-or-script-built list.
func nfsSharedIsoContainsSubnet(current, mgmtCIDR string) (bool, error) {
	m := nfsSharedIsoSharenfsRE.FindStringSubmatch(current)
	if m == nil {
		return false, nfsSharedIsoUnrecognizedShape
	}
	token := "@" + mgmtCIDR
	for _, t := range strings.Split(m[1], ":") {
		if t == token {
			return true, nil
		}
	}
	return false, nil
}

// nfsSharedIsoAppendSubnet returns current with "@mgmtCIDR" appended to the
// end of its ro= subnet list, preserving every existing token's order and
// the ",<rest>" suffix (typically ",sec=sys") byte-for-byte. Callers must
// have already confirmed (nfsSharedIsoContainsSubnet) the subnet is not
// already present; this function does not itself de-duplicate.
func nfsSharedIsoAppendSubnet(current, mgmtCIDR string) (string, error) {
	m := nfsSharedIsoSharenfsRE.FindStringSubmatch(current)
	if m == nil {
		return "", nfsSharedIsoUnrecognizedShape
	}
	list, rest := m[1], m[2]
	token := "@" + mgmtCIDR
	if list == "" {
		list = token
	} else {
		list = list + ":" + token
	}
	return "ro=" + list + rest, nil
}

// --- top-level server-side ensure phase ------------------------------------

// nfsServerEnsure performs plan's full server-side phase against host over
// ssh, in order: create the images/backup datasets if missing (recordsize
// 128K/1M respectively — scripts/60-nfs-service's own per-export
// recordsize), ensure the lab's quota-parent dataset carries the resolved
// `quota` (never `refquota` — see plan.quotaGB's doc comment), ensure both
// images/backup carry the lab-scoped rw sharenfs value, then ensure the
// cluster-wide shared/iso export's ro= subnet list includes this lab's mgmt
// /24 (never removing or reordering any OTHER lab's entry). Returns one
// STEP/STATUS row per sub-step, in this same order, for the caller to render
// ahead of the existing pvesm-add rows. A non-nil error aborts the whole
// phase immediately (including the pvesm-add loop the caller has not yet
// reached) — this phase performs no dataset/export DELETION on any error
// path, so a partial run is always safe to retry.
func nfsServerEnsure(deps *cli.Deps, host string, plan nfsServerEnsurePlan) ([][]string, error) {
	rows := make([][]string, 0, 6)

	imagesCreated, err := nfsZfsDatasetEnsureExists(deps, host, plan.imagesDataset, "128K")
	if err != nil {
		return nil, fmt.Errorf("ensure zfs dataset %q: %w", plan.imagesDataset, err)
	}
	rows = append(rows, []string{plan.imagesDataset, nfsServerDatasetStatus(imagesCreated)})

	backupCreated, err := nfsZfsDatasetEnsureExists(deps, host, plan.backupDataset, "1M")
	if err != nil {
		return nil, fmt.Errorf("ensure zfs dataset %q: %w", plan.backupDataset, err)
	}
	rows = append(rows, []string{plan.backupDataset, nfsServerDatasetStatus(backupCreated)})

	quotaValue := fmt.Sprintf("%dG", plan.quotaGB)
	quotaChanged, err := nfsZfsEnsureProp(deps, host, plan.labDataset, "quota", quotaValue)
	if err != nil {
		return nil, fmt.Errorf("ensure quota=%s on %q: %w", quotaValue, plan.labDataset, err)
	}
	rows = append(rows, []string{plan.labDataset + " quota", nfsServerPropStatus(quotaChanged, quotaValue)})

	rw := nfsLabRwSharenfs(plan.mgmtCIDR)

	imagesShareChanged, err := nfsZfsEnsureProp(deps, host, plan.imagesDataset, "sharenfs", rw)
	if err != nil {
		return nil, fmt.Errorf("ensure sharenfs on %q: %w", plan.imagesDataset, err)
	}
	rows = append(rows, []string{plan.imagesDataset + " sharenfs", nfsServerPropStatus(imagesShareChanged, rw)})

	backupShareChanged, err := nfsZfsEnsureProp(deps, host, plan.backupDataset, "sharenfs", rw)
	if err != nil {
		return nil, fmt.Errorf("ensure sharenfs on %q: %w", plan.backupDataset, err)
	}
	rows = append(rows, []string{plan.backupDataset + " sharenfs", nfsServerPropStatus(backupShareChanged, rw)})

	isoRow, err := nfsServerEnsureSharedIso(deps, host, plan)
	if err != nil {
		return nil, err
	}
	rows = append(rows, isoRow)

	return rows, nil
}

// nfsServerEnsureSharedIso ensures the cluster-wide shared/iso export's
// sharenfs ro= subnet list includes plan.mgmtCIDR. A sharenfs value that is
// unset, "off"/"-", or does not match the recognized "ro=..." shape is a
// loud, non-mutating failure naming scripts/60-nfs-service (lab repo) as the
// remediation — this phase can only ever ensure MEMBERSHIP in an existing
// list; it has no way to reconstruct every other lab's subnet a fresh value
// would need.
func nfsServerEnsureSharedIso(deps *cli.Deps, host string, plan nfsServerEnsurePlan) ([]string, error) {
	current, err := nfsZfsGetProp(deps, host, plan.sharedIsoDataset, "sharenfs")
	if err != nil {
		return nil, fmt.Errorf("read sharenfs on %q: %w", plan.sharedIsoDataset, err)
	}

	if current == "" || current == "off" || current == "-" {
		return nil, fmt.Errorf(
			"shared ISO export %q has no sharenfs configured (value %q); this pmx step only ensures "+
				"lab-subnet MEMBERSHIP in an already-built ro= list, it never constructs one from scratch "+
				"(it cannot know every other lab's subnet) — run scripts/60-nfs-service (lab repo) 'shared' "+
				"group first, then retry `pmx lab nfs attach`",
			plan.sharedIsoDataset, current)
	}

	contains, cerr := nfsSharedIsoContainsSubnet(current, plan.mgmtCIDR)
	if cerr != nil {
		return nil, fmt.Errorf(
			"shared ISO export %q's sharenfs value %q is not a recognized shape (%w); run "+
				"scripts/60-nfs-service (lab repo) 'shared' group to rebuild it, then retry `pmx lab nfs attach`",
			plan.sharedIsoDataset, current, cerr)
	}
	if contains {
		return []string{plan.sharedIsoDataset + " sharenfs", "skip (already includes @" + plan.mgmtCIDR + ")"}, nil
	}

	newValue, aerr := nfsSharedIsoAppendSubnet(current, plan.mgmtCIDR)
	if aerr != nil {
		return nil, fmt.Errorf(
			"shared ISO export %q's sharenfs value %q is not a recognized shape (%w); run "+
				"scripts/60-nfs-service (lab repo) 'shared' group to rebuild it, then retry `pmx lab nfs attach`",
			plan.sharedIsoDataset, current, aerr)
	}
	if serr := nfsZfsSetProp(deps, host, plan.sharedIsoDataset, "sharenfs", newValue); serr != nil {
		return nil, fmt.Errorf("append @%s to sharenfs ro= list on %q: %w", plan.mgmtCIDR, plan.sharedIsoDataset, serr)
	}
	return []string{plan.sharedIsoDataset + " sharenfs", "appended @" + plan.mgmtCIDR}, nil
}

// nfsServerDatasetStatus renders a dataset-ensure sub-step's status cell.
func nfsServerDatasetStatus(created bool) string {
	if created {
		return "created"
	}
	return "skip (already exists)"
}

// nfsServerPropStatus renders a property-ensure sub-step's status cell.
func nfsServerPropStatus(changed bool, value string) string {
	if changed {
		return fmt.Sprintf("set (%s)", value)
	}
	return fmt.Sprintf("skip (already %s)", value)
}
