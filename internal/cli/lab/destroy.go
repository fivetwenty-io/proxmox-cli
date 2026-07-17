package lab

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/pools"
	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/peppi"
)

// newDestroyCmd builds `pmx lab destroy <name>`.
//
// It resolves the lab (peppi-guarding every identifier the config exposes),
// classifies every live VM in the lab's pool by name (findLabVMs/
// classifyLabVMName — the same classification start/stop/status use, not
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
		yes    bool
		dryRun bool
		purge  bool
	)

	cmd := &cobra.Command{
		Use:   "destroy <name>",
		Short: "Destroy a lab's node VMs (and QDevice, if any)",
		Long: "Destroy every node VM belonging to the named lab (and its QDevice VM, if the " +
			"lab's topology has one), in reverse start order — the QDevice first (if present), " +
			"then node N-1 down to node 0: stop each if running, then delete it. Every live VM " +
			"in the lab's pool is classified by name against the node/QDevice naming " +
			"convention (the same classification start/stop/status use); a pool member whose " +
			"name matches none of it refuses the whole command rather than guessing which VM " +
			"it is. Refuses to run without --yes/-y or an interactive 'y' confirmation. Pass " +
			"--purge to also remove the lab's resource pool and storage definition. Pass " +
			"--dry-run to preview what would be destroyed without mutating anything or " +
			"prompting.",
		Example: `  pmx lab destroy wayne --yes
  pmx lab destroy wayne --dry-run
  pmx lab destroy wayne --yes --purge`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			ctx := cmd.Context()

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

			var plan []string
			for _, d := range toDestroy {
				plan = append(plan, fmt.Sprintf("node %s VM %d on node %q", d.target.label, d.vm.VMID, d.vm.Node))
			}
			if purge {
				plan = append(plan, fmt.Sprintf("pool %q", poolID))
				plan = append(plan, fmt.Sprintf("storage %q", stgID))
			}

			if len(plan) == 0 {
				res := output.Result{
					Message: fmt.Sprintf(
						"lab %q: nothing to destroy — no VM found in pool %q", name, poolID),
				}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			summary := strings.Join(plan, "; ")

			if dryRun {
				res := output.Result{
					Message: fmt.Sprintf("[dry-run] would destroy lab %q: %s", name, summary),
				}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			if !yes {
				ok, err := confirmYesNo(cmd, fmt.Sprintf("Destroy lab %q (%s)?", name, summary))
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

			res := output.Result{Message: fmt.Sprintf("lab %q destroyed: %s", name, summary)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"preview what would be destroyed without mutating anything or prompting")
	cmd.Flags().BoolVar(&purge, "purge", false,
		"also remove the lab's resource pool and storage definition")

	return cmd
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

// destroyDeletePool deletes the lab's resource pool. A 404 (already deleted,
// or never created) is treated as already-gone, not an error.
func destroyDeletePool(ctx context.Context, api *apiclient.APIClient, poolID string) error {
	err := api.Pools.DeletePools(ctx, &pools.DeletePoolsParams{Poolid: poolID})
	if err != nil && !errors.Is(err, pveerrors.ErrNotFound) {
		return fmt.Errorf("delete pool %q: %w", poolID, err)
	}
	return nil
}

// destroyDeleteStorage deletes the lab's storage.cfg entry. A 404 (already
// deleted, or never created) is treated as already-gone, not an error.
func destroyDeleteStorage(ctx context.Context, api *apiclient.APIClient, stgID string) error {
	err := api.ClusterStorage.DeleteStorage(ctx, stgID)
	if err != nil && !errors.Is(err, pveerrors.ErrNotFound) {
		return fmt.Errorf("delete storage %q: %w", stgID, err)
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
