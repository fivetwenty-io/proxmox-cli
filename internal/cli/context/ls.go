package context

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newLsCmd builds `pve context ls` (alias: list).
func newLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "ls",
		Aliases:     []string{"list"},
		Short:       "List all named contexts",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			// Collect and sort context names for deterministic output.
			names := make([]string, 0)
			if cfg.Contexts != nil {
				for k := range cfg.Contexts {
					names = append(names, k)
				}
			}
			sort.Strings(names)

			// Build table rows and raw map for json/yaml.
			type rawEntry struct {
				Name          string `json:"name"`
				Active        bool   `json:"active"`
				Host          string `json:"host"`
				Port          int    `json:"port"`
				AuthType      string `json:"auth_type"`
				Username      string `json:"username"`
				DefaultNode   string `json:"default_node"`
				DefaultOutput string `json:"default_output"`
			}

			rawEntries := make([]rawEntry, 0, len(names))
			rows := make([][]string, 0, len(names))

			for _, n := range names {
				ctx := cfg.Contexts[n]
				if ctx == nil {
					continue
				}
				active := n == cfg.CurrentContext
				marker := ""
				if active {
					marker = "*"
				}
				displayName := fmt.Sprintf("%s%s", marker, n)
				port := ctx.Port
				if port == 0 {
					port = 8006
				}
				rows = append(rows, []string{
					displayName,
					ctx.Host,
					fmt.Sprintf("%d", port),
					ctx.Auth.Type,
					ctx.Auth.Username,
					ctx.DefaultNode,
					ctx.DefaultOutput,
				})
				rawEntries = append(rawEntries, rawEntry{
					Name:          n,
					Active:        active,
					Host:          ctx.Host,
					Port:          port,
					AuthType:      ctx.Auth.Type,
					Username:      ctx.Auth.Username,
					DefaultNode:   ctx.DefaultNode,
					DefaultOutput: ctx.DefaultOutput,
				})
			}

			res := output.Result{
				Headers: []string{"NAME", "HOST", "PORT", "AUTH TYPE", "USERNAME", "DEFAULT NODE", "DEFAULT OUTPUT"},
				Rows:    rows,
				Raw:     rawEntries,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
