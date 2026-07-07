package pbs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	pbstape "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/tape"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newTapeMediaCmd builds `pmx pbs tape media` — inspect the tape media
// database (registered media, their backed-up content, and media sets), and
// move, destroy, or set the status of individual media (/tape/media/*).
//
// GET /tape/media, GET /tape/media/list/{uuid}, and GET
// /tape/media/list/{uuid}/status are not exposed as commands: per the PBS
// API schema all three are directory-index/status-probe endpoints whose
// declared return type is "null" or carry no data of their own here — `ls`
// (GET /tape/media/list) and `set-status` (POST .../status) already surface
// the equivalent information.
func newTapeMediaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "media",
		Short: "Manage tape media",
		Long: "List tape media and their backed-up content and media-set " +
			"membership, move media to a vault or offline, destroy media " +
			"records, and set a medium's status.",
	}
	cmd.AddCommand(
		newTapeMediaLsCmd(),
		newTapeMediaContentCmd(),
		newTapeMediaSetsCmd(),
		newTapeMediaMoveCmd(),
		newTapeMediaDestroyCmd(),
		newTapeMediaSetStatusCmd(),
	)
	return cmd
}

// tapeMediaListEntry is the decoded shape of one element of GET
// /tape/media/list: a registered backup medium.
type tapeMediaListEntry struct {
	BytesUsed     *int64  `json:"bytes-used,omitempty"`
	Catalog       bool    `json:"catalog"`
	Ctime         int64   `json:"ctime"`
	Expired       bool    `json:"expired"`
	LabelText     string  `json:"label-text"`
	Location      string  `json:"location"`
	MediaSetCtime *int64  `json:"media-set-ctime,omitempty"`
	MediaSetName  *string `json:"media-set-name,omitempty"`
	MediaSetUuid  *string `json:"media-set-uuid,omitempty"`
	Pool          *string `json:"pool,omitempty"`
	SeqNr         *int64  `json:"seq-nr,omitempty"`
	Status        string  `json:"status"`
	Uuid          string  `json:"uuid"`
}

// newTapeMediaLsCmd builds `pmx pbs tape media ls` — list registered backup
// media (GET /tape/media/list).
func newTapeMediaLsCmd() *cobra.Command {
	var (
		pool                string
		updateStatus        bool
		updateStatusChanger string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List registered backup media",
		Long: "List the tape media registered in the media database (GET " +
			"/tape/media/list). Pass --update-status together with " +
			"--update-status-changer to refresh each medium's online/offline " +
			"status from that changer's inventory before listing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbstape.ListMediaListParams{}
			fl := cmd.Flags()

			if fl.Changed("pool") {
				params.Pool = strPtr(pool)
			}

			if fl.Changed("update-status") {
				params.UpdateStatus = boolPtr(updateStatus)
			}

			if fl.Changed("update-status-changer") {
				params.UpdateStatusChanger = strPtr(updateStatusChanger)
			}

			resp, err := deps.PBS.Tape.ListMediaList(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list tape media: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]tapeMediaListEntry, 0, len(items))

			for _, raw := range items {
				var e tapeMediaListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode tape media entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].LabelText < entries[j].LabelText })

			headers := []string{
				"LABEL-TEXT", "UUID", "STATUS", "LOCATION", "POOL", "MEDIA-SET-NAME",
				"SEQ-NR", "BYTES-USED", "EXPIRED", "CATALOG",
			}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.LabelText, e.Uuid, e.Status, e.Location, pbsFormatOptionalString(e.Pool),
					pbsFormatOptionalString(e.MediaSetName), pbsFormatOptionalInt64(e.SeqNr),
					pbsFormatOptionalInt64(e.BytesUsed), strconv.FormatBool(e.Expired), strconv.FormatBool(e.Catalog),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&pool, "pool", "", "restrict to media in this pool")
	f.BoolVar(&updateStatus, "update-status", false,
		"query the changer given by --update-status-changer to refresh online/offline status")
	f.StringVar(&updateStatusChanger, "update-status-changer", "",
		"changer identifier to query when --update-status is set")
	return cmd
}

// tapeMediaContentEntry is the decoded shape of one element of GET
// /tape/media/content: one backup snapshot recorded on a medium.
type tapeMediaContentEntry struct {
	BackupTime    int64  `json:"backup-time"`
	LabelText     string `json:"label-text"`
	MediaSetCtime int64  `json:"media-set-ctime"`
	MediaSetName  string `json:"media-set-name"`
	MediaSetUuid  string `json:"media-set-uuid"`
	Pool          string `json:"pool"`
	SeqNr         int64  `json:"seq-nr"`
	Snapshot      string `json:"snapshot"`
	Store         string `json:"store"`
	Uuid          string `json:"uuid"`
}

// newTapeMediaContentCmd builds `pmx pbs tape media content` — list the
// backup snapshots recorded on tape media (GET /tape/media/content),
// optionally filtered by backup ID/type, media, media set, or pool.
func newTapeMediaContentCmd() *cobra.Command {
	var (
		backupId   string
		backupType string
		labelText  string
		media      string
		mediaSet   string
		pool       string
	)
	cmd := &cobra.Command{
		Use:   "content",
		Short: "List backup snapshots stored on tape media",
		Long: "List the backup snapshots recorded on tape media (GET " +
			"/tape/media/content), optionally filtered by backup ID/type, media, " +
			"media set, or pool.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbstape.ListMediaContentParams{}
			fl := cmd.Flags()

			if fl.Changed("backup-id") {
				params.BackupId = strPtr(backupId)
			}

			if fl.Changed("backup-type") {
				params.BackupType = strPtr(backupType)
			}

			if fl.Changed("label-text") {
				params.LabelText = strPtr(labelText)
			}

			if fl.Changed("media") {
				params.Media = strPtr(media)
			}

			if fl.Changed("media-set") {
				params.MediaSet = strPtr(mediaSet)
			}

			if fl.Changed("pool") {
				params.Pool = strPtr(pool)
			}

			resp, err := deps.PBS.Tape.ListMediaContent(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list tape media content: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]tapeMediaContentEntry, 0, len(items))

			for _, raw := range items {
				var e tapeMediaContentEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode tape media content entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].Store != entries[j].Store {
					return entries[i].Store < entries[j].Store
				}

				return entries[i].Snapshot < entries[j].Snapshot
			})

			headers := []string{
				"STORE", "SNAPSHOT", "LABEL-TEXT", "UUID", "POOL", "MEDIA-SET-NAME", "SEQ-NR", "BACKUP-TIME",
			}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Store, e.Snapshot, e.LabelText, e.Uuid, e.Pool, e.MediaSetName,
					strconv.FormatInt(e.SeqNr, 10), strconv.FormatInt(e.BackupTime, 10),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&backupId, "backup-id", "", "filter by backup ID")
	f.StringVar(&backupType, "backup-type", "", "filter by backup type")
	f.StringVar(&labelText, "label-text", "", "filter by media label/barcode")
	f.StringVar(&media, "media", "", "filter by media UUID")
	f.StringVar(&mediaSet, "media-set", "", "filter by media-set UUID")
	f.StringVar(&pool, "pool", "", "filter by media pool")
	return cmd
}

// tapeMediaSetEntry is the decoded shape of one element of GET
// /tape/media/media-sets: one media set.
type tapeMediaSetEntry struct {
	MediaSetCtime int64  `json:"media-set-ctime"`
	MediaSetName  string `json:"media-set-name"`
	MediaSetUuid  string `json:"media-set-uuid"`
	Pool          string `json:"pool"`
}

// newTapeMediaSetsCmd builds `pmx pbs tape media sets` — list tape media
// sets (GET /tape/media/media-sets).
func newTapeMediaSetsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sets",
		Short: "List tape media sets",
		Long:  "List every media set known to the tape backend (GET /tape/media/media-sets).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Tape.ListMediaMediaSets(cmd.Context())
			if err != nil {
				return fmt.Errorf("list tape media sets: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]tapeMediaSetEntry, 0, len(items))

			for _, raw := range items {
				var e tapeMediaSetEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode tape media set entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].MediaSetName < entries[j].MediaSetName })

			headers := []string{"MEDIA-SET-NAME", "MEDIA-SET-UUID", "POOL", "MEDIA-SET-CTIME"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.MediaSetName, e.MediaSetUuid, e.Pool, strconv.FormatInt(e.MediaSetCtime, 10),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapeMediaMoveCmd builds `pmx pbs tape media move` — move a tape medium
// to a vault or mark it offline (POST /tape/media/move). This endpoint's
// PBS API schema declares a "null" return type: it carries no UPID, so this
// command reports success directly instead of going through
// finishAsync/--async task-wait semantics.
func newTapeMediaMoveCmd() *cobra.Command {
	var (
		labelText string
		uuid      string
		vaultName string
	)
	cmd := &cobra.Command{
		Use:   "move",
		Short: "Move tape media to a vault or offline",
		Long: "Change a tape medium's recorded location to a named vault (with " +
			"--vault-name), or mark it offline (POST /tape/media/move). Identify " +
			"the medium with --uuid or --label-text; at least one is required. " +
			"This is a synchronous operation: the PBS API returns no task ID for it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if !fl.Changed("uuid") && !fl.Changed("label-text") {
				return fmt.Errorf("move tape media: --uuid or --label-text is required to identify the medium")
			}

			params := &pbstape.CreateMediaMoveParams{}

			if fl.Changed("label-text") {
				params.LabelText = strPtr(labelText)
			}

			if fl.Changed("uuid") {
				params.Uuid = strPtr(uuid)
			}

			if fl.Changed("vault-name") {
				params.VaultName = strPtr(vaultName)
			}

			err := deps.PBS.Tape.CreateMediaMove(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("move tape media: %w", err)
			}

			dest := "offline"
			if fl.Changed("vault-name") {
				dest = fmt.Sprintf("vault %q", vaultName)
			}

			res := output.Result{Message: fmt.Sprintf("Tape media moved to %s.", dest)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&labelText, "label-text", "", "media label/barcode identifying the medium")
	f.StringVar(&uuid, "uuid", "", "media UUID identifying the medium")
	f.StringVar(&vaultName, "vault-name", "", "vault name to move the medium to (omit to mark it offline)")
	return cmd
}

// newTapeMediaDestroyCmd builds `pmx pbs tape media destroy` — completely
// remove a tape medium's record from the media database (GET
// /tape/media/destroy — a destructive read per the PBS API schema). Its
// PBS API schema declares a "null" return type: it carries no UPID, so this
// command reports success directly instead of going through
// finishAsync/--async task-wait semantics.
func newTapeMediaDestroyCmd() *cobra.Command {
	var (
		force     bool
		labelText string
		uuid      string
		yes       bool
	)
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy a tape medium's database record",
		Long: "Completely remove a tape medium's record from the media database " +
			"(GET /tape/media/destroy). Identify the medium with --uuid or " +
			"--label-text; at least one is required. Pass --force to remove it " +
			"even if it belongs to a media set. This is a synchronous operation: " +
			"the PBS API returns no task ID for it. This is destructive: the " +
			"medium's cataloged backup content becomes unrecoverable through PBS. " +
			"Pass --yes/-y to confirm.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if !fl.Changed("uuid") && !fl.Changed("label-text") {
				return fmt.Errorf("destroy tape media: --uuid or --label-text is required to identify the medium")
			}

			if !yes {
				return fmt.Errorf("refusing to destroy tape media without confirmation: pass --yes/-y")
			}

			params := &pbstape.ListMediaDestroyParams{}

			if fl.Changed("force") {
				params.Force = boolPtr(force)
			}

			if fl.Changed("label-text") {
				params.LabelText = strPtr(labelText)
			}

			if fl.Changed("uuid") {
				params.Uuid = strPtr(uuid)
			}

			err := deps.PBS.Tape.ListMediaDestroy(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("destroy tape media: %w", err)
			}

			res := output.Result{Message: "Tape media destroyed."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "remove the medium even if it belongs to a media set")
	f.StringVar(&labelText, "label-text", "", "media label/barcode identifying the medium")
	f.StringVar(&uuid, "uuid", "", "media UUID identifying the medium")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newTapeMediaSetStatusCmd builds `pmx pbs tape media set-status <uuid>` —
// set a tape medium's status (POST /tape/media/list/{uuid}/status). Its PBS
// API schema declares a "null" return type: it carries no UPID, so this
// command reports success directly instead of going through
// finishAsync/--async task-wait semantics.
func newTapeMediaSetStatusCmd() *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "set-status <uuid>",
		Short: "Set a tape medium's status",
		Long: "Update a tape medium's status to 'full', 'damaged', or 'retired' " +
			"(POST /tape/media/list/{uuid}/status). Pass --status; omitting it " +
			"clears the medium's status back to its internally managed state " +
			"('writable' and 'unknown' are managed automatically and cannot be " +
			"set directly). This is a synchronous operation: the PBS API returns " +
			"no task ID for it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			uuid := args[0]

			params := &pbstape.CreateMediaListStatusParams{}

			if cmd.Flags().Changed("status") {
				if status == "" {
					return fmt.Errorf("--status: value must not be empty")
				}

				params.Status = strPtr(status)
			}

			err := deps.PBS.Tape.CreateMediaListStatus(cmd.Context(), uuid, params)
			if err != nil {
				return fmt.Errorf("set tape media status %q: %w", uuid, err)
			}

			msg := fmt.Sprintf("Tape media %q status cleared.", uuid)
			if cmd.Flags().Changed("status") {
				msg = fmt.Sprintf("Tape media %q status set to %q.", uuid, status)
			}

			res := output.Result{Message: msg}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&status, "status", "",
		"medium status: full, damaged, or retired (omit to clear the status back to its managed default)")
	return cmd
}
