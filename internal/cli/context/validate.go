package context

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// connectTimeout bounds each --connect probe so one dead host cannot hang
// `validate --all --connect` indefinitely.
const connectTimeout = 5 * time.Second

// validateFlags holds the raw flag values for `pmx context validate`.
type validateFlags struct {
	all     bool
	connect bool
}

// newValidateCmd builds `pmx context validate [<name>] [--all]`.
// With no argument it validates the current context; with a name it validates
// that specific context; with --all it validates every context in the config.
// Renders a table of context → OK / error-list; exits non-zero if any invalid.
func newValidateCmd() *cobra.Command {
	var f validateFlags

	cmd := &cobra.Command{
		Use:         "validate [<name>]",
		Short:       "Validate one or all contexts",
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

With --connect, each structurally valid context is probed live: the version
endpoint is fetched over TLS (honoring tls.insecure), the server's
identification header is compared against the context's product, and the
stored secret is checked for resolvability. Unreachable contexts fail the
command; a product mismatch is reported as a warning only.

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
				name      string
				status    string
				errors    []string
				reachable string
				product   string
				auth      string
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

				if f.connect && r.status == "OK" {
					// Probe a defaults-applied copy so the stored config is
					// never mutated by display/probe defaulting.
					probeCtx := *ctx
					config.ApplyDefaults(&probeCtx)

					pr := probeContext(&probeCtx, connectTimeout)
					if pr.Reachable {
						r.reachable = "yes"
					} else {
						r.reachable = "no"
						r.errors = append(r.errors, fmt.Sprintf("unreachable: %s", pr.ReachErr))
						anyInvalid = true
					}

					switch {
					case !pr.Reachable:
						r.product = ""
					case pr.ProductGuess == "":
						r.product = "unverified"
					case pr.ProductGuess == probeCtx.Product:
						r.product = fmt.Sprintf("match (%s)", probeCtx.Product)
					default:
						// Warn, never block: a mismatch is reported but does
						// not flip the exit code.
						r.product = fmt.Sprintf("mismatch (endpoint looks like %s)", pr.ProductGuess)
					}

					if probeCtx.Auth.Secret == "" {
						r.auth = "no credentials"
					} else if _, err := config.ResolveSecret(probeCtx.Auth.Secret); err != nil {
						r.auth = "secret does not resolve"
					} else {
						r.auth = "secret resolves (verify live with 'auth whoami')"
					}
				}

				results = append(results, r)
			}

			// Render: table with NAME / STATUS / ERRORS columns (plus
			// REACHABLE / PRODUCT / AUTH under --connect).
			type rawEntry struct {
				Name      string   `json:"name"`
				Status    string   `json:"status"`
				Reachable string   `json:"reachable,omitempty"`
				Product   string   `json:"product_check,omitempty"`
				Auth      string   `json:"auth_check,omitempty"`
				Errors    []string `json:"errors"`
			}
			rawEntries := make([]rawEntry, 0, len(results))
			rows := make([][]string, 0, len(results))

			for _, r := range results {
				errStr := strings.Join(r.errors, "; ")
				if f.connect {
					rows = append(rows, []string{r.name, r.status, r.reachable, r.product, r.auth, errStr})
				} else {
					rows = append(rows, []string{r.name, r.status, errStr})
				}
				rawEntries = append(rawEntries, rawEntry{
					Name: r.name, Status: r.status,
					Reachable: r.reachable, Product: r.product, Auth: r.auth,
					Errors: r.errors,
				})
			}

			headers := []string{"NAME", "STATUS", "ERRORS"}
			if f.connect {
				headers = []string{"NAME", "STATUS", "REACHABLE", "PRODUCT", "AUTH", "ERRORS"}
			}

			res := output.Result{
				Headers: headers,
				Rows:    rows,
				Raw:     rawEntries,
			}
			if err := deps.Out.Render(cmd.OutOrStdout(), res, deps.Format); err != nil {
				return err
			}

			if anyInvalid {
				return fmt.Errorf("one or more contexts are invalid or unreachable")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&f.all, "all", false, "validate all contexts in the config")
	cmd.Flags().BoolVar(&f.connect, "connect", false,
		"probe each structurally valid context live: TLS reachability of the version endpoint, "+
			"plus a product sanity check from the server's identification header")

	cmd.ValidArgsFunction = cli.FirstArgContextNames

	return cmd
}
