// Package sdn implements the `pve sdn` command group: software-defined network
// zones, vnets, and subnets, plus the apply step that commits pending changes.
//
// PVE SDN configuration is staged: creating or deleting a zone, vnet, or subnet
// only edits the pending config. The changes take effect on the nodes only
// after `pve sdn apply` (PUT /cluster/sdn) reloads the network configuration.
package sdn

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

func init() {
	cli.RegisterGroup(newGroupCmd)
}

// newGroupCmd builds the `pve sdn` command and all of its sub-commands. The
// passed *cli.Deps is a placeholder used only so cobra can assemble the command
// tree; live dependencies are resolved per-invocation via cli.GetDeps.
func newGroupCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdn",
		Short: "Manage software-defined networking (zones, vnets, subnets)",
		Long: "List, create, and delete SDN zones, vnets, and subnets. Changes are " +
			"staged until committed with `pve sdn apply`.",
	}
	cmd.AddCommand(
		newApplyCmd(),
		newZoneCmd(),
		newVnetCmd(),
		newSubnetCmd(),
		newControllerCmd(),
		newIpamCmd(),
		newDnsCmd(),
	)
	return cmd
}

// strPtr returns a pointer to v.
func strPtr(v string) *string { return &v }

// boolPtr returns a pointer to v.
func boolPtr(v bool) *bool { return &v }

// int64Ptr returns a pointer to v.
func int64Ptr(v int64) *int64 { return &v }

// anyFlagChanged reports whether any of the named flags was set on the command
// line. Used by `set` verbs to refuse a no-op update.
func anyFlagChanged(fl *pflag.FlagSet, names ...string) bool {
	for _, n := range names {
		if fl.Changed(n) {
			return true
		}
	}
	return false
}

// anyCell renders an arbitrary JSON value as a single table cell.
func anyCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// objectToSingle marshals v to JSON and flattens the top-level object into a
// key→cell map (plus the decoded object for lossless Raw output). A
// *json.RawMessage marshals to its own bytes, so an aliased Get* response works
// directly.
func objectToSingle(v any) (map[string]string, any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, nil, err
	}
	single := make(map[string]string, len(obj))
	for k, val := range obj {
		single[k] = anyCell(val)
	}
	return single, obj, nil
}

// renderObject renders v as a key/value single-object table with lossless Raw.
func renderObject(cmd *cobra.Command, deps *cli.Deps, v any) error {
	single, obj, err := objectToSingle(v)
	if err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: obj}, deps.Format)
}

// renderObjectScrubbed renders v as renderObject does, but first removes the
// named secret keys (a provider API token or key) from both the table and the
// lossless Raw output. The Get* response shape for these endpoints is opaque
// (json.RawMessage), so a stored secret is stripped defensively in the CLI
// rather than trusting the API to omit it.
func renderObjectScrubbed(cmd *cobra.Command, deps *cli.Deps, v any, secretKeys ...string) error {
	single, obj, err := objectToSingle(v)
	if err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if m, ok := obj.(map[string]any); ok {
		for _, k := range secretKeys {
			delete(single, k)
			delete(m, k)
		}
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: obj}, deps.Format)
}

// renderRawList renders a list of heterogeneous JSON objects as a union-of-keys
// table with sorted, upper-cased headers, preserving the raw elements for
// lossless JSON/YAML output.
func renderRawList(cmd *cobra.Command, deps *cli.Deps, raws []json.RawMessage) error {
	objs := make([]map[string]any, 0, len(raws))
	keySet := map[string]struct{}{}
	for _, raw := range raws {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return fmt.Errorf("decode list entry: %w", err)
		}
		objs = append(objs, obj)
		for k := range obj {
			keySet[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	res := output.Result{Raw: raws}
	for _, k := range keys {
		res.Headers = append(res.Headers, upperKey(k))
	}
	for _, obj := range objs {
		row := make([]string, 0, len(keys))
		for _, k := range keys {
			row = append(row, anyCell(obj[k]))
		}
		res.Rows = append(res.Rows, row)
	}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// upperKey upper-cases a JSON key for use as a table header.
func upperKey(k string) string {
	b := []byte(k)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}
