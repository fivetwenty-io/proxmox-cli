package qemu

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// warnDangerousConfig emits unconditional stderr warnings for high-risk VM
// configuration changes on `qemu config set` and `qemu create`, whether the
// risky key arrives via its dedicated flag or the --set escape hatch.
// Mutations warn regardless of output format (table/plain/json/yaml) —
// stderr never corrupts a structured stdout payload. There is no --force
// gate on any of these: they are legitimate, common operations.
//
// clearingProtection is computed by the caller (protectionCleared, config
// set only); qemu create always passes false since a freshly created VM has
// no prior protection state to clear.
func warnDangerousConfig(cmd *cobra.Command, fl *pflag.FlagSet, sets []cli.KeyValue, clearingProtection bool) {
	w := cmd.ErrOrStderr()

	if fl.Changed("args") || setHasKey(sets, "args") {
		_, _ = fmt.Fprintln(w,
			"WARNING: --args passes raw arguments to the QEMU process, bypassing all PVE validation (root@pam only)")
	}
	if fl.Changed("hookscript") || setHasKey(sets, "hookscript") {
		_, _ = fmt.Fprintln(w,
			"WARNING: the hookscript executes on the HOST during VM lifecycle events")
	}
	if hostpciChanged(fl, sets) {
		_, _ = fmt.Fprintln(w,
			"WARNING: PCI passthrough gives the guest direct, DMA-capable access to host hardware")
	}
	if clearingProtection {
		_, _ = fmt.Fprintln(w,
			"WARNING: clearing the protection flag re-enables destroy and disk-removal operations")
	}
}

// setHasKey reports whether key was supplied via --set in this invocation.
func setHasKey(sets []cli.KeyValue, key string) bool {
	for _, kv := range sets {
		if kv.Key == key {
			return true
		}
	}
	return false
}

// hostpciChanged reports whether any hostpci slot was supplied, either via
// the repeatable --hostpci INDEX=VALUE flag or a hostpciN key arriving
// through --set.
func hostpciChanged(fl *pflag.FlagSet, sets []cli.KeyValue) bool {
	if hf := fl.Lookup("hostpci"); hf != nil && hf.Changed {
		return true
	}
	for _, kv := range sets {
		if strings.HasPrefix(kv.Key, "hostpci") {
			return true
		}
	}
	return false
}

// protectionCleared reports whether a `qemu config set` invocation clears
// the protection flag: --protection=false, a --delete list naming
// protection, or the same effect arriving through --set (--set
// protection=0, or --set delete=...,protection,...).
func protectionCleared(fl *pflag.FlagSet, protection bool, deleteKeys string, sets []cli.KeyValue) bool {
	if fl.Changed("protection") && !protection {
		return true
	}
	if fl.Changed("delete") && deleteListNames(deleteKeys, "protection") {
		return true
	}
	for _, kv := range sets {
		switch kv.Key {
		case "protection":
			if isFalsy(kv.Value) {
				return true
			}
		case "delete":
			if deleteListNames(kv.Value, "protection") {
				return true
			}
		}
	}
	return false
}

// deleteListNames reports whether name appears as a whole token in a
// comma-separated PVE --delete list.
func deleteListNames(list, name string) bool {
	for tok := range strings.SplitSeq(list, ",") {
		if strings.TrimSpace(tok) == name {
			return true
		}
	}
	return false
}

// isFalsy reports whether a raw --set value represents a false/off boolean,
// the convention PVE and this CLI use for "disabled".
func isFalsy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}
