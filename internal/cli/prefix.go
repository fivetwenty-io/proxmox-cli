package cli

import "github.com/spf13/cobra"

// PersonaOf returns the persona the command tree was built for by inspecting
// the root command's name: "pve", "pbs", or "pdm" for a persona binary, and
// "pmx" for anything else (the combined tree, and tests that mount a group
// standalone). It mirrors Persona(os.Args[0]) but reads the already-built
// tree, so callers deep in a RunE need no os.Args access and tests can pick
// a persona by naming their root command.
func PersonaOf(cmd *cobra.Command) string {
	if cmd == nil {
		return "pmx"
	}
	switch name := cmd.Root().Name(); name {
	case "pve", "pbs", "pdm":
		return name
	default:
		return "pmx"
	}
}

// CommandPrefix returns the prefix an operator types to reach a shared
// command from the current binary: the persona name under a persona binary,
// "pmx" otherwise. Compose it as CommandPrefix(cmd) + " context select".
func CommandPrefix(cmd *cobra.Command) string {
	return PersonaOf(cmd)
}
