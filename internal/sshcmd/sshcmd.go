// Package sshcmd holds the SSH connection flags and argv builder shared by the
// commands that shell out to ssh (node ssh/shell/console/exec and qemu ssh).
package sshcmd

import (
	"fmt"
	"strconv"

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

// BaseArgs builds the leading ssh argv (options + user@host) for the given host
// using the supplied flags. The remote command, if any, is appended by the
// caller.
func BaseArgs(f *Flags, host string) []string {
	args := make([]string, 0, 12)
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
	args = append(args, fmt.Sprintf("%s@%s", f.User, host))
	return args
}
