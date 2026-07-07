package pbs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newEncryptionKeyCmd builds `pve pbs encryption-key` — manage datastore
// encryption keys (/config/encryption-keys), used to encrypt/decrypt backup
// content client-side. Not to be confused with tape encryption keys
// (/config/tape-encryption-keys), which are a separate key store managed by
// their own commands.
func newEncryptionKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "encryption-key",
		Short: "Manage datastore encryption keys",
		Long: "List, create, delete, and toggle the archive state of datastore " +
			"encryption keys used to encrypt/decrypt backup content client-side " +
			"(/config/encryption-keys). Not to be confused with tape encryption keys.",
	}
	cmd.AddCommand(
		newEncKeyLsCmd(),
		newEncKeyAddCmd(),
		newEncKeyDeleteCmd(),
		newEncKeyToggleArchiveCmd(),
	)
	return cmd
}

// encKeyEntry is the decoded shape of one element of GET
// /config/encryption-keys.
type encKeyEntry struct {
	ArchivedAt  *int64  `json:"archived-at,omitempty"`
	Created     int64   `json:"created"`
	Fingerprint *string `json:"fingerprint,omitempty"`
	Hint        *string `json:"hint,omitempty"`
	Id          string  `json:"id"`
	Kdf         string  `json:"kdf"`
	Modified    int64   `json:"modified"`
	Path        *string `json:"path,omitempty"`
}

// newEncKeyLsCmd builds `pve pbs encryption-key ls` — list configured
// datastore encryption keys (GET /config/encryption-keys).
func newEncKeyLsCmd() *cobra.Command {
	var includeArchived bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List configured encryption keys",
		Long: "List the datastore encryption keys configured on this server (GET " +
			"/config/encryption-keys). Pass --include-archived to also list " +
			"archived keys.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsconfig.ListEncryptionKeysParams{}
			if cmd.Flags().Changed("include-archived") {
				params.IncludeArchived = boolPtr(includeArchived)
			}

			resp, err := deps.PBS.Config.ListEncryptionKeys(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list encryption keys: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]encKeyEntry, 0, len(items))

			for _, raw := range items {
				var e encKeyEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode encryption key entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Id < entries[j].Id })

			headers := []string{"ID", "KDF", "FINGERPRINT", "HINT", "CREATED", "MODIFIED", "ARCHIVED-AT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Id, e.Kdf, pbsFormatOptionalString(e.Fingerprint), pbsFormatOptionalString(e.Hint),
					strconv.FormatInt(e.Created, 10), strconv.FormatInt(e.Modified, 10),
					pbsFormatOptionalInt64(e.ArchivedAt),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "also list archived keys")
	return cmd
}

// newEncKeyAddCmd builds `pve pbs encryption-key add <id>` — create a
// datastore encryption key (POST /config/encryption-keys).
func newEncKeyAddCmd() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create a datastore encryption key",
		Long: "Create a new datastore encryption key, or register an existing " +
			"one, under <id> (POST /config/encryption-keys). Pass --key to " +
			"register an existing key instead of generating a new one; the " +
			"required key material format/encoding is defined by PBS, not this CLI.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			params := &pbsconfig.CreateEncryptionKeysParams{Id: id}
			if cmd.Flags().Changed("key") {
				params.Key = strPtr(key)
			}

			err := deps.PBS.Config.CreateEncryptionKeys(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create encryption key %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Encryption key %q created.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "use this existing key instead of generating a new one")
	return cmd
}

// newEncKeyDeleteCmd builds `pve pbs encryption-key delete <id>` — remove a
// datastore encryption key (DELETE /config/encryption-keys/{id}).
func newEncKeyDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a datastore encryption key",
		Long: "Remove a datastore encryption key (DELETE /config/encryption-keys/{id}). " +
			"This is destructive: backups encrypted with this key become unrecoverable. " +
			"Pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete encryption key %q without confirmation: pass --yes/-y", id)
			}

			params := &pbsconfig.DeleteEncryptionKeysParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteEncryptionKeys(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("delete encryption key %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Encryption key %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newEncKeyToggleArchiveCmd builds `pve pbs encryption-key toggle-archive
// <id>` — flip a key's archived state (POST /config/encryption-keys/{id}).
//
// This command's name and behavior follow the binding's generated method,
// CreateEncryptionKeys2, whose PBS API schema description is: "Toggle the
// archive state for the key by given id, archived keys are no longer usable
// to encrypt contents." The endpoint takes no target-state parameter (only
// an optional --digest guard) — every call flips the current state, so
// running it twice on the same key restores its original state. Archived
// keys remain usable to decrypt existing content; they can just no longer
// encrypt new content.
func newEncKeyToggleArchiveCmd() *cobra.Command {
	var digest string
	cmd := &cobra.Command{
		Use:   "toggle-archive <id>",
		Short: "Toggle a key's archived state",
		Long: "Toggle the archive state of a datastore encryption key (POST " +
			"/config/encryption-keys/{id}). Archived keys can no longer encrypt " +
			"new content, but remain usable to decrypt existing content. Running " +
			"this again on an archived key un-archives it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			params := &pbsconfig.CreateEncryptionKeys2Params{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.CreateEncryptionKeys2(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("toggle archive state of encryption key %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Encryption key %q archive state toggled.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only toggle if the current config digest matches")
	return cmd
}
