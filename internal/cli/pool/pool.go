package pool

import (
	"bufio"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/pools"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// Group builds the `pve pool` command and all of its sub-commands.
// The passed *cli.Deps is a placeholder used only so cobra can assemble the
// command tree; live dependencies are resolved per-invocation via cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Manage resource pools",
		Long:  "List, inspect, create, update, and delete Proxmox VE resource pools.",
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

// newListCmd builds `pve pool list`.
func newListCmd() *cobra.Command {
	var poolType, poolid string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List resource pools",
		Args:  cobra.NoArgs,
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

// newGetCmd builds `pve pool get <poolid>`.
// Uses GET /pools?poolid=<id> (non-deprecated) instead of GET /pools/{poolid}.
func newGetCmd() *cobra.Command {
	var poolType string
	cmd := &cobra.Command{
		Use:   "get <poolid>",
		Short: "Show a resource pool's configuration and members",
		Args:  cobra.ExactArgs(1),
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

// newShowCmd builds `pve pool show <poolid>`.
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
		Args:  cobra.ExactArgs(1),
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

// newCreateCmd builds `pve pool create`.
func newCreateCmd() *cobra.Command {
	var poolid, comment string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new resource pool",
		Args:  cobra.NoArgs,
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

// newSetCmd builds `pve pool set <poolid>`.
// Uses PUT /pools (non-deprecated) instead of PUT /pools/{poolid}.
func newSetCmd() *cobra.Command {
	var comment, vms, storage string
	var del, allowMove bool
	cmd := &cobra.Command{
		Use:   "set <poolid>",
		Short: "Update a resource pool's members or comment",
		Args:  cobra.ExactArgs(1),
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

// newDeleteCmd builds `pve pool delete <poolid>`.
// Uses DELETE /pools (non-deprecated) instead of DELETE /pools/{poolid}.
func newDeleteCmd() *cobra.Command {
	var yes, destroyVMs, destroyStorage bool
	cmd := &cobra.Command{
		Use:   "delete <poolid>",
		Short: "Delete a resource pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			poolid := args[0]

			// The DELETE /pools endpoint still does not support member destruction.
			// Fail loudly rather than silently ignoring the documented flags.
			if destroyVMs || destroyStorage {
				return fmt.Errorf(
					"--destroy-vms/--destroy-storage are not supported: the Proxmox VE pool delete API " +
						"does not accept member-destruction options; remove members first with `pve pool set`")
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
