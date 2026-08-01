package context

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	yaml "github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newEditCmd builds `pmx context edit [<name>]`.
// When name is omitted the current context is used; errors if none is set.
// The verb marshals the context YAML to a 0600 tempfile, launches $EDITOR
// (fallback $VISUAL), reads the result back, validates it, and merges it into
// the config.  If the editor exits non-zero the config is not modified.  If the
// edited YAML fails to parse, an error is returned and the tempfile is
// preserved so the operator can recover their edits.
func newEditCmd() *cobra.Command {
	var productFlag string

	cmd := &cobra.Command{
		Use:         "edit [<name>]",
		Short:       "Edit a named context in $EDITOR",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		Long: "Edit a named context in $EDITOR, or $VISUAL if that is what is set.\n\n" +
			"The context is marshalled to a temporary YAML file and opened in the editor. On " +
			"save and exit, the file is parsed and validated, and the config updated if it " +
			"passes.\n\n" +
			"Nothing is saved when the editor exits non-zero. When the edited file has " +
			"invalid YAML or fails validation, the command errors and prints the temp file " +
			"path so your edits are recoverable.\n\n" +
			"Two things worth knowing: the context name cannot be changed here, so use " +
			"`context rename` for that; and saving rewrites the config file, which does not " +
			"preserve comments.\n\n" +
			"Passing --product <pve|pbs|pdm> edits that one field directly, without opening " +
			"an editor. A port still sitting at the old product's default moves to the new " +
			"product's default: 8006 pve, 8007 pbs, 8443 pdm.",
		Example: `  pmx context edit lab
  pmx context edit lab --product pbs`,
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

			// --product performs a direct, non-interactive field edit: change
			// the product, re-default the port when it was the old product's
			// default, validate, save. $EDITOR is never launched on this path.
			if cmd.Flags().Changed("product") {
				return runProductEdit(cmd, deps, cfg, name, ctx, productFlag)
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

			// Write to a 0600 tempfile inside the config directory rather than
			// $TMPDIR. The marshalled context carries Auth.Secret verbatim,
			// which may be an inline literal password or token, and the two
			// failure paths below deliberately preserve the file so the
			// operator can recover their edits — leaving a plaintext
			// credential in a world-traversable /tmp until the OS reaps it,
			// which on macOS is days and on a long-lived host may be never.
			// The config directory is already the 0700 home for exactly this
			// material (config.WriteRaw tightens it on every write).
			tmpDir := filepath.Dir(deps.ConfigPath)
			if tmpDir == "" || tmpDir == "." {
				tmpDir = ""
			}
			tmp, err := os.CreateTemp(tmpDir, ".pmx-context-*.yml")
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
				_ = tmp.Close()
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
	cmd.Flags().StringVar(&productFlag, "product", "",
		fmt.Sprintf("change the context's product (%s) without opening $EDITOR; "+
			"a port equal to the old product's default is re-defaulted to the new product's port",
			strings.Join(config.Products(), "|")))
	_ = cmd.RegisterFlagCompletionFunc("product", cli.ProductCompletion)
	cmd.ValidArgsFunction = cli.FirstArgContextNames
	return cmd
}

// runProductEdit implements `context edit <name> --product <p>`: a direct
// field edit that never launches $EDITOR.
//
// Port rule: a port of 0 or exactly the OLD product's default port means the
// operator never customized it, so it silently becomes the NEW product's
// default. Any other port is preserved, with a stderr note naming the new
// product's default so a wrong-port context is a conscious choice, not an
// accident.
func runProductEdit(
	cmd *cobra.Command, deps *cli.Deps, cfg *config.Config, name string, ctx *config.Context, newProduct string,
) error {
	if !config.IsValidProduct(newProduct) {
		return fmt.Errorf("--product must be one of: %s, got %q",
			strings.Join(config.Products(), ", "), newProduct)
	}

	oldProduct := ctx.ProductOrDefault()

	oldDefault := config.DefaultPortForProduct(oldProduct)
	newDefault := config.DefaultPortForProduct(newProduct)

	switch {
	case ctx.Port == 0 || ctx.Port == oldDefault:
		ctx.Port = newDefault
	case ctx.Port != newDefault:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"note: port %d kept; %s default is %d\n", ctx.Port, newProduct, newDefault)
	}
	ctx.Product = newProduct

	config.ApplyDefaults(ctx)
	if strictErrs := config.StrictValidateContext(ctx); len(strictErrs) > 0 {
		return fmt.Errorf("context %q fails validation after product change: %s",
			name, strings.Join(strictErrs, "; "))
	}

	cfg.Contexts[name] = ctx

	if err := config.Save(deps.ConfigPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	res := output.Result{Message: fmt.Sprintf(
		"Context %q updated (product: %s, port: %d).", name, newProduct, ctx.Port)}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}
