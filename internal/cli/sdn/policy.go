package sdn

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
)

// --- prefix-list ---

// newPrefixListCmd builds `pmx sdn prefix-list` and its sub-commands.
func newPrefixListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "prefix-list",
		Aliases: []string{"prefix-lists"},
		Short:   "Manage SDN prefix lists (route-filtering rule sets)",
		Long: "List, create, inspect, update, and delete SDN prefix lists and their " +
			"entries. Changes are staged until committed with `pmx sdn apply`.",
	}
	cmd.AddCommand(
		newPrefixListListCmd(),
		newPrefixListCreateCmd(),
		newPrefixListGetCmd(),
		newPrefixListSetCmd(),
		newPrefixListDeleteCmd(),
		newPrefixListEntryCmd(),
	)
	return cmd
}

func newPrefixListListCmd() *cobra.Command {
	var (
		pending bool
		running bool
		verbose bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List SDN prefix lists",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &cluster.ListSdnPrefixListsParams{}
			fl := cmd.Flags()
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			if fl.Changed("verbose") {
				params.Verbose = boolPtr(verbose)
			}
			resp, err := deps.API.Cluster.ListSdnPrefixLists(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list SDN prefix lists: %w", err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	f.BoolVar(&verbose, "verbose", false, "return all properties, not just identifiers")
	return cmd
}

func newPrefixListCreateCmd() *cobra.Command {
	var (
		entries   []string
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create an SDN prefix list",
		Long:  "Create an SDN prefix list. The change is staged until `pmx sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			params := &cluster.CreateSdnPrefixListsParams{Id: id}
			if fl.Changed("entry") {
				params.Entries = entries
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.CreateSdnPrefixLists(cmd.Context(), params); err != nil {
				return fmt.Errorf("create SDN prefix list %q: %w", id, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN prefix list %q created (run `pmx sdn apply` to commit).", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&entries, "entry", nil, "prefix list entry, e.g. \"seq 10 permit 10.0.0.0/8\" (repeatable)")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

func newPrefixListGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show an SDN prefix list's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			resp, err := deps.API.Cluster.GetSdnPrefixLists(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get SDN prefix list %q: %w", id, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	return cmd
}

func newPrefixListSetCmd() *cobra.Command {
	var (
		entries   []string
		del       []string
		digest    string
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Update an SDN prefix list",
		Long:  "Update an SDN prefix list. The change is staged until `pmx sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "entry", "delete", "digest", "lock-token") {
				return fmt.Errorf("no changes requested: pass at least one field flag")
			}
			params := &cluster.UpdateSdnPrefixListsParams{}
			if fl.Changed("entry") {
				params.Entries = entries
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.UpdateSdnPrefixLists(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update SDN prefix list %q: %w", id, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN prefix list %q updated (run `pmx sdn apply` to commit).", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&entries, "entry", nil, "replacement prefix list entry (repeatable)")
	f.StringArrayVar(&del, "delete", nil, "entry to clear (repeatable)")
	f.StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

func newPrefixListDeleteCmd() *cobra.Command {
	var (
		yes       bool
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an SDN prefix list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete SDN prefix list %q without confirmation: pass --yes", id)
			}
			params := &cluster.DeleteSdnPrefixListsParams{}
			if cmd.Flags().Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			err := deps.API.Cluster.DeleteSdnPrefixLists(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("delete SDN prefix list %q: %w", id, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN prefix list %q deleted (run `pmx sdn apply` to commit).", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

// newPrefixListEntryCmd builds `pmx sdn prefix-list entry` and its sub-commands.
func newPrefixListEntryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "entry",
		Short: "Manage individual entries of an SDN prefix list",
	}
	cmd.AddCommand(
		newPrefixListEntryListCmd(),
		newPrefixListEntryAddCmd(),
		newPrefixListEntryGetCmd(),
		newPrefixListEntrySetCmd(),
		newPrefixListEntryDeleteCmd(),
	)
	return cmd
}

func newPrefixListEntryListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <id>",
		Short: "List the entries of an SDN prefix list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			resp, err := deps.API.Cluster.ListSdnPrefixListsEntries(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("list entries of SDN prefix list %q: %w", id, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
	return cmd
}

func newPrefixListEntryAddCmd() *cobra.Command {
	var (
		action    string
		prefix    string
		ge        int64
		le        int64
		seq       int64
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "add <id> --action <permit|deny> --prefix <cidr>",
		Short: "Add an entry to an SDN prefix list",
		Long:  "Add an entry to an SDN prefix list. The change is staged until `pmx sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			params := &cluster.CreateSdnPrefixListsEntriesParams{Action: action, Prefix: prefix}
			if fl.Changed("ge") {
				params.Ge = int64Ptr(ge)
			}
			if fl.Changed("le") {
				params.Le = int64Ptr(le)
			}
			if fl.Changed("seq") {
				params.Seq = int64Ptr(seq)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.CreateSdnPrefixListsEntries(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("add entry to SDN prefix list %q: %w", id, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Entry added to SDN prefix list %q (run `pmx sdn apply` to commit).", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&action, "action", "", "match policy: permit or deny (required)")
	f.StringVar(&prefix, "prefix", "", "the IP prefix to match, e.g. 10.0.0.0/8 (required)")
	f.Int64Var(&ge, "ge", 0, "minimum prefix length to match (greater-or-equal)")
	f.Int64Var(&le, "le", 0, "maximum prefix length to match (less-or-equal)")
	f.Int64Var(&seq, "seq", 0, "sequence number of this entry")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	cli.MustMarkRequired(cmd, "action")
	cli.MustMarkRequired(cmd, "prefix")
	return cmd
}

func newPrefixListEntryGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id> <seq>",
		Short: "Show a single entry of an SDN prefix list",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id, seq := args[0], args[1]
			resp, err := deps.API.Cluster.GetSdnPrefixListsEntries(cmd.Context(), id, seq)
			if err != nil {
				return fmt.Errorf("get entry %q of SDN prefix list %q: %w", seq, id, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	return cmd
}

func newPrefixListEntrySetCmd() *cobra.Command {
	var (
		action    string
		prefix    string
		ge        int64
		le        int64
		seq       int64
		del       []string
		digest    string
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "set <id> <seq>",
		Short: "Update a single entry of an SDN prefix list",
		Long:  "Update a single entry of an SDN prefix list. The change is staged until `pmx sdn apply`.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id, urlSeq := args[0], args[1]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "action", "prefix", "ge", "le", "seq", "delete", "digest", "lock-token") {
				return fmt.Errorf("no changes requested: pass at least one field flag")
			}
			params := &cluster.UpdateSdnPrefixListsEntriesParams{}
			if fl.Changed("action") {
				params.Action = strPtr(action)
			}
			if fl.Changed("prefix") {
				params.Prefix = strPtr(prefix)
			}
			if fl.Changed("ge") {
				params.Ge = int64Ptr(ge)
			}
			if fl.Changed("le") {
				params.Le = int64Ptr(le)
			}
			if fl.Changed("seq") {
				params.Seq = int64Ptr(seq)
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.UpdateSdnPrefixListsEntries(cmd.Context(), id, urlSeq, params); err != nil {
				return fmt.Errorf("update entry %q of SDN prefix list %q: %w", urlSeq, id, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Entry %q of SDN prefix list %q updated (run `pmx sdn apply` to commit).", urlSeq, id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&action, "action", "", "match policy: permit or deny")
	f.StringVar(&prefix, "prefix", "", "the IP prefix to match")
	f.Int64Var(&ge, "ge", 0, "minimum prefix length to match (greater-or-equal)")
	f.Int64Var(&le, "le", 0, "maximum prefix length to match (less-or-equal)")
	f.Int64Var(&seq, "seq", 0, "sequence number of this entry")
	f.StringArrayVar(&del, "delete", nil, "property to clear (repeatable)")
	f.StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

func newPrefixListEntryDeleteCmd() *cobra.Command {
	var (
		yes       bool
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "delete <id> <seq>",
		Short: "Delete a single entry of an SDN prefix list",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id, seq := args[0], args[1]
			if !yes {
				return fmt.Errorf(
					"refusing to delete entry %q of SDN prefix list %q without confirmation: pass --yes", seq, id)
			}
			params := &cluster.DeleteSdnPrefixListsEntriesParams{}
			if cmd.Flags().Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			err := deps.API.Cluster.DeleteSdnPrefixListsEntries(cmd.Context(), id, seq, params)
			if err != nil {
				return fmt.Errorf("delete entry %q of SDN prefix list %q: %w", seq, id, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Entry %q of SDN prefix list %q deleted (run `pmx sdn apply` to commit).", seq, id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

// --- route-map ---

// newRouteMapCmd builds `pmx sdn route-map` and its sub-commands. A route map is
// an ordered list of entries; there is no standalone route-map object, so a
// route map comes into existence when its first entry is added.
func newRouteMapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "route-map",
		Aliases: []string{"route-maps"},
		Short:   "Manage SDN route maps (BGP route policy)",
		Long: "List route maps, show their entries, and manage individual entries. " +
			"Changes are staged until committed with `pmx sdn apply`.",
	}
	cmd.AddCommand(
		newRouteMapListCmd(),
		newRouteMapGetCmd(),
		newRouteMapEntryCmd(),
	)
	return cmd
}

func newRouteMapListCmd() *cobra.Command {
	var running bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List SDN route maps",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &cluster.ListSdnRouteMapsParams{}
			if cmd.Flags().Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.ListSdnRouteMaps(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list SDN route maps: %w", err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
	cmd.Flags().BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

func newRouteMapGetCmd() *cobra.Command {
	var (
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "get <route-map>",
		Short: "Show the entries of an SDN route map",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			routeMap := args[0]
			params := &cluster.GetSdnRouteMapsEntriesParams{}
			fl := cmd.Flags()
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.GetSdnRouteMapsEntries(cmd.Context(), routeMap, params)
			if err != nil {
				return fmt.Errorf("get SDN route map %q: %w", routeMap, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

// newRouteMapEntryCmd builds `pmx sdn route-map entry` and its sub-commands.
func newRouteMapEntryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "entry",
		Short: "Manage individual entries of SDN route maps",
	}
	cmd.AddCommand(
		newRouteMapEntryListCmd(),
		newRouteMapEntryAddCmd(),
		newRouteMapEntryGetCmd(),
		newRouteMapEntrySetCmd(),
		newRouteMapEntryDeleteCmd(),
	)
	return cmd
}

func newRouteMapEntryListCmd() *cobra.Command {
	var (
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List route map entries across all route maps",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &cluster.ListSdnRouteMapsEntriesParams{}
			fl := cmd.Flags()
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.ListSdnRouteMapsEntries(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list SDN route map entries: %w", err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

func newRouteMapEntryAddCmd() *cobra.Command {
	var (
		order      int64
		action     string
		match      []string
		set        []string
		call       string
		exitAction string
		lockToken  string
	)
	cmd := &cobra.Command{
		Use:   "add <route-map> --order <n> --action <permit|deny>",
		Short: "Add an entry to an SDN route map",
		Long:  "Add an entry to an SDN route map. The change is staged until `pmx sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			routeMap := args[0]
			fl := cmd.Flags()
			params := &cluster.CreateSdnRouteMapsEntriesParams{
				RouteMapId: routeMap,
				Order:      order,
				Action:     action,
			}
			if fl.Changed("match") {
				params.Match = match
			}
			if fl.Changed("set") {
				params.Set = set
			}
			if fl.Changed("call") {
				params.Call = strPtr(call)
			}
			if fl.Changed("exit-action") {
				params.ExitAction = strPtr(exitAction)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.CreateSdnRouteMapsEntries(cmd.Context(), params); err != nil {
				return fmt.Errorf("add entry to SDN route map %q: %w", routeMap, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Entry added to SDN route map %q (run `pmx sdn apply` to commit).", routeMap)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&order, "order", 0, "index of this entry within the route map (required)")
	f.StringVar(&action, "action", "", "match policy: permit or deny (required)")
	f.StringArrayVar(&match, "match", nil, "match clause, e.g. \"ip address prefix-list foo\" (repeatable)")
	f.StringArrayVar(&set, "set", nil, "set clause, e.g. \"local-preference 200\" (repeatable)")
	f.StringVar(&call, "call", "", "route map to call")
	f.StringVar(&exitAction, "exit-action", "", "action on exit: next or goto")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	cli.MustMarkRequired(cmd, "order")
	cli.MustMarkRequired(cmd, "action")
	return cmd
}

func newRouteMapEntryGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <route-map> <order>",
		Short: "Show a single entry of an SDN route map",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			routeMap, order := args[0], args[1]
			resp, err := deps.API.Cluster.GetSdnRouteMapsEntriesEntry(cmd.Context(), routeMap, order)
			if err != nil {
				return fmt.Errorf("get entry %q of SDN route map %q: %w", order, routeMap, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	return cmd
}

func newRouteMapEntrySetCmd() *cobra.Command {
	var (
		action     string
		match      []string
		set        []string
		call       string
		exitAction string
		del        []string
		digest     string
		lockToken  string
	)
	cmd := &cobra.Command{
		Use:   "set <route-map> <order>",
		Short: "Update a single entry of an SDN route map",
		Long:  "Update a single entry of an SDN route map. The change is staged until `pmx sdn apply`.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			routeMap, order := args[0], args[1]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "action", "match", "set", "call", "exit-action", "delete", "digest", "lock-token") {
				return fmt.Errorf("no changes requested: pass at least one field flag")
			}
			params := &cluster.UpdateSdnRouteMapsEntriesEntryParams{}
			if fl.Changed("action") {
				params.Action = strPtr(action)
			}
			if fl.Changed("match") {
				params.Match = match
			}
			if fl.Changed("set") {
				params.Set = set
			}
			if fl.Changed("call") {
				params.Call = strPtr(call)
			}
			if fl.Changed("exit-action") {
				params.ExitAction = strPtr(exitAction)
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.UpdateSdnRouteMapsEntriesEntry(cmd.Context(), routeMap, order, params); err != nil {
				return fmt.Errorf("update entry %q of SDN route map %q: %w", order, routeMap, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Entry %q of SDN route map %q updated (run `pmx sdn apply` to commit).", order, routeMap)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&action, "action", "", "match policy: permit or deny")
	f.StringArrayVar(&match, "match", nil, "replacement match clause (repeatable)")
	f.StringArrayVar(&set, "set", nil, "replacement set clause (repeatable)")
	f.StringVar(&call, "call", "", "route map to call")
	f.StringVar(&exitAction, "exit-action", "", "action on exit: next or goto")
	f.StringArrayVar(&del, "delete", nil, "property to clear (repeatable)")
	f.StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

func newRouteMapEntryDeleteCmd() *cobra.Command {
	var (
		yes       bool
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "delete <route-map> <order>",
		Short: "Delete a single entry of an SDN route map",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			routeMap, order := args[0], args[1]
			if !yes {
				return fmt.Errorf(
					"refusing to delete entry %q of SDN route map %q without confirmation: pass --yes", order, routeMap)
			}
			params := &cluster.DeleteSdnRouteMapsEntriesEntryParams{}
			if cmd.Flags().Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			err := deps.API.Cluster.DeleteSdnRouteMapsEntriesEntry(cmd.Context(), routeMap, order, params)
			if err != nil {
				return fmt.Errorf("delete entry %q of SDN route map %q: %w", order, routeMap, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Entry %q of SDN route map %q deleted (run `pmx sdn apply` to commit).", order, routeMap)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}
