package optionschema

import (
	"fmt"
	"sort"
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
	// TypeSets maps discriminator values (e.g. storage types) to the options
	// valid for each. When non-nil, describe gains a --type flag filtering
	// the catalog to one type's options, and the table gains a TYPES column:
	// the accepting types per option ("all" when every type accepts it), or,
	// under --type, that type's usage markers (required / create-only).
	TypeSets map[string]map[string]TypeUse
}

// NewDescribeCmd builds a `describe [option]` subcommand: an offline catalog
// of every settable option from the PVE API schema. It never contacts the
// cluster.
func NewDescribeCmd(cfg DescribeConfig) *cobra.Command {
	var typeFilter string
	cmd := &cobra.Command{
		Use:         "describe [option]",
		Short:       cfg.Short,
		Long:        cfg.Long,
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			schemas := cfg.Schemas
			if typeFilter != "" {
				set, ok := cfg.TypeSets[typeFilter]
				if !ok {
					return fmt.Errorf("unknown type %q: valid types are %s",
						typeFilter, strings.Join(typeNames(cfg.TypeSets), ", "))
				}
				schemas = filterByNames(schemas, set)
			}
			catalog := true
			if len(args) == 1 {
				s := Find(schemas, args[0])
				if s == nil {
					// Distinguish "no such option" from "exists, but the
					// filtered type does not accept it".
					if typeFilter != "" && Find(cfg.Schemas, args[0]) != nil {
						return fmt.Errorf("option %q is not accepted by type %q", args[0], typeFilter)
					}
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
				row := []string{name, s.Type, s.Default,
					valuesCell(s.Enum, s.Minimum, s.Maximum), describeCell(s.Description, catalog)}
				if cfg.TypeSets != nil {
					row = insertTypesCell(row, typesCell(cfg.TypeSets, typeFilter, s.Name))
				}
				rows = append(rows, row)
				if catalog && !cfg.SubKeyRowsInCatalog {
					continue
				}
				for _, sk := range s.SubKeys {
					desc := sk.Description
					if sk.Required {
						desc = strings.TrimSpace("required. " + desc)
					}
					row := []string{name + "." + sk.Name, sk.Type, sk.Default,
						valuesCell(sk.Enum, sk.Minimum, sk.Maximum), describeCell(desc, catalog)}
					if cfg.TypeSets != nil {
						row = insertTypesCell(row, "")
					}
					rows = append(rows, row)
				}
			}
			headers := []string{"OPTION", "TYPE", "DEFAULT", "VALUES", "DESCRIPTION"}
			if cfg.TypeSets != nil {
				col := "TYPES"
				if typeFilter != "" {
					col = "USE"
				}
				headers = insertTypesCell(headers, col)
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{
				Headers: headers,
				Rows:    rows,
				Raw:     schemas,
			}, deps.Format)
		},
	}
	if cfg.TypeSets != nil {
		cmd.Flags().StringVar(&typeFilter, "type", "",
			"limit the catalog to the options one type accepts: "+strings.Join(typeNames(cfg.TypeSets), "|"))
	}
	return cmd
}

// typeNames returns the discriminator values in sorted order.
func typeNames(sets map[string]map[string]TypeUse) []string {
	names := make([]string, 0, len(sets))
	for t := range sets {
		names = append(names, t)
	}
	sort.Strings(names)
	return names
}

// filterByNames keeps the schemas whose API name is in the set.
func filterByNames(schemas []Schema, set map[string]TypeUse) []Schema {
	kept := make([]Schema, 0, len(set))
	for _, s := range schemas {
		if _, ok := set[s.Name]; ok {
			kept = append(kept, s)
		}
	}
	return kept
}

// typesCell renders the TYPES column. Without a type filter it lists the
// types accepting the option ("all" when every type does); with one, the
// filtered type's usage markers.
func typesCell(sets map[string]map[string]TypeUse, typeFilter, name string) string {
	if typeFilter != "" {
		use := sets[typeFilter][name]
		switch {
		case use.Required && use.Fixed:
			return "required, create-only"
		case use.Fixed:
			return "create-only"
		case use.Required:
			return "required"
		}
		return ""
	}
	var accepting []string
	for t, set := range sets {
		if _, ok := set[name]; ok {
			accepting = append(accepting, t)
		}
	}
	// An explicit marker keeps a schema option no type accepts (e.g. one only
	// meaningful at create time) distinguishable from a rendering gap.
	if len(accepting) == 0 {
		return "none"
	}
	if len(accepting) == len(sets) {
		return "all"
	}
	sort.Strings(accepting)
	return strings.Join(accepting, ",")
}

// insertTypesCell places the types cell between VALUES and DESCRIPTION.
func insertTypesCell(row []string, cell string) []string {
	out := make([]string, 0, len(row)+1)
	out = append(out, row[:4]...)
	out = append(out, cell)
	return append(out, row[4:]...)
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
