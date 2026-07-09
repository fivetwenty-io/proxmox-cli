package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newRemoteCmd builds `pmx pbs remote` — manage remote Proxmox Backup Server
// connections used as sync-job endpoints, and scan them for datastores,
// backup groups, and namespaces (/config/remote CRUD and scan sub-tree).
func newRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage remote Proxmox Backup Server connections",
		Long: "List, inspect, create, update, and delete remote Proxmox Backup Server " +
			"connections used as sync-job endpoints, and scan a remote for its " +
			"datastores, backup groups, and namespaces.",
	}
	cmd.AddCommand(
		newRemoteLsCmd(),
		newRemoteShowCmd(),
		newRemoteAddCmd(),
		newRemoteUpdateCmd(),
		newRemoteDeleteCmd(),
		newRemoteScanCmd(),
	)
	return cmd
}

// remoteListEntry is the decoded shape of one element of GET /config/remote.
type remoteListEntry struct {
	AuthId      string  `json:"auth-id"`
	Comment     *string `json:"comment,omitempty"`
	Fingerprint *string `json:"fingerprint,omitempty"`
	Host        string  `json:"host"`
	Name        string  `json:"name"`
	Port        *int64  `json:"port,omitempty"`
}

// newRemoteLsCmd builds `pmx pbs remote ls` — list configured remotes
// (GET /config/remote).
func newRemoteLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List configured remotes",
		Long:  "List the remote Proxmox Backup Server connections configured on this server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListRemote(cmd.Context())
			if err != nil {
				return fmt.Errorf("list remotes: %w", err)
			}

			items := rawItemsOf(resp)
			type remoteListRow struct {
				entry remoteListEntry
				raw   map[string]any
			}
			table := make([]remoteListRow, 0, len(items))

			for _, raw := range items {
				var e remoteListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode remote entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode remote entry: %w", err)
				}

				table = append(table, remoteListRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Name < table[j].entry.Name })

			headers := []string{"NAME", "HOST", "PORT", "AUTH-ID", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Name, e.Host, pbsFormatOptionalInt64(e.Port), e.AuthId, pbsFormatOptionalString(e.Comment),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRemoteShowCmd builds `pmx pbs remote show <name>` — show a single
// remote's configuration (GET /config/remote/{name}).
func newRemoteShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a single remote's configuration",
		Long: "Show every populated field of a single remote configuration (GET " +
			"/config/remote/{name}). The PBS API omits options left at their built-in " +
			"defaults; pass --defaults to also list those, with the value they " +
			"effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			resp, err := deps.PBS.Config.GetRemote(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("get remote %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode remote %q: %w", name, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(remoteOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// remoteFlags collects the remote attribute flags shared by `add` and
// `update`. Every field maps directly onto a CreateRemoteParams /
// UpdateRemoteParams field of the same name.
type remoteFlags struct {
	authId       string
	comment      string
	fingerprint  string
	host         string
	password     string
	port         int64
	useNodeProxy bool

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both `add` and `update`.
func (rf *remoteFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&rf.authId, "auth-id", "", "authentication ID on the remote (e.g. 'root@pam' or an API token)")
	f.StringVar(&rf.comment, "comment", "", "comment")
	f.StringVar(&rf.fingerprint, "fingerprint", "", "X509 certificate fingerprint (sha256) of the remote")
	f.StringVar(&rf.host, "host", "", "DNS name or IP address of the remote")
	f.StringVar(&rf.password, "password", "", "password or API token secret for the remote")
	f.Int64Var(&rf.port, "port", 0, "port of the remote (default: 8007)")
	f.BoolVar(&rf.useNodeProxy, "use-node-proxy", false, "use the node's HTTP proxy configuration for this remote")
}

// registerUpdate binds every flag `update` accepts, including the
// update-only delete/digest fields.
func (rf *remoteFlags) registerUpdate(cmd *cobra.Command) {
	rf.registerCommon(cmd)
	f := cmd.Flags()
	f.StringArrayVar(&rf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&rf.digest, "digest", "", "only update if the current config digest matches")
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (rf *remoteFlags) applyUpdate(cmd *cobra.Command, p *pbsconfig.UpdateRemoteParams) {
	fl := cmd.Flags()
	if fl.Changed("auth-id") {
		p.AuthId = &rf.authId
	}
	if fl.Changed("comment") {
		p.Comment = &rf.comment
	}
	if fl.Changed("fingerprint") {
		p.Fingerprint = &rf.fingerprint
	}
	if fl.Changed("host") {
		p.Host = &rf.host
	}
	if fl.Changed("password") {
		p.Password = &rf.password
	}
	if fl.Changed("port") {
		p.Port = &rf.port
	}
	if fl.Changed("use-node-proxy") {
		p.UseNodeProxy = &rf.useNodeProxy
	}
	if fl.Changed("delete") {
		p.Delete = rf.del
	}
	if fl.Changed("digest") {
		p.Digest = &rf.digest
	}
}

// newRemoteAddCmd builds `pmx pbs remote add <name>` — create a remote
// configuration (POST /config/remote). --host, --auth-id, and --password
// are required; every other option is optional.
func newRemoteAddCmd() *cobra.Command {
	var rf remoteFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a remote connection",
		Long: "Create a new remote Proxmox Backup Server connection (POST " +
			"/config/remote). --host, --auth-id, and --password are required; " +
			"every other option is optional and only forwarded when explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if rf.host == "" {
				return fmt.Errorf("--host is required")
			}

			if rf.authId == "" {
				return fmt.Errorf("--auth-id is required")
			}

			if rf.password == "" {
				return fmt.Errorf("--password is required")
			}

			params := &pbsconfig.CreateRemoteParams{
				Name:     name,
				Host:     rf.host,
				AuthId:   rf.authId,
				Password: rf.password,
			}

			fl := cmd.Flags()
			if fl.Changed("comment") {
				params.Comment = &rf.comment
			}
			if fl.Changed("fingerprint") {
				params.Fingerprint = &rf.fingerprint
			}
			if fl.Changed("port") {
				params.Port = &rf.port
			}
			if fl.Changed("use-node-proxy") {
				params.UseNodeProxy = &rf.useNodeProxy
			}

			err := deps.PBS.Config.CreateRemote(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create remote %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Remote %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	rf.registerCommon(cmd)
	cli.MustMarkRequired(cmd, "host")
	cli.MustMarkRequired(cmd, "auth-id")
	cli.MustMarkRequired(cmd, "password")
	return cmd
}

// newRemoteUpdateCmd builds `pmx pbs remote update <name>` — update a remote
// configuration (PUT /config/remote/{name}). Only flags explicitly set are
// sent; use --delete to reset properties to their default.
func newRemoteUpdateCmd() *cobra.Command {
	var rf remoteFlags
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a remote connection",
		Long: "Update an existing remote Proxmox Backup Server connection (PUT " +
			"/config/remote/{name}). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update remote %q: no changes requested: pass at least one flag", name)
			}

			if cmd.Flags().Changed("delete") {
				for _, key := range rf.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateRemoteParams{}
			rf.applyUpdate(cmd, params)

			err := deps.PBS.Config.UpdateRemote(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update remote %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Remote %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	rf.registerUpdate(cmd)
	return cmd
}

// newRemoteDeleteCmd builds `pmx pbs remote delete <name>` — remove a remote
// configuration (DELETE /config/remote/{name}).
func newRemoteDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a remote connection",
		Long: "Remove a remote Proxmox Backup Server connection (DELETE /config/remote/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete remote %q without confirmation: pass --yes/-y", name)
			}

			params := &pbsconfig.DeleteRemoteParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteRemote(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("delete remote %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Remote %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newRemoteScanCmd builds `pmx pbs remote scan` — list a remote's
// datastores, and the backup groups or namespaces within one of them
// (GET /config/remote/{name}/scan and its /groups, /namespaces children).
//
// GET /config/remote/{name}/scan/{store} (Config.GetRemoteScan) is not
// exposed as a command: per the PBS API schema it is a directory-index
// endpoint whose declared return type is "null" — it carries no data of its
// own, only routing to the /groups and /namespaces children below it.
// Rendering it as a data-bearing command would misrepresent an empty
// response as a real result.
func newRemoteScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan a remote for datastores, backup groups, and namespaces",
		Long: "List the datastores visible on a remote, or the backup groups and " +
			"namespaces within one of its datastores.",
	}
	cmd.AddCommand(newRemoteScanLsCmd(), newRemoteScanGroupsCmd(), newRemoteScanNamespacesCmd())
	return cmd
}

// remoteScanDatastoreEntry is the decoded shape of one element of
// GET /config/remote/{name}/scan.
type remoteScanDatastoreEntry struct {
	BackendType string  `json:"backend-type"`
	Comment     *string `json:"comment,omitempty"`
	Maintenance *string `json:"maintenance,omitempty"`
	MountStatus string  `json:"mount-status"`
	Store       string  `json:"store"`
}

// newRemoteScanLsCmd builds `pmx pbs remote scan ls <remote>` — list the
// datastores accessible on a remote (GET /config/remote/{name}/scan).
func newRemoteScanLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls <remote>",
		Short: "List a remote's accessible datastores",
		Long:  "List the datastores accessible on a remote connection (GET /config/remote/{name}/scan).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			resp, err := deps.PBS.Config.ListRemoteScan(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("scan remote %q: %w", name, err)
			}

			items := rawItemsOf(resp)
			type remoteScanDatastoreRow struct {
				entry remoteScanDatastoreEntry
				raw   map[string]any
			}
			table := make([]remoteScanDatastoreRow, 0, len(items))

			for _, raw := range items {
				var e remoteScanDatastoreEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode remote datastore entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode remote datastore entry: %w", err)
				}

				table = append(table, remoteScanDatastoreRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Store < table[j].entry.Store })

			headers := []string{"STORE", "BACKEND-TYPE", "MOUNT-STATUS", "MAINTENANCE", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Store, e.BackendType, e.MountStatus,
					pbsFormatOptionalString(e.Maintenance), pbsFormatOptionalString(e.Comment),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// remoteScanGroupEntry is the decoded shape of one element of
// GET /config/remote/{name}/scan/{store}/groups.
type remoteScanGroupEntry struct {
	BackupCount int64    `json:"backup-count"`
	BackupId    string   `json:"backup-id"`
	BackupType  string   `json:"backup-type"`
	Comment     *string  `json:"comment,omitempty"`
	Files       []string `json:"files"`
	LastBackup  int64    `json:"last-backup"`
	Owner       *string  `json:"owner,omitempty"`
}

// newRemoteScanGroupsCmd builds `pmx pbs remote scan groups <remote>
// <store>` — list the backup groups in a remote's datastore (GET
// /config/remote/{name}/scan/{store}/groups), optionally scoped to a
// namespace with --namespace.
func newRemoteScanGroupsCmd() *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "groups <remote> <store>",
		Short: "List backup groups in a remote's datastore",
		Long: "List the backup groups accessible in a datastore on a remote (GET " +
			"/config/remote/{name}/scan/{store}/groups), scoped to a namespace with --namespace.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			store := args[1]

			params := &pbsconfig.ListRemoteScanGroupsParams{}
			if cmd.Flags().Changed("namespace") {
				params.Namespace = strPtr(namespace)
			}

			resp, err := deps.PBS.Config.ListRemoteScanGroups(cmd.Context(), name, store, params)
			if err != nil {
				return fmt.Errorf("scan groups on remote %q datastore %q: %w", name, store, err)
			}

			items := rawItemsOf(resp)
			type remoteScanGroupRow struct {
				entry remoteScanGroupEntry
				raw   map[string]any
			}
			table := make([]remoteScanGroupRow, 0, len(items))

			for _, raw := range items {
				var e remoteScanGroupEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode remote group entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode remote group entry: %w", err)
				}

				table = append(table, remoteScanGroupRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool {
				if table[i].entry.BackupType != table[j].entry.BackupType {
					return table[i].entry.BackupType < table[j].entry.BackupType
				}
				return table[i].entry.BackupId < table[j].entry.BackupId
			})

			headers := []string{"TYPE", "ID", "BACKUP-COUNT", "LAST-BACKUP", "OWNER", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.BackupType, e.BackupId, pbsFormatOptionalInt64(&e.BackupCount),
					pbsFormatOptionalInt64(&e.LastBackup), pbsFormatOptionalString(e.Owner),
					pbsFormatOptionalString(e.Comment),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "restrict the scan to this namespace")
	return cmd
}

// remoteScanNamespaceEntry is the decoded shape of one element of
// GET /config/remote/{name}/scan/{store}/namespaces.
type remoteScanNamespaceEntry struct {
	Comment *string `json:"comment,omitempty"`
	Ns      string  `json:"ns"`
}

// newRemoteScanNamespacesCmd builds `pmx pbs remote scan namespaces
// <remote> <store>` — list the namespaces in a remote's datastore (GET
// /config/remote/{name}/scan/{store}/namespaces).
func newRemoteScanNamespacesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "namespaces <remote> <store>",
		Short: "List namespaces in a remote's datastore",
		Long: "List the namespaces accessible in a datastore on a remote (GET " +
			"/config/remote/{name}/scan/{store}/namespaces).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			store := args[1]

			resp, err := deps.PBS.Config.ListRemoteScanNamespaces(cmd.Context(), name, store)
			if err != nil {
				return fmt.Errorf("scan namespaces on remote %q datastore %q: %w", name, store, err)
			}

			items := rawItemsOf(resp)
			type remoteScanNamespaceRow struct {
				entry remoteScanNamespaceEntry
				raw   map[string]any
			}
			table := make([]remoteScanNamespaceRow, 0, len(items))

			for _, raw := range items {
				var e remoteScanNamespaceEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode remote namespace entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode remote namespace entry: %w", err)
				}

				table = append(table, remoteScanNamespaceRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Ns < table[j].entry.Ns })

			headers := []string{"NS", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				rows = append(rows, []string{t.entry.Ns, pbsFormatOptionalString(t.entry.Comment)})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
