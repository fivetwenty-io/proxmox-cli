package context

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newPreviousCmd builds `pmx context previous` (alias: prev).
//
// Swaps CurrentContext ↔ PreviousContext. Errors if PreviousContext is empty or
// if either name no longer exists in cfg.Contexts (stale reference). On a stale
// reference the dangling field is cleared before returning the error so that
// subsequent runs do not repeat the same failure.
func newPreviousCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "previous",
		Aliases:     []string{"prev"},
		Short:       "Switch back to the previously active context",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg
			if cfg == nil {
				cfg = &config.Config{}
			}
			return runPrevious(cmd, cfg, deps)
		},
	}
	return cmd
}

// runPrevious implements the swap logic shared by newPreviousCmd and the
// `select -` shorthand in select.go. Both callers are in the same package.
func runPrevious(cmd *cobra.Command, cfg *config.Config, deps *cli.Deps) error {
	if cfg.PreviousContext == "" {
		return fmt.Errorf("no previous context recorded; run 'pmx context select <name>' first")
	}

	prev := cfg.PreviousContext
	curr := cfg.CurrentContext

	// Validate both names still exist (stale-reference guard).
	if err := checkContextRef(cfg, "previous-context", prev); err != nil {
		// Clear the dangling reference so future runs do not hit the same error.
		cfg.PreviousContext = ""
		_ = config.Save(deps.ConfigPath, cfg) //nolint:errcheck // best-effort cleanup
		return err
	}
	if curr != "" {
		if err := checkContextRef(cfg, "current-context", curr); err != nil {
			cfg.CurrentContext = ""
			_ = config.Save(deps.ConfigPath, cfg) //nolint:errcheck // best-effort cleanup
			return err
		}
	}

	// Perform the swap.
	cfg.CurrentContext = prev
	cfg.PreviousContext = curr

	if err := config.Save(deps.ConfigPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	res := output.Result{Message: fmt.Sprintf("Switched to context %q (was %q).", prev, curr)}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// checkContextRef returns an error when name is not present in cfg.Contexts.
func checkContextRef(cfg *config.Config, field, name string) error {
	if name == "" {
		return nil
	}
	if cfg.Contexts != nil {
		if _, ok := cfg.Contexts[name]; ok {
			return nil
		}
	}
	return fmt.Errorf(
		"%s references context %q which no longer exists; "+
			"the stale reference has been cleared — use 'pmx context select <name>'",
		field, name,
	)
}
