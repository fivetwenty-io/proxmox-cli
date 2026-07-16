package qemu

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newHookscriptCmd builds `pmx pve qemu hookscript` — a focused view over the
// hookscript config key so the attach/inspect/detach workflow does not require
// spelling `config set --hookscript` / `config set --delete hookscript`.
func newHookscriptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hookscript",
		Short: "Manage the hookscript attached to a VM",
		Long: "Show, set, or remove the hookscript volume attached to a VM. A hookscript is " +
			"an executable stored on snippets-capable storage (e.g. local:snippets/hook.pl) " +
			"that PVE runs on the HOST at each guest lifecycle phase (pre-start, post-start, " +
			"pre-stop, post-stop). Upload one with " +
			"`pmx pve storage upload <storage> --file <script> --content snippets`.",
		Example: `  pmx pve qemu hookscript get 100
  pmx pve qemu hookscript set 100 local:snippets/hook.pl
  pmx pve qemu hookscript unset 100`,
	}
	cmd.AddCommand(
		newHookscriptGetCmd(),
		newHookscriptSetCmd(),
		newHookscriptUnsetCmd(),
	)
	return cmd
}

// newHookscriptGetCmd builds `pmx pve qemu hookscript get <vmid|name>`.
func newHookscriptGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <vmid|name>",
		Short: "Show the hookscript attached to a VM",
		Long: "Show the hookscript volume attached to a VM, or report that none is " +
			"configured.",
		Example: `  pmx pve qemu hookscript get 100`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			path := fmt.Sprintf("/nodes/%s/qemu/%s/config", url.PathEscape(node), url.PathEscape(vmid))
			data, err := deps.API.Raw.GetCtx(cmd.Context(), path, nil)
			if err != nil {
				return fmt.Errorf("get config for VM %s on node %q: %w", vmid, node, err)
			}
			m, ok := data.(map[string]any)
			if !ok {
				return fmt.Errorf("decode VM config: unexpected response shape %T", data)
			}
			hookscript, _ := m["hookscript"].(string)
			if hookscript == "" {
				res := output.Result{Message: fmt.Sprintf("VM %s has no hookscript configured.", vmid)}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}
			res := output.Result{
				Single: map[string]string{"vmid": vmid, "hookscript": hookscript},
				Raw:    map[string]string{"vmid": vmid, "hookscript": hookscript},
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newHookscriptSetCmd builds `pmx pve qemu hookscript set <vmid|name> <volume>`.
func newHookscriptSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <vmid|name> <volume>",
		Short: "Attach a hookscript volume to a VM",
		Long: "Attach a hookscript volume (e.g. local:snippets/hook.pl) to a VM. The script " +
			"must already exist on a snippets-capable storage; PVE executes it on the HOST " +
			"during VM lifecycle events.",
		Example: `  pmx pve qemu hookscript set 100 local:snippets/hook.pl`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			volume := args[1]

			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"WARNING: the hookscript executes on the HOST during VM lifecycle events")

			params := &nodes.UpdateQemuConfigParams{Hookscript: strPtr(volume)}
			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("set hookscript on VM %s: %w", vmid, err)
			}
			res := output.Result{Message: fmt.Sprintf("Hookscript %q set on VM %s.", volume, vmid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newHookscriptUnsetCmd builds `pmx pve qemu hookscript unset <vmid|name>`.
func newHookscriptUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <vmid|name>",
		Short: "Remove the hookscript from a VM",
		Long: "Detach the hookscript from a VM. The script file itself stays on its " +
			"storage; remove it with `pmx pve storage volume delete` if it is no longer needed.",
		Example: `  pmx pve qemu hookscript unset 100`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.UpdateQemuConfigParams{Delete: strPtr("hookscript")}
			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("remove hookscript from VM %s: %w", vmid, err)
			}
			res := output.Result{Message: fmt.Sprintf("Hookscript removed from VM %s.", vmid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
