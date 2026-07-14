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
	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/peppi"
)

// newDestroyCmd builds `pmx lab destroy <name>`.
//
// It resolves the lab (peppi-guarding every identifier the config exposes),
// locates the lab's VM by its pool's qemu membership, peppi-guards the
// resolved VMID a second time, then — after a confirmation gate and an
// optional --dry-run preview — stops the VM if running and deletes it.
// --purge additionally removes the lab's resource pool and storage
// definition. Every step queries live state first, so re-running destroy
// against a lab that is partially or fully torn down already reports the
// missing pieces as already gone rather than erroring.
func newDestroyCmd() *cobra.Command {
	var (
		yes    bool
		dryRun bool
		purge  bool
	)

	cmd := &cobra.Command{
		Use:   "destroy <name>",
		Short: "Destroy a lab's VM",
		Long: "Destroy the named lab's VM: stop it if running, then delete it. Refuses to " +
			"run without --yes/-y or an interactive 'y' confirmation. Pass --purge to also " +
			"remove the lab's resource pool and storage definition. Pass --dry-run to preview " +
			"what would be destroyed without mutating anything or prompting.",
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

			vmid, node, vmFound, err := destroyLocateVM(ctx, deps.API, poolID)
			if err != nil {
				return fmt.Errorf("locate VM for lab %q: %w", name, err)
			}

			if vmFound {
				target := peppi.Target{
					VMID: vmid,
					Names: []string{
						poolID,
						lab.Network.VnetID,
						stgID,
						lab.DNS.Zone,
						lab.Name,
					},
				}
				if err := peppi.Guard(target); err != nil {
					return err
				}
			}

			var plan []string
			if vmFound {
				plan = append(plan, fmt.Sprintf("VM %d on node %q", vmid, node))
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

			if vmFound {
				if err := destroyStopIfRunning(ctx, deps.API, node, vmid); err != nil {
					return err
				}
				if err := destroyDeleteVM(ctx, deps.API, node, vmid); err != nil {
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

// destroyPoolMember is the minimal decoded shape of one entry from
// pools.GetPoolsResponse.Members. Vmid uses pve.PVEInt since PVE's Perl-based
// API renders an integer field as either a JSON number or a JSON string
// depending on endpoint.
type destroyPoolMember struct {
	ID   string     `json:"id"`
	Type string     `json:"type"`
	Node string     `json:"node"`
	VMID pve.PVEInt `json:"vmid"`
}

// destroyLocateVM finds the single qemu guest belonging to poolID by calling
// pools.GetPools filtered to type=qemu. It returns found=false, with no
// error, both when the pool itself does not exist (HTTP 404) and when the
// pool exists but currently has no qemu member — both cases mean "no VM to
// destroy" rather than a failure. More than one qemu member is refused as
// ambiguous: lab tooling only ever expects at most one VM per lab pool, and
// guessing which one to destroy would be unsafe.
func destroyLocateVM(ctx context.Context, api *apiclient.APIClient, poolID string) (vmid int, node string, found bool, err error) {
	qemuType := "qemu"
	resp, err := api.Pools.GetPools(ctx, poolID, &pools.GetPoolsParams{Type: &qemuType})
	if err != nil {
		if errors.Is(err, pveerrors.ErrNotFound) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("look up VM for pool %q: %w", poolID, err)
	}
	if resp == nil {
		return 0, "", false, nil
	}

	var match *destroyPoolMember
	for _, raw := range resp.Members {
		var m destroyPoolMember
		if err := json.Unmarshal(raw, &m); err != nil {
			return 0, "", false, fmt.Errorf("decode member of pool %q: %w", poolID, err)
		}
		if m.Type != "qemu" || m.VMID.Int() == 0 {
			continue
		}
		if match != nil {
			return 0, "", false, fmt.Errorf(
				"pool %q has more than one qemu member (VMIDs %d and %d); refusing to guess which VM to destroy",
				poolID, match.VMID.Int(), m.VMID.Int())
		}
		mCopy := m
		match = &mCopy
	}

	if match == nil {
		return 0, "", false, nil
	}
	return int(match.VMID.Int()), match.Node, true, nil
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
// the delete task completes. A 404 (the VM vanished between the pool lookup
// and this call) is treated as already-gone, not an error.
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
