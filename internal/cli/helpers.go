package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// MustMarkRequired marks flag as required on cmd and panics if the flag is
// not defined. A panic here indicates a programming error (flag name
// mismatch between Flags() registration and the MarkFlagRequired call) and
// surfaces in any test or invocation that constructs the command — it can
// never be triggered by user input at runtime.
func MustMarkRequired(cmd *cobra.Command, flag string) {
	if err := cmd.MarkFlagRequired(flag); err != nil {
		panic(fmt.Sprintf("MustMarkRequired: flag %q not defined on command %q: %v", flag, cmd.Use, err))
	}
}
