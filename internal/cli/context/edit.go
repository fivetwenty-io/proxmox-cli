package context

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	yaml "github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newEditCmd builds `pmx context edit [<name>]`.
// When name is omitted the current context is used; errors if none is set.
// The verb marshals the context YAML to a 0600 tempfile, launches $EDITOR
// (fallback $VISUAL), reads the result back, validates it, and merges it into
// the config.  If the editor exits non-zero the config is not modified.  If the
// edited YAML fails to parse, an error is returned and the tempfile is
// preserved so the operator can recover their edits.
func newEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "edit [<name>]",
		Short:       "Edit a named context in $EDITOR",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		Long: `Edit a named context using $EDITOR (or $VISUAL).

The context is marshalled to a temporary YAML file and opened in the editor.
On save and exit the file is parsed and validated.  If validation succeeds the
config is updated.  If the editor exits with a non-zero status, no changes are
saved.  If the edited file contains invalid YAML or fails validation, an error
is returned and the temp file path is printed so you can recover your edits.

Note: the context name cannot be changed via edit; rename is not supported.
Note: config.Save rewrites the config file and does not preserve comments.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			// Resolve context name: explicit arg > current-context.
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				name = cfg.CurrentContext
			}
			if name == "" {
				return fmt.Errorf(
					"no context name specified and no current-context is set; " +
						"use 'pmx context edit <name>' or 'pmx context select <name>' first",
				)
			}

			if cfg.Contexts == nil {
				return fmt.Errorf("context %q not found: config has no contexts", name)
			}
			ctx, ok := cfg.Contexts[name]
			if !ok || ctx == nil {
				return fmt.Errorf("context %q not found in config", name)
			}

			// Resolve the editor binary.
			editorBin := os.Getenv("EDITOR")
			if editorBin == "" {
				editorBin = os.Getenv("VISUAL")
			}
			if editorBin == "" {
				return fmt.Errorf(
					"$EDITOR is not set; use 'pmx context add' to modify fields directly",
				)
			}

			// Marshal the context struct to YAML.
			data, err := yaml.Marshal(ctx)
			if err != nil {
				return fmt.Errorf("marshal context %q to YAML: %w", name, err)
			}

			// Write to a 0600 tempfile.
			tmp, err := os.CreateTemp("", "pve-context-*.yml")
			if err != nil {
				return fmt.Errorf("create temp file for editing: %w", err)
			}
			tmpPath := tmp.Name()

			// Ensure the tempfile is cleaned up unless parsing fails (in which case
			// we preserve it for the operator to recover their edits).
			removeOnExit := true
			defer func() {
				if removeOnExit {
					_ = os.Remove(tmpPath)
				}
			}()

			if err := tmp.Chmod(0o600); err != nil {
				return fmt.Errorf("chmod temp file: %w", err)
			}
			if _, err := tmp.Write(data); err != nil {
				_ = tmp.Close()
				return fmt.Errorf("write temp file: %w", err)
			}
			if err := tmp.Close(); err != nil {
				return fmt.Errorf("close temp file: %w", err)
			}

			// Launch the editor; wire stdin/stdout/stderr so terminal editors work.
			editorCmd := exec.Command(editorBin, tmpPath) //nolint:gosec // editor binary from user env
			editorCmd.Stdin = cmd.InOrStdin()
			editorCmd.Stdout = cmd.OutOrStdout()
			editorCmd.Stderr = cmd.ErrOrStderr()

			if err := editorCmd.Run(); err != nil {
				// Editor exited non-zero — abort without saving.
				return fmt.Errorf("editor exited with error: %w; config not modified", err)
			}

			// Read back the edited file.
			edited, err := os.ReadFile(tmpPath) //nolint:gosec // G304: tmpPath is os.CreateTemp path created by this process, not untrusted input
			if err != nil {
				return fmt.Errorf("read edited temp file: %w", err)
			}

			// Unmarshal strictly into a Context.
			var updated config.Context
			if err := yaml.UnmarshalWithOptions(edited, &updated, yaml.Strict()); err != nil {
				removeOnExit = false // preserve for recovery
				return fmt.Errorf(
					"edited YAML is invalid (%w); temp file preserved at %s",
					err, tmpPath,
				)
			}

			// Run the full write-time structural validation (StrictValidateContext).
			// This is the same rule set enforced by context add and context validate,
			// ensuring that anything writable via edit passes validate.
			// ApplyDefaults fills in Port/Protocol/Realm before checking.
			config.ApplyDefaults(&updated)
			if strictErrs := config.StrictValidateContext(&updated); len(strictErrs) > 0 {
				removeOnExit = false
				return fmt.Errorf(
					"edited context fails validation (%s); temp file preserved at %s",
					strings.Join(strictErrs, "; "), tmpPath,
				)
			}

			// Merge back: name stays unchanged (edit modifies the body, not the key).
			cfg.Contexts[name] = &updated

			if err := config.Save(deps.ConfigPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			res := output.Result{Message: fmt.Sprintf("Context %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
