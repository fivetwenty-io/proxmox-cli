package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// AliasFlags makes each alias in aliases resolve to the canonical flag it maps
// to on cmd's own flag set, so both spellings work and only the canonical one
// is listed in help.
//
// The canonical names here mirror the PVE API parameter they carry, which is
// why the CLI ends up with both spellings in the first place: `qm migrate`
// takes `targetstorage`, while the remote-migration endpoint takes
// `target-storage`. Mirroring the API keeps each flag findable from the
// Proxmox documentation, but it also means an operator who learned one
// spelling gets "unknown flag" from the neighbouring command. Accepting both
// costs nothing and removes the trap.
//
// Aliasing rather than registering a second flag keeps help output and shell
// completion showing exactly one name per setting, and leaves Changed() and
// Lookup() working through either spelling, since pflag normalises the name
// before it reaches them.
func AliasFlags(cmd *cobra.Command, aliases map[string]string) {
	if len(aliases) == 0 {
		return
	}

	set := cmd.Flags()
	prev := set.GetNormalizeFunc()
	set.SetNormalizeFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		if canonical, ok := aliases[name]; ok {
			name = canonical
		}
		if prev != nil {
			return prev(f, name)
		}
		return pflag.NormalizedName(name)
	})
}
