package cli

import (
	"fmt"
	"strconv"
	"strings"

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

// ParseIndexedValues converts repeated "INDEX=VALUE" flag values into the
// map[int]string shape the apiclient-go expands into indexed keys such as
// scsi0, net1, hostpci0, and acmedomain0. It rejects malformed entries,
// negative indices, and duplicate indices so a typo never silently overwrites
// another slot. flagName is used only to build error messages.
func ParseIndexedValues(vals []string, flagName string) (map[int]string, error) {
	out := make(map[int]string, len(vals))
	for _, v := range vals {
		idxStr, val, ok := strings.Cut(v, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --%s %q: want INDEX=VALUE", flagName, v)
		}
		idx, err := strconv.Atoi(strings.TrimSpace(idxStr))
		if err != nil || idx < 0 {
			return nil, fmt.Errorf("invalid --%s %q: index must be a non-negative integer", flagName, v)
		}
		if _, dup := out[idx]; dup {
			return nil, fmt.Errorf("invalid --%s: index %d specified more than once", flagName, idx)
		}
		out[idx] = val
	}
	return out, nil
}
