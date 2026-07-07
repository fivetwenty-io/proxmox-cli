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

// TestBaseArgs_IsOptionArgsPlusDest locks in that BaseArgs stays exactly
// OptionArgs followed by Dest, so existing callers see no behavior change.
func TestBaseArgs_IsOptionArgsPlusDest(t *testing.T) {
	cases := []Flags{
		{User: "root", Port: 22},
		{User: "admin", Port: 2222, Identity: "/home/admin/.ssh/id_ed25519", Agent: true, NoStrict: true},
	}

	for _, f := range cases {
		want := append(OptionArgs(&f), Dest(&f, "10.0.0.5"))
		require.Equal(t, want, BaseArgs(&f, "10.0.0.5"))
	}
}

func TestOptionArgs(t *testing.T) {
	cases := []struct {
		name string
		f    Flags
		want []string
	}{
		{
			name: "defaults",
			f:    Flags{User: "root", Port: 22},
			want: []string{"-p", "22"},
		},
		{
			name: "custom port",
			f:    Flags{User: "root", Port: 2222},
			want: []string{"-p", "2222"},
		},
		{
			name: "identity file",
			f:    Flags{User: "root", Port: 22, Identity: "/home/root/.ssh/id_rsa"},
			want: []string{"-p", "22", "-i", "/home/root/.ssh/id_rsa"},
		},
		{
			name: "agent forwarding",
			f:    Flags{User: "root", Port: 22, Agent: true},
			want: []string{"-p", "22", "-A"},
		},
		{
			name: "no-strict",
			f:    Flags{User: "root", Port: 22, NoStrict: true},
			want: []string{"-p", "22", "-o", "StrictHostKeyChecking=no"},
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
			want: []string{
				"-p", "2222",
				"-i", "/home/admin/.ssh/id_ed25519",
				"-A",
				"-o", "StrictHostKeyChecking=no",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, OptionArgs(&tc.f))
		})
	}
}

func TestBatchOptionArgs(t *testing.T) {
	cases := []struct {
		name string
		f    Flags
		want []string
	}{
		{
			name: "defaults",
			f:    Flags{User: "root", Port: 22},
			want: []string{"-p", "22", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"},
		},
		{
			name: "custom port",
			f:    Flags{User: "root", Port: 2222},
			want: []string{"-p", "2222", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"},
		},
		{
			name: "identity file",
			f:    Flags{User: "root", Port: 22, Identity: "/home/root/.ssh/id_rsa"},
			want: []string{"-p", "22", "-i", "/home/root/.ssh/id_rsa", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"},
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
			want: []string{
				"-p", "2222",
				"-i", "/home/admin/.ssh/id_ed25519",
				"-A",
				"-o", "StrictHostKeyChecking=no",
				"-o", "BatchMode=yes",
				"-o", "ConnectTimeout=10",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, BatchOptionArgs(&tc.f))
		})
	}
}

// TestBatchOptionArgs_IsOptionArgsPlusBatch locks in that BatchOptionArgs stays
// exactly OptionArgs followed by the two non-interactive hardening options, so
// the interactive OptionArgs/BaseArgs paths keep their behavior unchanged.
func TestBatchOptionArgs_IsOptionArgsPlusBatch(t *testing.T) {
	cases := []Flags{
		{User: "root", Port: 22},
		{User: "admin", Port: 2222, Identity: "/home/admin/.ssh/id_ed25519", Agent: true, NoStrict: true},
	}

	for _, f := range cases {
		want := append(OptionArgs(&f), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10")
		require.Equal(t, want, BatchOptionArgs(&f))
	}
}

func TestDest(t *testing.T) {
	cases := []struct {
		name string
		f    Flags
		host string
		want string
	}{
		{name: "default user", f: Flags{User: "root"}, host: "10.0.0.5", want: "root@10.0.0.5"},
		{name: "custom user", f: Flags{User: "admin"}, host: "vm.example.com", want: "admin@vm.example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Dest(&tc.f, tc.host))
		})
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "/home/root/.ssh/id_rsa", want: `'/home/root/.ssh/id_rsa'`},
		{name: "spaces", in: "/Users/jane doe/.ssh/id", want: `'/Users/jane doe/.ssh/id'`},
		{name: "embedded single quote", in: "it's", want: `'it'\''s'`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ShellQuote(tc.in))
		})
	}
}

func TestRemoteShell(t *testing.T) {
	cases := []struct {
		name string
		f    Flags
		want string
	}{
		{
			name: "defaults",
			f:    Flags{User: "root", Port: 22},
			want: "ssh -p 22",
		},
		{
			name: "custom port",
			f:    Flags{User: "root", Port: 2222},
			want: "ssh -p 2222",
		},
		{
			name: "identity without spaces is not quoted",
			f:    Flags{User: "root", Port: 22, Identity: "/home/root/.ssh/id_rsa"},
			want: "ssh -p 22 -i /home/root/.ssh/id_rsa",
		},
		{
			name: "identity with spaces is quoted",
			f:    Flags{User: "root", Port: 2222, Identity: "/Users/jane doe/.ssh/id"},
			want: "ssh -p 2222 -i '/Users/jane doe/.ssh/id'",
		},
		{
			name: "agent and no-strict, no quoting needed",
			f:    Flags{User: "root", Port: 22, Agent: true, NoStrict: true},
			want: "ssh -p 22 -A -o StrictHostKeyChecking=no",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, RemoteShell(&tc.f))
		})
	}
}
