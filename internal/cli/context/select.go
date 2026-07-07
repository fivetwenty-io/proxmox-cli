package context

import (
	"bufio"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newSelectCmd builds `pmx context select [<name>]` (aliases: use, switch).
//
// Behaviour:
//   - Arg "-"         → delegate to previous-context swap (same as `pmx context previous`).
//   - Arg <name>      → validate exists, set CurrentContext, save PreviousContext.
//   - No arg (TTY or piped stdin) → numbered-list picker: read from cmd.InOrStdin().
func newSelectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "select [<name>]",
		Aliases:     []string{"use", "switch"},
		Short:       "Select the active named context",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg
			if cfg == nil {
				cfg = &config.Config{}
				deps = &cli.Deps{Cfg: cfg, ConfigPath: deps.ConfigPath, Out: deps.Out, Format: deps.Format}
			}

			// Arg "-" → behave as `pmx context previous`.
			if len(args) == 1 && args[0] == "-" {
				return runPrevious(cmd, cfg, deps)
			}

			var name string
			if len(args) == 1 {
				name = args[0]
			} else {
				// No argument: interactive numbered-list picker.
				picked, err := pickContext(cmd, cfg)
				if err != nil {
					return err
				}
				name = picked
			}

			if name == "" {
				return fmt.Errorf("context name must not be empty")
			}

			// Validate the name exists.
			if err := requireContextExists(cfg, name); err != nil {
				return err
			}

			// Save previous context only when the selection actually changes.
			if cfg.CurrentContext != name && cfg.CurrentContext != "" {
				cfg.PreviousContext = cfg.CurrentContext
			}
			cfg.CurrentContext = name

			if err := config.Save(deps.ConfigPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			res := output.Result{Message: fmt.Sprintf("Switched to context %q.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// pickContext prints a numbered list of contexts to stdout and reads a selection
// (number or name) from cmd.InOrStdin(). Returns the selected context name.
func pickContext(cmd *cobra.Command, cfg *config.Config) (string, error) {
	if len(cfg.Contexts) == 0 {
		return "", fmt.Errorf("no contexts defined in config; use 'pmx context add' first")
	}

	names := make([]string, 0, len(cfg.Contexts))
	for k := range cfg.Contexts {
		names = append(names, k)
	}
	sort.Strings(names)

	out := cmd.OutOrStdout()
	for i, n := range names {
		marker := " "
		if n == cfg.CurrentContext {
			marker = "*"
		}
		_, _ = fmt.Fprintf(out, "  %s %d) %s\n", marker, i+1, n)
	}
	_, _ = fmt.Fprint(out, "Select context [number or name]: ")

	scanner := bufio.NewScanner(cmd.InOrStdin())
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("reading selection: %w", err)
		}
		return "", fmt.Errorf("no input provided (EOF); selection cancelled")
	}
	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return "", fmt.Errorf("empty input; selection cancelled")
	}

	// Try numeric index first.
	if idx, err := strconv.Atoi(input); err == nil {
		if idx < 1 || idx > len(names) {
			return "", fmt.Errorf("index %d out of range [1, %d]", idx, len(names))
		}
		return names[idx-1], nil
	}

	// Try literal name match.
	for _, n := range names {
		if n == input {
			return n, nil
		}
	}
	return "", fmt.Errorf("no context named %q; valid names: %s",
		input, strings.Join(names, ", "))
}

// requireContextExists returns an error listing available context names when
// name is not present in cfg.Contexts.
func requireContextExists(cfg *config.Config, name string) error {
	if cfg.Contexts != nil {
		if _, ok := cfg.Contexts[name]; ok {
			return nil
		}
	}
	available := availableNames(cfg)
	if len(available) == 0 {
		return fmt.Errorf("context %q not found: no contexts defined in config", name)
	}
	return fmt.Errorf("context %q not found; available: %s",
		name, strings.Join(available, ", "))
}

// availableNames returns a sorted slice of context names from cfg.
func availableNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Contexts))
	for k := range cfg.Contexts {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
