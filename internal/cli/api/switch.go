package api

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newSwitchCmd builds `pve api switch <name>`, which sets the current target.
func newSwitchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "switch <name>",
		Short: "Set the current target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			t, ok := deps.Cfg.Targets[name]
			if !ok || t == nil {
				return fmt.Errorf("target %q not found", name)
			}

			deps.Cfg.CurrentTarget = name
			if err := config.SaveForce(configPath(cmd), deps.Cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			msg := fmt.Sprintf("Switched to target %q (%s).", name, t.Host)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: msg}, deps.Format)
		},
	}
	return noClient(cmd)
}
