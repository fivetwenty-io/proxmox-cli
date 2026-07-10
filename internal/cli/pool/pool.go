package pool

import (
	"bufio"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/pools"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// Group builds the `pmx pool` command and all of its sub-commands.
// The passed *cli.Deps is a placeholder used only so cobra can assemble the
// command tree; live dependencies are resolved per-invocation via cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Manage resource pools",
		Long: `Manage Proxmox VE resource pools: list, inspect, create, update, and
delete pools, add or remove member VMs and storage, and inspect a pool's
effective permissions. Requires a configured Proxmox VE API connection.

Sub-commands take a pool by its poolid. 'pool set' adds or removes members via
--vms/--storage (pass --delete to remove instead of add); 'pool delete' does
not accept member-destruction flags and prompts for confirmation unless
--yes/-y is given.`,
		Example: `  pmx pve pool create --poolid backups --comment "Backup targets"
  pmx pve pool set backups --vms 100,101
  pmx pve pool list
  pmx pve pool get backups`,
	}
	cmd.AddCommand(
		newListCmd(),
		newGetCmd(),
		newShowCmd(),
		newCreateCmd(),
		newSetCmd(),
		newDeleteCmd(),
		newPermissionsCmd(),
	)
	return cmd
}

// poolListEntry is the subset of a /pools list element the CLI renders.
type poolListEntry struct {
	Poolid  string            `json:"poolid"`
	Comment string            `json:"comment"`
	Members []json.RawMessage `json:"members"`
}

// newListCmd builds `pmx pool list`.
func newListCmd() *cobra.Command {
	var poolType, poolid string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List resource pools",
		Long: "List the resource pools defined on the cluster, showing each pool's " +
			"identifier, comment, and member count. Use --type to list only pools that " +
			"contain a given member kind (qemu, lxc, or storage), or --poolid to show a " +
			"single pool.",
		Example: `  pmx pve pool list
  pmx pve pool list --type qemu`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pools.ListPoolsParams{}
			if poolType != "" {
				params.Type = &poolType
			}
			if poolid != "" {
				params.Poolid = &poolid
			}

			resp, err := deps.API.Pools.ListPools(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list pools: %w", err)
			}

			entries := make([]poolListEntry, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e poolListEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode pool entry: %w", err)
					}
					entries = append(entries, e)
				}
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Poolid < entries[j].Poolid })

			res := output.Result{
				Headers: []string{"POOLID", "COMMENT", "MEMBERS"},
				Raw:     entries,
			}
			for _, e := range entries {
				res.Rows = append(res.Rows, []string{e.Poolid, e.Comment, fmt.Sprintf("%d", len(e.Members))})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&poolType, "type", "", "filter by member type: qemu|lxc|storage")
	cmd.Flags().StringVar(&poolid, "poolid", "", "show only the pool with this identifier")
	return cmd
}

// poolGetEntry is the shape of a single element returned by GET /pools?poolid=<id>.
type poolGetEntry struct {
	Poolid  string            `json:"poolid"`
	Comment *string           `json:"comment"`
	Members []json.RawMessage `json:"members"`
}

// newGetCmd builds `pmx pool get <poolid>`.
// Uses GET /pools?poolid=<id> (non-deprecated) instead of GET /pools/{poolid}.
func newGetCmd() *cobra.Command {
	var poolType string
	cmd := &cobra.Command{
		Use:   "get <poolid>",
		Short: "Show a resource pool's configuration and members",
		Long: "Show a resource pool's comment and member count by its poolid, read through " +
			"the current list-based pools endpoint. Pass --type to restrict the members " +
			"counted to a kind (qemu, lxc, or storage). See 'pool show' for the equivalent " +
			"that reads the older single-pool endpoint.",
		Example: `  pmx pve pool get backups
  pmx pve pool get backups --type qemu`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			poolid := args[0]

			params := &pools.ListPoolsParams{Poolid: &poolid}
			if poolType != "" {
				params.Type = &poolType
			}

			resp, err := deps.API.Pools.ListPools(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get pool %q: %w", poolid, err)
			}

			if resp == nil || len(*resp) == 0 {
				return fmt.Errorf("get pool %q: not found", poolid)
			}

			var entry poolGetEntry
			if err := json.Unmarshal((*resp)[0], &entry); err != nil {
				return fmt.Errorf("get pool %q: decode response: %w", poolid, err)
			}

			single := map[string]string{"poolid": poolid}
			if entry.Comment != nil {
				single["comment"] = *entry.Comment
			}
			single["members"] = fmt.Sprintf("%d", len(entry.Members))

			res := output.Result{
				Single: single,
				Raw: map[string]any{
					"poolid":  poolid,
					"comment": entry.Comment,
					"members": entry.Members,
				},
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&poolType, "type", "", "filter members by type: qemu|lxc|storage")
	return cmd
}

// newShowCmd builds `pmx pool show <poolid>`.
// Uses the deprecated-but-still-live GET /pools/{poolid} endpoint (single-object
// response keyed by poolid), distinct from `pool get`'s GET /pools?poolid=<id>
// (list response filtered to one element). Proxmox VE has not removed this
// endpoint despite the deprecation notice; it is exposed here for parity with
// the API surface and for operators/scripts that already target it directly.
func newShowCmd() *cobra.Command {
	var poolType string
	cmd := &cobra.Command{
		Use:   "show <poolid>",
		Short: "Show a resource pool's configuration and members (single-item endpoint)",
		Long: "Show a resource pool's comment and member count by its poolid using the older " +
			"single-pool endpoint, kept for parity with the API surface and for scripts that " +
			"target it directly. The output matches 'pool get'; pass --type to restrict the " +
			"members counted to a kind (qemu, lxc, or storage).",
		Example: `  pmx pve pool show backups
  pmx pve pool show backups --type storage`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			poolid := args[0]

			params := &pools.GetPoolsParams{}
			if poolType != "" {
				params.Type = &poolType
			}

			resp, err := deps.API.Pools.GetPools(cmd.Context(), poolid, params)
			if err != nil {
				return fmt.Errorf("show pool %q: %w", poolid, err)
			}

			single := map[string]string{"poolid": poolid}
			if resp.Comment != nil {
				single["comment"] = *resp.Comment
			}
			single["members"] = fmt.Sprintf("%d", len(resp.Members))

			res := output.Result{
				Single: single,
				Raw: map[string]any{
					"poolid":  poolid,
					"comment": resp.Comment,
					"members": resp.Members,
				},
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&poolType, "type", "", "filter members by type: qemu|lxc|storage")
	return cmd
}

// newCreateCmd builds `pmx pool create`.
func newCreateCmd() *cobra.Command {
	var poolid, comment string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new resource pool",
		Long: "Create a new, empty resource pool. --poolid is required and becomes the pool's " +
			"identifier; --comment sets an optional description. Members are added afterward " +
			"with 'pool set'.",
		Example: `  pmx pve pool create --poolid backups
  pmx pve pool create --poolid backups --comment "Backup targets"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pools.CreatePoolsParams{Poolid: poolid}
			if comment != "" {
				params.Comment = &comment
			}

			if err := deps.API.Pools.CreatePools(cmd.Context(), params); err != nil {
				return fmt.Errorf("create pool %q: %w", poolid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Pool %q created.", poolid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&poolid, "poolid", "", "pool identifier (required)")
	cmd.Flags().StringVar(&comment, "comment", "", "pool comment")
	cli.MustMarkRequired(cmd, "poolid")
	return cmd
}

// newSetCmd builds `pmx pool set <poolid>`.
// Uses PUT /pools (non-deprecated) instead of PUT /pools/{poolid}.
func newSetCmd() *cobra.Command {
	var comment, vms, storage string
	var del, allowMove bool
	cmd := &cobra.Command{
		Use:   "set <poolid>",
		Short: "Update a resource pool's members or comment",
		Long: "Update a resource pool: change its --comment or add members with --vms and " +
			"--storage (comma-separated guest VMIDs and storage IDs). Pass --delete to remove " +
			"the listed members instead of adding them, and --allow-move to add a guest that " +
			"already belongs to another pool, moving it out of its current pool.",
		Example: `  pmx pve pool set backups --vms 100,101
  pmx pve pool set backups --storage local,local-lvm
  pmx pve pool set backups --vms 100 --delete`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			poolid := args[0]

			params := &pools.UpdatePoolsParams{Poolid: poolid}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if vms != "" {
				params.Vms = &vms
			}
			if storage != "" {
				params.Storage = &storage
			}
			if del {
				params.Delete = &del
			}
			if allowMove {
				params.AllowMove = &allowMove
			}

			if err := deps.API.Pools.UpdatePools(cmd.Context(), params); err != nil {
				return fmt.Errorf("update pool %q: %w", poolid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Pool %q updated.", poolid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "pool comment")
	cmd.Flags().StringVar(&vms, "vms", "", "comma-separated guest VMIDs to add (or remove with --delete)")
	cmd.Flags().StringVar(&storage, "storage", "", "comma-separated storage IDs to add (or remove with --delete)")
	cmd.Flags().BoolVar(&del, "delete", false, "remove the passed VMIDs and/or storage IDs instead of adding them")
	cmd.Flags().BoolVar(&allowMove, "allow-move", false,
		"add a guest even if it already belongs to another pool, moving it from its current pool")
	return cmd
}

// newDeleteCmd builds `pmx pool delete <poolid>`.
// Uses DELETE /pools (non-deprecated) instead of DELETE /pools/{poolid}.
func newDeleteCmd() *cobra.Command {
	var yes, destroyVMs, destroyStorage bool
	cmd := &cobra.Command{
		Use:   "delete <poolid>",
		Short: "Delete a resource pool",
		Long: "Delete a resource pool by its poolid. Prompts for confirmation unless --yes/-y " +
			"is passed. This removes the pool definition only and never destroys member " +
			"guests or storage; the --destroy-vms and --destroy-storage flags are rejected " +
			"because the Proxmox VE pool delete API does not support member destruction.",
		Example: `  pmx pve pool delete backups
  pmx pve pool delete backups --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			poolid := args[0]

			// The DELETE /pools endpoint still does not support member destruction.
			// Fail loudly rather than silently ignoring the documented flags.
			if destroyVMs || destroyStorage {
				return fmt.Errorf(
					"--destroy-vms/--destroy-storage are not supported: the Proxmox VE pool delete API " +
						"does not accept member-destruction options; remove members first with `pmx pool set`")
			}

			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Delete pool %q?", poolid))
				if err != nil {
					return err
				}
				if !ok {
					res := output.Result{Message: "Aborted."}
					return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
				}
			}

			if err := deps.API.Pools.DeletePools(cmd.Context(), &pools.DeletePoolsParams{Poolid: poolid}); err != nil {
				return fmt.Errorf("delete pool %q: %w", poolid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Pool %q deleted.", poolid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&destroyVMs, "destroy-vms", false,
		"destroy member guests (unsupported by the API; errors if set)")
	cmd.Flags().BoolVar(&destroyStorage, "destroy-storage", false,
		"destroy member storage (unsupported by the API; errors if set)")
	return cmd
}

// confirm prints a yes/no prompt to stderr and reads a single line from the
// command's input. It returns true only when the response begins with 'y' or
// 'Y'. The prompt is written to stderr (never stdout) so it does not corrupt
// machine-readable output captured on stdout.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N]: ", prompt)
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	line = strings.TrimSpace(line)
	return strings.HasPrefix(strings.ToLower(line), "y"), nil
}
