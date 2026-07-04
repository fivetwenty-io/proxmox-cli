package sshcmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// newTestCmd builds a throwaway cobra.Command with the shared SSH flags
// registered on it, mirroring how the real ssh/shell/console/exec commands
// wire up sshcmd.Flags.
func newTestCmd(f *Flags) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	RegisterFlags(cmd, f)
	return cmd
}

func TestRegisterFlags_Defaults(t *testing.T) {
	var f Flags
	cmd := newTestCmd(&f)

	require.NoError(t, cmd.ParseFlags(nil))
	require.Equal(t, "root", f.User)
	require.Equal(t, "", f.Identity)
	require.Equal(t, 22, f.Port)
	require.False(t, f.Agent)
	require.False(t, f.NoStrict)
}

func TestRegisterFlags_Shorthands(t *testing.T) {
	cases := []struct {
		name      string
		flagName  string
		shorthand string
	}{
		{"user", "user", "l"},
		{"identity", "identity", "i"},
		{"port", "port", "p"},
		{"agent", "agent", "A"},
	}

	var f Flags
	cmd := newTestCmd(&f)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flag := cmd.Flags().Lookup(tc.flagName)
			require.NotNil(t, flag, "flag %q must be registered", tc.flagName)
			require.Equal(t, tc.shorthand, flag.Shorthand)
		})
	}

	// --no-strict is a long-only flag with no shorthand.
	noStrict := cmd.Flags().Lookup("no-strict")
	require.NotNil(t, noStrict)
	require.Equal(t, "", noStrict.Shorthand)
}

func TestRegisterFlags_ShorthandBinding(t *testing.T) {
	var f Flags
	cmd := newTestCmd(&f)

	require.NoError(t, cmd.ParseFlags([]string{
		"-l", "admin",
		"-i", "/home/admin/.ssh/id_ed25519",
		"-p", "2222",
		"-A",
		"--no-strict",
	}))

	require.Equal(t, "admin", f.User)
	require.Equal(t, "/home/admin/.ssh/id_ed25519", f.Identity)
	require.Equal(t, 2222, f.Port)
	require.True(t, f.Agent)
	require.True(t, f.NoStrict)
}

func TestBaseArgs(t *testing.T) {
	cases := []struct {
		name string
		f    Flags
		host string
		want []string
	}{
		{
			name: "defaults",
			f:    Flags{User: "root", Port: 22},
			host: "10.0.0.5",
			want: []string{"-p", "22", "root@10.0.0.5"},
		},
		{
			name: "custom port",
			f:    Flags{User: "root", Port: 2222},
			host: "10.0.0.5",
			want: []string{"-p", "2222", "root@10.0.0.5"},
		},
		{
			name: "identity file",
			f:    Flags{User: "root", Port: 22, Identity: "/home/root/.ssh/id_rsa"},
			host: "10.0.0.5",
			want: []string{"-p", "22", "-i", "/home/root/.ssh/id_rsa", "root@10.0.0.5"},
		},
		{
			name: "agent forwarding",
			f:    Flags{User: "root", Port: 22, Agent: true},
			host: "10.0.0.5",
			want: []string{"-p", "22", "-A", "root@10.0.0.5"},
		},
		{
			name: "no-strict",
			f:    Flags{User: "root", Port: 22, NoStrict: true},
			host: "10.0.0.5",
			want: []string{"-p", "22", "-o", "StrictHostKeyChecking=no", "root@10.0.0.5"},
		},
		{
			name: "all combined",
			f: Flags{
				User:     "admin",
				Port:     2222,
				Identity: "/home/admin/.ssh/id_ed25519",
				Agent:    true,
				NoStrict: true,
			},
			host: "vm.example.com",
			want: []string{
				"-p", "2222",
				"-i", "/home/admin/.ssh/id_ed25519",
				"-A",
				"-o", "StrictHostKeyChecking=no",
				"admin@vm.example.com",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BaseArgs(&tc.f, tc.host)
			require.Equal(t, tc.want, got)
		})
	}
}
