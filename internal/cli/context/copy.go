package context

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// copyFlags holds the raw flag values for `pmx context copy`.
type copyFlags struct {
	force     bool
	selectCtx bool
}

// newCopyCmd builds `pmx context copy <src> <dst>` (alias: cp).
func newCopyCmd() *cobra.Command {
	var f copyFlags

	cmd := &cobra.Command{
		Use:     "copy <src> <dst>",
		Aliases: []string{"cp"},
		Short:   "Copy a named context to a new name",
		Long: "Copy a named context's full configuration (host, auth, TLS, defaults) to a new " +
			"name, deep-copying the source so subsequent edits to either context do not affect " +
			"the other. Errors if <dst> already exists unless --force is passed. --select makes " +
			"the newly copied context the active one, recording the previous current-context as " +
			"previous-context.",
		Example: `  pmx context copy lab lab-staging
  pmx context copy lab lab-staging --select
  pmx context copy lab lab-old --force`,
		Args:        cobra.ExactArgs(2),
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			src, dst := args[0], args[1]

			if src == "" {
				return fmt.Errorf("src context name must not be empty")
			}
			if dst == "" {
				return fmt.Errorf("dst context name must not be empty")
			}
			if src == dst {
				return fmt.Errorf("src and dst context names must differ")
			}

			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg
			if cfg == nil {
				cfg = &config.Config{}
			}

			// src must exist.
			if cfg.Contexts == nil {
				return fmt.Errorf("context %q not found: config has no contexts", src)
			}
			srcCtx, ok := cfg.Contexts[src]
			if !ok || srcCtx == nil {
				return fmt.Errorf("context %q not found in config", src)
			}

			// dst must not exist unless --force.
			if _, exists := cfg.Contexts[dst]; exists && !f.force {
				return fmt.Errorf(
					"context %q already exists; use --force to overwrite",
					dst,
				)
			}

			// Deep copy the Context struct.  TLSBlock and AuthBlock are values
			// (not pointers) so a struct literal copy is sufficient for most
			// fields.  The only pointer field inside Context is Auth.Session.
			copied := deepCopyContext(srcCtx)

			cfg.Contexts[dst] = copied

			// If --select, update current-context.
			if f.selectCtx {
				if cfg.CurrentContext != "" && cfg.CurrentContext != dst {
					cfg.PreviousContext = cfg.CurrentContext
				}
				cfg.CurrentContext = dst
			}

			if err := config.Save(deps.ConfigPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			msg := fmt.Sprintf("Context %q copied to %q.", src, dst)
			if f.selectCtx {
				msg = fmt.Sprintf("Context %q copied to %q and selected.", src, dst)
			}
			res := output.Result{Message: msg}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&f.force, "force", false, "overwrite an existing context with the dst name")
	cmd.Flags().BoolVar(&f.selectCtx, "select", false, "make the copied context the current context")

	cmd.ValidArgsFunction = cli.FirstArgContextNames

	return cmd
}

// deepCopyContext returns a deep copy of src such that mutating the copy does
// not affect the original.  The only heap-allocated sub-field is Auth.Session
// (*Session); all other fields are value types (string, int, bool, struct).
func deepCopyContext(src *config.Context) *config.Context {
	if src == nil {
		return nil
	}
	c := &config.Context{
		Host:          src.Host,
		Port:          src.Port,
		Protocol:      src.Protocol,
		Realm:         src.Realm,
		DefaultNode:   src.DefaultNode,
		DefaultOutput: src.DefaultOutput,
		Product:       src.Product,
		Auth: config.AuthBlock{
			Type:     src.Auth.Type,
			Username: src.Auth.Username,
			TokenID:  src.Auth.TokenID,
			Secret:   src.Auth.Secret,
			// Session is a pointer — deep copy the pointed-at struct if present.
			Session: nil,
		},
		TLS: config.TLSBlock{
			Insecure:    src.TLS.Insecure,
			Fingerprint: src.TLS.Fingerprint,
			CACert:      src.TLS.CACert,
			Tofu:        src.TLS.Tofu,
		},
	}
	if src.Auth.Session != nil {
		sess := *src.Auth.Session // copy the Session value
		c.Auth.Session = &sess
	}
	return c
}
