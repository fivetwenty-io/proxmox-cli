// Package sshcmd holds the SSH connection flags and argv builder shared by the
// commands that shell out to ssh (node ssh/shell/console/exec and qemu ssh).
package sshcmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// Flags holds the connection options shared by every ssh-based command.
type Flags struct {
	User     string
	Identity string
	Port     int
	Agent    bool
	NoStrict bool
}

// RegisterFlags installs the shared SSH connection flags on cmd.
func RegisterFlags(cmd *cobra.Command, f *Flags) {
	cmd.Flags().StringVarP(&f.User, "user", "l", "root", "SSH login user")
	cmd.Flags().StringVarP(&f.Identity, "identity", "i", "", "path to SSH identity (private key) file")
	cmd.Flags().IntVarP(&f.Port, "port", "p", 22, "SSH port")
	cmd.Flags().BoolVarP(&f.Agent, "agent", "A", false, "enable SSH agent forwarding")
	cmd.Flags().BoolVar(&f.NoStrict, "no-strict", false, "disable strict host key checking")
}

// OptionArgs builds the ssh option argv (everything before the destination)
// from the supplied flags: -p, and optionally -i, -A, -o StrictHostKeyChecking=no.
func OptionArgs(f *Flags) []string {
	args := make([]string, 0, 8)
	args = append(args, "-p", strconv.Itoa(f.Port))
	if f.Identity != "" {
		args = append(args, "-i", f.Identity)
	}
	if f.Agent {
		args = append(args, "-A")
	}
	if f.NoStrict {
		args = append(args, "-o", "StrictHostKeyChecking=no")
	}
	return args
}

// Dest builds the ssh destination ("user@host") for the given host using the
// supplied flags.
func Dest(f *Flags, host string) string {
	return fmt.Sprintf("%s@%s", f.User, host)
}

// BaseArgs builds the leading ssh argv (options + user@host) for the given host
// using the supplied flags. The remote command, if any, is appended by the
// caller.
func BaseArgs(f *Flags, host string) []string {
	return append(OptionArgs(f), Dest(f, host))
}

// ShellQuote wraps s in single quotes, escaping any embedded single quotes, so
// it survives rsync's word-splitting of the -e remote-shell string as a single
// argument.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// needsShellQuote reports whether s contains characters that would be split or
// misinterpreted by rsync's whitespace-based re-parsing of the -e value.
func needsShellQuote(s string) bool {
	return strings.ContainsAny(s, " \t'\"\\$`;&|<>(){}*?[]!#~")
}

// RemoteShell builds the ssh(1) argument for rsync's -e flag: "ssh" followed by
// the same connection options OptionArgs would pass to ssh directly, with any
// option needing quoting passed through ShellQuote so it survives rsync's
// word-splitting of the -e string.
func RemoteShell(f *Flags) string {
	opts := OptionArgs(f)
	parts := make([]string, 0, len(opts)+1)
	parts = append(parts, "ssh")
	for _, a := range opts {
		if needsShellQuote(a) {
			a = ShellQuote(a)
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}
