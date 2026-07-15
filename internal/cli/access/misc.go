package access

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newPermissionsCmd builds `pmx pve access permissions`. The response is a map of
// path to a map of privilege to a propagate flag.
func newPermissionsCmd() *cobra.Command {
	var path, userid string
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Show effective permissions for a user or token",
		Long: "Show the effective privilege set after role and ACL propagation is resolved " +
			"server-side, as a path-to-privilege-list table. Without --userid, shows the " +
			"calling user's own effective permissions; pass --userid to query a different " +
			"user or token (requires Sys.Audit on /access). Pass --path to restrict the " +
			"result to a single path.",
		Example: `  pmx pve access permissions
  pmx pve access permissions --path /vms/100
  pmx pve access permissions --userid alice@pve`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &access.ListPermissionsParams{}
			setIfChanged(cmd, "path", &params.Path, path)
			setIfChanged(cmd, "userid", &params.Userid, userid)

			resp, err := deps.API.Access.ListPermissions(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list permissions: %w", err)
			}

			tree := map[string]map[string]any{}
			if resp != nil && len(*resp) > 0 {
				if err := json.Unmarshal(*resp, &tree); err != nil {
					return fmt.Errorf("decode permissions: %w", err)
				}
			}

			paths := make([]string, 0, len(tree))
			for p := range tree {
				paths = append(paths, p)
			}
			sort.Strings(paths)

			rows := make([][]string, 0, len(paths))
			for _, p := range paths {
				privs := make([]string, 0, len(tree[p]))
				for priv := range tree[p] {
					privs = append(privs, priv)
				}
				sort.Strings(privs)
				rows = append(rows, []string{p, joinComma(privs)})
			}

			result := output.Result{
				Headers: []string{"PATH", "PRIVS"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "only show this specific path")
	cmd.Flags().StringVar(&userid, "userid", "", "user ID or full API token ID")
	return cmd
}

// newPasswordCmd builds `pmx pve access password set`.
func newPasswordCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "password",
		Short: "Manage user passwords",
		Long:  "Change a user's password.",
	}
	cmd.AddCommand(newPasswordSetCmd())
	return cmd
}

// newPasswordSetCmd builds `pmx pve access password set`.
func newPasswordSetCmd() *cobra.Command {
	var userid, password, confirmationPassword string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Change a user's password",
		Long: "Change the password of a Proxmox VE user. --userid is required. When " +
			"--password is omitted, prompts for it interactively (hidden input on a " +
			"terminal, or a single line read from stdin otherwise); the new password is " +
			"never accepted as a positional argument or echoed back.",
		Example: `  pmx pve access password set --userid alice@pve`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if userid == "" {
				return fmt.Errorf("--userid is required")
			}
			if password == "" {
				prompted, err := promptPassword(cmd)
				if err != nil {
					return err
				}
				password = prompted
			}
			if password == "" {
				return fmt.Errorf("a non-empty password is required")
			}

			params := &access.UpdatePasswordParams{Userid: userid, Password: password}
			setIfChanged(cmd, "confirmation-password", &params.ConfirmationPassword, confirmationPassword)
			if err := deps.API.Access.UpdatePassword(cmd.Context(), params); err != nil {
				return fmt.Errorf("update password for %q: %w", userid, err)
			}

			result := output.Result{Message: "Password updated."}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&userid, "userid", "", "full user ID in name@realm format (required)")
	cmd.Flags().StringVar(&password, "password", "", "new password (prompts if absent)")
	cmd.Flags().StringVar(&confirmationPassword, "confirmation-password", "", "current password of the operator performing the change")
	return cmd
}

// promptPassword reads a secret from the command's input, used when the caller
// omits --password. The prompt is written to stderr so it never contaminates
// JSON/YAML output on stdout. When input is an interactive terminal, the typed
// characters are not echoed; piped or redirected input falls back to a line
// read so scripts and tests can supply the secret on stdin.
func promptPassword(cmd *cobra.Command) (string, error) {
	_, _ = fmt.Fprint(cmd.ErrOrStderr(), "New password: ")
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		secret, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimRight(string(secret), "\r\n"), nil
	}
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
