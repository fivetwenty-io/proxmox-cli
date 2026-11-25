// Package epoch reads the time flags that the Proxmox APIs take as UNIX epochs.
package epoch

import (
	"fmt"
	"strconv"
	"time"
)

// Parse reads a UNIX epoch or an RFC 3339 time and returns epoch seconds. flag
// names the option in the error, so the operator sees which value was refused.
func Parse(flag, value string) (int64, error) {
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return n, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0, fmt.Errorf("invalid --%s %q: want a UNIX epoch or an RFC 3339 time such as 2026-11-01T12:00:00Z", flag, value)
	}
	return t.Unix(), nil
}

// Format renders epoch seconds as an RFC 3339 UTC time.
func Format(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}
