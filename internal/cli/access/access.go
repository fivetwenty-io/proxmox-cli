package access

import (
	"bytes"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// Group builds the `pmx pve access` command and all of its sub-commands for
// managing users, API tokens, groups, roles, ACLs, permissions, and passwords.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Manage users, tokens, groups, roles, and access control",
		Long: `Manage Proxmox VE access control: users and their API tokens, groups,
roles, ACL entries, authentication realms (domains, including OpenID Connect
and LDAP/AD sync), TFA/two-factor device enrollment, effective permissions,
and user passwords. Requires a configured Proxmox VE API connection.

Sub-commands take the resource's own identifier: a full userid (name@realm),
groupid, roleid, tokenid, or realm name. Destructive verbs (user delete,
token delete, group delete, role delete, domain delete, tfa delete) require
--yes/-y and otherwise refuse to run.`,
		Example: `  pmx pve access user list
  pmx pve access user create alice@pve --password secret123
  pmx pve access user token create alice@pve ci-token
  pmx pve access acl set --path /vms/100 --roles PVEVMAdmin --users alice@pve`,
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

// pveBool is an optional boolean for the hand-rolled raw decodes in this
// package. The decoding is the SDK's client.PVEBool; what this type adds is
// the distinction the SDK's plain bool cannot carry, between a flag PVE set
// to false and one it never sent, which is what separates a "0" cell from an
// empty one.
type pveBool struct {
	set bool
	val client.PVEBool
}

// UnmarshalJSON records that the field arrived and hands the value to the
// SDK's decoder, which accepts every encoding PVE emits: a real JSON bool,
// the numbers 1/0, and the strings "1"/"0", "true"/"false", "yes"/"no", and
// "on"/"off".
func (b *pveBool) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if err := b.val.UnmarshalJSON(data); err != nil {
		return err
	}
	b.set = true
	return nil
}

// cell renders the boolean as "1"/"0"; an unset value renders as "".
func (b pveBool) cell() string {
	if !b.set {
		return ""
	}
	if b.val.Bool() {
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
