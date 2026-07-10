package context

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newRenameCmd builds `pmx context rename <old> <new>` (alias: mv).
//
// The context body moves to the new map key unchanged; CurrentContext and
// PreviousContext are updated when they pointed at the old name, so an
// operator renaming the active context stays on it. Errors when the old name
// does not exist (listing available contexts) or the new name is taken —
// overwriting via rename would silently destroy a context, which is rm's job.
func newRenameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rename <old> <new>",
		Aliases: []string{"mv"},
		Short:   "Rename a named context, following current/previous pointers",
		Long: "Rename a named context from <old> to <new>, moving its configuration to the new " +
			"map key. If <old> is the current-context or previous-context, that pointer is " +
			"updated to <new> so an operator renaming the active context stays on it. Errors if " +
			"<old> does not exist or if <new> already exists — rename never overwrites an " +
			"existing context; remove it first with 'rm' if that is intended.",
		Example:           `  pmx context rename lab lab-old`,
		Args:              cobra.ExactArgs(2),
		Annotations:       map[string]string{"noClient": "true"},
		ValidArgsFunction: cli.FirstArgContextNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName, newName := args[0], args[1]
			if oldName == "" || newName == "" {
				return fmt.Errorf("context names must not be empty")
			}
			if oldName == newName {
				return fmt.Errorf("old and new context names must differ")
			}

			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg
			if cfg == nil {
				cfg = &config.Config{}
			}

			if cfg.Contexts == nil {
				return fmt.Errorf("context %q not found: config has no contexts", oldName)
			}
			ctx, ok := cfg.Contexts[oldName]
			if !ok || ctx == nil {
				available := config.ContextNamesWithProducts(cfg)
				if len(available) == 0 {
					return fmt.Errorf("context %q not found: config has no contexts", oldName)
				}
				return fmt.Errorf("context %q not found; available: %s",
					oldName, strings.Join(available, ", "))
			}
			if _, exists := cfg.Contexts[newName]; exists {
				return fmt.Errorf(
					"context %q already exists; rename never overwrites — remove it first with 'rm' or pick another name",
					newName,
				)
			}

			delete(cfg.Contexts, oldName)
			cfg.Contexts[newName] = ctx
			if cfg.CurrentContext == oldName {
				cfg.CurrentContext = newName
			}
			if cfg.PreviousContext == oldName {
				cfg.PreviousContext = newName
			}

			if err := config.Save(deps.ConfigPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			res := output.Result{Message: fmt.Sprintf("Context %q renamed to %q.", oldName, newName)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
