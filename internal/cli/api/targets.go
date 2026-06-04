package api

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// targetRow is the typed JSON/YAML shape for a single configured target. It is
// collected into a slice so structured output is a proper array (an empty array
// when no targets are configured) rather than a Message object.
type targetRow struct {
	Name     string `json:"name" yaml:"name"`
	Host     string `json:"host" yaml:"host"`
	Port     int    `json:"port" yaml:"port"`
	Realm    string `json:"realm" yaml:"realm"`
	AuthType string `json:"auth_type" yaml:"auth_type"`
	Current  bool   `json:"current" yaml:"current"`
}

// newTargetsCmd builds `pve api targets`, which lists every configured target.
func newTargetsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "targets",
		Short: "List configured targets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			names := make([]string, 0, len(cfg.Targets))
			for name := range cfg.Targets {
				names = append(names, name)
			}
			sort.Strings(names)

			rows := make([][]string, 0, len(names))
			entries := make([]targetRow, 0, len(names))
			for _, name := range names {
				t := cfg.Targets[name]
				isCurrent := name == cfg.CurrentTarget
				current := ""
				if isCurrent {
					current = "*"
				}
				rows = append(rows, []string{
					name,
					t.Host,
					portString(t.Port),
					realmOrDefault(t.Realm),
					t.Auth.Type,
					current,
				})
				entries = append(entries, targetRow{
					Name:     name,
					Host:     t.Host,
					Port:     t.Port,
					Realm:    realmOrDefault(t.Realm),
					AuthType: t.Auth.Type,
					Current:  isCurrent,
				})
			}

			// For the empty case, table/plain show a friendly message while
			// JSON/YAML serialise an empty array (Raw=[]targetRow{}).
			if len(entries) == 0 {
				return deps.Out.Render(cmd.OutOrStdout(), output.Result{
					Message: "No targets configured.",
					Raw:     entries,
				}, deps.Format)
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{
				Headers: []string{"NAME", "HOST", "PORT", "REALM", "AUTH-TYPE", "CURRENT"},
				Rows:    rows,
				Raw:     entries,
			}, deps.Format)
		},
	}
	return noClient(cmd)
}
