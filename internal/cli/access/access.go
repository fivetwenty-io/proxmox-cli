package access

import (
	"bytes"
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// Group builds the `pve access` command and all of its sub-commands for
// managing users, API tokens, groups, roles, ACLs, permissions, and passwords.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Manage users, tokens, groups, roles, and access control",
		Long: "Manage Proxmox VE access control: users and API tokens, groups, " +
			"roles, ACL entries, effective permissions, and passwords.",
	}
	cmd.AddCommand(
		newUserCmd(),
		newGroupResourceCmd(),
		newRoleCmd(),
		newACLCmd(),
		newDomainCmd(),
		newTfaCmd(),
		newOpenidCmd(),
		newPermissionsCmd(),
		newPasswordCmd(),
	)
	return cmd
}

// pveBool is an optional boolean that tolerates the several JSON encodings the
// Proxmox VE API uses for boolean flags: a real JSON bool, the numbers 1/0, and
// the strings "1"/"0". A nil value means the field was absent.
type pveBool struct {
	set bool
	val bool
}

// UnmarshalJSON decodes bool, numeric, and string encodings of a boolean.
func (b *pveBool) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	switch data[0] {
	case 't', 'f':
		var v bool
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		b.set, b.val = true, v
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		b.set, b.val = true, s == "1" || s == "true"
	default:
		var n float64
		if err := json.Unmarshal(data, &n); err != nil {
			return err
		}
		b.set, b.val = true, n != 0
	}
	return nil
}

// cell renders the boolean as "1"/"0"; an unset value renders as "".
func (b pveBool) cell() string {
	if !b.set {
		return ""
	}
	if b.val {
		return "1"
	}
	return "0"
}

// strVal dereferences an optional string pointer, returning "" when nil.
func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// boolCell renders an optional bool pointer as "1"/"0"; nil renders as "".
func boolCell(p *bool) string {
	if p == nil {
		return ""
	}
	if *p {
		return "1"
	}
	return "0"
}

// pveBoolCell renders an optional client.PVEBool (the tolerant response boolean)
// as "1"/"0"; nil renders as "". Response structs use *client.PVEBool so they
// decode PVE's loosely-typed booleans (1/0, "1"/"0", true/false).
func pveBoolCell(p *client.PVEBool) string {
	if p == nil {
		return ""
	}
	if p.Bool() {
		return "1"
	}
	return "0"
}

// intCell renders an optional int64 pointer as a decimal string; nil renders
// as "".
func intCell(p *int64) string {
	if p == nil {
		return ""
	}
	return itoa(*p)
}

// itoa formats an int64 without importing strconv at every call site.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
