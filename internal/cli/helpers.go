package cli

import (
	"encoding/json"
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

// StringifyValue renders a JSON-decoded value as a table cell. It is the
// shared renderer for the endpoints that return an untyped config map or a
// pending-change list, where a value's JSON type varies by key.
//
// The nil case is the one that matters: fmt's %v renders a nil interface as
// the literal "<nil>", which reaches the table as text and reads as a value
// the server sent. An absent value renders as an empty cell instead. The
// float case matters for the same reason: %v switches to exponent notation
// above six digits, so a memory size would render as "1.048576e+06".
func StringifyValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
