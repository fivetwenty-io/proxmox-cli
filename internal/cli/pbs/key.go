package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newTapeKeyCmd builds `pmx pbs tape key` — create, inspect, update, and
// delete tape encryption key configurations (GET/POST/PUT/DELETE
// /config/tape-encryption-keys).
func newTapeKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage tape encryption keys",
		Long: "Create, inspect, update, and delete tape encryption key configurations " +
			"(GET/POST/PUT/DELETE /config/tape-encryption-keys).",
	}
	cmd.AddCommand(
		newTapeKeyLsCmd(),
		newTapeKeyShowCmd(),
		newTapeKeyAddCmd(),
		newTapeKeyUpdateCmd(),
		newTapeKeyDeleteCmd(),
	)
	return cmd
}

// tapeKeyEntry is the decoded shape of one element of GET
// /config/tape-encryption-keys.
type tapeKeyEntry struct {
	Created     int64   `json:"created"`
	Fingerprint *string `json:"fingerprint,omitempty"`
	Hint        *string `json:"hint,omitempty"`
	Kdf         string  `json:"kdf"`
	Modified    int64   `json:"modified"`
	Path        *string `json:"path,omitempty"`
}

// decodeTapeKeyEntries decodes a Config.ListTapeEncryptionKeys response into
// typed entries, skipping any element that fails to decode.
func decodeTapeKeyEntries(resp *pbsconfig.ListTapeEncryptionKeysResponse) []tapeKeyEntry {
	entries := make([]tapeKeyEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e tapeKeyEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newTapeKeyLsCmd builds `pmx pbs tape key ls` — list every tape encryption
// key (GET /config/tape-encryption-keys).
func newTapeKeyLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List tape encryption keys",
		Long:    "List every tape encryption key visible to the caller (GET /config/tape-encryption-keys).",
		Example: "  pmx pbs tape key ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListTapeEncryptionKeys(cmd.Context())
			if err != nil {
				return fmt.Errorf("list tape encryption keys: %w", err)
			}

			entries := decodeTapeKeyEntries(resp)
			sort.Slice(entries, func(i, j int) bool {
				return pbsFormatOptionalString(entries[i].Fingerprint) < pbsFormatOptionalString(entries[j].Fingerprint)
			})

			headers := []string{"FINGERPRINT", "KDF", "HINT", "PATH", "CREATED", "MODIFIED"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					pbsFormatOptionalString(e.Fingerprint), e.Kdf, pbsFormatOptionalString(e.Hint),
					pbsFormatOptionalString(e.Path), pbsFormatOptionalInt64(&e.Created), pbsFormatOptionalInt64(&e.Modified),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapeKeyShowCmd builds `pmx pbs tape key show <fingerprint>` — show one
// tape encryption key's metadata (GET
// /config/tape-encryption-keys/{fingerprint}). The key material and password
// are never returned by this endpoint.
func newTapeKeyShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <fingerprint>",
		Short: "Show one tape encryption key's metadata",
		Long: "Show every populated field of a single tape encryption key's metadata " +
			"(GET /config/tape-encryption-keys/{fingerprint}). The key material and " +
			"password are never returned by this endpoint.",
		Example: "  pmx pbs tape key show AB:CD:EF:01:23:45:67:89",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fingerprint := args[0]
			if fingerprint == "" {
				return fmt.Errorf("fingerprint must not be empty")
			}

			resp, err := deps.PBS.Config.GetTapeEncryptionKeys(cmd.Context(), fingerprint)
			if err != nil {
				return fmt.Errorf("show tape encryption key %q: %w", fingerprint, err)
			}

			if resp == nil {
				return fmt.Errorf("show tape encryption key %q: nil response from PBS", fingerprint)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode tape encryption key %q: %w", fingerprint, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapeKeyAddCmd builds `pmx pbs tape key add` — create a new tape
// encryption key, or restore one from an exported key file (POST
// /config/tape-encryption-keys). --password is required.
//
// The response is the raw JSON string sha256 fingerprint of the new key
// (CreateTapeEncryptionKeysResponse is a json.RawMessage alias holding that
// string). This fingerprint is the only handle by which the key can later be
// referenced (`pool add --encrypt`, `key show`, `key update`, `key delete`),
// so it is rendered prominently in both the Single map and the plain Message,
// mirroring how `user token add` surfaces a token secret that likewise
// cannot be retrieved again later.
func newTapeKeyAddCmd() *cobra.Command {
	var (
		hint     string
		kdf      string
		key      string
		password string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create or restore a tape encryption key",
		Long: "Create a new tape encryption key, or restore/re-create one from an " +
			"exported key file via --key (POST /config/tape-encryption-keys). " +
			"--password is required. The response carries the new key's sha256 " +
			"fingerprint, shown here; it is needed to reference the key afterward.",
		Example: "  pmx pbs tape key add --password '${TAPE_KEY_PASSWORD}'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if password == "" {
				return fmt.Errorf("--password is required")
			}

			params := &pbsconfig.CreateTapeEncryptionKeysParams{Password: password}

			fl := cmd.Flags()
			if fl.Changed("hint") {
				params.Hint = strPtr(hint)
			}

			if fl.Changed("kdf") {
				if !stringInSlice(kdf, tapeKeyKdfValues) {
					return fmt.Errorf("--kdf must be one of none, scrypt, pbkdf2 (got %q)", kdf)
				}

				params.Kdf = strPtr(kdf)
			}

			if fl.Changed("key") {
				params.Key = strPtr(key)
			}

			resp, err := deps.PBS.Config.CreateTapeEncryptionKeys(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create tape encryption key: %w", err)
			}

			if resp == nil {
				return fmt.Errorf("create tape encryption key: nil response from PBS")
			}

			var fingerprint string

			err = json.Unmarshal(*resp, &fingerprint)
			if err != nil {
				return fmt.Errorf("create tape encryption key: decode fingerprint: %w", err)
			}

			if fingerprint == "" {
				return fmt.Errorf("create tape encryption key: empty fingerprint in response")
			}

			res := output.Result{
				Single:  map[string]string{"fingerprint": fingerprint},
				Raw:     map[string]string{"fingerprint": fingerprint},
				Message: fmt.Sprintf("Tape encryption key created. Fingerprint: %s", fingerprint),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&hint, "hint", "", "password hint")
	f.StringVar(&kdf, "kdf", "", "key derivation function: none|scrypt|pbkdf2")
	f.StringVar(&key, "key", "", "restore/re-create a key from this JSON string")
	f.StringVar(&password, "password", "", "secret password protecting the key (required)")
	cli.MustMarkRequired(cmd, "password")
	return cmd
}

// tapeKeyKdfValues are the kdf enum values PBS accepts for a tape encryption
// key's key-derivation function.
var tapeKeyKdfValues = []string{"none", "scrypt", "pbkdf2"}

// newTapeKeyUpdateCmd builds `pmx pbs tape key update <fingerprint>` —
// change a tape encryption key's password (PUT
// /config/tape-encryption-keys/{fingerprint}). --hint and --new-password are
// always required by the API, regardless of whether other flags are set.
func newTapeKeyUpdateCmd() *cobra.Command {
	var (
		digest      string
		force       bool
		hint        string
		kdf         string
		newPassword string
		password    string
	)
	cmd := &cobra.Command{
		Use:   "update <fingerprint>",
		Short: "Change a tape encryption key's password",
		Long: "Change an existing tape encryption key's password (PUT " +
			"/config/tape-encryption-keys/{fingerprint}). --hint and --new-password " +
			"are always required by this endpoint; --password is the current " +
			"password (omit it only with --force, which resets the passphrase using " +
			"the root-only accessible key copy).",
		Example: "  pmx pbs tape key update AB:CD:EF:01:23:45:67:89 --hint backup --new-password '${TAPE_KEY_NEW_PASSWORD}'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fingerprint := args[0]
			if fingerprint == "" {
				return fmt.Errorf("fingerprint must not be empty")
			}

			if hint == "" {
				return fmt.Errorf("--hint is required")
			}

			if newPassword == "" {
				return fmt.Errorf("--new-password is required")
			}

			params := &pbsconfig.UpdateTapeEncryptionKeysParams{
				Hint:        hint,
				NewPassword: newPassword,
			}

			fl := cmd.Flags()
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			if fl.Changed("force") {
				params.Force = boolPtr(force)
			}

			if fl.Changed("kdf") {
				if !stringInSlice(kdf, tapeKeyKdfValues) {
					return fmt.Errorf("--kdf must be one of none, scrypt, pbkdf2 (got %q)", kdf)
				}

				params.Kdf = strPtr(kdf)
			}

			if fl.Changed("password") {
				params.Password = strPtr(password)
			}

			err := deps.PBS.Config.UpdateTapeEncryptionKeys(cmd.Context(), fingerprint, params)
			if err != nil {
				return fmt.Errorf("update tape encryption key %q: %w", fingerprint, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape encryption key %q updated.", fingerprint)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	f.BoolVar(&force, "force", false, "reset the passphrase using the root-only accessible key copy")
	f.StringVar(&hint, "hint", "", "password hint (required)")
	f.StringVar(&kdf, "kdf", "", "key derivation function: none|scrypt|pbkdf2")
	f.StringVar(&newPassword, "new-password", "", "the new password (required)")
	f.StringVar(&password, "password", "", "the current password")
	cli.MustMarkRequired(cmd, "hint")
	cli.MustMarkRequired(cmd, "new-password")
	return cmd
}

// newTapeKeyDeleteCmd builds `pmx pbs tape key delete <fingerprint>` —
// remove a tape encryption key (DELETE
// /config/tape-encryption-keys/{fingerprint}).
func newTapeKeyDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <fingerprint>",
		Short: "Delete a tape encryption key",
		Long: "Remove a tape encryption key (DELETE /config/tape-encryption-keys/{fingerprint}). " +
			"This is destructive: tape backups encrypted with this key become unrecoverable. " +
			"Pass --yes/-y to confirm.",
		Example: "  pmx pbs tape key delete AB:CD:EF:01:23:45:67:89 --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fingerprint := args[0]
			if fingerprint == "" {
				return fmt.Errorf("fingerprint must not be empty")
			}

			if !yes {
				return fmt.Errorf("refusing to delete tape encryption key %q without confirmation: pass --yes/-y",
					fingerprint)
			}

			params := &pbsconfig.DeleteTapeEncryptionKeysParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteTapeEncryptionKeys(cmd.Context(), fingerprint, params)
			if err != nil {
				return fmt.Errorf("delete tape encryption key %q: %w", fingerprint, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape encryption key %q deleted.", fingerprint)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
