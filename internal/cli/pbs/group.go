package pbs

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// groupEntry mirrors one element of the JSON array PBS returns from
// GET /admin/datastore/{store}/groups. The generated binding types this
// response as []json.RawMessage (see ListDatastoreGroupsResponse in
// pkg/pbs/admin), so this CLI defines its own decode target based on the
// PBS API's documented GroupListItem schema.
type groupEntry struct {
	BackupType  string   `json:"backup-type"`
	BackupID    string   `json:"backup-id"`
	BackupCount int64    `json:"backup-count"`
	LastBackup  *int64   `json:"last-backup,omitempty"`
	Owner       *string  `json:"owner,omitempty"`
	Comment     *string  `json:"comment,omitempty"`
	Files       []string `json:"files,omitempty"`
}

// decodeGroupEntries unmarshals each raw element of resp into a typed
// groupEntry. A malformed element is a hard error (not silently skipped) so
// a partially-decoded group list is never mistaken for a complete one.
func decodeGroupEntries(resp *pbsadmin.ListDatastoreGroupsResponse) ([]groupEntry, error) {
	if resp == nil {
		return []groupEntry{}, nil
	}

	entries := make([]groupEntry, 0, len(*resp))
	for i, raw := range *resp {
		var e groupEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			return nil, fmt.Errorf("unmarshal group entry %d: %w", i, err)
		}

		entries = append(entries, e)
	}

	return entries, nil
}

// newGroupCmd builds the `pmx pbs group` command and its sub-commands for
// managing backup groups (the collection of every snapshot sharing a
// backup-type/backup-id pair) in a PBS datastore.
func newGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Manage PBS backup groups",
		Long: "List, delete, and annotate backup groups — the collection of every " +
			"snapshot sharing a backup-type/backup-id pair — in a datastore. A group is " +
			"addressed as <type>/<id>, where type is one of vm, ct, or host.",
	}
	cmd.AddCommand(
		newGroupLsCmd(),
		newGroupDeleteCmd(),
		newGroupNotesCmd(),
	)

	return cmd
}

// newGroupLsCmd builds `pmx pbs group ls` — list backup groups in a
// datastore (GET /admin/datastore/{store}/groups).
func newGroupLsCmd() *cobra.Command {
	var df storeFlags

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List backup groups in a datastore",
		Long:  "List every backup group in a datastore, with backup count, last-backup time, owner, and comment.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsadmin.ListDatastoreGroupsParams{Ns: df.nsPtr(cmd)}

			resp, err := deps.PBS.Admin.ListDatastoreGroups(cmd.Context(), df.store, params)
			if err != nil {
				return fmt.Errorf("list groups in datastore %q: %w", df.store, err)
			}

			entries, err := decodeGroupEntries(resp)
			if err != nil {
				return fmt.Errorf("decode group list in datastore %q: %w", df.store, err)
			}

			headers := []string{"GROUP", "BACKUP-COUNT", "LAST-BACKUP", "OWNER", "COMMENT"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					groupRefString(e.BackupType, e.BackupID),
					strconv.FormatInt(e.BackupCount, 10),
					epochCellPtr(e.LastBackup),
					strCellPtr(e.Owner),
					strCellPtr(e.Comment),
				})
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
	df.register(cmd)

	return cmd
}

// newGroupDeleteCmd builds `pmx pbs group delete <type>/<id>` —
// permanently remove a backup group and every snapshot it contains (DELETE
// /admin/datastore/{store}/groups).
func newGroupDeleteCmd() *cobra.Command {
	var (
		df               storeFlags
		errorOnProtected bool
		yes              bool
	)

	cmd := &cobra.Command{
		Use:   "delete <type>/<id>",
		Short: "Delete a backup group and all its snapshots",
		Long: "Permanently remove a backup group and every snapshot it contains from a " +
			"datastore. With --error-on-protected, the deletion fails instead of silently " +
			"skipping protected snapshots. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			btype, bid, err := parseGroupRef(args[0])
			if err != nil {
				return err
			}

			if !yes {
				return fmt.Errorf("refusing to delete group %q without confirmation: pass --yes/-y", args[0])
			}

			params := &pbsadmin.DeleteDatastoreGroupsParams{
				BackupType: btype,
				BackupId:   bid,
				Ns:         df.nsPtr(cmd),
			}
			if cmd.Flags().Changed("error-on-protected") {
				params.ErrorOnProtected = boolPtr(errorOnProtected)
			}

			resp, err := deps.PBS.Admin.DeleteDatastoreGroups(cmd.Context(), df.store, params)
			if err != nil {
				return fmt.Errorf("delete group %q in datastore %q: %w", args[0], df.store, err)
			}

			msg := fmt.Sprintf("Group %s deleted.", args[0])
			if resp != nil {
				msg = fmt.Sprintf("Group %s deleted (%d snapshots removed, %d protected snapshots skipped).",
					args[0], resp.RemovedSnapshots, resp.ProtectedSnapshots)
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg, Raw: resp}, deps.Format)
		},
	}
	df.register(cmd)
	cmd.Flags().BoolVar(&errorOnProtected, "error-on-protected", false,
		"fail instead of skipping when the group has protected snapshots")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")

	return cmd
}

// newGroupNotesCmd builds `pmx pbs group notes <type>/<id>` — without
// --set, print the backup group's notes text (GET
// /admin/datastore/{store}/group-notes); with --set TEXT, replace it (PUT
// /admin/datastore/{store}/group-notes).
func newGroupNotesCmd() *cobra.Command {
	var (
		df  storeFlags
		set string
	)

	cmd := &cobra.Command{
		Use:   "notes <type>/<id>",
		Short: "Get or set the notes attached to a backup group",
		Long: "Without --set, print the free-text notes attached to a backup group. " +
			"With --set TEXT, replace the notes with the given text.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			btype, bid, err := parseGroupRef(args[0])
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("set") {
				params := &pbsadmin.UpdateDatastoreGroupNotesParams{
					BackupType: btype,
					BackupId:   bid,
					Ns:         df.nsPtr(cmd),
					Notes:      set,
				}

				err = deps.PBS.Admin.UpdateDatastoreGroupNotes(cmd.Context(), df.store, params)
				if err != nil {
					return fmt.Errorf("set notes for group %q in datastore %q: %w", args[0], df.store, err)
				}

				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{Message: fmt.Sprintf("Notes for group %s updated.", args[0])}, deps.Format)
			}

			notes, err := fetchGroupNotes(cmd, deps, df.store, btype, bid, df.nsPtr(cmd))
			if err != nil {
				return err
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: map[string]string{"notes": notes}, Raw: notes, Message: notes}, deps.Format)
		},
	}
	df.register(cmd)
	cmd.Flags().StringVar(&set, "set", "", "replace the group's notes with this text")

	return cmd
}

// fetchGroupNotes retrieves the free-text notes for a backup group. Same raw
// transport workaround as fetchSnapshotNotes (see its comment): the
// generated Admin.ListDatastoreGroupNotes binding discards its response
// body, so this calls the shared raw transport directly against the
// identical path and params.
func fetchGroupNotes(cmd *cobra.Command, deps *cli.Deps, store, btype, bid string, ns *string) (string, error) {
	params := map[string]interface{}{
		"backup-type": btype,
		"backup-id":   bid,
	}
	if ns != nil {
		params["ns"] = *ns
	}

	path := "/admin/datastore/" + url.PathEscape(store) + "/group-notes"

	data, err := deps.PBS.Raw.GetCtx(cmd.Context(), path, params)
	if err != nil {
		return "", fmt.Errorf("get notes for group in datastore %q: %w", store, err)
	}

	notes, ok := data.(string)
	if !ok {
		return "", fmt.Errorf("get notes for group in datastore %q: unexpected response type %T", store, data)
	}

	return notes, nil
}
