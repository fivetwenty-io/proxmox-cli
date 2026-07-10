package context

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newRmCmd builds `pmx context rm <name>` (aliases: remove, delete).
//
// Guards:
//   - Name must exist in config.
//   - If name == CurrentContext: requires --force; without it returns an error.
//     With --force: removes context and clears CurrentContext.
//   - Requires --yes to confirm the destructive action (mirrors qemu/delete.go).
//   - If name == PreviousContext: PreviousContext is cleared after removal.
func newRmCmd() *cobra.Command {
	var (
		force bool
		yes   bool
	)

	cmd := &cobra.Command{
		Use:         "rm <name>",
		Aliases:     []string{"remove", "delete"},
		Short:       "Remove a named context from the config file",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == "" {
				return fmt.Errorf("context name must not be empty")
			}

			if !yes {
				return fmt.Errorf(
					"refusing to remove context %q without confirmation: pass --yes/-y",
					name,
				)
			}

			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg
			if cfg == nil {
				cfg = &config.Config{}
			}

			// Name must exist.
			if cfg.Contexts == nil {
				return fmt.Errorf("context %q not found: config has no contexts", name)
			}
			if _, ok := cfg.Contexts[name]; !ok {
				available := config.ContextNamesWithProducts(cfg)
				if len(available) == 0 {
					return fmt.Errorf("context %q not found: config has no contexts", name)
				}
				return fmt.Errorf("context %q not found; available: %s",
					name, strings.Join(available, ", "))
			}

			// Guard against removing the active context without --force.
			if name == cfg.CurrentContext && !force {
				return fmt.Errorf(
					"context %q is the active context; "+
						"run 'pmx context select <other>' first, or pass --force to remove anyway",
					name,
				)
			}

			// Remove the context.
			delete(cfg.Contexts, name)

			// Clear current-context if this was the active one (--force path).
			if name == cfg.CurrentContext {
				cfg.CurrentContext = ""
			}

			// Clear previous-context if it referenced the removed name.
			if name == cfg.PreviousContext {
				cfg.PreviousContext = ""
			}

			if err := config.Save(deps.ConfigPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			res := output.Result{Message: fmt.Sprintf("Context %q removed.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false,
		"allow removing the active context (also clears current-context)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false,
		"confirm destructive removal without prompting")

	cmd.ValidArgsFunction = cli.FirstArgContextNames

	return cmd
}
