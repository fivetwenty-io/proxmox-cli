package context

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// lsFlags holds the raw flag values for `pmx context ls`.
type lsFlags struct {
	product string
}

// newLsCmd builds `pmx context ls [--product <p>]` (alias: list).
// Under a persona binary, rows whose product differs from the persona are
// marked "(mismatch)" in the table PRODUCT column; json/yaml raw entries
// always carry the plain product value.
func newLsCmd() *cobra.Command {
	var f lsFlags

	cmd := &cobra.Command{
		Use:         "ls",
		Aliases:     []string{"list"},
		Short:       "List all named contexts",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			if f.product != "" && !config.IsValidProduct(f.product) {
				return fmt.Errorf("--product must be one of: %s, got %q",
					strings.Join(config.Products(), ", "), f.product)
			}

			persona := cli.PersonaOf(cmd)

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
				Product       string `json:"product"`
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
				product := ctx.Product
				if product == "" {
					product = config.ProductPVE
				}
				if f.product != "" && product != f.product {
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
					port = config.DefaultPortForProduct(product)
				}
				// Under a persona binary, flag rows targeting another product
				// so an operator scanning the table sees which contexts this
				// binary's commands will reject.
				displayProduct := product
				if persona != "pmx" && product != persona {
					displayProduct = product + " (mismatch)"
				}
				rows = append(rows, []string{
					displayName,
					ctx.Host,
					fmt.Sprintf("%d", port),
					displayProduct,
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
					Product:       product,
					AuthType:      ctx.Auth.Type,
					Username:      ctx.Auth.Username,
					DefaultNode:   ctx.DefaultNode,
					DefaultOutput: ctx.DefaultOutput,
				})
			}

			res := output.Result{
				Headers: []string{"NAME", "HOST", "PORT", "PRODUCT", "AUTH TYPE", "USERNAME", "DEFAULT NODE", "DEFAULT OUTPUT"},
				Rows:    rows,
				Raw:     rawEntries,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&f.product, "product", "",
		fmt.Sprintf("only list contexts targeting this product: %s", strings.Join(config.Products(), "|")))
	_ = cmd.RegisterFlagCompletionFunc("product", cli.ProductCompletion)

	return cmd
}
