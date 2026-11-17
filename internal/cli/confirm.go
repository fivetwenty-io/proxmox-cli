package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// StdinInteractive reports whether in is an interactive terminal. It is a
// package variable so tests can stand in for a terminal; production code never
// reassigns it.
var StdinInteractive = isInteractiveInput

// ConfirmDestructive gates a verb that deletes data. It reports whether the
// verb may proceed.
//
// With yes set it returns true and prints nothing. Without it, an interactive
// terminal gets a yes/no prompt on stderr (never stdout, so machine-readable
// output stays clean) and only an answer beginning with 'y' or 'Y' proceeds;
// anything else, including end of input, returns false and the caller reports
// that nothing was done. A non-interactive run cannot be asked, so it fails
// with the standard refusal that names the flag: "refusing to <action> without
// confirmation: pass --yes/-y". action completes that sentence, and prompt is
// the question shown on a terminal.
func ConfirmDestructive(cmd *cobra.Command, yes bool, action, prompt string) (bool, error) {
	if yes {
		return true, nil
	}

	in := cmd.InOrStdin()
	if !StdinInteractive(in) {
		return false, fmt.Errorf("refusing to %s without confirmation: pass --yes/-y", action)
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N]: ", prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}

	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y"), nil
}
