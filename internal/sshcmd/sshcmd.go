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
	// Jump is an ssh -J destination list ("[user@]host[:port]", comma-separated
	// for a chain), used to reach a target that is not directly routable from
	// where pmx runs. Empty means connect directly.
	Jump string
}

// RegisterFlags installs the shared SSH connection flags on cmd.
func RegisterFlags(cmd *cobra.Command, f *Flags) {
	cmd.Flags().StringVarP(&f.User, "user", "l", "root", "SSH login user")
	cmd.Flags().StringVarP(&f.Identity, "identity", "i", "", "path to SSH identity (private key) file")
	cmd.Flags().IntVarP(&f.Port, "port", "p", 22, "SSH port")
	cmd.Flags().BoolVarP(&f.Agent, "agent", "A", false, "enable SSH agent forwarding")
	cmd.Flags().BoolVar(&f.NoStrict, "no-strict", false, "disable strict host key checking")
	cmd.Flags().StringVarP(&f.Jump, "jump", "J", "",
		"jump host to tunnel through, as [user@]host[:port] (comma-separated for a chain)")
}

// OptionArgs builds the ssh option argv (everything before the destination)
// from the supplied flags: -p, and optionally -J, -i, -A,
// -o StrictHostKeyChecking=no.
//
// -J is emitted first so it is never separated from its value by another
// option, and because ssh applies -i/-A to the jump connection as well as the
// final hop, which is what an operator reaching a lab behind a bastion with
// one key expects.
func OptionArgs(f *Flags) []string {
	args := make([]string, 0, 10)
	args = append(args, "-p", strconv.Itoa(f.Port))
	if f.Jump != "" {
		args = append(args, "-J", f.Jump)
	}
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

// DefaultConnectTimeoutSec bounds connection establishment for scripted
// (non-interactive) ssh invocations built by BatchOptionArgs.
const DefaultConnectTimeoutSec = 10

// Keepalive bounds for scripted ssh invocations. ConnectTimeout covers only
// connection establishment; once the session is up, a peer that stops
// answering without sending a RST or FIN leaves ssh blocked with no wall-clock
// bound at all. That is not an exotic fault here: `pvecm add` restarts
// corosync, which reconfigures the management network and can transiently
// partition it, and the ssh session running that very command is what
// blackholes. These two options make ssh probe a silent peer and give up
// after DefaultServerAliveIntervalSec * DefaultServerAliveCountMax seconds.
// Scripted invocations have no interactive session worth preserving, so
// disconnecting is always better than hanging.
const (
	DefaultServerAliveIntervalSec = 15
	DefaultServerAliveCountMax    = 4
)

// KeepaliveOptionArgs returns the ssh keepalive options shared by every
// scripted invocation.
func KeepaliveOptionArgs() []string {
	return []string{
		"-o", fmt.Sprintf("ServerAliveInterval=%d", DefaultServerAliveIntervalSec),
		"-o", fmt.Sprintf("ServerAliveCountMax=%d", DefaultServerAliveCountMax),
	}
}

// BatchOptionArgs builds the ssh option argv for scripted, non-interactive
// invocations: OptionArgs plus the options that make ssh safe to run without
// a terminal. BatchMode=yes makes ssh fail instead of prompting for a
// password, passphrase, or host-key confirmation, ConnectTimeout bounds how
// long connection establishment may hang, and the keepalive pair bounds how
// long an established session may hang. It never mutates the OptionArgs
// result: interactive callers (pmx ssh, node shell, qemu ssh) keep their
// prompting behavior.
func BatchOptionArgs(f *Flags) []string {
	args := append(OptionArgs(f),
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", DefaultConnectTimeoutSec),
	)
	return append(args, KeepaliveOptionArgs()...)
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
