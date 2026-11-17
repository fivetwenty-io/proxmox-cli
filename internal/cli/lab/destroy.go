package lab

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/pools"
	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/peppi"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// newDestroyCmd builds `pmx lab destroy <name>`.
//
// It resolves the lab (peppi-guarding every identifier the config exposes),
// classifies every live VM in the lab's pool by name (findLabVMs/
// classifyLabVMName — the same classification start/stop use, not
// the single-VM assumption this command carried before multi-node labs
// existed), peppi-guards each classified VM's VMID a second time, then —
// after a confirmation gate and an optional --dry-run preview — stops each
// VM if running and deletes it, in reverse start order (the QDevice first
// if present, then node N-1 down to node 0). --purge additionally removes
// the lab's resource pool and storage definition. Every step queries live
// state first, so re-running destroy against a lab that is partially or
// fully torn down already reports the missing pieces as already gone
// rather than erroring. A pool member whose name matches none of the
// node/QDevice naming convention refuses the whole command rather than
// guessing which VM it is.
func newDestroyCmd() *cobra.Command {
	var (
		yes          bool
		dryRun       bool
		purge        bool
		purgeDataset bool
		keepContext  bool
	)

	cmd := &cobra.Command{
		Use:   "destroy <name>",
		Short: "Destroy a lab's node VMs (and QDevice, if any)",
		Long: "Destroy every node VM belonging to the named lab, and its QDevice VM if the " +
			"lab's topology has one.\n\n" +
			"VMs go in reverse start order (the QDevice first, if present, then node N-1 down " +
			"to node 0), stopping each one that is running before deleting it.\n\n" +
			"Every live VM in the lab's pool is classified by name against the node/QDevice " +
			"naming convention, the same classification start and stop use. A pool " +
			"member whose name matches none of it refuses the whole command rather than " +
			"guessing which VM it is. The read-only list and status commands only warn " +
			"about such a member.\n\n" +
			"Destroying requires --yes/-y or an interactive 'y' confirmation. Pass --purge to " +
			"remove the lab's resource pool and storage definition as well, or --dry-run to " +
			"preview what would be destroyed without mutating anything or prompting.\n\n" +
			"Unless --keep-context is given, destroy also removes the lab's `lab-<name>` pmx " +
			"context. It deletes a keychain secret only when that context's own secret reference " +
			"names this lab's `pmx-lab.<outer-context>-<config-hash>.<name>` item for the active " +
			"outer context and this config file, and no other context references the same item. " +
			"An older `pmx-lab-<name>` item is kept and the command that removes it is printed. " +
			"When the context's item belongs to another outer context or config file, destroy " +
			"keeps both the context and the item and names the commands that would remove them. " +
			"With no `lab-<name>` context in the active config, the keychain is left untouched. " +
			"Removing the context needs --yes/-y or an interactive 'y' even when the lab has no " +
			"VMs left. The --dry-run preview reports exactly what would be removed or kept.\n\n" +
			"Pass --purge-dataset to also destroy the lab's ZFS dataset (implies --purge; " +
			"irreversible). The dataset is destroyed over ssh on the node that hosts it, reached " +
			"the way \"pmx lab create\" reaches it. That node is the one the lab's VMs live on, " +
			"whatever $PMX_NODE or the context's default node says. The command refuses when the " +
			"VMs span more than one node, when an explicit --node names a different node than the " +
			"VMs, and, when the lab has no VMs left, unless --node names the node. The node's " +
			"address must be listed by the cluster the API reaches, and the command refuses " +
			"rather than fall back to the bare node name. The ssh user, port, and identity come " +
			"from the active context, and the ssh jump is the one the API connection uses. " +
			"Before it deletes anything, the command connects to that address and checks that the " +
			"machine's host name is the node's name and that its pve-ssl.pem fingerprint is the one " +
			"the API reports for the node; any mismatch, an unreachable target, or a fingerprint the " +
			"API will not return refuses the run. It also refuses outright when the API connection " +
			"goes through a proxy, because ssh cannot follow one. --dry-run skips the check. " +
			"Labs running Ceph should tear down the storage " +
			"layer first (see the lab repo's Ceph teardown script); the OSD data is destroyed " +
			"with the VMs either way.",
		Example: `  pmx lab destroy wayne --yes
  pmx lab destroy wayne --dry-run
  pmx lab destroy wayne --yes --purge`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			ctx := cmd.Context()

			if purgeDataset {
				purge = true
			}
			if purgeDataset && deps.Ctx == nil {
				return errors.New(
					"lab destroy --purge-dataset: an active pmx context is required to reach the dataset's node " +
						"over ssh; select one with --context/-c")
			}

			lab, err := resolveLabForMutate(cmd, name)
			if err != nil {
				return err
			}

			poolID := labPoolID(lab)
			stgID := storageID(lab)

			vms, err := listLiveVMs(ctx, deps)
			if err != nil {
				return err
			}

			classified, err := findLabVMs(vms, poolID, lab.Name)
			if err != nil {
				return fmt.Errorf("lab %q: %w", name, err)
			}

			toDestroy := destroyTargets(lab, classified)

			// Peppi-guard every VM this run would touch before building or
			// rendering any plan, so a single protected VMID anywhere in the
			// lab's pool aborts the whole command before any mutation, and
			// before the preview even names the lab's other, harmless VMs.
			for _, d := range toDestroy {
				target := peppi.Target{
					VMID:  int(d.vm.VMID),
					Names: []string{poolID, lab.Network.VnetID, stgID, lab.DNS.Zone, lab.Name},
				}
				if err := peppi.Guard(target); err != nil {
					return err
				}
			}

			// The dataset's ssh target is resolved before anything is destroyed,
			// so an unresolvable node or address fails the run while every VM
			// is still intact rather than after they are gone.
			var (
				datasetFlags sshcmd.Flags
				datasetHost  string
				datasetNode  string
			)
			if purgeDataset {
				// Every live VM in the lab's pool counts, not only the ones
				// this run destroys: the dataset is shared by all of them, so
				// a pool member on another node leaves its node in doubt.
				datasetNode, err = labDatasetNode(
					deps, "lab destroy --purge-dataset", labPoolVMNodes(vms, poolID), labDatasetNodeExplicit)
				if err != nil {
					return err
				}
				datasetFlags, datasetHost, err = resolveLabDatasetSSH(
					ctx, deps, "lab destroy --purge-dataset", datasetNode, true)
				if err != nil {
					return err
				}
			}

			var plan []string
			for _, d := range toDestroy {
				plan = append(plan, fmt.Sprintf("node %s VM %d on node %q", d.target.label, d.vm.VMID, d.vm.Node))
			}
			if purge {
				plan = append(plan, fmt.Sprintf("pool %q", poolID))
				plan = append(plan, fmt.Sprintf("storage %q", stgID))
			}
			if purgeDataset {
				plan = append(plan, fmt.Sprintf("ZFS dataset %s on node %s (%s) (recursive)",
					zfsDatasetPath(lab), datasetNode, datasetHost))
			}

			if len(plan) == 0 {
				msg := fmt.Sprintf(
					"lab %q: nothing to destroy, no VM found in pool %q", name, poolID)
				switch {
				case dryRun:
					// --dry-run must not mutate: preview the context/keychain
					// cleanup instead of performing it.
					msg = "[dry-run] " + msg + destroyContextCleanupPreview(deps, keepContext, name)
				case !keepContext:
					// Removing the context (and possibly its keychain item)
					// is the only mutation left, and it is gated the same way
					// a VM teardown is.
					if plan := planLabContextCleanup(deps, name); plan.removesContext() && !yes {
						ok, err := confirmYesNo(cmd, fmt.Sprintf("Clean up lab %q (%s)?", name, plan.describe(true)))
						if err != nil {
							return err
						}
						if !ok {
							res := output.Result{Message: "Aborted."}
							return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
						}
					}
					msg += destroyContextCleanupOutcome(deps, name)
				}
				res := output.Result{Message: msg}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			summary := strings.Join(plan, "; ")

			if dryRun {
				res := output.Result{
					Message: fmt.Sprintf("[dry-run] would destroy lab %q: %s%s",
						name, summary, destroyContextCleanupPreview(deps, keepContext, name)),
				}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			// Prove the dataset's ssh target is the node the API described
			// before the confirmation and before anything is deleted, so a
			// target that is not refuses the run while every resource is intact.
			if purgeDataset {
				if err := verifyLabDatasetTarget(
					ctx, deps, "lab destroy --purge-dataset", datasetNode, datasetFlags, datasetHost); err != nil {
					return err
				}
			}

			if !yes {
				ok, err := confirmYesNo(cmd, fmt.Sprintf("Destroy lab %q (%s%s)?",
					name, summary, destroyContextCleanupPreview(deps, keepContext, name)))
				if err != nil {
					return err
				}
				if !ok {
					res := output.Result{Message: "Aborted."}
					return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
				}
			}

			for _, d := range toDestroy {
				vmid := int(d.vm.VMID)
				if err := destroyStopIfRunning(ctx, deps.API, d.vm.Node, vmid); err != nil {
					return err
				}
				if err := destroyDeleteVM(ctx, deps.API, d.vm.Node, vmid); err != nil {
					return err
				}
			}

			if purge {
				if err := destroyDeletePool(ctx, deps.API, poolID); err != nil {
					return err
				}
				if err := destroyDeleteStorage(ctx, deps.API, stgID); err != nil {
					return err
				}
			}

			if purgeDataset {
				if err := destroyDatasetOnHost(deps, lab, datasetFlags, datasetHost); err != nil {
					return err
				}
			}

			if !keepContext {
				summary += destroyContextCleanupOutcome(deps, name)
			}

			res := output.Result{Message: fmt.Sprintf("lab %q destroyed: %s", name, summary)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"preview what would be destroyed without mutating anything or prompting")
	cmd.Flags().BoolVar(&purge, "purge", false,
		"also remove the lab's resource pool and storage definition")
	cmd.Flags().BoolVar(&purgeDataset, "purge-dataset", false,
		"also destroy the lab's ZFS dataset on the node that hosts the lab's VMs (implies --purge; irreversible)")
	cmd.Flags().BoolVar(&keepContext, "keep-context", false,
		"do not remove the lab's pmx context or the keychain secret it references on destroy")

	return cmd
}

// destroyContextCleanupPreview returns the dry-run suffix describing what a
// real destroy would do to the lab's context and keychain secret, or "" when
// --keep-context suppresses that cleanup. It is built from the same plan the
// real cleanup executes, so the preview and the real message always agree.
func destroyContextCleanupPreview(deps *cli.Deps, keepContext bool, name string) string {
	if keepContext {
		return ""
	}
	return "; " + planLabContextCleanup(deps, name).describe(true)
}

// destroyContextCleanupOutcome runs the lab's context cleanup for a real
// destroy and returns the message suffix reporting it: the plan's outcome on
// success, or a warning naming the failure. Cleanup is best-effort, so a
// failure never fails a destroy whose VMs are already gone.
func destroyContextCleanupOutcome(deps *cli.Deps, name string) string {
	plan, err := cleanupLabContext(deps, name)
	if err != nil {
		return fmt.Sprintf("; context cleanup warning: %v", err)
	}
	return "; " + plan.describe(false)
}

// labSecretKind classifies the keychain secret a lab-<name> context references.
type labSecretKind int

const (
	// labSecretNone: the context references no token item of this lab (an env
	// reference, a literal, another item, or nothing).
	labSecretNone labSecretKind = iota
	// labSecretCurrent: a namespaced item of this lab under the namespace of
	// the outer context and config this invocation runs against. Only this
	// kind is ever deleted.
	labSecretCurrent
	// labSecretLegacy: this lab's legacy "pmx-lab-<name>" item, which a
	// same-named lab in another config may share. It is kept.
	labSecretLegacy
	// labSecretForeign: a namespaced item of this lab under another namespace
	// (another outer context, another config, a config that moved, or an
	// invocation whose namespace cannot be determined). The context and the
	// item are both kept, because the context belongs to that other lab.
	labSecretForeign
)

// labContextCleanup is the plan for removing a lab's lab-<name> pmx context
// and the keychain secret it references. The context's own stored reference
// is the only thing that ever names a keychain item to delete, and only a
// namespaced item of this lab under the current namespace (see
// labCurrentKeychainService) that no other context references is deleted. A
// legacy item is kept and its removal command printed. A namespaced item under
// another namespace means the context was registered through another outer
// context or config, so neither it nor the item is touched. With no context,
// the keychain is left alone: a lab name alone does not identify an item.
type labContextCleanup struct {
	// labName is the base lab name.
	labName string
	// ctxName is the lab-<name> context name.
	ctxName string
	// ctxPresent reports whether the context exists in config (a nil-valued
	// map entry counts as present and is removed).
	ctxPresent bool
	// kind classifies the item the context references.
	kind labSecretKind
	// service and account name the lab-owned keychain item the context
	// references; both are empty when kind is labSecretNone.
	service, account string
	// currentService is the namespaced service this invocation would use, or
	// "" when currentErr explains why it cannot be determined. It is reported
	// when a foreign item is kept.
	currentService string
	currentErr     error
	// sharedWith names another context that also references the item, in
	// which case the item is kept.
	sharedWith string
	// keychainUnsupported reports that this platform has no keychain backend,
	// so the item cannot be deleted. The plan sets it from
	// labKeychainSupported; cleanupLabContext also sets it when the delete
	// itself reports config.ErrKeychainUnsupported.
	keychainUnsupported bool
}

// labKeychainSupported reports whether this build has a keychain backend; a
// package var so tests can exercise the unsupported-platform plan on macOS.
var labKeychainSupported = config.KeychainSupported

// removesContext reports whether executing the plan removes the context.
func (p labContextCleanup) removesContext() bool {
	return p.ctxPresent && p.kind != labSecretForeign
}

// deletesSecret reports whether executing the plan deletes a keychain item.
func (p labContextCleanup) deletesSecret() bool {
	return p.ctxPresent && p.kind == labSecretCurrent && p.sharedWith == "" && !p.keychainUnsupported
}

// planLabContextCleanup inspects the config and returns what removing the lab
// named name's context would do. It reads only config and the invocation's
// outer context and config path, never the keychain.
func planLabContextCleanup(deps *cli.Deps, name string) labContextCleanup {
	p := labContextCleanup{labName: name, ctxName: labContextName(name)}
	cfg := deps.Cfg
	if cfg == nil || cfg.Contexts == nil {
		return p
	}
	ctx, present := cfg.Contexts[p.ctxName]
	if !present {
		return p
	}
	p.ctxPresent = true
	if ctx == nil {
		return p
	}
	service, account, owned := labOwnedSecretRef(ctx.Auth.Secret, name)
	if !owned {
		return p
	}
	p.service, p.account = service, account
	p.currentService, p.currentErr = labCurrentKeychainService(deps, name)

	switch {
	case labLegacyKeychainServiceOf(service, name):
		p.kind = labSecretLegacy
	case p.currentErr == nil && service == p.currentService:
		p.kind = labSecretCurrent
	default:
		p.kind = labSecretForeign
		return p
	}
	p.sharedWith = labKeychainItemReferencedBy(cfg, p.ctxName, service, account)
	p.keychainUnsupported = !labKeychainSupported()
	return p
}

// describe renders the plan as a message clause, in the conditional mood for
// a dry-run preview and in the past tense for a completed cleanup.
func (p labContextCleanup) describe(dryRun bool) string {
	pick := func(would, did string) string {
		if dryRun {
			return would
		}
		return did
	}
	item := p.service + "/" + p.account
	removeCtx := pick("would remove", "removed")

	switch {
	case !p.ctxPresent:
		return fmt.Sprintf("context %q not found, so %s", p.ctxName,
			pick("no keychain secret would be removed", "no keychain secret was removed"))
	case p.kind == labSecretNone:
		return fmt.Sprintf("%s context %q, which references no keychain secret of lab %q, so %s",
			removeCtx, p.ctxName, p.labName,
			pick("no keychain secret would be removed", "no keychain secret was removed"))
	case p.kind == labSecretForeign:
		return fmt.Sprintf("%s context %q and its keychain secret %s, because the secret belongs to "+
			"another outer context or config (%s), and to remove them, run `pmx lab context rm %s` "+
			"with the outer context and config that registered it, or remove them by hand with "+
			"`pmx context rm %s` and `%s`",
			pick("would keep", "kept"), p.ctxName, item, p.foreignReason(), p.labName,
			p.ctxName, labKeychainDeleteCommand(p.service, p.account))
	case p.sharedWith != "":
		return fmt.Sprintf("%s context %q but %s its keychain secret %s, which context %q also references",
			removeCtx, p.ctxName, pick("keep", "kept"), item, p.sharedWith)
	case p.kind == labSecretLegacy:
		return fmt.Sprintf("%s context %q but %s its legacy keychain secret %s, since a same-named lab "+
			"in another config may use it. If nothing uses it, remove it with: %s",
			removeCtx, p.ctxName, pick("keep", "kept"), item, labKeychainDeleteCommand(p.service, p.account))
	case p.keychainUnsupported:
		return fmt.Sprintf("%s context %q, but keychain secret %s %s because this platform has no keychain",
			removeCtx, p.ctxName, item, pick("would not be removed", "was not removed"))
	default:
		return fmt.Sprintf("%s context %q and its keychain secret %s", removeCtx, p.ctxName, item)
	}
}

// foreignReason explains, for a foreign item, which namespace this invocation
// would have used instead.
func (p labContextCleanup) foreignReason() string {
	if p.currentErr != nil {
		return fmt.Sprintf("this invocation's namespace cannot be determined: %v", p.currentErr)
	}
	return "this invocation's lab secrets use " + p.currentService
}

// cleanupLabContext removes the lab-<name> pmx context from config, deletes
// the keychain secret that context references when planLabContextCleanup
// allows it, and persists the config. It returns the executed plan so callers
// render the same outcome a dry-run previewed. A plan that keeps the context
// changes nothing. The keychain item is deleted before the context, so a
// failed delete leaves the context (and its reference) in place for a retry.
func cleanupLabContext(deps *cli.Deps, name string) (labContextCleanup, error) {
	plan := planLabContextCleanup(deps, name)
	if !plan.removesContext() {
		return plan, nil
	}

	if plan.deletesSecret() {
		// On a non-darwin build DeleteKeychainSecret always returns
		// ErrKeychainUnsupported (secrets_keychain_other.go documents delete
		// as a no-op there). That is tolerated rather than aborting before the
		// context itself is removed below.
		derr := labDeleteSecretFn(plan.service, plan.account)
		switch {
		case errors.Is(derr, config.ErrKeychainUnsupported):
			plan.keychainUnsupported = true
		case derr != nil:
			return plan, derr
		}
	}

	// Never leave the destroyed lab as the active context.
	if deps.Cfg.CurrentContext == plan.ctxName {
		deps.Cfg.CurrentContext = ""
	}
	delete(deps.Cfg.Contexts, plan.ctxName)
	return plan, config.Save(deps.ConfigPath, deps.Cfg)
}

// destroyTarget pairs one classified live VM with the lifecycleTarget role
// (a node index or the QDevice) its name identifies it as.
type destroyTarget struct {
	target lifecycleTarget
	vm     labVM
}

// destroyTargets returns every classified VM in lab's pool that destroy
// should act on, in destroy order: the QDevice first (if present), then
// node N-1 down to node 0 — the reverse of create/start order, mirroring
// lifecycle.go's stop sequencing and the multi-node lab plan's §9 scale-
// down sequencing (highest-index/QDevice evacuated before lower nodes). A
// target with no classified VM (a partially-created lab) is simply absent
// from the result, not an error: destroy tears down whatever exists.
func destroyTargets(lab *config.Lab, classified []classifiedLabVM) []destroyTarget {
	targets := reverseLifecycleTargets(lifecycleTargetsForLab(lab))
	out := make([]destroyTarget, 0, len(targets))
	for _, tgt := range targets {
		vm, found := tgt.lookup(classified)
		if !found {
			continue
		}
		out = append(out, destroyTarget{target: tgt, vm: vm})
	}
	return out
}

// destroyStopIfRunning reads the VM's current status and, if it is running,
// stops it (hard power-off, matching the qemu delete precedent's own
// pre-destroy stop) and blocks until the stop task completes.
func destroyStopIfRunning(ctx context.Context, api *apiclient.APIClient, node string, vmid int) error {
	vmidStr := strconv.Itoa(vmid)

	status, err := api.Nodes.ListQemuStatusCurrent(ctx, node, vmidStr)
	if err != nil {
		return fmt.Errorf("check status of VM %d on node %q: %w", vmid, node, err)
	}
	if status == nil || status.Status != "running" {
		return nil
	}

	resp, err := api.Nodes.CreateQemuStatusStop(ctx, node, vmidStr, &nodes.CreateQemuStatusStopParams{})
	if err != nil {
		return fmt.Errorf("stop VM %d on node %q: %w", vmid, node, err)
	}
	if resp == nil {
		return fmt.Errorf("stop VM %d on node %q: nil response", vmid, node)
	}

	upid, err := apiclient.UPIDFromRaw(json.RawMessage(*resp))
	if err != nil {
		return fmt.Errorf("stop VM %d: %w", vmid, err)
	}
	if err := apiclient.WaitTask(ctx, api, upid, nil); err != nil {
		return fmt.Errorf("wait for VM %d to stop: %w", vmid, err)
	}
	return nil
}

// destroyDeleteVM deletes the VM's configuration and disks and blocks until
// the delete task completes. A 404 (the VM vanished between discovery and
// this call) is treated as already-gone, not an error.
func destroyDeleteVM(ctx context.Context, api *apiclient.APIClient, node string, vmid int) error {
	vmidStr := strconv.Itoa(vmid)

	resp, err := api.Nodes.DeleteQemu(ctx, node, vmidStr, &nodes.DeleteQemuParams{})
	if err != nil {
		if errors.Is(err, pveerrors.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("delete VM %d on node %q: %w", vmid, node, err)
	}
	if resp == nil {
		return fmt.Errorf("delete VM %d on node %q: nil response", vmid, node)
	}

	upid, err := apiclient.UPIDFromRaw(json.RawMessage(*resp))
	if err != nil {
		return fmt.Errorf("delete VM %d: %w", vmid, err)
	}
	if err := apiclient.WaitTask(ctx, api, upid, nil); err != nil {
		return fmt.Errorf("wait for VM %d delete: %w", vmid, err)
	}
	return nil
}

// destroyDeletePool deletes the lab's resource pool. Already-gone (never
// created, or already deleted) is treated as success, not an error: PVE's
// DELETE /pools reports a missing pool the same way GET /pools/{poolid}
// does, a bare HTTP 500 "pool '<id>' does not exist" body rather than a 404,
// so a plain pveerrors.ErrNotFound check misses it (see isPoolNotFound in
// create.go).
func destroyDeletePool(ctx context.Context, api *apiclient.APIClient, poolID string) error {
	err := api.Pools.DeletePools(ctx, &pools.DeletePoolsParams{Poolid: poolID})
	if err != nil && !isPoolNotFound(err, poolID) {
		return fmt.Errorf("delete pool %q: %w", poolID, err)
	}
	return nil
}

// destroyDeleteStorage deletes the lab's storage.cfg entry. Already-gone
// (never created, or already deleted) is treated as success, not an error:
// PVE's storage delete reports a missing storage ID the same way pool
// lookups do, a bare HTTP 500 "storage '<id>' does not exist" body rather
// than a 404, so a plain pveerrors.ErrNotFound check misses it (see
// isStorageNotFound in create.go).
func destroyDeleteStorage(ctx context.Context, api *apiclient.APIClient, stgID string) error {
	err := api.ClusterStorage.DeleteStorage(ctx, stgID)
	if err != nil && !isStorageNotFound(err, stgID) {
		return fmt.Errorf("delete storage %q: %w", stgID, err)
	}
	return nil
}

// destroyDatasetOnHost removes the lab's ZFS dataset over SSH to host, the
// address of the node that hosts it, using f's connection flags (user, port,
// identity, and jump from the context). It is the same target and transport
// the create verb's dataset step and quota set use (resolveLabDatasetSSH),
// NOT guest SSH: the dataset lives on the outer node, not in a lab guest.
// Absent dataset is success (idempotent re-destroy); a real zfs destroy
// failure propagates.
func destroyDatasetOnHost(deps *cli.Deps, lab *config.Lab, f sshcmd.Flags, host string) error {
	ds := zfsDatasetPath(lab)
	argv := sshcmd.BaseArgs(&f, host)
	argv = append(argv, fmt.Sprintf(`if zfs list %q >/dev/null 2>&1; then zfs destroy -r %q; fi`, ds, ds))

	var stderr bytes.Buffer
	if err := deps.Runner.Run("ssh", argv, nil, nil, io.Discard, &stderr); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return exec.NewCapturedError(fmt.Errorf("destroy dataset %s on %s: %w (stderr: %s)", ds, host, err, msg))
		}
		return exec.NewCapturedError(fmt.Errorf("destroy dataset %s on %s: %w", ds, host, err))
	}
	return nil
}

// confirmYesNo prints a yes/no prompt to stderr and reads a single line from
// cmd's input, mirroring the pmx pve pool delete confirmation precedent. It
// returns true only when the response begins with 'y' or 'Y'; a closed or
// empty (non-interactive) stdin, or any read error, is treated as declined
// rather than erroring, so a non-interactive invocation without --yes safely
// refuses instead of hanging or panicking. Shared by every mutating lab verb
// that gates on a confirmation prompt (destroy, quota set).
func confirmYesNo(cmd *cobra.Command, prompt string) (bool, error) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N]: ", prompt)
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	line = strings.TrimSpace(line)
	return strings.HasPrefix(strings.ToLower(line), "y"), nil
}
