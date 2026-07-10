package context

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
)

// warnAfterSwitch prints non-blocking stderr guidance after a successful
// context switch (select, select -, previous):
//
//   - Under a persona binary whose product differs from the selected
//     context's product, a one-line mismatch warning. Plain pmx exposes
//     every product, so it never warns.
//   - When the context has no credential material at all (no secret and no
//     live session ticket), a one-line pointer at auth login/set-token.
//
// Warnings never change the exit code; the switch has already been saved.
func warnAfterSwitch(cmd *cobra.Command, cfg *config.Config, name string) {
	ctx := cfg.Contexts[name]
	if ctx == nil {
		return
	}

	errOut := cmd.ErrOrStderr()

	product := ctx.ProductOrDefault()

	persona := cli.PersonaOf(cmd)
	if persona != "pmx" && product != persona {
		_, _ = fmt.Fprintf(errOut,
			"warning: context %q targets %s (%s); this binary provides %s commands — "+
				"use the '%s' binary or 'pmx %s' for %s commands\n",
			name, cli.ProductDisplayName(product), product, persona, product, product, product)
	}

	if ctx.Auth.Secret == "" && (ctx.Auth.Session == nil || ctx.Auth.Session.Ticket == "") {
		prefix := cli.CommandPrefix(cmd)
		_, _ = fmt.Fprintf(errOut,
			"note: context %q has no credentials; run '%s auth login --context %s' or '%s auth set-token --context %s ...'\n",
			name, prefix, name, prefix, name)
	}
}
