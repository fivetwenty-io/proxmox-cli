package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// ContextNamesCompletion is a cobra ValidArgsFunction / flag-completion
// function that completes context names from the --config file, each with a
// "product@host" description. It loads the config itself (from cmd's --config
// flag, falling back to config.DefaultPath) instead of using GetDeps because
// completion runs under cobra's "__complete" dispatcher, whose
// PersistentPreRunE invocation never sees the user's real flag values (see
// the noClient comment in persistentPreRunE). It never returns an error
// state: an unreadable or absent config yields no completions, because
// completion output must never break the shell.
func ContextNamesCompletion(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfgPath := config.DefaultPath()
	if f := cmd.Flags().Lookup("config"); f != nil && f.Value.String() != "" {
		cfgPath = f.Value.String()
	}

	cfg, err := config.Load(cfgPath)
	if err != nil || cfg == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	entries := make([]string, 0, len(cfg.Contexts))
	for name, ctx := range cfg.Contexts {
		if !strings.HasPrefix(name, toComplete) {
			continue
		}
		product := config.ProductPVE
		host := ""
		if ctx != nil {
			product = ctx.ProductOrDefault()
			host = ctx.Host
		}
		entries = append(entries, fmt.Sprintf("%s\t%s@%s", name, product, host))
	}
	sort.Strings(entries)

	return entries, cobra.ShellCompDirectiveNoFileComp
}

// FirstArgContextNames restricts ContextNamesCompletion to the first
// positional argument, for verbs whose later positionals are new names
// (copy <src> <dst>, rename <old> <new>) or that take a single name.
func FirstArgContextNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return ContextNamesCompletion(cmd, args, toComplete)
}

// ProductCompletion completes --product flag values with the supported
// product identifiers, each described by its full product name.
func ProductCompletion(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return []string{
		config.ProductPVE + "\tProxmox VE",
		config.ProductPBS + "\tProxmox Backup Server",
		config.ProductPDM + "\tProxmox Datacenter Manager",
	}, cobra.ShellCompDirectiveNoFileComp
}
