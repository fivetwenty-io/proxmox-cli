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

// validateFlags holds the raw flag values for `pmx context validate`.
type validateFlags struct {
	all bool
}

// newValidateCmd builds `pmx context validate [<name>] [--all]`.
// With no argument it validates the current context; with a name it validates
// that specific context; with --all it validates every context in the config.
// Renders a table of context → OK / error-list; exits non-zero if any invalid.
//
// Network connectivity is NOT checked in v1 (--connect is deferred; document
// the flag as reserved so callers know to expect it in a future release).
func newValidateCmd() *cobra.Command {
	var f validateFlags

	cmd := &cobra.Command{
		Use:         "validate [<name>]",
		Short:       "Validate one or all contexts (structural checks only; no network connect in v1)",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		Long: `Validate one or all named contexts against structural rules:
  - host is present
  - auth type is "token" or "password"
  - token auth: token-id and secret are set
  - password auth: username and secret are set
  - port is in range [1, 65535] (0 means "use default 8006"; accepted)
  - protocol is "https" or "http" (empty means "use default https"; accepted)
  - default-output, if set, is one of: table, ascii, plain, json, yaml
  - fingerprint, if set, matches the XX:XX:...:XX hex pattern (32 pairs)

Network connectivity is not checked in this version (--connect is reserved for
a future release).

Exit status: 0 if all validated contexts are valid; 1 if any are invalid.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			// Build the list of names to validate.
			var names []string
			if f.all {
				if cfg.Contexts != nil {
					for n := range cfg.Contexts {
						names = append(names, n)
					}
					sort.Strings(names)
				}
				if len(names) == 0 {
					res := output.Result{Message: "No contexts configured."}
					return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
				}
			} else {
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
							"use 'pmx context validate <name>' or 'pmx context select <name>' first",
					)
				}
				names = []string{name}
			}

			// Validate each named context and collect results.
			type result struct {
				name   string
				status string
				errors []string
			}

			results := make([]result, 0, len(names))
			anyInvalid := false

			for _, n := range names {
				r := result{name: n}

				if cfg.Contexts == nil {
					r.errors = append(r.errors, "config has no contexts")
					r.status = "INVALID"
					anyInvalid = true
					results = append(results, r)
					continue
				}
				ctx, ok := cfg.Contexts[n]
				if !ok || ctx == nil {
					r.errors = append(r.errors, "context not found in config")
					r.status = "INVALID"
					anyInvalid = true
					results = append(results, r)
					continue
				}

				errs := config.StrictValidateContext(ctx)
				if len(errs) == 0 {
					r.status = "OK"
				} else {
					r.status = "INVALID"
					r.errors = errs
					anyInvalid = true
				}
				results = append(results, r)
			}

			// Render: table with NAME / STATUS / ERRORS columns.
			type rawEntry struct {
				Name   string   `json:"name"`
				Status string   `json:"status"`
				Errors []string `json:"errors"`
			}
			rawEntries := make([]rawEntry, 0, len(results))
			rows := make([][]string, 0, len(results))

			for _, r := range results {
				errStr := strings.Join(r.errors, "; ")
				rows = append(rows, []string{r.name, r.status, errStr})
				rawEntries = append(rawEntries, rawEntry{
					Name:   r.name,
					Status: r.status,
					Errors: r.errors,
				})
			}

			res := output.Result{
				Headers: []string{"NAME", "STATUS", "ERRORS"},
				Rows:    rows,
				Raw:     rawEntries,
			}
			if err := deps.Out.Render(cmd.OutOrStdout(), res, deps.Format); err != nil {
				return err
			}

			if anyInvalid {
				return fmt.Errorf("one or more contexts are invalid")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&f.all, "all", false, "validate all contexts in the config")

	cmd.ValidArgsFunction = cli.FirstArgContextNames

	return cmd
}
