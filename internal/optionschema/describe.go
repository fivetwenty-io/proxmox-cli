package optionschema

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// DescribeConfig configures a NewDescribeCmd instance for one command tree.
type DescribeConfig struct {
	// Schemas is the generated option table the command renders.
	Schemas []Schema
	// Short is the cobra Short text.
	Short string
	// Long is the cobra Long text.
	Long string
	// CommandHint is the full command the unknown-option error points at,
	// e.g. "pve cluster options describe".
	CommandHint string
	// SubKeyRowsInCatalog includes one row per dict sub-key in the no-argument
	// catalog view. Disable for large tables (guest config); the single-option
	// view always shows sub-keys.
	SubKeyRowsInCatalog bool
}

// NewDescribeCmd builds a `describe [option]` subcommand: an offline catalog
// of every settable option from the PVE API schema. It never contacts the
// cluster.
func NewDescribeCmd(cfg DescribeConfig) *cobra.Command {
	return &cobra.Command{
		Use:         "describe [option]",
		Short:       cfg.Short,
		Long:        cfg.Long,
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			schemas := cfg.Schemas
			catalog := true
			if len(args) == 1 {
				s := Find(cfg.Schemas, args[0])
				if s == nil {
					return fmt.Errorf("unknown option %q: run `%s` for the full list", args[0], cfg.CommandHint)
				}
				schemas = []Schema{*s}
				catalog = false
			}

			rows := make([][]string, 0, len(schemas))
			for _, s := range schemas {
				name := s.Flag
				if s.Indexed {
					name = s.Name
				}
				rows = append(rows, []string{name, s.Type, s.Default,
					valuesCell(s.Enum, s.Minimum, s.Maximum), describeCell(s.Description, catalog)})
				if catalog && !cfg.SubKeyRowsInCatalog {
					continue
				}
				for _, sk := range s.SubKeys {
					desc := sk.Description
					if sk.Required {
						desc = strings.TrimSpace("required. " + desc)
					}
					rows = append(rows, []string{name + "." + sk.Name, sk.Type, sk.Default,
						valuesCell(sk.Enum, sk.Minimum, sk.Maximum), describeCell(desc, catalog)})
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{
				Headers: []string{"OPTION", "TYPE", "DEFAULT", "VALUES", "DESCRIPTION"},
				Rows:    rows,
				Raw:     schemas,
			}, deps.Format)
		},
	}
}

// valuesCell renders the VALUES column: the enum when the schema constrains
// values, otherwise the numeric range when one is bounded.
func valuesCell(enum []string, minimum, maximum string) string {
	if len(enum) > 0 {
		return strings.Join(enum, "|")
	}
	return rangeText(minimum, maximum)
}

// describeCell caps schema descriptions for the full-catalog table view;
// single-option views render them untruncated.
func describeCell(desc string, truncate bool) string {
	const max = 72
	if !truncate {
		return desc
	}
	runes := []rune(desc)
	if len(runes) <= max {
		return desc
	}
	return string(runes[:max-1]) + "…"
}
