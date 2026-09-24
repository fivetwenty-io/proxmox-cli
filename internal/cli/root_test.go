package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/api"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/pbs"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/pdm"
	pvegroup "github.com/fivetwenty-io/proxmox-cli/internal/cli/pve"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	personapkg "github.com/fivetwenty-io/proxmox-cli/internal/persona"
	"github.com/fivetwenty-io/proxmox-cli/internal/version"
)

// TestRootFlags_Defaults verifies that NewRootCmd sets the expected flag
// defaults for all persistent flags.
func TestRootFlags_Defaults(t *testing.T) {
	// Clear env vars that influence flag defaults.
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	flags := root.PersistentFlags()

	require.True(t, flags.HasFlags(), "root must have persistent flags")

	// --config default contains "pmx/config.yml".
	cfgFlag := flags.Lookup("config")
	require.NotNil(t, cfgFlag)
	require.Contains(t, cfgFlag.DefValue, "pmx")
	require.Contains(t, cfgFlag.DefValue, "config.yml")

	// --context default is empty string; short flag is -c.
	ctxFlag := flags.Lookup("context")
	require.NotNil(t, ctxFlag, "--context flag must exist")
	require.Equal(t, "", ctxFlag.DefValue)
	require.Equal(t, "c", ctxFlag.Shorthand, "--context short flag must be -c")

	// --target must not exist after the context rename.
	require.Nil(t, flags.Lookup("target"), "--target flag must not exist after rename")

	// --node default is empty (PMX_NODE unset).
	nodeFlag := flags.Lookup("node")
	require.NotNil(t, nodeFlag)
	require.Equal(t, "", nodeFlag.DefValue)

	// --output default is "table" (PMX_OUTPUT unset).
	outFlag := flags.Lookup("output")
	require.NotNil(t, outFlag)
	require.Equal(t, "table", outFlag.DefValue)

	// Boolean flags default to false.
	for _, name := range []string{"debug", "verbose", "trace", "no-log", "async", "insecure"} {
		f := flags.Lookup(name)
		require.NotNil(t, f, "flag %s must exist", name)
		require.Equal(t, "false", f.DefValue, "flag %s default must be false", name)
	}

	// --wait-timeout defaults to 0, which waits until the task ends.
	waitFlag := flags.Lookup("wait-timeout")
	require.NotNil(t, waitFlag, "--wait-timeout flag must exist")
	require.Equal(t, "0", waitFlag.DefValue)

	// The nine per-invocation connection overrides. The three timeouts are
	// strings rather than pflag durations, so a malformed value reaches
	// config.ParseTimeout's message instead of pflag's, and none of the nine
	// takes a shorthand.
	apiFlags := []struct{ name, typ, def string }{
		{"api-endpoint", "string", ""},
		{"api-jump", "string", ""},
		{"api-proxy", "string", ""},
		{"api-proxy-from-env", "bool", "false"},
		{"api-ca-cert", "string", ""},
		{"api-fingerprint", "string", ""},
		{"api-connect-timeout", "string", ""},
		{"api-tls-handshake-timeout", "string", ""},
		{"api-request-timeout", "string", ""},
	}
	for _, want := range apiFlags {
		f := flags.Lookup(want.name)
		require.NotNil(t, f, "--%s flag must exist", want.name)
		require.Equal(t, want.typ, f.Value.Type(), "--%s type", want.name)
		require.Equal(t, want.def, f.DefValue, "--%s default", want.name)
		require.Empty(t, f.Shorthand, "--%s must take no shorthand", want.name)
		require.NotEmpty(t, f.Usage, "--%s must carry help text", want.name)
	}

	// Thirteen persistent flags before the connection overrides, twenty-two
	// with them. cobra's own help and version flags are not persistent.
	count := 0
	flags.VisitAll(func(*pflag.Flag) { count++ })
	require.Equal(t, 22, count, "the root must register exactly twenty-two persistent flags")
}

// TestDepsConn_PopulatedBeforeNoClientReturn proves that a noClient command,
// which returns from persistentPreRunE before any client or context is
// resolved, still reaches the connection overrides through its Deps, so the
// auth and context groups can honour them.
func TestDepsConn_PopulatedBeforeNoClientReturn(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_API_PROXY", "socks5h://proxy.test:1080")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	cmd := buildInspectCmd(&capturedDeps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "config.yml"), "--no-log", "inspect"})

	require.NoError(t, root.Execute())
	require.NotNil(t, capturedDeps)
	require.Nil(t, capturedDeps.Ctx, "the command must have taken the noClient return")
	require.NotNil(t, capturedDeps.Conn, "Conn must be assigned before the noClient return")

	ov, err := capturedDeps.ConnectionOverrides()
	require.NoError(t, err)
	require.Equal(t, "socks5h://proxy.test:1080", ov.Proxy)
	require.Equal(t, "$PMX_API_PROXY", ov.ProxySource)
}

// TestDeps_ConnectionOverridesNilSafe proves that a Deps built by hand, as
// hundreds of tests and every group factory's placeholder do, yields no
// overrides rather than a nil-function panic.
func TestDeps_ConnectionOverridesNilSafe(t *testing.T) {
	ov, err := (&cli.Deps{}).ConnectionOverrides()
	require.NoError(t, err)
	require.Equal(t, cli.ConnectionOverrides{}, ov)

	var nilDeps *cli.Deps
	ov, err = nilDeps.ConnectionOverrides()
	require.NoError(t, err)
	require.Equal(t, cli.ConnectionOverrides{}, ov)
}

// TestDeps_ConnectionOverridesCarriesRootInsecure proves that the root's
// persistent --insecure reaches ConnectionOverrides.Insecure, and that a
// command's own local --insecure, such as the one context add registers to
// store tls.insecure, does not.
func TestDeps_ConnectionOverridesCarriesRootInsecure(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	run := func(t *testing.T, localInsecure bool, args ...string) cli.ConnectionOverrides {
		t.Helper()

		root, cleanup := cli.NewRootCmd("pmx")
		t.Cleanup(cleanup)
		root.SetContext(context.Background())

		var capturedDeps *cli.Deps
		cmd := buildInspectCmd(&capturedDeps)
		cmd.Annotations = map[string]string{"noClient": "true"}
		if localInsecure {
			cmd.Flags().Bool("insecure", false, "store tls.insecure")
		}
		root.AddCommand(cmd)

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append([]string{"--config", filepath.Join(t.TempDir(), "config.yml"), "--no-log"}, args...))

		require.NoError(t, root.Execute())
		require.NotNil(t, capturedDeps)

		ov, err := capturedDeps.ConnectionOverrides()
		require.NoError(t, err)

		return ov
	}

	require.True(t, run(t, false, "--insecure", "inspect").Insecure, "a root --insecure must reach the overrides")
	require.True(t, run(t, false, "inspect", "--insecure").Insecure,
		"a root --insecure typed after the verb must reach the overrides")
	require.False(t, run(t, false, "inspect").Insecure)
	require.False(t, run(t, true, "inspect", "--insecure").Insecure,
		"a command's local --insecure is not the root's and must not reach the overrides")
}

// TestRootCommand_OverridesFromCommandRejectsMalformed parses arguments with
// the real root's flag set and calls OverridesFromCommand on the leaf, so the
// registration the root ships is the one under test. Each malformed value
// fails naming its flag, and a timeout without a unit fails with the
// duration message rather than pflag's own parse error.
func TestRootCommand_OverridesFromCommandRejectsMalformed(t *testing.T) {
	parse := func(t *testing.T, args ...string) (cli.ConnectionOverrides, error) {
		t.Helper()

		root, cleanup := cli.NewRootCmd("pmx")
		t.Cleanup(cleanup)
		root.AddCommand(&cobra.Command{Use: "leaf", RunE: func(*cobra.Command, []string) error { return nil }})

		leaf, rest, err := root.Find(append([]string{"leaf"}, args...))
		require.NoError(t, err)
		require.Equal(t, "leaf", leaf.Name())
		require.NoError(t, leaf.ParseFlags(rest), "pflag itself must accept every value; the parse is ours")

		return cli.OverridesFromCommand(leaf)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"endpoint scheme", []string{"--api-endpoint", "ftp://pve1"},
			`invalid --api-endpoint "ftp://pve1": scheme must be https or http`},
		{"endpoint port", []string{"--api-endpoint", "pve1:99999"},
			`invalid --api-endpoint "pve1:99999": port 99999 is out of range [1, 65535]`},
		{"connect zero", []string{"--api-connect-timeout", "0s"},
			"--api-connect-timeout must be greater than zero"},
		{"connect negative", []string{"--api-connect-timeout=-1s"},
			"--api-connect-timeout must be greater than zero"},
		{"connect without a unit", []string{"--api-connect-timeout", "5"},
			`--api-connect-timeout "5" is not a duration (e.g. 5s, 500ms)`},
		{"handshake without a unit", []string{"--api-tls-handshake-timeout", "10"},
			`--api-tls-handshake-timeout "10" is not a duration (e.g. 5s, 500ms)`},
		{"request zero", []string{"--api-request-timeout", "0s"},
			"--api-request-timeout must be greater than zero"},
		{"fingerprint", []string{"--api-fingerprint", "garbage"},
			`--api-fingerprint "garbage" must be a colon-separated hex SHA-256 (e.g. AA:BB:..., 32 pairs)`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse(t, tc.args...)
			require.EqualError(t, err, tc.want)
			require.NotContains(t, err.Error(), "invalid argument", "pflag's own message must not surface")
		})
	}

	t.Run("well-formed values parse", func(t *testing.T) {
		ov, err := parse(t,
			"--api-endpoint", "https://pve2:8443",
			"--api-connect-timeout", "5s",
			"--api-tls-handshake-timeout", "10s",
			"--api-request-timeout", "1m",
			"--api-fingerprint", strings.Repeat("AB:", 31)+"AB",
		)
		require.NoError(t, err)
		require.Equal(t, "pve2", ov.Host)
		require.Equal(t, 8443, ov.Port)
		require.Equal(t, "https", ov.Protocol)
		require.Equal(t, 5*time.Second, ov.Connect)
		require.Equal(t, 10*time.Second, ov.TLSHandshake)
		require.Equal(t, time.Minute, ov.Request)
		require.Equal(t, "--api-fingerprint", ov.FingerprintSource)
	})
}

// newPersonaRoot builds a persona's real command tree, with every group the
// binary ships, writing both output streams to the returned buffers.
func newPersonaRoot(t *testing.T, persona string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	root, cleanup := cli.NewRootCmd(persona)
	t.Cleanup(cleanup)
	root.SetContext(context.Background())

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)

	// SetOut precedes AddGroups because cobra's completion sub-commands
	// capture the writer when AddGroups creates them.
	cli.AddGroups(root, &cli.Deps{}, personapkg.Factories(persona))

	return root, &out, &errOut
}

// TestNoClient_RefusesAPIConnectionFlags proves that a noClient command,
// which resolves no connection, refuses an --api-* flag rather than ignoring
// it and exiting 0. On context add and context update the jump and proxy
// flags point at the flag that stores the setting, and nothing is written.
// The PMX_API_* variables stay silent on the same commands, and a completion
// request carrying a flag never meets the refusal.
func TestNoClient_RefusesAPIConnectionFlags(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	addArgs := func(cfgPath string) []string {
		return []string{
			"--config", cfgPath, "--no-log", "context", "add", "lab",
			"--host", "pve1.example.test", "--username", "root@pam", "--token-id", "cli",
			"--secret", "${PMX_TEST_TOKEN}",
		}
	}

	// `pmx version` reports the server's version and builds a client, so the
	// build-info leaf `pmx version client` is the noClient command here.
	t.Run("version client names every flag it refuses", func(t *testing.T) {
		for _, tc := range []struct{ flag, value string }{
			{"--api-endpoint", "h"},
			{"--api-jump", "b"},
			{"--api-proxy", "socks5h://h:1080"},
			{"--api-proxy-from-env", ""},
			{"--api-ca-cert", "/ca.pem"},
			{"--api-fingerprint", "garbage"},
			{"--api-connect-timeout", "5s"},
			{"--api-tls-handshake-timeout", "5s"},
			{"--api-request-timeout", "5s"},
		} {
			root, out, _ := newPersonaRoot(t, "pmx")
			args := []string{
				"--config", filepath.Join(t.TempDir(), "config.yml"), "--no-log", "version", "client", tc.flag,
			}
			if tc.value != "" {
				args = append(args, tc.value)
			}
			root.SetArgs(args)

			err := root.Execute()
			require.EqualError(t, err, tc.flag+" has no effect on pmx version client")
			require.Empty(t, out.String(), "version client must not run")
		}
	})

	t.Run("a command that builds a client is not refused", func(t *testing.T) {
		root, _, _ := newPersonaRoot(t, "pmx")
		root.SetArgs([]string{
			"--config", filepath.Join(t.TempDir(), "config.yml"), "--no-log", "version", "--api-endpoint", "h",
		})

		err := root.Execute()
		require.Error(t, err, "with no context configured, pmx version must still fail")
		require.NotContains(t, err.Error(), "has no effect")
		require.Contains(t, err.Error(), "no context specified")
	})

	t.Run("context add refuses --api-jump and writes nothing", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.yml")
		root, out, _ := newPersonaRoot(t, "pmx")
		root.SetArgs(append(addArgs(cfgPath), "--api-jump", "b"))

		err := root.Execute()
		require.EqualError(t, err,
			"--api-jump has no effect on pmx context add; use --ssh-jump to store a bastion")
		require.Empty(t, out.String())

		_, statErr := os.Stat(cfgPath)
		require.ErrorIs(t, statErr, fs.ErrNotExist, "a refused context add must write no config")
	})

	t.Run("context add refuses --api-proxy with the stored equivalent", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.yml")
		root, _, _ := newPersonaRoot(t, "pmx")
		root.SetArgs(append(addArgs(cfgPath), "--api-proxy", "socks5h://h:1080"))

		err := root.Execute()
		require.EqualError(t, err,
			"--api-proxy has no effect on pmx context add; use --proxy-url to store a proxy")

		_, statErr := os.Stat(cfgPath)
		require.ErrorIs(t, statErr, fs.ErrNotExist)
	})

	t.Run("context add points every flag at the flag that stores it", func(t *testing.T) {
		for _, tc := range []struct{ flag, value, hint string }{
			{"--api-endpoint", "h", "use --host, --port, and --protocol to store the endpoint"},
			{"--api-proxy-from-env", "", "use --proxy-from-env to store the setting"},
			{"--api-ca-cert", "/ca.pem", "use --ca-cert to store a CA certificate"},
			{"--api-fingerprint", "garbage", "use --fingerprint to store a pin"},
			{"--api-connect-timeout", "5s", "use --timeout-connect to store a timeout"},
			{"--api-tls-handshake-timeout", "5s", "use --timeout-tls-handshake to store a timeout"},
			{"--api-request-timeout", "5s", "use --timeout-request to store a timeout"},
		} {
			cfgPath := filepath.Join(t.TempDir(), "config.yml")
			root, _, _ := newPersonaRoot(t, "pmx")
			args := append(addArgs(cfgPath), tc.flag)
			if tc.value != "" {
				args = append(args, tc.value)
			}
			root.SetArgs(args)

			require.EqualError(t, root.Execute(), tc.flag+" has no effect on pmx context add; "+tc.hint)

			_, statErr := os.Stat(cfgPath)
			require.ErrorIs(t, statErr, fs.ErrNotExist, "a refused context add must write no config")
		}
	})

	t.Run("a mistyped verb reports the unknown command, not the flag", func(t *testing.T) {
		cfgPath := writeTwoContextConfig(t)
		root, _, _ := newPersonaRoot(t, "pmx")
		root.SetArgs([]string{"--config", cfgPath, "--no-log", "context", "udpate", "alpha", "--api-jump", "b"})

		require.EqualError(t, root.Execute(), `unknown command "udpate" for "pmx context"`)
	})

	t.Run("a bare group command still refuses the flag", func(t *testing.T) {
		cfgPath := writeTwoContextConfig(t)
		root, out, _ := newPersonaRoot(t, "pmx")
		root.SetArgs([]string{"--config", cfgPath, "--no-log", "context", "--api-jump", "b"})

		require.EqualError(t, root.Execute(), "--api-jump has no effect on pmx context")
		require.Empty(t, out.String(), "the refused group must not print its help")
	})

	t.Run("help and completion accept the flags", func(t *testing.T) {
		for _, args := range [][]string{
			{"--api-endpoint", "h", "help", "context"},
			{"help", "--api-jump", "b"},
			{"completion", "bash", "--api-endpoint", "h"},
			{"completion", "--api-proxy", "socks5h://h:1080"},
		} {
			root, out, _ := newPersonaRoot(t, "pmx")
			root.SetArgs(append([]string{"--config", filepath.Join(t.TempDir(), "config.yml"), "--no-log"}, args...))

			require.NoError(t, root.Execute(), "pmx %s", strings.Join(args, " "))
			require.NotEmpty(t, out.String(), "pmx %s must print its text", strings.Join(args, " "))
		}
	})

	t.Run("context update refuses --api-proxy and leaves the file alone", func(t *testing.T) {
		cfgPath := writeTwoContextConfig(t)
		before, err := os.ReadFile(cfgPath)
		require.NoError(t, err)

		root, out, _ := newPersonaRoot(t, "pmx")
		root.SetArgs([]string{
			"--config", cfgPath, "--no-log", "context", "update", "alpha",
			"--host", "moved.invalid", "--api-proxy", "socks5h://h:1080",
		})

		err = root.Execute()
		require.EqualError(t, err,
			"--api-proxy has no effect on pmx context update; use --proxy-url to store a proxy")
		require.Empty(t, out.String())

		after, err := os.ReadFile(cfgPath)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after), "a refused context update must not touch the config")
	})

	t.Run("the hidden ctx alias carries the same hint", func(t *testing.T) {
		cfgPath := writeTwoContextConfig(t)
		root, _, _ := newPersonaRoot(t, "pmx")
		root.SetArgs([]string{"--config", cfgPath, "--no-log", "ctx", "update", "alpha", "--api-jump", "b"})

		require.EqualError(t, root.Execute(),
			"--api-jump has no effect on pmx ctx update; use --ssh-jump to store a bastion")
	})

	t.Run("PMX_API_JUMP on context add stays silent and succeeds", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "b")

		cfgPath := filepath.Join(t.TempDir(), "config.yml")
		root, out, errOut := newPersonaRoot(t, "pmx")
		root.SetArgs(addArgs(cfgPath))

		require.NoError(t, root.Execute())
		require.Contains(t, out.String(), `Context "lab" added.`)
		require.NotContains(t, errOut.String(), "has no effect")

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)
		require.Contains(t, cfg.Contexts, "lab")
		require.Empty(t, cfg.Contexts["lab"].SSH.Jump, "an exported variable must never be stored")
	})

	// "__complete" is never a noClient command, so the refusal could not fire
	// for it even if persistentPreRunE ran the refusal first. This pins the
	// completion output; the ordering itself is defensive and unobservable.
	t.Run("a completion request carrying a flag is never refused", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())

		root, out, errOut := newPersonaRoot(t, "pmx")
		root.SetArgs([]string{"__complete", "context", "add", "lab", "--api-endpoint", "h", "--product", ""})

		require.NoError(t, root.Execute())
		require.Equal(t,
			"pve\tProxmox VE\npbs\tProxmox Backup Server\npdm\tProxmox Datacenter Manager\n:4\n", out.String())
		require.Equal(t, "Completion ended with directive: ShellCompDirectiveNoFileComp\n", errOut.String())
	})
}

// TestClientCommand_RejectsMalformedOverridesBeforeResolving proves that a
// command that builds a client parses the --api-* flags and the PMX_API_*
// variables before it resolves a context, so a malformed value fails naming
// its source instead of being ignored. No context is configured, so any
// other outcome would surface as "no context specified".
func TestClientCommand_RejectsMalformedOverridesBeforeResolving(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	run := func(t *testing.T, args ...string) error {
		t.Helper()

		root, out, _ := newPersonaRoot(t, "pmx")
		root.SetArgs(append([]string{"--config", filepath.Join(t.TempDir(), "config.yml"), "--no-log", "version"},
			args...))

		err := root.Execute()
		require.Empty(t, out.String(), "the command must not run")

		return err
	}

	t.Run("a malformed flag", func(t *testing.T) {
		require.EqualError(t, run(t, "--api-endpoint", "ftp://pve1"),
			`invalid --api-endpoint "ftp://pve1": scheme must be https or http`)
		require.EqualError(t, run(t, "--api-connect-timeout", "5"),
			`--api-connect-timeout "5" is not a duration (e.g. 5s, 500ms)`)
		require.EqualError(t, run(t, "--api-fingerprint", "garbage"),
			`--api-fingerprint "garbage" must be a colon-separated hex SHA-256 (e.g. AA:BB:..., 32 pairs)`)
	})

	t.Run("a malformed variable", func(t *testing.T) {
		t.Setenv("PMX_API_REQUEST_TIMEOUT", "0s")

		require.EqualError(t, run(t), "$PMX_API_REQUEST_TIMEOUT must be greater than zero")
	})

	t.Run("well-formed values reach context resolution", func(t *testing.T) {
		t.Setenv("PMX_API_PROXY", "socks5://192.0.2.1:9")

		err := run(t, "--api-endpoint", "192.0.2.1:9", "--api-connect-timeout", "1s")
		require.Error(t, err)
		require.Contains(t, err.Error(), "no context specified")
	})
}

// TestNoClient_UsesConnectionAnnotationAcceptsAPIFlags proves that a noClient
// command that declares AnnotationUsesConnection receives the --api-* flags
// through its Deps instead of refusing them, and that a malformed value
// fails only when the command asks for the overrides, never in
// persistentPreRunE.
func TestNoClient_UsesConnectionAnnotationAcceptsAPIFlags(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	run := func(t *testing.T, args ...string) *cli.Deps {
		t.Helper()

		root, cleanup := cli.NewRootCmd("pmx")
		t.Cleanup(cleanup)
		root.SetContext(context.Background())

		var capturedDeps *cli.Deps
		cmd := buildInspectCmd(&capturedDeps)
		cmd.Annotations = map[string]string{"noClient": "true", cli.AnnotationUsesConnection: "true"}
		root.AddCommand(cmd)

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append([]string{"--config", filepath.Join(t.TempDir(), "config.yml"), "--no-log", "inspect"},
			args...))

		require.NoError(t, root.Execute())
		require.NotNil(t, capturedDeps)

		return capturedDeps
	}

	ov, err := run(t, "--api-endpoint", "pve9:9999", "--api-jump", "b").ConnectionOverrides()
	require.NoError(t, err)
	require.Equal(t, "pve9", ov.Host)
	require.Equal(t, 9999, ov.Port)
	require.Equal(t, "b", ov.Jump)
	require.Equal(t, "--api-jump", ov.JumpSource)

	_, err = run(t, "--api-endpoint", "ftp://pve1").ConnectionOverrides()
	require.EqualError(t, err, `invalid --api-endpoint "ftp://pve1": scheme must be https or http`)
}

// captureStdio runs fn with os.Stdout and os.Stderr redirected to pipes and
// returns what each received. Both pipes are drained concurrently, so fn can
// write any amount to either without blocking.
func captureStdio(t *testing.T, fn func()) (string, string) {
	t.Helper()

	drain := func(r *os.File, dst *bytes.Buffer, done chan<- error) {
		_, err := io.Copy(dst, r)
		done <- errors.Join(err, r.Close())
	}

	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	outDone, errDone := make(chan error, 1), make(chan error, 1)
	go drain(outR, &stdout, outDone)
	go drain(errR, &stderr, errDone)

	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	func() {
		defer func() { os.Stdout, os.Stderr = origOut, origErr }()
		fn()
	}()

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())
	require.NoError(t, <-outDone)
	require.NoError(t, <-errDone)

	return stdout.String(), stderr.String()
}

// TestComplete_ToleratesMalformedAPIEndpointEnv proves that a malformed
// $PMX_API_ENDPOINT, exported in a shell long ago, never breaks tab
// completion. The request runs through cli.Main, as the binary does, exits
// 0, prints its completions, and puts nothing on standard error apart from
// the directive trace cobra itself writes there for every completion
// request, which completion scripts discard.
func TestComplete_ToleratesMalformedAPIEndpointEnv(t *testing.T) {
	t.Setenv("PMX_API_ENDPOINT", "ftp://pve1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PMX_CONTEXT", "")

	old := os.Args
	os.Args = []string{"pmx", "__complete", "context", "add", "lab", "--product", ""}
	defer func() { os.Args = old }()

	var code int
	stdout, stderr := captureStdio(t, func() {
		code = cli.Main("pmx", personapkg.Factories("pmx"))
	})

	require.Equal(t, 0, code, "completion must exit 0; stderr: %s", stderr)
	require.Equal(t,
		"pve\tProxmox VE\npbs\tProxmox Backup Server\npdm\tProxmox Datacenter Manager\n:4\n", stdout)
	require.Equal(t, "Completion ended with directive: ShellCompDirectiveNoFileComp\n", stderr,
		"only cobra's own directive trace may reach standard error")
}

// TestHelp_ConnectionPrecedenceOnPersonaRootsOnly proves each persona root's
// --help states the connection precedence exactly once, and that no other
// command's help repeats it, which is what would happen if the line lived in
// the usage template every command inherits.
func TestHelp_ConnectionPrecedenceOnPersonaRootsOnly(t *testing.T) {
	const line = "Connection settings resolve flag > environment variable > context config > built-in default."

	for _, persona := range personapkg.Names() {
		t.Run(persona, func(t *testing.T) {
			root, out, _ := newPersonaRoot(t, persona)
			root.SetArgs([]string{"--help"})
			require.NoError(t, root.Execute())
			require.Equal(t, 1, strings.Count(out.String(), line), "%s --help must state the precedence once", persona)

			var offenders []string
			var walk func(*cobra.Command)
			walk = func(c *cobra.Command) {
				for _, sub := range c.Commands() {
					out.Reset()
					require.NoError(t, sub.Help())
					if strings.Contains(out.String(), line) {
						offenders = append(offenders, sub.CommandPath())
					}
					walk(sub)
				}
			}
			walk(root)

			require.Empty(t, offenders, "only the persona root's help may state the connection precedence")
		})
	}
}

// TestWaitTimeout_RejectsNegative verifies that the root rejects a negative
// --wait-timeout before it builds any client, so that every task-producing
// verb of every product answers a nonsensical bound the same way.
func TestWaitTimeout_RejectsNegative(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Cleanup(func() { apiclient.SetDefaultWaitTimeout(0) })

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	cmd := buildInspectCmd(&capturedDeps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"--wait-timeout=-5",
		"inspect",
	})

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(),
		"invalid --wait-timeout -5: want 0 (wait until the task ends) or a positive number of seconds")
	require.Nil(t, capturedDeps, "the command must not run with a nonsensical wait bound")
}

// TestWaitTimeout_ReachesDeps verifies that the resolved --wait-timeout is
// visible to commands that build their own wait options, such as the two Ceph
// restart-bulk verbs.
func TestWaitTimeout_ReachesDeps(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Cleanup(func() { apiclient.SetDefaultWaitTimeout(0) })

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	cmd := buildInspectCmd(&capturedDeps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"--wait-timeout", "30",
		"inspect",
	})

	require.NoError(t, root.Execute())
	require.NotNil(t, capturedDeps)
	require.Equal(t, int64(30), capturedDeps.WaitTimeout)
}

// TestWaitTimeout_ReachesTheProcessWidePolicy verifies the other half of the
// wire. Deps carries the bound to the commands that build their own wait
// options, and apiclient's process-wide policy carries it to every caller
// that passes nil options instead, which is most of them. Reading the policy
// back after Execute is what pins the SetDefaultWaitTimeout call in
// persistentPreRunE, because a command that never looks at Deps.WaitTimeout
// still has to honour the operator's bound.
func TestWaitTimeout_ReachesTheProcessWidePolicy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Cleanup(func() { apiclient.SetDefaultWaitTimeout(0) })

	require.Zero(t, apiclient.DefaultWaitTimeout(), "the policy starts unbounded")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	cmd := buildInspectCmd(&capturedDeps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"--wait-timeout", "45",
		"inspect",
	})

	require.NoError(t, root.Execute())
	require.Equal(t, int64(45), apiclient.DefaultWaitTimeout(),
		"the flag must reach the policy every nil-options wait reads, not only Deps")
}

// TestPersistentPreRunE_Insecure_WarnsOnStderr verifies that resolving an
// insecure (TLS-verification-disabled) connection emits a stderr warning.
func TestPersistentPreRunE_Insecure_WarnsOnStderr(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]*config.Context{
			"prod": {
				Host:     "127.0.0.1",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "root@pam",
					TokenID:  "cli",
					Secret:   "literal-secret",
				},
				TLS: config.TLSBlock{Insecure: true},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	called := false
	noop := buildNoopCmd(&called)
	root.AddCommand(noop)

	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--config", cfgPath, "noop"})

	require.NoError(t, root.Execute())
	require.True(t, called)
	require.Contains(t, errBuf.String(), "TLS certificate verification disabled",
		"an insecure connection must warn the operator on stderr")
}

// TestPersistentPreRunE_ASCII_Format verifies that -o ascii is wired through
// to deps.Format and renders tables with ASCII borders.
func TestPersistentPreRunE_ASCII_Format(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var deps *cli.Deps
	cmd := buildInspectCmd(&deps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", cfgPath, "-o", "ascii", "inspect"})
	require.NoError(t, root.Execute())
	require.NotNil(t, deps)
	require.Equal(t, output.FormatASCII, deps.Format)

	// Render a small table; with -o ascii the borders must use ASCII glyphs
	// (e.g. '+') rather than Unicode box-drawing characters.
	var rb bytes.Buffer
	require.NoError(t, deps.Out.Render(&rb, output.Result{
		Headers: []string{"A"},
		Rows:    [][]string{{"1"}},
	}, deps.Format))
	require.Contains(t, rb.String(), "+", "ascii table borders should contain '+'")
	require.NotContains(t, rb.String(), "─", "ascii mode must not use Unicode box-drawing")
}

// TestRootFlags_PVEOutput verifies --output default picks up PMX_OUTPUT.
func TestRootFlags_PVEOutput(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "json")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	outFlag := root.PersistentFlags().Lookup("output")
	require.NotNil(t, outFlag)
	require.Equal(t, "json", outFlag.DefValue)
}

// TestRootFlags_PVENode verifies --node default picks up PMX_NODE.
func TestRootFlags_PVENode(t *testing.T) {
	t.Setenv("PMX_NODE", "pve-host-01")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	nodeFlag := root.PersistentFlags().Lookup("node")
	require.NotNil(t, nodeFlag)
	require.Equal(t, "pve-host-01", nodeFlag.DefValue)
}

// TestPersistentPreRunE_NoConfig_NoContext verifies that when the config file is absent
// AND no context is specified, Execute() returns a non-nil error.
func TestPersistentPreRunE_NoConfig_NoContext(t *testing.T) {
	// Point config at a temp dir that contains no config file.
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "json")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	// A no-op child command that actually triggers PersistentPreRunE.
	called := false
	noop := buildNoopCmd(&called)
	root.AddCommand(noop)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"noop",
	})

	err := root.Execute()
	// PersistentPreRunE should fail because there is no context.
	require.Error(t, err, "expected error when no context is configured")
	require.False(t, called, "noop RunE must not be reached when pre-run fails")
}

// TestPersistentPreRunE_NoClient_AnnotationSkipsClientBuild verifies that a
// command annotated with Annotations["noClient"]="true" does NOT error when
// there is no usable config/context.
func TestPersistentPreRunE_NoClient_AnnotationSkipsClientBuild(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "json")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	called := false
	noop := buildNoopCmd(&called)
	noop.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(noop)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"noop",
	})

	err := root.Execute()
	require.NoError(t, err, "noClient annotation must bypass client build error")
	require.True(t, called, "noop RunE must run when noClient annotation is set")
}

// TestPersistentPreRunE_NoClient_DepsAreInjected verifies that GetDeps returns
// a populated Deps (Out, Format, Log, Runner) even for noClient commands.
func TestPersistentPreRunE_NoClient_DepsAreInjected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	cmd := buildInspectCmd(&capturedDeps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"inspect",
	})

	err := root.Execute()
	require.NoError(t, err)
	require.NotNil(t, capturedDeps)
	require.NotNil(t, capturedDeps.Out, "Out renderer must be populated")
	require.NotNil(t, capturedDeps.Log, "Log must be populated")
	require.NotNil(t, capturedDeps.Runner, "Runner must be populated")
	require.Equal(t, "table", string(capturedDeps.Format))
	require.Nil(t, capturedDeps.API, "API must be nil for noClient commands")
}

// TestPersistentPreRunE_NoConfig_CompletionSucceeds verifies that generating a
// shell completion script does not require a configured context. Every CI
// runner and fresh install has no ~/.config/pmx/config.yml, and goreleaser's
// before-hooks run `pmx completion <shell>` to produce the shipped scripts.
//
// A real sub-command is registered via AddGroups (as the actual binaries do)
// so that cobra's default "completion" command is built against a tree that
// HasSubCommands() — matching production shape, not an empty root.
func TestPersistentPreRunE_NoConfig_CompletionSucceeds(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("PMX_OUTPUT", "json")
			t.Setenv("PMX_NODE", "")
			t.Setenv("PMX_CONTEXT", "")

			root, cleanup := cli.NewRootCmd("pmx")
			defer cleanup()
			root.SetContext(context.Background())

			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)

			// SetOut must precede AddGroups: cobra's completion sub-commands
			// capture c.OutOrStdout() at creation time (inside
			// InitDefaultCompletionCmd, which AddGroups triggers), not at Run
			// time, so setting the writer afterward would be a no-op here.
			called := false
			root.AddCommand(buildNoopCmd(&called))
			cli.AddGroups(root, &cli.Deps{}, nil)

			root.SetArgs([]string{
				"--config", filepath.Join(tmpDir, "config.yml"),
				"completion", shell,
			})

			err := root.Execute()
			require.NoError(t, err, "completion %s must succeed without a configured context", shell)
			require.NotEmpty(t, out.String(), "completion %s must write a script to stdout", shell)
		})
	}
}

// TestPersistentPreRunE_NoConfig_HelpSucceeds verifies that the "help" command
// (as opposed to the --help flag, which never reaches PersistentPreRunE) does
// not require a configured context.
//
// A real sub-command is registered via AddGroups (as the actual binaries do)
// so that cobra's default "help" command actually gets created: cobra only
// installs it when the root HasSubCommands(); on an empty root, "help" as a
// bare positional resolves to the (non-runnable) root itself, which cobra
// turns into a help display WITHOUT ever invoking PersistentPreRunE — that
// would pass regardless of this fix and mask the bug.
func TestPersistentPreRunE_NoConfig_HelpSucceeds(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "json")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	called := false
	root.AddCommand(buildNoopCmd(&called))
	cli.AddGroups(root, &cli.Deps{}, nil)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"help",
	})

	err := root.Execute()
	require.NoError(t, err, "help command must succeed without a configured context")
	require.NotEmpty(t, out.String(), "help command must write usage output to stdout")
}

// TestAddGroups_GroupAppearsInHelp verifies that a factory passed to AddGroups
// is wired into the root command.
func TestAddGroups_GroupAppearsInHelp(t *testing.T) {
	factory := func(_ *cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:   "testgroup",
			Short: "test group for unit tests",
		}
	}

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{factory})

	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["testgroup"], "testgroup must appear in root commands after AddGroups")
}

// TestHelp_WrapsFlagUsagesToColumns verifies that flag descriptions wrap to
// $COLUMNS instead of running off the side. cobra's stock template never
// wraps, and the longest descriptions in this tree are well over 200 columns.
func TestHelp_WrapsFlagUsagesToColumns(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	// The --config default is the one token in the help that cannot wrap, and
	// the widest flag name, --api-tls-handshake-timeout, sets how far every
	// description is indented. A short config home keeps that token inside
	// eighty columns whatever the home directory of the machine running this.
	t.Setenv("XDG_CONFIG_HOME", "/cfg")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	sub := &cobra.Command{Use: "wrapme", Run: func(*cobra.Command, []string) {}}
	sub.Flags().String("long-one", "",
		"a deliberately long flag description that has to be wrapped by the help "+
			"renderer because it comfortably exceeds eighty columns on its own")
	root.AddCommand(sub)

	var buf bytes.Buffer
	sub.SetOut(&buf)
	require.NoError(t, sub.Usage())

	require.Contains(t, buf.String(), "--long-one")
	for line := range strings.SplitSeq(buf.String(), "\n") {
		require.LessOrEqual(t, len(line), 80, "help line exceeds $COLUMNS: %q", line)
	}
}

// TestHelp_WrapsFlagUsagesWithARealisticConfigHome runs the same check with
// a config home as long as a typical one. The --config default cannot wrap,
// so its line alone may run past $COLUMNS, and only by that token: the line
// without the path must still fit, and every other line must fit whole.
func TestHelp_WrapsFlagUsagesWithARealisticConfigHome(t *testing.T) {
	t.Setenv("COLUMNS", "80")

	configHome := "/Users/firstname.lastname/.config"
	t.Setenv("XDG_CONFIG_HOME", configHome)
	configDefault := filepath.Join(configHome, "pmx", "config.yml")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()

	var buf bytes.Buffer
	root.SetOut(&buf)
	require.NoError(t, root.Usage())
	require.Contains(t, buf.String(), configDefault)

	for line := range strings.SplitSeq(buf.String(), "\n") {
		if len(line) <= 80 {
			continue
		}

		require.Contains(t, line, configDefault, "only the --config default may overflow: %q", line)
		require.LessOrEqual(t, len(line)-len(configDefault), 80,
			"the --config line overflows by more than its default: %q", line)
	}
}

// TestHelp_UnwrappedWithoutTerminalWidth verifies the fallback: with no
// $COLUMNS and no terminal on stdout, help stays byte-identical to cobra's
// unwrapped output, so piped and captured help does not change shape.
func TestHelp_UnwrappedWithoutTerminalWidth(t *testing.T) {
	t.Setenv("COLUMNS", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	usage := "a deliberately long flag description that has to be left alone by the " +
		"help renderer because there is no width to wrap it to at all"
	sub := &cobra.Command{Use: "wrapme", Run: func(*cobra.Command, []string) {}}
	sub.Flags().String("long-one", "", usage)
	root.AddCommand(sub)

	var buf bytes.Buffer
	sub.SetOut(&buf)
	require.NoError(t, sub.Usage())

	require.Contains(t, buf.String(), usage)
}

// TestMain_HelpExitsZero verifies that Main() exits 0 when invoked with no
// subcommand (cobra prints help and exits 0).
func TestMain_HelpExitsZero(t *testing.T) {
	// Re-assign args so cobra prints help; os.Exit is NOT called — Main() returns.
	old := os.Args
	os.Args = []string{"pmx", "--help"}
	defer func() { os.Args = old }()

	code := cli.Main("pmx", nil)
	// cobra exits 0 for --help.
	require.Equal(t, 0, code)
}

// TestContextFlagPrecedence verifies the three-tier resolution chain:
// --context flag > $PMX_CONTEXT env > cfg.CurrentContext.
//
// Strategy: each sub-test passes a context name that does NOT exist in the
// config. ResolveContext returns a "not found" error whose message contains
// the name that was resolved. This lets us confirm which tier won without
// needing an actual API connection.
func TestContextFlagPrecedence(t *testing.T) {
	// Build a config with CurrentContext = "current" and one extra context
	// "existing". Tests pass context names that are absent to force a resolution
	// error whose message includes the attempted name.
	makeConfig := func(t *testing.T) string {
		t.Helper()
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yml")
		cfg := &config.Config{
			CurrentContext: "current",
			Contexts: map[string]*config.Context{
				"current": {
					Host: "10.0.0.1", Port: 8006, Protocol: "https", Realm: "pam",
					Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s1"},
				},
			},
		}
		require.NoError(t, config.SaveForce(cfgPath, cfg))
		return cfgPath
	}

	t.Run("flag wins over current-context", func(t *testing.T) {
		t.Setenv("PMX_CONTEXT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_OUTPUT", "table")
		cfgPath := makeConfig(t)

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		root.AddCommand(buildNoopCmd(&called))

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		// Pass a context name "from-flag" that is absent; error message must name it.
		root.SetArgs([]string{"--config", cfgPath, "--context", "from-flag", "noop"})

		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "from-flag",
			"--context flag value must appear in the resolution error")
		require.False(t, called)
	})

	t.Run("env var wins over current-context", func(t *testing.T) {
		t.Setenv("PMX_CONTEXT", "from-env")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_OUTPUT", "table")
		cfgPath := makeConfig(t)

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		root.AddCommand(buildNoopCmd(&called))

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"--config", cfgPath, "noop"})

		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "from-env",
			"$PMX_CONTEXT env value must appear in the resolution error")
		require.False(t, called)
	})

	t.Run("current-context used when no flag or env", func(t *testing.T) {
		t.Setenv("PMX_CONTEXT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_OUTPUT", "table")
		cfgPath := makeConfig(t)

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		root.AddCommand(buildNoopCmd(&called))

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		// No --context, no PMX_CONTEXT. "current" exists in config so no error —
		// but it cannot connect (NewAPIClient returns an error on connect).
		// Verify no "no context specified" error (resolution succeeded).
		root.SetArgs([]string{"--config", cfgPath, "noop"})

		err := root.Execute()
		// The command succeeds: noClient=false but NewAPIClient with a stub host
		// succeeds on construction (lazy HTTP). noop runs without error.
		// Confirm: no resolution error about missing context name.
		if err != nil {
			require.NotContains(t, err.Error(), "no context specified",
				"current-context 'current' must be resolved without error")
		}
	})

	t.Run("unknown-target-flag", func(t *testing.T) {
		// --target must not exist; cobra must return unknown flag error.
		t.Setenv("PMX_CONTEXT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_OUTPUT", "table")
		cfgPath := makeConfig(t)

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		noop := buildNoopCmd(&called)
		root.AddCommand(noop)

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"--config", cfgPath, "--target", "a", "noop"})

		err := root.Execute()
		require.Error(t, err, "--target must be an unknown flag error")
		require.Contains(t, err.Error(), "unknown flag",
			"error must identify --target as unknown")
		require.False(t, called, "noop must not run when an unknown flag is passed")
	})
}

// TestShellCompletionSkipsClientBuild is the regression test for a bug
// discovered while validating H-2 (dead `pmx ssh` node-name completion):
// cobra's built-in "__complete" hidden command has DisableFlagParsing set,
// so persistentPreRunE — which cobra runs for "__complete" itself, as the
// nearest ancestor with a PersistentPreRunE, BEFORE "__complete"'s own Run
// dispatches into the target command's ValidArgsFunction — never sees the
// real --config/--context the operator typed; it resolves the DEFAULT
// context instead. If that default context is broken in any way (bad
// secret reference, unresolvable), BuildContextClient errors and used to
// abort "__complete" entirely: every shell-completion request would print
// an error and exit non-zero, regardless of which command was being
// completed or whether ITS OWN ValidArgsFunction had nothing to do with the
// broken default context at all.
//
// persistentPreRunE now skips client construction for "__complete" itself
// (same as a noClient command), so completion always proceeds to the target
// command's own Run/ValidArgsFunction instead of failing here.
func TestShellCompletionSkipsClientBuild(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "table")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "broken",
		Contexts: map[string]*config.Context{
			"broken": {
				Host: "10.0.0.1", Port: 8006, Protocol: "https", Realm: "pam",
				Auth: config.AuthBlock{
					Type: "token", Username: "root@pam", TokenID: "tok",
					// Unresolvable on every platform (no keychain dependency):
					// ResolveSecret errors for an explicit but unset env reference.
					Secret: "${PMX_CLI_TEST_UNSET_SECRET_VAR_XYZ}",
				},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	buildRoot := func(t *testing.T) *cobra.Command {
		t.Helper()
		root, cleanup := cli.NewRootCmd("pmx")
		t.Cleanup(cleanup)
		root.SetContext(context.Background())
		called := false
		root.AddCommand(buildNoopCmd(&called))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		return root
	}

	t.Run("control: normal noop execution fails on the broken default context", func(t *testing.T) {
		root := buildRoot(t)
		root.SetArgs([]string{"--config", cfgPath, "noop"})
		err := root.Execute()
		require.Error(t, err, "the config's default context must genuinely be broken")
		require.Contains(t, err.Error(), "resolve secret")
	})

	t.Run("__complete noop does not error even though the default context is broken", func(t *testing.T) {
		root := buildRoot(t)
		root.SetArgs([]string{"--config", cfgPath, "__complete", "noop", ""})
		err := root.Execute()
		require.NoError(t, err, "shell completion must never fail due to the default context")
	})
}

// TestOutputChangedDetection verifies that cmd.Flags().Changed("output") correctly
// distinguishes an explicit -o flag from an absent one, even when the explicit
// value equals the global default ("table"). This is the mechanism used in
// persistentPreRunE to guard per-context DefaultOutput application (F-01 fix).
func TestOutputChangedDetection(t *testing.T) {
	t.Run("explicit -o table marks flag as Changed", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		var changedWhenExplicit bool
		probe := &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				changedWhenExplicit = cmd.Flags().Changed("output")
				return nil
			},
		}
		root.AddCommand(probe)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "c.yml"),
			"--output", "table", "probe"})
		require.NoError(t, root.Execute())
		require.True(t, changedWhenExplicit,
			"cmd.Flags().Changed(\"output\") must be true when -o is explicitly passed")
	})

	t.Run("absent -o flag does NOT mark flag as Changed", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		var changedWhenAbsent bool
		probe := &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				changedWhenAbsent = cmd.Flags().Changed("output")
				return nil
			},
		}
		root.AddCommand(probe)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "c.yml"), "probe"})
		require.NoError(t, root.Execute())
		require.False(t, changedWhenAbsent,
			"cmd.Flags().Changed(\"output\") must be false when -o was not passed")
	})
}

// TestContextDefaultsResolution verifies the three-layer defaults:
// explicit flag > context default > global default.
//
// Strategy: use a noClient probe that captures deps after PersistentPreRunE.
// Per-context defaults (DefaultNode, DefaultOutput) are applied in the non-noClient
// branch (after ResolveContext). To test them without a real API connection,
// use a context name that does NOT exist: ResolveContext returns an error
// that confirms the resolution chain ran. A separate sub-test for the noClient
// path verifies no context error when annotation bypasses resolution.
func TestContextDefaultsResolution(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "") // no global env override

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")

	t.Run("context DefaultNode applied when --node not set", func(t *testing.T) {
		// Config has context "lab" with DefaultNode="pve1".
		// Pass a nonexistent --context to trigger a ResolveContext error that
		// includes the name, confirming the resolution chain was entered.
		cfg := &config.Config{
			CurrentContext: "lab",
			Contexts: map[string]*config.Context{
				"lab": {
					Host:        "10.0.0.1",
					Port:        8006,
					Protocol:    "https",
					Realm:       "pam",
					DefaultNode: "pve1",
					Auth:        config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s1"},
				},
			},
		}
		require.NoError(t, config.SaveForce(cfgPath, cfg))

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		root.AddCommand(buildNoopCmd(&called))

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		// Use a missing context name to force a clear error message.
		root.SetArgs([]string{"--config", cfgPath, "--context", "missing-ctx", "noop"})

		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing-ctx",
			"resolution chain entered; error names the missing context")
	})

	t.Run("noClient command runs without context configured", func(t *testing.T) {
		// Empty config: no current-context. noClient command must succeed.
		emptyCfgPath := filepath.Join(t.TempDir(), "empty.yml")
		require.NoError(t, config.SaveForce(emptyCfgPath, &config.Config{}))

		t.Setenv("PMX_OUTPUT", "table")
		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		noop := buildNoopCmd(&called)
		noop.Annotations = map[string]string{"noClient": "true"}
		root.AddCommand(noop)

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"--config", emptyCfgPath, "noop"})

		err := root.Execute()
		require.NoError(t, err, "noClient command must succeed with no context configured")
		require.True(t, called)
	})
}

// TestNodeExplicitDetection pins how deps.NodeExplicit is wired: true only
// when --node is passed on the command line, false when the node arrives
// ambiently via $PMX_NODE (guest resolution must not trust an ambient node as
// a guest's location, so the distinction is load-bearing).
func TestNodeExplicitDetection(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "c.yml")
	require.NoError(t, config.SaveForce(cfgPath, &config.Config{}))

	newProbe := func(node *string, explicit *bool) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				deps := cli.GetDeps(cmd)
				*node = deps.Node
				*explicit = deps.NodeExplicit
				return nil
			},
		}
	}

	t.Run("--node on the command line is explicit", func(t *testing.T) {
		t.Setenv("PMX_NODE", "")
		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		var node string
		var explicit bool
		root.AddCommand(newProbe(&node, &explicit))

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", cfgPath, "--node", "pve9", "probe"})
		require.NoError(t, root.Execute())
		require.Equal(t, "pve9", node)
		require.True(t, explicit, "--node on the command line must set NodeExplicit")
	})

	t.Run("PMX_NODE is ambient, not explicit", func(t *testing.T) {
		t.Setenv("PMX_NODE", "pve9")
		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		var node string
		var explicit bool
		root.AddCommand(newProbe(&node, &explicit))

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", cfgPath, "probe"})
		require.NoError(t, root.Execute())
		require.Equal(t, "pve9", node)
		require.False(t, explicit, "$PMX_NODE must not set NodeExplicit")
	})
}

// TestOutputPrecedence_FourTiers pins the full 4-tier resolution order for
// --output: explicit flag > $PMX_OUTPUT > context default-output > built-in default.
//
// Each sub-test uses a noClient inspect command so no API connection is needed.
// Context default-output is NOT applied in the noClient branch (it runs before
// ResolveContext), so this suite tests flag and env tiers cleanly. The noClient
// path yields the format that pf.output resolved to (flag or env), proving tiers
// 1 and 2. Tiers 3 and 4 are covered by TestContextDefaultsResolution and the
// existing TestOutputChangedDetection tests.
func TestOutputPrecedence_FourTiers(t *testing.T) {
	makeCtxConfig := func(t *testing.T, defaultOutput string) (cfgPath string) {
		t.Helper()
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yml")
		cfg := &config.Config{
			CurrentContext: "test",
			Contexts: map[string]*config.Context{
				"test": {
					Host:          "10.0.0.1",
					Port:          8006,
					Protocol:      "https",
					Realm:         "pam",
					DefaultOutput: defaultOutput,
					Auth:          config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
				},
			},
		}
		require.NoError(t, config.SaveForce(p, cfg))
		return p
	}

	t.Run("tier1 explicit flag beats env and context default", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "json")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")
		// context default-output = yaml; flag = plain → plain must win.
		cfgPath := makeCtxConfig(t, "yaml")

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		var deps *cli.Deps
		cmd := buildInspectCmd(&deps)
		cmd.Annotations = map[string]string{"noClient": "true"}
		root.AddCommand(cmd)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", cfgPath, "--output", "plain", "inspect"})
		require.NoError(t, root.Execute())
		require.NotNil(t, deps)
		require.Equal(t, "plain", string(deps.Format),
			"explicit --output flag must win over $PMX_OUTPUT and context default-output")
	})

	t.Run("tier2 PMX_OUTPUT beats context default-output", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "yaml")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")
		// context default-output = json; $PMX_OUTPUT = yaml → yaml must win.
		// noClient branch: format = pf.output = yaml (baked from env); no context resolution.
		cfgPath := makeCtxConfig(t, "json")

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		var deps *cli.Deps
		cmd := buildInspectCmd(&deps)
		cmd.Annotations = map[string]string{"noClient": "true"}
		root.AddCommand(cmd)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", cfgPath, "inspect"})
		require.NoError(t, root.Execute())
		require.NotNil(t, deps)
		require.Equal(t, "yaml", string(deps.Format),
			"$PMX_OUTPUT must win over context default-output")
	})

	t.Run("tier4 built-in default table when no flag env or context default", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")
		cfgPath := makeCtxConfig(t, "") // no context default-output

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		var deps *cli.Deps
		cmd := buildInspectCmd(&deps)
		cmd.Annotations = map[string]string{"noClient": "true"}
		root.AddCommand(cmd)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", cfgPath, "inspect"})
		require.NoError(t, root.Execute())
		require.NotNil(t, deps)
		require.Equal(t, "table", string(deps.Format),
			"built-in default must be table when nothing overrides it")
	})
}

// TestOutputPrecedence_EnvBeatsContextDefault_NonNoClient verifies F-W6-03:
// $PMX_OUTPUT outranks context default-output even in the full (non-noClient)
// resolution path. Uses a token-auth context against a non-listening host;
// NewAPIClient succeeds lazily so the inspect probe captures deps.Format.
func TestOutputPrecedence_EnvBeatsContextDefault_NonNoClient(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "json")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "test",
		Contexts: map[string]*config.Context{
			"test": {
				Host:          "127.0.0.1",
				Port:          8006,
				Protocol:      "https",
				Realm:         "pam",
				DefaultOutput: "yaml", // context default; must NOT win
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "root@pam",
					TokenID:  "tok",
					Secret:   "literal-secret",
				},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			capturedDeps = cli.GetDeps(cmd)
			return nil
		},
	}
	root.AddCommand(probe)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", cfgPath, "probe"})

	// NewAPIClient is lazy, and this config is entirely local: a temp file, an
	// inline secret, and a host nothing ever dials. There is no environmental
	// reason for this to fail, so a failure is a regression and must fail the
	// test rather than skip it.
	require.NoError(t, root.Execute())
	require.NotNil(t, capturedDeps)
	require.Equal(t, "json", string(capturedDeps.Format),
		"$PMX_OUTPUT=json must beat context default-output=yaml in non-noClient path")
}

// TestPersistentPreRunE_Ctx_PopulatedForNonNoClient verifies that Deps.Ctx is
// populated with the resolved *config.Context for a normal (non-noClient)
// command, so top-level commands (e.g. `pmx ssh`/`pmx rsync`) can read
// per-context SSH defaults without re-resolving the config themselves.
func TestPersistentPreRunE_Ctx_PopulatedForNonNoClient(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "json")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "test",
		Contexts: map[string]*config.Context{
			"test": {
				Host:     "127.0.0.1",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "root@pam",
					TokenID:  "tok",
					Secret:   "literal-secret",
				},
				SSH: config.SSHBlock{User: "admin", Port: 2222},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			capturedDeps = cli.GetDeps(cmd)
			return nil
		},
	}
	root.AddCommand(probe)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", cfgPath, "probe"})

	// See the sibling test: client construction is lazy and the config is
	// local, so a failure here is a regression, not an absent lab.
	require.NoError(t, root.Execute())
	require.NotNil(t, capturedDeps)
	require.NotNil(t, capturedDeps.Ctx, "Ctx must be populated after successful context resolution")
	require.Equal(t, "127.0.0.1", capturedDeps.Ctx.Host)
	require.Equal(t, "admin", capturedDeps.Ctx.SSH.User)
	require.Equal(t, 2222, capturedDeps.Ctx.SSH.Port)
}

// TestPersistentPreRunE_Ctx_NilForNoClient verifies that Deps.Ctx stays nil for
// noClient commands, since the noClient early-return in persistentPreRunE
// returns before context resolution runs. Callers reading deps.Ctx from a
// noClient command must nil-check.
func TestPersistentPreRunE_Ctx_NilForNoClient(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	cmd := buildInspectCmd(&capturedDeps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"inspect",
	})

	require.NoError(t, root.Execute())
	require.NotNil(t, capturedDeps)
	require.Nil(t, capturedDeps.Ctx, "Ctx must stay nil for noClient commands")
}

// ---------------------------------------------------------------------------
// Execute() stderr suppression for *exec.ExitError (child-exit passthrough)
// ---------------------------------------------------------------------------

// captureStderr temporarily redirects the process-wide os.Stderr to a pipe for
// the duration of fn, and returns everything written to it. It restores
// os.Stderr unconditionally, even if fn panics.
//
// This mutates process-wide state, so it is safe only because no test in this
// package runs in parallel (no t.Parallel calls) — see CLAUDE.md conventions.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = orig
	}()

	fn()

	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, copyErr := io.Copy(&buf, r)
	require.NoError(t, copyErr)
	require.NoError(t, r.Close())

	return buf.String()
}

// TestExecute_ExitError_SuppressesStderr verifies that Execute does NOT print
// a redundant second diagnostic line when the returned error chain contains
// an *exec.ExitError: the child process (ssh, rsync) already wrote its own
// diagnostics, so pmx must not add its own.
func TestExecute_ExitError_SuppressesStderr(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	factory := func(_ *cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(_ *cobra.Command, _ []string) error {
				return &exec.ExitError{Code: 42, Err: errors.New("exit status 42")}
			},
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(t.TempDir(), "c.yml"), "probe"}
	defer func() { os.Args = oldArgs }()

	var execErr error
	stderrOut := captureStderr(t, func() {
		execErr = cli.Execute("pmx", []cli.GroupFactory{factory})
	})

	require.Error(t, execErr, "the child exit code must still be returned as an error")
	var exitErr *exec.ExitError
	require.ErrorAs(t, execErr, &exitErr)
	require.Equal(t, 42, exitErr.Code)
	require.Empty(t, stderrOut,
		"Execute must not print a redundant diagnostic line for an *exec.ExitError: "+
			"the child process already wrote its own")
}

// TestExecute_NonExitError_StillPrintsStderr guards the other side of the
// suppression logic: any error that is NOT an *exec.ExitError must still be
// printed to stderr exactly as before.
func TestExecute_NonExitError_StillPrintsStderr(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	const sentinel = "deliberate-non-exit-error-sentinel"
	factory := func(_ *cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(_ *cobra.Command, _ []string) error {
				return errors.New(sentinel)
			},
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(t.TempDir(), "c.yml"), "probe"}
	defer func() { os.Args = oldArgs }()

	var execErr error
	stderrOut := captureStderr(t, func() {
		execErr = cli.Execute("pmx", []cli.GroupFactory{factory})
	})

	require.Error(t, execErr)
	require.Contains(t, stderrOut, sentinel,
		"a non-exec error must still be printed to stderr exactly as before")
}

// TestExecute_CapturedGuestSSHExitErrorIsPrinted covers the live pve-cpi-az2
// finding: internal/cli/lab.runGuestSSH wires ssh's stdout/stderr to its own
// in-memory buffers (never the real terminal), so unlike the pass-through
// case TestExecute_ExitError_SuppressesStderr covers, nothing has been shown
// to the user by the time its wrapped *exec.ExitError reaches Execute — an
// error whose chain contains *exec.ExitError AND exec.CapturedError must
// still be printed in full (including whatever captured child output the
// caller folded into its message), not silently swallowed by the same
// "the child already printed this" assumption that correctly suppresses a
// pass-through *exec.ExitError.
func TestExecute_CapturedGuestSSHExitErrorIsPrinted(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	const capturedStderrText = "Received disconnect from 10.255.0.10 port 22:2: Too many authentication failures"
	factory := func(_ *cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(_ *cobra.Command, _ []string) error {
				exitErr := &exec.ExitError{Code: 255, Err: errors.New("exit status 255")}
				wrapped := fmt.Errorf("ssh root@10.255.0.11 %q: %w (stderr: %s)", "pvecm apiver", exitErr, capturedStderrText)
				return exec.NewCapturedError(wrapped)
			},
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(t.TempDir(), "c.yml"), "probe"}
	defer func() { os.Args = oldArgs }()

	var execErr error
	stderrOut := captureStderr(t, func() {
		execErr = cli.Execute("pmx", []cli.GroupFactory{factory})
	})

	require.Error(t, execErr, "the child exit code must still be returned as an error")
	var exitErr *exec.ExitError
	require.ErrorAs(t, execErr, &exitErr, "the underlying *exec.ExitError must still be reachable through the CapturedError wrapper")
	require.Equal(t, 255, exitErr.Code)
	require.NotEmpty(t, stderrOut,
		"a CapturedError-wrapped *exec.ExitError must be printed — its captured output was never shown any other way")
	require.Contains(t, stderrOut, capturedStderrText,
		"the captured stderr the caller folded into its error message must reach the user")
}

// TestExecute_CapturedDatasetEnsureExitErrorIsPrinted covers the
// internal/cli/lab.createZfsDatasetEnsure/createZfsDatasetExists fix
// (R1-PMXCLI MAJOR-1): both helpers capture ssh's stdout/stderr into their
// own in-memory buffers (never the real terminal) while probing/creating a
// lab's ZFS dataset, so — exactly like runGuestSSH's captured ssh calls —
// nothing has been shown to the user by the time their wrapped
// *exec.ExitError reaches Execute. Before the fix, both returned a plain
// fmt.Errorf-wrapped *exec.ExitError with no exec.CapturedError marker, so
// `pmx lab create`/`pmx lab scale` exited non-zero with a blank terminal on
// any ssh transport failure or "zfs create" failure. This test reproduces
// the exact error shape createZfsDatasetEnsure now returns (message format
// and exec.NewCapturedError wrapping) and asserts Execute prints it in
// full, mirroring TestExecute_CapturedGuestSSHExitErrorIsPrinted.
func TestExecute_CapturedDatasetEnsureExitErrorIsPrinted(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	const capturedStderrText = "ssh: connect to host 192.168.1.180 port 22: Connection refused"
	factory := func(_ *cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(_ *cobra.Command, _ []string) error {
				exitErr := &exec.ExitError{Code: 255, Err: errors.New("exit status 255")}
				wrapped := fmt.Errorf(
					"create zfs dataset %q via ssh %s@%s (exit %d): %w (stderr: %s)",
					"tank/labs/wayne", "root", "192.168.1.180", 255, exitErr, capturedStderrText)
				return exec.NewCapturedError(wrapped)
			},
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(t.TempDir(), "c.yml"), "probe"}
	defer func() { os.Args = oldArgs }()

	var execErr error
	stderrOut := captureStderr(t, func() {
		execErr = cli.Execute("pmx", []cli.GroupFactory{factory})
	})

	require.Error(t, execErr, "the child exit code must still be returned as an error")
	var exitErr *exec.ExitError
	require.ErrorAs(t, execErr, &exitErr, "the underlying *exec.ExitError must still be reachable through the CapturedError wrapper")
	require.Equal(t, 255, exitErr.Code)
	require.NotEmpty(t, stderrOut,
		"a CapturedError-wrapped dataset-ensure *exec.ExitError must be printed — its captured output was never shown any other way")
	require.Contains(t, stderrOut, capturedStderrText,
		"the captured ssh stderr createZfsDatasetEnsure folded into its error message must reach the user")
	require.Contains(t, stderrOut, "tank/labs/wayne",
		"the dataset path createZfsDatasetEnsure names must reach the user")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildNoopCmd returns a cobra.Command whose RunE sets *called = true.
func buildNoopCmd(called *bool) *cobra.Command {
	return &cobra.Command{
		Use: "noop",
		RunE: func(cmd *cobra.Command, _ []string) error {
			*called = true
			return nil
		},
	}
}

// buildInspectCmd returns a cobra.Command that stores the Deps from context.
func buildInspectCmd(deps **cli.Deps) *cobra.Command {
	return &cobra.Command{
		Use: "inspect",
		RunE: func(cmd *cobra.Command, _ []string) error {
			*deps = cli.GetDeps(cmd)
			return nil
		},
	}
}

// TestLogCloser_RunERecordsSurvive_F01 is the regression test for F-01.
//
// It verifies that log records emitted during RunE are present in the JSONL
// log file after Execute returns. Before the fix, defer logCloser.Close() fired
// when PersistentPreRunE returned — before RunE ran — so every RunE-time record
// was silently lost (EBADF on the closed fd).
//
// Strategy:
//   - Redirect HOME to a temp dir so logx.Init writes the log file there.
//   - Wire a noClient command that emits a distinctive log record in RunE.
//   - Execute via root.Execute() (not cli.Execute(), to keep test isolation).
//   - After Execute returns, read every *.jsonl file under the temp log dir.
//   - Assert the distinctive message appears in at least one record.
func TestLogCloser_RunERecordsSurvive_F01(t *testing.T) {
	tmpDir := t.TempDir()
	// Redirect HOME so logx writes ~/.pmx/logs under tmpDir.
	t.Setenv("HOME", tmpDir)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	// Empty config is fine; noClient command bypasses context resolution.
	cfgPath := filepath.Join(tmpDir, "config.yml")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	const sentinel = "f01-rune-sentinel-record"

	probe := &cobra.Command{
		Use:         "probe",
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			// Emit a log record during RunE. This is what was silently lost before
			// the fix, because the log file fd was closed when PreRunE returned.
			deps.Log.Info(sentinel)
			return nil
		},
	}
	root.AddCommand(probe)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", cfgPath, "probe"})

	require.NoError(t, root.Execute())

	// cleanup() is deferred above — it closes the log file fd. Read the log dir
	// after root.Execute() but before t.Cleanup flushes the defer (defer in test
	// runs at function end, but we need to call cleanup() early to flush the
	// write buffer before reading the file).
	//
	// In practice slog.JSONHandler writes synchronously (no buffering beyond the
	// OS page cache), so the record is visible before Close(). We call cleanup
	// explicitly here to guarantee the fd is flushed on all platforms, then reset
	// the deferred call to a no-op via the already-closed state (Close on a closed
	// *os.File returns error; the nolint:errcheck suppresses it in production code).
	// The defer above is still safe: noopLogCloser.Close() is idempotent.

	// Default layout is nested: the probe command's log lands under
	// ~/.pmx/logs/probe/{ts}.jsonl, so walk the tree rather than reading a
	// single flat directory.
	logDir := filepath.Join(tmpDir, ".pmx", "logs")
	var found bool
	walkErr := filepath.WalkDir(logDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(data, []byte(sentinel)) {
			found = true
		}
		return nil
	})
	require.NoError(t, walkErr, "log directory must exist after Execute")
	require.True(t, found,
		"sentinel log record emitted during RunE must be present in the JSONL log file after Execute returns; "+
			"if missing, the log closer fired before RunE (F-01 regression)")
}

// TestLogLayout_NestedDefaultAndFlatOverride verifies the log destination
// layout wiring: nested per-command directories by default, flat filenames
// when PMX_LOG_LAYOUT=flat.
func TestLogLayout_NestedDefaultAndFlatOverride(t *testing.T) {
	run := func(t *testing.T, layoutEnv string) string {
		t.Helper()
		tmpDir := t.TempDir()
		t.Setenv("HOME", tmpDir)
		t.Setenv("PMX_OUTPUT", "table")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")
		t.Setenv("PMX_LOG_LAYOUT", layoutEnv)

		root, cleanup := cli.NewRootCmd("pmx")
		defer cleanup()
		root.SetContext(context.Background())

		probe := &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE:        func(*cobra.Command, []string) error { return nil },
		}
		root.AddCommand(probe)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", filepath.Join(tmpDir, "config.yml"), "probe"})
		require.NoError(t, root.Execute())

		return filepath.Join(tmpDir, ".pmx", "logs")
	}

	t.Run("nested default", func(t *testing.T) {
		logDir := run(t, "")
		entries, err := os.ReadDir(filepath.Join(logDir, "probe"))
		require.NoError(t, err, "nested layout must create a per-command subdirectory")
		require.NotEmpty(t, entries)
		require.Regexp(t, `^\d{8}-\d{6}\.jsonl$`, entries[0].Name(),
			"nested filenames must be bare timestamps")
	})

	t.Run("flat via PMX_LOG_LAYOUT", func(t *testing.T) {
		logDir := run(t, "flat")
		entries, err := os.ReadDir(logDir)
		require.NoError(t, err)
		require.NotEmpty(t, entries)
		for _, e := range entries {
			require.False(t, e.IsDir(), "flat layout must not create subdirectories")
		}
		require.Regexp(t, `^probe-\d{8}-\d{6}\.jsonl$`, entries[0].Name())
	})
}

// readLogRecords parses every JSONL record found under logDir into maps.
// TestShellCompletion_WritesNoLogFile covers the log-tree growth an
// interactive shell caused on its own: cobra dispatches its hidden
// "__complete" command through the same PersistentPreRunE as a real command,
// so every tab press opened a JSONL file under a "__complete" directory and
// wrote an invocation record into it. A keystroke mutates nothing and answers
// no audit question, so it must leave the log tree untouched.
func TestShellCompletion_WritesNoLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	completed := &cobra.Command{
		Use:         "widget",
		Annotations: map[string]string{"noClient": "true"},
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return []string{"alpha", "beta"}, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	}
	root.AddCommand(completed)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		cobra.ShellCompRequestCmd, "widget", "",
	})

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "alpha", "completion output must still be produced")

	// The log directory is not merely empty — it is never created, since no
	// log file is opened at all.
	logDir := filepath.Join(tmpDir, ".pmx", "logs")
	_, statErr := os.Stat(logDir)
	require.ErrorIs(t, statErr, fs.ErrNotExist,
		"a shell-completion request must not open a log file")
}

func readLogRecords(t *testing.T, logDir string) []map[string]any {
	t.Helper()
	var records []map[string]any
	walkErr := filepath.WalkDir(logDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for line := range bytes.SplitSeq(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var rec map[string]any
			require.NoError(t, json.Unmarshal(line, &rec), "log line must be valid JSON: %s", line)
			records = append(records, rec)
		}
		return nil
	})
	require.NoError(t, walkErr)
	return records
}

// findRecord returns the first record whose msg equals want, or nil.
func findRecord(records []map[string]any, want string) map[string]any {
	for _, r := range records {
		if r["msg"] == want {
			return r
		}
	}
	return nil
}

// TestInvocationAuditRecords verifies that every invocation writes an
// "invocation" record at open (command path, redacted args, context,
// version) and that cli.Execute writes the matching "exit" record
// (exit_code, duration_ms) — so no log file is ever empty, even for
// commands that make no API calls.
func TestInvocationAuditRecords(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_LOG_LAYOUT", "")
	t.Setenv("PMX_LOG_LEVEL", "")

	factory := func(*cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			Args:        cobra.ArbitraryArgs,
			RunE:        func(*cobra.Command, []string) error { return nil },
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(tmpDir, "c.yml"),
		"probe", "vm-101", "password=supersecret"}
	defer func() { os.Args = oldArgs }()

	require.NoError(t, cli.Execute("pmx", []cli.GroupFactory{factory}))

	records := readLogRecords(t, filepath.Join(tmpDir, ".pmx", "logs"))
	require.NotEmpty(t, records, "log file must never be empty")

	inv := findRecord(records, "invocation")
	require.NotNil(t, inv, "invocation record must open the log")
	require.Equal(t, "pmx probe", inv["command_path"])
	require.Contains(t, inv, "version")

	args, ok := inv["args"].([]any)
	require.True(t, ok, "invocation record must carry args")
	require.Contains(t, args, "vm-101")
	require.Contains(t, args, "password=***", "sensitive key=value args must be masked")
	for _, a := range args {
		require.NotContains(t, a.(string), "supersecret", "secret value must never reach the log")
	}

	exit := findRecord(records, "exit")
	require.NotNil(t, exit, "exit record must close the audit trail")
	require.Equal(t, float64(0), exit["exit_code"])
	require.Contains(t, exit, "duration_ms")
}

// TestInvocationAuditRecord_NamesTheTarget covers what the audit trail is for.
// The context name alone does not say which machine a mutation reached:
// contexts get renamed, repointed at another host, and copied between
// workstations, so a log read months later cannot resolve one back to a
// target. The record must name the host, product, and authenticated user —
// and must still never carry the secret.
func TestInvocationAuditRecord_NamesTheTarget(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_LOG_LAYOUT", "")
	t.Setenv("PMX_LOG_LEVEL", "")

	const secret = "tok-must-never-be-logged"
	cfgPath := filepath.Join(tmpDir, "c.yml")
	require.NoError(t, config.SaveForce(cfgPath, &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]*config.Context{
			"prod": {
				Host:    "pve1.example.test",
				Port:    8006,
				Product: "pve",
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "auditor@pve",
					Secret:   secret,
				},
			},
		},
	}))

	factory := func(*cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			Args:        cobra.ArbitraryArgs,
			RunE:        func(*cobra.Command, []string) error { return nil },
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", cfgPath, "probe"}
	defer func() { os.Args = oldArgs }()

	require.NoError(t, cli.Execute("pmx", []cli.GroupFactory{factory}))

	records := readLogRecords(t, filepath.Join(tmpDir, ".pmx", "logs"))
	inv := findRecord(records, "invocation")
	require.NotNil(t, inv)
	require.Equal(t, "prod", inv["context"])
	require.Equal(t, "pve1.example.test", inv["host"])
	require.Equal(t, float64(8006), inv["port"])
	require.Equal(t, "pve", inv["product"])
	require.Equal(t, "auditor@pve", inv["user"])

	for _, rec := range records {
		raw, err := json.Marshal(rec)
		require.NoError(t, err)
		require.NotContains(t, string(raw), secret, "the secret must never reach any log record")
	}
}

// TestInvocationExitRecord_Error verifies a failing command writes the exit
// record at error level with the semantic exit code and the error text.
func TestInvocationExitRecord_Error(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_LOG_LEVEL", "error")

	factory := func(*cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(*cobra.Command, []string) error {
				return errors.New("probe boom")
			},
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(tmpDir, "c.yml"), "probe"}
	defer func() { os.Args = oldArgs }()

	require.Error(t, cli.Execute("pmx", []cli.GroupFactory{factory}))

	records := readLogRecords(t, filepath.Join(tmpDir, ".pmx", "logs"))
	exit := findRecord(records, "exit")
	require.NotNil(t, exit,
		"exit record must be written at error level so it survives log.level=error")
	require.Equal(t, "ERROR", exit["level"])
	require.Equal(t, float64(1), exit["exit_code"])
	require.Contains(t, exit["error"], "probe boom")
}

// TestRedactArgs covers the sensitive key=value masking table.
func TestRedactArgs(t *testing.T) {
	got := cli.RedactArgs([]string{
		"vm-101",
		"password=hunter2",
		"new-secret=abc",
		"API-Token=xyz",
		"ticket=PVE:...",
		"CSRFPreventionToken=tok",
		"comment=plain",
		"noequals",
	})
	require.Equal(t, []string{
		"vm-101",
		"password=***",
		"new-secret=***",
		"API-Token=***",
		"ticket=***",
		"CSRFPreventionToken=***",
		"comment=plain",
		"noequals",
	}, got)

	require.Nil(t, cli.RedactArgs(nil))
}

// TestInvocationArgs_PassthroughRedaction verifies that commands carrying a
// foreign command line — the passthroughArgs annotation (ssh/exec wrappers)
// or DisableFlagParsing (rsync) — never get their argv logged verbatim: the
// invocation record carries only a count placeholder, since inline secrets
// like `mysql -pSECRET` are invisible to key=value masking.
func TestInvocationArgs_PassthroughRedaction(t *testing.T) {
	annotated := &cobra.Command{
		Use:         "ssh",
		Annotations: map[string]string{cli.AnnotationPassthroughArgs: "true"},
	}
	require.Equal(t, []string{"(3 passthrough args redacted)"},
		cli.InvocationArgs(annotated, []string{"pve1", "mysql", "-pSECRET"}))

	flagless := &cobra.Command{Use: "rsync", DisableFlagParsing: true}
	require.Equal(t, []string{"(2 passthrough args redacted)"},
		cli.InvocationArgs(flagless, []string{"-av", "pve1:/etc/pve/"}))

	plain := &cobra.Command{Use: "start"}
	require.Equal(t, []string{"vm-101", "password=***"},
		cli.InvocationArgs(plain, []string{"vm-101", "password=hunter2"}))
}

// TestInvocationExitRecord_ClientBuildFailure guards the paired-audit
// invariant on the pre-RunE error path: when context resolution / client
// construction fails in PersistentPreRunE (expired token, unreachable host,
// no context at all), the log must still close with an exit record — deps
// are stashed before the client build precisely so Execute's exit hook can
// find the logger.
func TestInvocationExitRecord_ClientBuildFailure(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_LOG_LEVEL", "")

	// No noClient annotation: PersistentPreRunE attempts the client build,
	// which fails against an empty config with no context configured.
	factory := func(*cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:  "probe",
			RunE: func(*cobra.Command, []string) error { return nil },
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(tmpDir, "c.yml"), "probe"}
	defer func() { os.Args = oldArgs }()

	require.Error(t, cli.Execute("pmx", []cli.GroupFactory{factory}))

	records := readLogRecords(t, filepath.Join(tmpDir, ".pmx", "logs"))
	require.NotNil(t, findRecord(records, "invocation"))

	exit := findRecord(records, "exit")
	require.NotNil(t, exit,
		"a pre-RunE failure (client build) must still write the exit record")
	require.Equal(t, "ERROR", exit["level"])
	require.NotEqual(t, float64(0), exit["exit_code"])
	require.NotEmpty(t, exit["error"])
}

// TestAutoPrune_RunsWithRetentionConfigured verifies the post-command daily
// prune fires when log.retention is set: an aged log file disappears and the
// sentinel is stamped.
func TestAutoPrune_RunsWithRetentionConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	logDir := filepath.Join(tmpDir, ".pmx", "logs")
	old := filepath.Join(logDir, "pve", "task", "ls", "20250101-000000.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o700))
	require.NoError(t, os.WriteFile(old, []byte(`{"msg":"old"}`+"\n"), 0o600))
	stale := time.Now().Add(-90 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(old, stale, stale))

	cfgPath := filepath.Join(tmpDir, "c.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("log:\n  retention: 30\n"), 0o600))

	factory := func(*cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE:        func(*cobra.Command, []string) error { return nil },
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", cfgPath, "probe"}
	defer func() { os.Args = oldArgs }()

	require.NoError(t, cli.Execute("pmx", []cli.GroupFactory{factory}))

	require.NoFileExists(t, old, "aged log must be auto-pruned when log.retention is set")
	require.FileExists(t, filepath.Join(logDir, ".last-prune"))

	// This invocation's own log survives (fresh and non-empty).
	records := readLogRecords(t, logDir)
	require.NotNil(t, findRecord(records, "invocation"))
}

// ---------------------------------------------------------------------------
// ApplyTOFUOptions (IMP-02b — per-context opt-in TOFU)
// ---------------------------------------------------------------------------

// alwaysTTY and neverTTY are fixed isTTY funcs for ApplyTOFUOptions tests;
// they are never actually invoked because gating happens before the callback
// is built (tofu disabled or insecure) or the callback is only invoked by a
// real certificate-verification handshake, which these tests do not perform.
func alwaysTTY() bool { return true }

func TestApplyTOFUOptions_TofuDisabled_OptionsUnchanged(t *testing.T) {
	base := pve.Options{Host: "pve.example.com"}
	var promptOut bytes.Buffer

	got := cli.ApplyTOFUOptions(base, false, false, "/home/user/.config/pmx/config.yml", "prod",
		&promptOut, strings.NewReader(""), alwaysTTY)

	require.Empty(t, got.FingerprintCachePath,
		"tofu=false must leave FingerprintCachePath empty")
	require.Nil(t, got.ManualVerifyCallback,
		"tofu=false must leave ManualVerifyCallback nil")
	require.Equal(t, base.Host, got.Host, "unrelated Options fields must be preserved")
}

func TestApplyTOFUOptions_TofuEnabled_WiresFingerprintPinning(t *testing.T) {
	base := pve.Options{Host: "pve.example.com"}
	var promptOut bytes.Buffer

	got := cli.ApplyTOFUOptions(base, true, false, "/home/user/.config/pmx/config.yml", "prod",
		&promptOut, strings.NewReader(""), alwaysTTY)

	require.Equal(t, "/home/user/.config/pmx/fingerprints/prod.json", got.FingerprintCachePath,
		"tofu=true must set the per-context fingerprint cache path")
	require.NotNil(t, got.ManualVerifyCallback,
		"tofu=true must install the manual-verify callback")
}

func TestApplyTOFUOptions_TofuEnabledButInsecure_OptionsUnchanged(t *testing.T) {
	base := pve.Options{Host: "pve.example.com"}
	var promptOut bytes.Buffer

	got := cli.ApplyTOFUOptions(base, true, true, "/home/user/.config/pmx/config.yml", "prod",
		&promptOut, strings.NewReader(""), alwaysTTY)

	require.Empty(t, got.FingerprintCachePath,
		"--insecure must suppress TOFU wiring even when tofu=true, so it never re-imposes "+
			"a trust decision the operator explicitly opted out of")
	require.Nil(t, got.ManualVerifyCallback)
}

func TestApplyTOFUOptions_DifferentContexts_DistinctCachePaths(t *testing.T) {
	base := pve.Options{Host: "pve.example.com"}
	var promptOut bytes.Buffer

	prod := cli.ApplyTOFUOptions(base, true, false, "/home/user/.config/pmx/config.yml", "prod",
		&promptOut, strings.NewReader(""), alwaysTTY)
	staging := cli.ApplyTOFUOptions(base, true, false, "/home/user/.config/pmx/config.yml", "staging",
		&promptOut, strings.NewReader(""), alwaysTTY)

	require.NotEqual(t, prod.FingerprintCachePath, staging.FingerprintCachePath,
		"each context must persist trust decisions to its own cache file")
}

// TestVersionFlag_PrintsBuildInfo verifies that `pmx --version` prints the
// full build-info line from internal/version and exits without running
// PersistentPreRunE (no config load, no API client construction).
func TestVersionFlag_PrintsBuildInfo(t *testing.T) {
	// Point --config at a nonexistent path: if PersistentPreRunE ran, it
	// would be exercised with this config; --version must not need it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--version"})

	require.NoError(t, root.Execute())
	require.Equal(t, version.String()+"\n", out.String(),
		"--version must print exactly the internal/version build-info line")
	require.Contains(t, out.String(), version.Version)
	require.Contains(t, out.String(), version.Commit)
}

// TestVersionFlag_ShortV verifies the -v shorthand maps to --version and is
// not shadowed by any other persistent flag.
func TestVersionFlag_ShortV(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"-v"})

	require.NoError(t, root.Execute())
	require.Contains(t, out.String(), "pmx version")

	vFlag := root.Flags().Lookup("version")
	require.NotNil(t, vFlag, "--version flag must exist")
	require.Equal(t, "v", vFlag.Shorthand, "--version shorthand must be -v")
}

// newThreeContextConfig builds a *config.Config with three token-auth
// contexts — "pve1" (product pve), "pbs1" (product pbs), and "pdm1" (product
// pdm) — so BuildContextAnyClient tests can select a client by product
// without any network or keychain access (Auth.Secret is a literal, resolved
// with no external lookup).
func newThreeContextConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		CurrentContext: "pve1",
		Contexts: map[string]*config.Context{
			"pve1": {
				Host: "10.0.0.1", Port: 8006, Protocol: "https", Realm: "pam",
				Product: config.ProductPVE,
				Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s1"},
			},
			"pbs1": {
				Host: "10.0.0.2", Port: 8007, Protocol: "https", Realm: "pam",
				Product: config.ProductPBS,
				Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s2"},
			},
			"pdm1": {
				Host: "10.0.0.3", Port: 8443, Protocol: "https", Realm: "pam",
				Product: config.ProductPDM,
				Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s3"},
			},
		},
	}
}

// TestBuildContextAnyClient_ReturnsExactlyOneClient verifies that
// BuildContextAnyClient resolves the requested context and builds exactly
// the client matching that context's product — one non-nil Clients field per
// product — with no cross-product guard (unlike BuildContextClient /
// BuildContextPBSClient / BuildContextPDMClient, which each reject every
// other product).
func TestBuildContextAnyClient_ReturnsExactlyOneClient(t *testing.T) {
	cfg := newThreeContextConfig(t)

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()

	cases := []struct {
		contextName string
		product     string
	}{
		{"pve1", config.ProductPVE},
		{"pbs1", config.ProductPBS},
		{"pdm1", config.ProductPDM},
	}

	for _, tc := range cases {
		t.Run(tc.contextName, func(t *testing.T) {
			clients, ctx, err := cli.BuildContextAnyClient(root, cfg, "", tc.contextName, false, func() bool { return false })
			require.NoError(t, err)
			require.Equal(t, tc.product, ctx.Product)

			nonNil := 0
			if clients.API != nil {
				nonNil++
			}
			if clients.PBS != nil {
				nonNil++
			}
			if clients.PDM != nil {
				nonNil++
			}
			require.Equal(t, 1, nonNil, "exactly one Clients field must be non-nil for product %q", tc.product)

			switch tc.product {
			case config.ProductPVE:
				require.NotNil(t, clients.API)
			case config.ProductPBS:
				require.NotNil(t, clients.PBS)
			case config.ProductPDM:
				require.NotNil(t, clients.PDM)
			}
		})
	}
}

// TestBuildContextAnyClient_UnknownProductFailsLoudly verifies that
// BuildContextAnyClient rejects a context whose product is not one of the
// three known products, rather than silently falling back to a PVE client
// (see the Global Constraints "no silent PVE fallthrough" rule).
func TestBuildContextAnyClient_UnknownProductFailsLoudly(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "bogus1",
		Contexts: map[string]*config.Context{
			"bogus1": {
				Host: "10.0.0.9", Port: 1, Protocol: "https", Realm: "pam",
				Product: "bogus",
				Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
			},
		},
	}

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()

	clients, ctx, err := cli.BuildContextAnyClient(root, cfg, "", "bogus1", false, func() bool { return false })
	require.Error(t, err)
	require.Contains(t, err.Error(), `unsupported product "bogus"`)
	require.Nil(t, ctx)
	require.Zero(t, clients)
}

// TestPersona verifies that Persona maps an invocation name (os.Args[0]) to
// its command surface: "pve", "pbs", and "pdm" each select that product's
// hoisted tree; every other name (including "pmx", `go run`/`go test` temp
// binary names, and the empty string) falls back to the full "pmx" tree.
func TestPersona(t *testing.T) {
	cases := map[string]string{
		"pve": "pve", "pbs": "pbs", "pdm": "pdm", "pmx": "pmx",
		"/usr/local/bin/pve": "pve", "pbs.exe": "pbs",
		"/usr/local/bin/pdm": "pdm", "pdm.exe": "pdm",
		"pmx.exe": "pmx", "go_build_x": "pmx", "": "pmx",
	}
	for in, want := range cases {
		require.Equal(t, want, cli.Persona(in), "Persona(%q)", in)
	}
}

// TestNewRootCmd_PersonaSetsUseAndAnnotation verifies that NewRootCmd sets
// root.Use to the persona name and, for "pbs", "pdm" (and by symmetry
// "pve"), tags the root with ProductAnnotation so requiredProduct resolves
// correctly for commands hoisted directly onto the root.
func TestNewRootCmd_PersonaSetsUseAndAnnotation(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pbs")
	defer cleanup()
	require.Equal(t, "pbs", root.Use)
	require.Equal(t, config.ProductPBS, root.Annotations[cli.ProductAnnotation])

	root, cleanup = cli.NewRootCmd("pdm")
	defer cleanup()
	require.Equal(t, "pdm", root.Use)
	require.Equal(t, config.ProductPDM, root.Annotations[cli.ProductAnnotation])

	root, cleanup = cli.NewRootCmd("pmx")
	defer cleanup()
	require.Equal(t, "pmx", root.Use)
	require.Empty(t, root.Annotations[cli.ProductAnnotation])
}

// TestRequiredProduct_PDMAnnotationChain verifies that requiredProduct walks
// up the parent chain to resolve config.ProductPDM from a "pdm"-annotated
// group's unannotated child — the same inheritance mechanism already proven
// for PBS by TestHoistedPBSChildrenRequirePBSProduct.
func TestRequiredProduct_PDMAnnotationChain(t *testing.T) {
	group := &cobra.Command{
		Use:         "pdmgroup",
		Annotations: map[string]string{cli.ProductAnnotation: config.ProductPDM},
	}
	child := &cobra.Command{Use: "child"}
	group.AddCommand(child)

	require.Equal(t, config.ProductPDM, cli.RequiredProduct(child),
		"a command with no annotation of its own must inherit product from its nearest annotated ancestor")
}

// TestNewRootCmd_PersonaDescribesActiveProduct verifies that the root
// command's Short/Long text describes the persona actually invoked, rather
// than always describing the combined "pmx" tree: the "pve" persona must
// read as a Proxmox VE CLI, the "pbs" persona as a Proxmox Backup Server
// CLI, the "pdm" persona as a Proxmox Datacenter Manager CLI, and the
// default "pmx" persona must keep its existing combined description
// unchanged. Each product persona's Long text must also point back at the
// `pmx` binary for the full combined tree.
func TestNewRootCmd_PersonaDescribesActiveProduct(t *testing.T) {
	pmxRoot, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	pveRoot, cleanup := cli.NewRootCmd("pve")
	defer cleanup()
	pbsRoot, cleanup := cli.NewRootCmd("pbs")
	defer cleanup()
	pdmRoot, cleanup := cli.NewRootCmd("pdm")
	defer cleanup()

	require.Equal(t, "pmx: Proxmox CLI", pmxRoot.Short,
		"the default pmx persona's Short text must be unchanged")

	require.Contains(t, pbsRoot.Short, "Backup Server",
		"the pbs persona's Short text must describe Proxmox Backup Server")
	require.NotContains(t, pveRoot.Short, "Backup Server",
		"the pve persona's Short text must not mention Backup Server")

	require.Contains(t, pveRoot.Short, "Proxmox VE",
		"the pve persona's Short text must describe Proxmox VE")
	require.NotContains(t, pbsRoot.Short, "Proxmox VE",
		"the pbs persona's Short text must not mention Proxmox VE")

	require.Contains(t, pdmRoot.Short, "Datacenter Manager",
		"the pdm persona's Short text must describe Proxmox Datacenter Manager")
	require.NotContains(t, pveRoot.Short, "Datacenter Manager")
	require.NotContains(t, pbsRoot.Short, "Datacenter Manager")

	require.NotEqual(t, pveRoot.Short, pbsRoot.Short,
		"pve and pbs personas must have distinct Short text")
	require.NotEqual(t, pmxRoot.Short, pveRoot.Short)
	require.NotEqual(t, pmxRoot.Short, pbsRoot.Short)
	require.NotEqual(t, pmxRoot.Short, pdmRoot.Short)
	require.NotEqual(t, pveRoot.Short, pdmRoot.Short)
	require.NotEqual(t, pbsRoot.Short, pdmRoot.Short)

	require.Contains(t, pveRoot.Long, "pmx",
		"the pve persona's Long text must mention the pmx binary for the full combined tree")
	require.Contains(t, pbsRoot.Long, "pmx",
		"the pbs persona's Long text must mention the pmx binary for the full combined tree")
	require.Contains(t, pdmRoot.Long, "pmx",
		"the pdm persona's Long text must mention the pmx binary for the full combined tree")
}

// TestHoistedPBSChildrenRequirePBSProduct verifies that a "pbs" persona root
// tags itself with ProductAnnotation (see NewRootCmd) so that a PBS resource
// command hoisted directly onto the root by pbs.ChildFactories() — which sets
// no annotation of its own — still resolves to config.ProductPBS by walking
// up to the persona root.
func TestHoistedPBSChildrenRequirePBSProduct(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pbs")
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{}, pbs.ChildFactories())

	ds, _, err := root.Find([]string{"datastore"})
	require.NoError(t, err)
	require.Equal(t, config.ProductPBS, cli.RequiredProduct(ds),
		"hoisted pbs child must resolve to pbs via the persona root annotation")
}

// TestHoistedPVEChildrenRequirePVEProduct is the PVE symmetric case of
// TestHoistedPBSChildrenRequirePBSProduct.
func TestHoistedPVEChildrenRequirePVEProduct(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pve")
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{}, pvegroup.ChildFactories())

	node, _, err := root.Find([]string{"node"})
	require.NoError(t, err)
	require.Equal(t, config.ProductPVE, cli.RequiredProduct(node))
}

// TestHoistedPDMChildrenRequirePDMProduct is the PDM symmetric case of
// TestHoistedPBSChildrenRequirePBSProduct / TestHoistedPVEChildrenRequirePVEProduct:
// a pdm.ChildFactories() group hoisted directly onto the "pdm" persona root
// — which sets no annotation of its own — still resolves to config.ProductPDM
// by walking up to the persona root annotation. "remote" is a pdm-native
// group (not a proxied pve/pbs one), so this also confirms the annotation
// chain works for pdm's own commands, not just its proxied pve/pbs subtrees.
func TestHoistedPDMChildrenRequirePDMProduct(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pdm")
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{}, pdm.ChildFactories())

	remote, _, err := root.Find([]string{"remote"})
	require.NoError(t, err)
	require.Equal(t, config.ProductPDM, cli.RequiredProduct(remote),
		"hoisted pdm-native child must resolve to pdm via the persona root annotation")

	// The proxied "pve"/"pbs" groups (and their nested children) must also
	// resolve to pdm — they are PDM API calls (proxying to a managed
	// remote), not PVE/PBS-product calls, so they must NOT inherit
	// config.ProductPVE/ProductPBS from anywhere.
	pveProxy, _, err := root.Find([]string{"pve", "qemu"})
	require.NoError(t, err)
	require.Equal(t, config.ProductPDM, cli.RequiredProduct(pveProxy),
		"pdm's proxied pve subtree must resolve to pdm, not pve, since it is a PDM API call")

	pbsProxy, _, err := root.Find([]string{"pbs", "datastore"})
	require.NoError(t, err)
	require.Equal(t, config.ProductPDM, cli.RequiredProduct(pbsProxy),
		"pdm's proxied pbs subtree must resolve to pdm, not pbs, since it is a PDM API call")
}

// TestAuthWhoamiResolvesByContextUnderEveryPersona verifies that `auth
// whoami` sets its own ProductAnnotation = cli.ProductFromContext (see
// newAuthWhoamiCmd), so it always resolves against whichever product the
// active *context* targets (PVE, PBS, or PDM) rather than inheriting the
// persona root's own product tag (e.g. the "pbs" persona root tags itself
// config.ProductPBS — see NewRootCmd). This holds under every persona,
// including "pbs", since whoami now supports PVE, PBS, and PDM contexts
// alike via deps.API/deps.PBS/deps.PDM selection in its RunE.
func TestAuthWhoamiResolvesByContextUnderEveryPersona(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pbs")
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{api.Auth})

	whoami, _, err := root.Find([]string{"auth", "whoami"})
	require.NoError(t, err)
	require.Equal(t, cli.ProductFromContext, cli.RequiredProduct(whoami),
		"auth whoami must resolve by the active context's own product under every persona, "+
			"including pbs, rather than inheriting the persona root's product tag")
}

// TestRequireSubcommands_GroupingCommandsNeedNoClient covers a help path that
// failed on credentials. RequireSubcommands makes a grouping command runnable
// so a stray positional exits non-zero, but that also subjected it to client
// construction: a bare `pmx pve` or `pmx context` resolved the context secret
// and shelled out to the keychain just to print help, and reported a
// credential error instead of helping when the entry was missing.
func TestRequireSubcommands_GroupingCommandsNeedNoClient(t *testing.T) {
	root := &cobra.Command{Use: "pmx"}
	group := &cobra.Command{Use: "grp", Short: "a grouping command"}
	group.AddCommand(&cobra.Command{
		Use:  "leaf",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.AddCommand(group)

	cli.RequireSubcommands(root)

	require.Equal(t, "true", group.Annotations["noClient"],
		"a grouping command only prints help or rejects a positional; it must not build a client")

	// The leaf keeps whatever it declared: noClient is per-command, never
	// inherited, so this must not have widened to real commands.
	leaf, _, err := group.Find([]string{"leaf"})
	require.NoError(t, err)
	require.NotEqual(t, "true", leaf.Annotations["noClient"],
		"noClient must not leak onto commands that do real work")
}

// TestRequireSubcommands_StrayPositionalStillFails pins the property the RunE
// exists for, so the noClient change above cannot quietly restore cobra's
// exit-0-on-unknown-subcommand behavior.
func TestRequireSubcommands_StrayPositionalStillFails(t *testing.T) {
	root := &cobra.Command{Use: "pmx"}
	group := &cobra.Command{Use: "grp"}
	group.AddCommand(&cobra.Command{
		Use:  "leaf",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.AddCommand(group)
	cli.RequireSubcommands(root)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	root.SetArgs([]string{"grp", "bogus"})
	require.Error(t, root.Execute(), "an unknown subcommand must not exit 0")

	buf.Reset()
	root.SetArgs([]string{"grp"})
	require.NoError(t, root.Execute(), "a bare grouping command must print help and exit 0")
	require.Contains(t, buf.String(), "Usage")
}

// TestExitRecord_RedactsCredentialsInErrorURLs covers the disclosure path a
// GET or DELETE opens. The SDK encodes parameters into the request URL and
// quotes that URL in its error, so a command taking --password (node scan pbs
// requires one) wrote the cleartext credential into the exit record — a file
// retained indefinitely by default — and printed it to stderr as well.
func TestExitRecord_RedactsCredentialsInErrorURLs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_LOG_LAYOUT", "")
	t.Setenv("PMX_LOG_LEVEL", "")

	const password = "SUPERSECRET123"

	factory := func(*cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(*cobra.Command, []string) error {
				// Shaped exactly like the SDK's transport error.
				return fmt.Errorf(
					"failed to execute GET request to %q: connection refused",
					"https://h:8006/api2/json/nodes/n1/scan/pbs?password="+password+"&server=pbs.example.com")
			},
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(tmpDir, "c.yml"), "probe"}
	defer func() { os.Args = oldArgs }()

	require.Error(t, cli.Execute("pmx", []cli.GroupFactory{factory}))

	for _, rec := range readLogRecords(t, filepath.Join(tmpDir, ".pmx", "logs")) {
		raw, err := json.Marshal(rec)
		require.NoError(t, err)
		require.NotContains(t, string(raw), password,
			"a credential in a request URL must never reach the audit log")
	}

	// The non-sensitive part of the URL must survive, or the error stops
	// being diagnosable.
	records := readLogRecords(t, filepath.Join(tmpDir, ".pmx", "logs"))
	exit := findRecord(records, "exit")
	require.NotNil(t, exit)
	errText, _ := exit["error"].(string)
	require.Contains(t, errText, "scan/pbs")
	require.Contains(t, errText, "server=pbs.example.com")
	require.Contains(t, errText, "password=<redacted>")
}

// warningsAsErrorsConfig writes a config whose warnings-as-errors key is set
// to want, and returns its path. The context is never dialled: the test only
// needs PersistentPreRunE to reach its flag/env/config resolution.
func warningsAsErrorsConfig(t *testing.T, want bool) string {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	cfg := &config.Config{
		CurrentContext:   "prod",
		WarningsAsErrors: want,
		Contexts: map[string]*config.Context{
			"prod": {
				Host: "127.0.0.1", Port: 8006, Protocol: "https", Realm: "pam",
				Auth: config.AuthBlock{
					Type: "token", Username: "root@pam", TokenID: "cli", Secret: "literal-secret",
				},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	return cfgPath
}

// TestPersistentPreRunE_WarningsAsErrors_Precedence pins the resolution order
// for the opt-in that turns a "WARNINGS: N" task into a failure. The flag has
// to beat the environment and the config file, and --warnings-as-errors=false
// has to be able to switch off a config file that enables it — otherwise an
// operator whose config sets it has no per-invocation escape hatch.
func TestPersistentPreRunE_WarningsAsErrors_Precedence(t *testing.T) {
	cases := []struct {
		name string
		cfg  bool
		env  string
		args []string
		want bool
	}{
		{name: "default is off", want: false},
		{name: "config enables", cfg: true, want: true},
		{name: "env enables", env: "1", want: true},
		{name: "env disables what config enabled", cfg: true, env: "0", want: false},
		{name: "flag beats env", env: "0", args: []string{"--warnings-as-errors"}, want: true},
		{
			name: "explicit false flag beats config",
			cfg:  true,
			args: []string{"--warnings-as-errors=false"},
			want: false,
		},
		{name: "unparseable env falls through to config", cfg: true, env: "maybe", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PMX_OUTPUT", "table")
			t.Setenv("PMX_NODE", "")
			t.Setenv("PMX_CONTEXT", "")
			t.Setenv("PMX_WARNINGS_AS_ERRORS", tc.env)

			// The policy is process-wide; leave it as found.
			apiclient.SetWarningsAsErrors(false)
			t.Cleanup(func() { apiclient.SetWarningsAsErrors(false) })

			root, cleanup := cli.NewRootCmd("pmx")
			defer cleanup()
			root.SetContext(context.Background())

			called := false
			root.AddCommand(buildNoopCmd(&called))

			var outBuf, errBuf bytes.Buffer
			root.SetOut(&outBuf)
			root.SetErr(&errBuf)
			root.SetArgs(append([]string{"--config", warningsAsErrorsConfig(t, tc.cfg)}, append(tc.args, "noop")...))

			require.NoError(t, root.Execute())
			require.True(t, called)
			require.Equal(t, tc.want, apiclient.WarningsAsErrorsEnabled(),
				"resolved policy for flags=%v env=%q config=%v", tc.args, tc.env, tc.cfg)
		})
	}
}

// TestPersistentPreRunE_CtxName_Precedence pins the context resolution every
// command that builds no API client depends on: the context group and the
// auth group act on a NAME, not on a client, so if that name does not follow
// --context/-c > $PMX_CONTEXT > current-context they silently read, validate,
// or write credentials against the wrong context.
//
// deps.CtxName used to be assigned after the noClient early return, so it was
// empty for exactly the commands that needed it and they fell back to
// current-context unconditionally.
func TestPersistentPreRunE_CtxName_Precedence(t *testing.T) {
	cfgPath := writeTwoContextConfig(t)

	cases := []struct {
		name string
		env  string
		args []string
		want string
	}{
		{name: "current-context only", args: []string{"inspect"}, want: "alpha"},
		{name: "env wins over current-context", env: "beta", args: []string{"inspect"}, want: "beta"},
		{name: "long flag wins over current-context", args: []string{"--context", "beta", "inspect"}, want: "beta"},
		{name: "short flag wins over current-context", args: []string{"-c", "beta", "inspect"}, want: "beta"},
		{name: "long flag wins over env", env: "alpha", args: []string{"--context", "beta", "inspect"}, want: "beta"},
		{name: "short flag wins over env", env: "alpha", args: []string{"-c", "beta", "inspect"}, want: "beta"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PMX_CONTEXT", tc.env)
			t.Setenv("PMX_OUTPUT", "")
			t.Setenv("PMX_NODE", "")

			root, cleanup := cli.NewRootCmd("pmx")
			defer cleanup()
			root.SetContext(context.Background())

			var deps *cli.Deps
			cmd := buildInspectCmd(&deps)
			cmd.Annotations = map[string]string{"noClient": "true"}
			root.AddCommand(cmd)

			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetArgs(append([]string{"--config", cfgPath}, tc.args...))
			require.NoError(t, root.Execute())
			require.NotNil(t, deps)
			require.Equal(t, tc.want, deps.CtxName)
		})
	}
}

// writeTwoContextConfig writes a config with contexts alpha and beta, alpha
// current, so a resolution test can tell which one won.
func writeTwoContextConfig(t *testing.T) string {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	ctx := func(host string) *config.Context {
		return &config.Context{
			Host:    host,
			Port:    8006,
			Product: config.ProductPVE,
			Auth: config.AuthBlock{
				Type:     "token",
				Username: "root@pam",
				TokenID:  "cli",
				Secret:   "literal-secret",
			},
			TLS: config.TLSBlock{Insecure: true},
		}
	}
	require.NoError(t, config.SaveForce(cfgPath, &config.Config{
		CurrentContext: "alpha",
		Contexts:       map[string]*config.Context{"alpha": ctx("alpha.invalid"), "beta": ctx("beta.invalid")},
	}))
	return cfgPath
}

// ---------------------------------------------------------------------------
// ContextOptions and the override-aware client builders
// ---------------------------------------------------------------------------

// testPinA and testPinB are two distinct, well-formed SHA-256 pins.
var (
	testPinA = strings.TrimSuffix(strings.Repeat("AA:", 32), ":")
	testPinB = strings.TrimSuffix(strings.Repeat("BB:", 32), ":")
)

// connectionLeaf parses args with the real root's flag set onto a bare leaf
// command and returns the leaf, the overrides OverridesFromCommand reads off
// it, and the buffer the root writes standard error to. It fails the test
// when the overrides do not parse.
func connectionLeaf(t *testing.T, args ...string) (*cobra.Command, cli.ConnectionOverrides, *bytes.Buffer) {
	t.Helper()

	root, cleanup := cli.NewRootCmd("pmx")
	t.Cleanup(cleanup)
	root.AddCommand(&cobra.Command{Use: "leaf", RunE: func(*cobra.Command, []string) error { return nil }})

	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))

	leaf, rest, err := root.Find(append([]string{"leaf"}, args...))
	require.NoError(t, err)
	require.NoError(t, leaf.ParseFlags(rest))

	ov, err := cli.OverridesFromCommand(leaf)
	require.NoError(t, err)

	return leaf, ov, &stderr
}

// rawLabContext returns a context as it would sit in cfg.Contexts straight
// from the file, with no port, protocol, or realm, so any default written
// back into it would show. Each call returns a fresh value, so a test can
// compare the one it passed in against an untouched twin.
func rawLabContext() *config.Context {
	fromEnv := false

	return &config.Context{
		Host: "pve1.example.com",
		Auth: config.AuthBlock{
			Type: "token", Username: "root", TokenID: "tok", Secret: "s3cret",
			Session: &config.Session{Ticket: "old-ticket", CSRF: "old-csrf", ExpiresAt: 1},
		},
		TLS: config.TLSBlock{Tofu: true, Fingerprint: testPinA},
		SSH: config.SSHBlock{Jump: "bastion.example.com"},
		Proxy: config.ProxyBlock{
			URL: "socks5h://proxy.test:1080", Username: "proxyuser", Password: "proxypass", FromEnv: &fromEnv,
		},
		Timeout: config.TimeoutBlock{Connect: "3s", TLSHandshake: "4s", Request: "45s"},
	}
}

// countLines returns how many lines of s contain substr.
func countLines(s, substr string) int {
	n := 0

	for line := range strings.SplitSeq(s, "\n") {
		if strings.Contains(line, substr) {
			n++
		}
	}

	return n
}

// TestContextOptions_LeavesStoredContextUnchanged passes the raw stored
// context and proves that ContextOptions writes nothing back into it, while
// the options it returns still carry the product defaults, the derived
// credential, and the whole transport.
func TestContextOptions_LeavesStoredContextUnchanged(t *testing.T) {
	cmd, ov, _ := connectionLeaf(t)
	stored := rawLabContext()

	before, err := json.Marshal(stored)
	require.NoError(t, err)

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	opts, conn, err := cli.ContextOptions(cmd, stored, "lab", cfgPath, ov, cli.Credentials{}, neverTTY)
	require.NoError(t, err)

	after, err := json.Marshal(stored)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "the stored context must stay byte-for-byte unchanged")
	require.Equal(t, rawLabContext(), stored)

	require.Equal(t, "pve1.example.com", opts.Host)
	require.Equal(t, 8006, opts.Port, "the product default port must reach the options")
	require.Equal(t, "https", opts.Protocol)
	require.Equal(t, "root@pam!tok=s3cret", opts.APIToken, "the default realm must qualify the token user")

	require.Equal(t, 3, opts.DialTimeoutSec)
	require.Equal(t, 3+4+1, opts.TLSHandshakeTimeoutSec, "through a jump the handshake bound adds the connect bound")
	require.Equal(t, 45*time.Second, opts.Timeout)
	require.NotNil(t, opts.Proxy, "the stored proxy must be installed")
	require.NotNil(t, opts.DialContext, "the stored jump must be installed")

	require.Equal(t, "lab", conn.ContextName)
	require.Equal(t, "pve1.example.com", conn.Host)
	require.Equal(t, "bastion.example.com", conn.Jump.Chain)
	require.Equal(t, "proxypass", conn.Proxy.PasswordRef, "the connection keeps the stored value unresolved")
}

// TestBuildContextClient_BareContextOptionsUnchanged pins the pve.Options a
// context with no proxy, ssh.jump, or timeout block produces, so a future
// change to ApplyToOptions or resolvedTimeouts fails this test instead of
// silently changing a bare context's transport.
func TestBuildContextClient_BareContextOptionsUnchanged(t *testing.T) {
	bare := &config.Context{
		Host: "pve1.example.com",
		Auth: config.AuthBlock{Type: "token", Username: "root", TokenID: "tok", Secret: "s3cret"},
	}

	t.Run("no proxy, no jump, no timeout block", func(t *testing.T) {
		cmd, _, _ := connectionLeaf(t)

		opts, conn, err := cli.ContextOptions(
			cmd, bare, "lab", "", cli.ConnectionOverrides{}, cli.Credentials{}, neverTTY)
		require.NoError(t, err)

		require.Equal(t, 5, opts.DialTimeoutSec)
		require.Equal(t, 10, opts.TLSHandshakeTimeoutSec)
		require.Equal(t, 30*time.Second, opts.Timeout)
		require.Nil(t, opts.Proxy)
		require.Nil(t, opts.DialContext)
		require.Empty(t, opts.FingerprintCachePath)
		require.Nil(t, opts.ManualVerifyCallback)
		require.Nil(t, opts.SSLOptions)
		require.Equal(t, "root@pam!tok=s3cret", opts.APIToken)

		require.Empty(t, conn.Jump.Chain)
	})

	t.Run("ssh.jump set", func(t *testing.T) {
		jumpy := &config.Context{
			Host: "pve1.example.com",
			Auth: config.AuthBlock{Type: "token", Username: "root", TokenID: "tok", Secret: "s3cret"},
			SSH:  config.SSHBlock{Jump: "bastion.example.com"},
		}
		cmd, _, _ := connectionLeaf(t)

		opts, conn, err := cli.ContextOptions(
			cmd, jumpy, "lab", "", cli.ConnectionOverrides{}, cli.Credentials{}, neverTTY)
		require.NoError(t, err)

		require.Equal(t, 5+10+1, opts.TLSHandshakeTimeoutSec,
			"the intended change: a jump adds the connect bound and one second")
		require.NotNil(t, opts.DialContext, "the intended change: a jump installs a dialer")
		require.Equal(t, "bastion.example.com", conn.Jump.Chain)
	})
}

// TestContextOptions_WiresCACert proves that a stored CA bundle reaches the
// kit as peer verification with hostname checks, and that an insecure
// connection keeps verification off instead.
func TestContextOptions_WiresCACert(t *testing.T) {
	cmd, ov, _ := connectionLeaf(t)

	ctx := &config.Context{
		Host: "pve1.example.com",
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
		TLS:  config.TLSBlock{CACert: "/etc/pmx/ca.pem"},
	}

	opts, conn, err := cli.ContextOptions(cmd, ctx, "lab", "", ov, cli.Credentials{}, neverTTY)
	require.NoError(t, err)
	require.NotNil(t, opts.SSLOptions)
	require.Equal(t, "/etc/pmx/ca.pem", opts.SSLOptions.CACert)
	require.Equal(t, pve.SSLVerifyPeer, opts.SSLOptions.VerifyMode)
	require.True(t, opts.SSLOptions.VerifyHostname)
	require.Equal(t, "/etc/pmx/ca.pem", conn.CACert)

	ctx.TLS.Insecure = true
	opts, _, err = cli.ContextOptions(cmd, ctx, "lab", "", ov, cli.Credentials{}, neverTTY)
	require.NoError(t, err)
	require.NotNil(t, opts.SSLOptions)
	require.Equal(t, pve.SSLVerifyNone, opts.SSLOptions.VerifyMode, "insecure must win over the CA bundle")
	require.Empty(t, opts.SSLOptions.CACert)
}

// TestContextOptions_StoredPinKeepsTOFUPrompt proves that a context which
// pins a fingerprint and also enables trust on first use keeps both the
// cache path and the manual-verify callback, exactly as it always has.
func TestContextOptions_StoredPinKeepsTOFUPrompt(t *testing.T) {
	cmd, ov, _ := connectionLeaf(t)
	cfgPath := filepath.Join(t.TempDir(), "config.yml")

	opts, conn, err := cli.ContextOptions(cmd, rawLabContext(), "lab", cfgPath, ov, cli.Credentials{}, neverTTY)
	require.NoError(t, err)
	require.Equal(t, apiclient.FingerprintCachePath(cfgPath, "lab"), opts.FingerprintCachePath)
	require.NotNil(t, opts.ManualVerifyCallback)
	require.True(t, opts.CachedFingerprints[testPinA])
	require.True(t, conn.TOFU)
}

// TestContextOptions_FingerprintOverrideIsExclusive proves that a
// per-invocation pin replaces the context's whole trust mode: no trust-on-
// first-use cache, no prompt, and only the override's pin.
func TestContextOptions_FingerprintOverrideIsExclusive(t *testing.T) {
	cmd, ov, _ := connectionLeaf(t, "--api-fingerprint", testPinB)
	cfgPath := filepath.Join(t.TempDir(), "config.yml")

	opts, conn, err := cli.ContextOptions(cmd, rawLabContext(), "lab", cfgPath, ov, cli.Credentials{}, neverTTY)
	require.NoError(t, err)
	require.Empty(t, opts.FingerprintCachePath, "a per-invocation pin must not read or write the TOFU cache")
	require.Nil(t, opts.ManualVerifyCallback, "a per-invocation pin must never prompt")
	require.Equal(t, map[string]bool{testPinB: true}, opts.CachedFingerprints,
		"only the override's pin may be trusted")
	require.False(t, conn.TOFU)
	require.False(t, conn.TOFUReadOnly)
	require.Equal(t, "--api-fingerprint", conn.FingerprintSource)
}

// TestContextOptions_EndpointOverrideReadsTOFUCache proves that an endpoint
// override on a trust-on-first-use context reads the context's cache with no
// callback, so nothing new can be trusted, and that an insecure connection
// gets neither.
func TestContextOptions_EndpointOverrideReadsTOFUCache(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yml")

	newCtx := func() *config.Context {
		return &config.Context{
			Host: "pve1.example.com",
			Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
			TLS:  config.TLSBlock{Tofu: true},
		}
	}

	t.Run("verifying", func(t *testing.T) {
		cmd, ov, _ := connectionLeaf(t, "--api-endpoint", "pve9:9999")

		opts, conn, err := cli.ContextOptions(cmd, newCtx(), "lab", cfgPath, ov, cli.Credentials{}, neverTTY)
		require.NoError(t, err)
		require.Equal(t, "pve9", opts.Host)
		require.Equal(t, 9999, opts.Port)
		require.Equal(t, apiclient.FingerprintCachePath(cfgPath, "lab"), opts.FingerprintCachePath)
		require.Nil(t, opts.ManualVerifyCallback, "an endpoint override must never write the TOFU cache")
		require.False(t, conn.TOFU)
		require.True(t, conn.TOFUReadOnly)
	})

	t.Run("insecure", func(t *testing.T) {
		cmd, ov, _ := connectionLeaf(t, "--api-endpoint", "pve9:9999")
		ctx := newCtx()
		ctx.TLS.Insecure = true

		opts, conn, err := cli.ContextOptions(cmd, ctx, "lab", cfgPath, ov, cli.Credentials{}, neverTTY)
		require.NoError(t, err)
		require.Empty(t, opts.FingerprintCachePath, "insecure outranks trust on first use")
		require.Nil(t, opts.ManualVerifyCallback)
		require.False(t, conn.TOFUReadOnly)
	})

	t.Run("root insecure", func(t *testing.T) {
		cmd, ov, _ := connectionLeaf(t, "--insecure", "--api-endpoint", "pve9:9999")

		opts, _, err := cli.ContextOptions(cmd, newCtx(), "lab", cfgPath, ov, cli.Credentials{}, neverTTY)
		require.NoError(t, err)
		require.Empty(t, opts.FingerprintCachePath)
		require.Nil(t, opts.ManualVerifyCallback)
	})
}

// TestContextOptions_InsecureWarnsOnce proves that an insecure connection
// prints the warning exactly once per build, whether the context, the root's
// --insecure, or both turned verification off.
func TestContextOptions_InsecureWarnsOnce(t *testing.T) {
	const warning = "WARN: TLS certificate verification disabled"

	for _, tc := range []struct {
		name        string
		args        []string
		ctxInsecure bool
		wantWarning int
	}{
		{"context", nil, true, 1},
		{"flag", []string{"--insecure"}, false, 1},
		{"both", []string{"--insecure"}, true, 1},
		{"neither", nil, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, ov, stderr := connectionLeaf(t, tc.args...)
			ctx := &config.Context{
				Host: "pve1.example.com",
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
				TLS:  config.TLSBlock{Insecure: tc.ctxInsecure},
			}

			_, _, err := cli.ContextOptions(cmd, ctx, "lab", "", ov, cli.Credentials{}, neverTTY)
			require.NoError(t, err)
			require.Equal(t, tc.wantWarning, countLines(stderr.String(), warning), "stderr: %q", stderr.String())
		})
	}
}

// TestContextOptions_PrintsEnvOverrideNoteOnce proves that an endpoint from
// $PMX_API_ENDPOINT is announced exactly once on standard error, and that the
// same endpoint typed as --api-endpoint is not announced at all.
func TestContextOptions_PrintsEnvOverrideNoteOnce(t *testing.T) {
	newCtx := func() *config.Context {
		return &config.Context{
			Host: "pve1.example.com",
			Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
		}
	}

	t.Run("environment", func(t *testing.T) {
		t.Setenv("PMX_API_ENDPOINT", "pve9:9999")
		cmd, ov, stderr := connectionLeaf(t)

		_, conn, err := cli.ContextOptions(cmd, newCtx(), "lab", "", ov, cli.Credentials{}, neverTTY)
		require.NoError(t, err)
		require.Equal(t, "pve9", conn.Host)
		require.Equal(t, 1, countLines(stderr.String(), "note:"), "stderr: %q", stderr.String())
		require.Equal(t, 1, countLines(stderr.String(),
			`note: $PMX_API_ENDPOINT (pve9:9999) overrides the endpoint of context "lab"`),
			"stderr: %q", stderr.String())
	})

	t.Run("flag", func(t *testing.T) {
		cmd, ov, stderr := connectionLeaf(t, "--api-endpoint", "pve9:9999")

		_, conn, err := cli.ContextOptions(cmd, newCtx(), "lab", "", ov, cli.Credentials{}, neverTTY)
		require.NoError(t, err)
		require.Equal(t, "pve9", conn.Host)
		require.Equal(t, "--api-endpoint", conn.EndpointSource)
		require.Zero(t, countLines(stderr.String(), "note:"), "stderr: %q", stderr.String())
	})
}

// TestContextOptions_UnresolvableProxyPassword proves that a proxy password
// naming an unset variable fails the build with the context named, rather
// than building a client that skips the proxy or sends no credential.
func TestContextOptions_UnresolvableProxyPassword(t *testing.T) {
	t.Setenv("PMX_TEST_UNSET_PROXY_PASSWORD", "")
	require.NoError(t, os.Unsetenv("PMX_TEST_UNSET_PROXY_PASSWORD"))

	cmd, ov, _ := connectionLeaf(t)
	ctx := &config.Context{
		Host: "pve1.example.com",
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
		Proxy: config.ProxyBlock{
			URL: "socks5h://proxy.test:1080", Username: "proxyuser", Password: "${PMX_TEST_UNSET_PROXY_PASSWORD}",
		},
	}

	opts, conn, err := cli.ContextOptions(cmd, ctx, "lab", "", ov, cli.Credentials{}, neverTTY)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), `resolve proxy.password for context "lab": `), err.Error())
	require.Zero(t, opts.Host, "a failed build must return zero options")
	require.Nil(t, opts.Proxy)
	require.Zero(t, conn.Host, "a failed build must return a zero connection")
}

// TestContextOptions_CredentialsOverride proves that an explicit credential
// replaces the one ctx.Auth would produce, without resolving the stored
// secret, and that a zero value derives it from the context.
func TestContextOptions_CredentialsOverride(t *testing.T) {
	t.Setenv("PMX_TEST_UNSET_SECRET", "")
	require.NoError(t, os.Unsetenv("PMX_TEST_UNSET_SECRET"))

	cmd, ov, _ := connectionLeaf(t)
	ctx := &config.Context{
		Host: "pve1.example.com",
		Auth: config.AuthBlock{Type: "password", Username: "root", Secret: "${PMX_TEST_UNSET_SECRET}"},
	}

	opts, _, err := cli.ContextOptions(cmd, ctx, "lab", "", ov,
		cli.Credentials{Override: true, Username: "alice", Realm: "pve", Password: "typed"}, neverTTY)
	require.NoError(t, err, "an explicit credential must not resolve the stored secret")
	require.Equal(t, "alice@pve", opts.Username)
	require.Equal(t, "typed", opts.Password)
	require.Empty(t, opts.APIToken)

	opts, _, err = cli.ContextOptions(cmd, ctx, "lab", "", ov,
		cli.Credentials{Override: true, Ticket: "PVE:t", CSRF: "c"}, neverTTY)
	require.NoError(t, err)
	require.Equal(t, "PVE:t", opts.Ticket)
	require.Equal(t, "c", opts.CSRFToken)
	require.Empty(t, opts.Password)

	_, _, err = cli.ContextOptions(cmd, ctx, "lab", "", ov, cli.Credentials{}, neverTTY)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), `resolve secret for context "lab": `), err.Error())

	ctx.Auth.Secret = "stored"
	opts, _, err = cli.ContextOptions(cmd, ctx, "lab", "", ov, cli.Credentials{}, neverTTY)
	require.NoError(t, err)
	require.Equal(t, "root@pam", opts.Username, "the default realm must qualify the stored user")
	require.Equal(t, "stored", opts.Password)

	ctx.Auth.Session = &config.Session{Ticket: "PVE:session", CSRF: "session-csrf"}
	opts, _, err = cli.ContextOptions(cmd, ctx, "lab", "", ov, cli.Credentials{}, neverTTY)
	require.NoError(t, err)
	require.Equal(t, "PVE:session", opts.Ticket, "a stored session outranks the password")
	require.Equal(t, "session-csrf", opts.CSRFToken)
	require.Empty(t, opts.Password)
}

// TestContextOptions_OverridesReachTransport proves that the jump, proxy, and
// timeout overrides reach the options, and that "none" drops the stored jump
// and the stored proxy.
func TestContextOptions_OverridesReachTransport(t *testing.T) {
	cmd, ov, _ := connectionLeaf(t, "--api-jump", "none", "--api-proxy", "none",
		"--api-connect-timeout", "7s", "--api-request-timeout", "90s")

	opts, conn, err := cli.ContextOptions(cmd, rawLabContext(), "lab", "", ov, cli.Credentials{}, neverTTY)
	require.NoError(t, err)
	require.Nil(t, opts.DialContext, "--api-jump none must dial direct")
	require.Nil(t, opts.Proxy, "--api-proxy none must drop the stored proxy")
	require.Equal(t, 7, opts.DialTimeoutSec)
	require.Equal(t, 4, opts.TLSHandshakeTimeoutSec, "a direct dial keeps the stored handshake bound")
	require.Equal(t, 90*time.Second, opts.Timeout)
	require.Equal(t, "direct", conn.Via())
}

// TestContextOptions_RejectsInvalidInputs proves that a nil command, an empty
// context name, a nil context, and an override the resolver refuses each fail
// before anything is printed or built.
func TestContextOptions_RejectsInvalidInputs(t *testing.T) {
	cmd, ov, stderr := connectionLeaf(t)
	ctx := &config.Context{
		Host: "pve1.example.com",
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
		TLS:  config.TLSBlock{Insecure: true},
	}

	_, _, err := cli.ContextOptions(nil, ctx, "lab", "", ov, cli.Credentials{}, neverTTY)
	require.EqualError(t, err, `build options for context "lab": the command is nil`)

	_, _, err = cli.ContextOptions(cmd, ctx, "", "", ov, cli.Credentials{}, neverTTY)
	require.EqualError(t, err, "build options: the context name is empty")

	_, _, err = cli.ContextOptions(cmd, nil, "lab", "", ov, cli.Credentials{}, neverTTY)
	require.EqualError(t, err, `context "lab" is not defined`)

	// A stored field that fails validation names the context it came from,
	// so a caller that targets another context's connection, such as the
	// lab family, never reads the failure as if it were its own context.
	badJumpCtx := &config.Context{
		Host: "pve1.example.com",
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
		SSH:  config.SSHBlock{Jump: "bad host!"},
	}
	_, _, err = cli.ContextOptions(cmd, badJumpCtx, "lab", "", ov, cli.Credentials{}, neverTTY)
	require.ErrorContains(t, err, `context "lab": ssh.jump "bad host!" is not valid`)

	fpCmd, fpOv, fpStderr := connectionLeaf(t, "--api-fingerprint", testPinB)
	_, _, err = cli.ContextOptions(fpCmd, ctx, "lab", "", fpOv, cli.Credentials{}, neverTTY)
	require.NoError(t, err, "a trust override replaces the context's insecure setting")
	require.Empty(t, fpStderr.String(), "a pinned connection is not insecure and must not warn")

	insecureCmd, insecureOv, insecureStderr := connectionLeaf(t, "--insecure", "--api-fingerprint", testPinB)
	_, _, err = cli.ContextOptions(insecureCmd, ctx, "lab", "", insecureOv, cli.Credentials{}, neverTTY)
	require.EqualError(t, err, "--api-fingerprint cannot be combined with --insecure")
	require.Empty(t, insecureStderr.String(), "a refused override must print nothing")

	require.Empty(t, stderr.String())
}

// TestBuildContextClientConn_ReturnsResolvedConnection runs each of the four
// override-aware builders under an endpoint override and proves that each
// returns its client, the stored context, and the connection it dials.
func TestBuildContextClientConn_ReturnsResolvedConnection(t *testing.T) {
	cfg := newThreeContextConfig(t)
	cmd, ov, _ := connectionLeaf(t, "--api-endpoint", "pve9:9999")

	check := func(t *testing.T, ctx *config.Context, conn cli.Connection, wantProduct string) {
		t.Helper()
		require.Equal(t, wantProduct, ctx.Product)
		require.Equal(t, "pve9", conn.Host)
		require.Equal(t, 9999, conn.Port)
		require.Equal(t, "--api-endpoint", conn.EndpointSource)
	}

	ac, ctx, conn, err := cli.BuildContextClientConn(cmd, cfg, "", "pve1", ov, neverTTY)
	require.NoError(t, err)
	require.NotNil(t, ac)
	check(t, ctx, conn, config.ProductPVE)
	require.Equal(t, "pve1", conn.ContextName)

	pc, ctx, conn, err := cli.BuildContextPBSClientConn(cmd, cfg, "", "pbs1", ov, neverTTY)
	require.NoError(t, err)
	require.NotNil(t, pc)
	check(t, ctx, conn, config.ProductPBS)

	dc, ctx, conn, err := cli.BuildContextPDMClientConn(cmd, cfg, "", "pdm1", ov, neverTTY)
	require.NoError(t, err)
	require.NotNil(t, dc)
	check(t, ctx, conn, config.ProductPDM)

	for _, name := range []string{"pve1", "pbs1", "pdm1"} {
		clients, ctx, conn, err := cli.BuildContextAnyClientConn(cmd, cfg, "", name, ov, neverTTY)
		require.NoError(t, err, name)
		check(t, ctx, conn, cfg.Contexts[name].Product)
		require.Equal(t, name, conn.ContextName)
		require.True(t, clients.API != nil || clients.PBS != nil || clients.PDM != nil)
	}

	// Without an override the connection carries the stored endpoint.
	_, _, conn, err = cli.BuildContextPBSClientConn(cmd, cfg, "", "pbs1", cli.ConnectionOverrides{}, neverTTY)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.2", conn.Host)
	require.Equal(t, 8007, conn.Port)
	require.Empty(t, conn.EndpointSource)
}

// TestBuildContextClientConn_ProductGuardAndResolveErrors proves that each
// product builder still rejects another product's context, and that a
// refused override fails the build, each with a zero connection.
func TestBuildContextClientConn_ProductGuardAndResolveErrors(t *testing.T) {
	cfg := newThreeContextConfig(t)
	cmd, ov, _ := connectionLeaf(t)

	_, _, conn, err := cli.BuildContextClientConn(cmd, cfg, "", "pbs1", ov, neverTTY)
	require.ErrorContains(t, err, "this command requires a PVE context")
	require.Zero(t, conn.Host)

	_, _, conn, err = cli.BuildContextPBSClientConn(cmd, cfg, "", "pve1", ov, neverTTY)
	require.ErrorContains(t, err, "this command requires a PBS context")
	require.Zero(t, conn.Host)

	_, _, conn, err = cli.BuildContextPDMClientConn(cmd, cfg, "", "pve1", ov, neverTTY)
	require.ErrorContains(t, err, "this command requires a PDM context")
	require.Zero(t, conn.Host)

	downgrade := cli.ConnectionOverrides{Host: "pve9", Protocol: "http", EndpointSource: "--api-endpoint"}
	_, _, conn, err = cli.BuildContextClientConn(cmd, cfg, "", "pve1", downgrade, neverTTY)
	require.EqualError(t, err,
		`--api-endpoint would downgrade context "pve1" from https to http; `+
			`set protocol: http on the context to allow it`)
	require.Zero(t, conn.Host)

	_, _, _, err = cli.BuildContextAnyClientConn(cmd, nil, "/tmp/config.yml", "pve1", ov, neverTTY)
	require.EqualError(t, err, "no configuration is loaded (config: /tmp/config.yml)")
}

// TestBuildContextClientConn_ConnectErrorNamesResolvedHost proves that a
// client construction failure names the host that was dialled, which under an
// endpoint override is the overridden host rather than the stored one.
func TestBuildContextClientConn_ConnectErrorNamesResolvedHost(t *testing.T) {
	cfg := newThreeContextConfig(t)
	missingCA := filepath.Join(t.TempDir(), "missing-ca.pem")

	for _, name := range []string{"pve1", "pbs1", "pdm1"} {
		cfg.Contexts[name].TLS.CACert = missingCA
	}

	cmd, ov, _ := connectionLeaf(t, "--api-endpoint", "pve9")
	const want = "connect to pve9: "

	_, _, _, err := cli.BuildContextClientConn(cmd, cfg, "", "pve1", ov, neverTTY)
	require.ErrorContains(t, err, want)

	_, _, _, err = cli.BuildContextPBSClientConn(cmd, cfg, "", "pbs1", ov, neverTTY)
	require.ErrorContains(t, err, want)

	_, _, _, err = cli.BuildContextPDMClientConn(cmd, cfg, "", "pdm1", ov, neverTTY)
	require.ErrorContains(t, err, want)

	for _, name := range []string{"pve1", "pbs1", "pdm1"} {
		_, _, _, err = cli.BuildContextAnyClientConn(cmd, cfg, "", name, ov, neverTTY)
		require.ErrorContains(t, err, want, name)
	}
}

// TestBuildContextClient_LegacyIgnoresEnvironmentOverrides proves that the
// legacy builders apply only the insecure flag they are handed, so a
// PMX_API_* variable meant for the invocation's own context can never
// redirect a caller that targets another context.
func TestBuildContextClient_LegacyIgnoresEnvironmentOverrides(t *testing.T) {
	t.Setenv("PMX_API_ENDPOINT", "ftp://malformed")
	t.Setenv("PMX_API_JUMP", "bastion.example.com")

	cfg := newThreeContextConfig(t)
	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()

	var stderr bytes.Buffer
	root.SetErr(&stderr)

	ac, ctx, err := cli.BuildContextClient(root, cfg, "", "pve1", false, neverTTY)
	require.NoError(t, err)
	require.NotNil(t, ac)
	require.Equal(t, "10.0.0.1", ctx.Host)

	pc, _, err := cli.BuildContextPBSClient(root, cfg, "", "pbs1", true, neverTTY)
	require.NoError(t, err)
	require.NotNil(t, pc)

	dc, _, err := cli.BuildContextPDMClient(root, cfg, "", "pdm1", false, neverTTY)
	require.NoError(t, err)
	require.NotNil(t, dc)

	clients, _, err := cli.BuildContextAnyClient(root, cfg, "", "pdm1", false, neverTTY)
	require.NoError(t, err)
	require.NotNil(t, clients.PDM)

	require.Zero(t, countLines(stderr.String(), "note:"), "stderr: %q", stderr.String())
	require.Equal(t, 1, countLines(stderr.String(), "WARN: TLS certificate verification disabled"),
		"only the insecure PBS build may warn; stderr: %q", stderr.String())
}

// ---------------------------------------------------------------------------
// The root's own client path under the connection overrides
// ---------------------------------------------------------------------------

// rootRun is what runThroughRoot observed: the Deps persistentPreRunE built,
// whether or not it failed, whether the command's RunE ran, the error, and
// the root's standard error.
type rootRun struct {
	deps   *cli.Deps
	ran    bool
	err    error
	stderr string
}

// runThroughRoot runs args through the real root with one command, "probe",
// carrying annotations, whose RunE calls run. It captures the Deps
// persistentPreRunE stashed on the command even when persistentPreRunE
// failed, so a test can prove no client was built.
func runThroughRoot(
	t *testing.T, annotations map[string]string, run func(*cobra.Command, *cli.Deps) error, args ...string,
) rootRun {
	t.Helper()
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	t.Cleanup(cleanup)
	root.SetContext(context.Background())

	var res rootRun

	root.AddCommand(&cobra.Command{
		Use:         "probe",
		Annotations: annotations,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res.ran = true
			if run == nil {
				return nil
			}

			return run(cmd, cli.GetDeps(cmd))
		},
	})

	std := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		err := std(cmd, args)
		res.deps = depsIfStashed(cmd)

		return err
	}

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--no-log"}, args...))

	res.err = root.Execute()
	res.stderr = stderr.String()

	return res
}

// depsIfStashed returns the Deps persistentPreRunE stashed on cmd, or nil
// when it failed before stashing them, which cli.GetDeps reports by
// panicking.
func depsIfStashed(cmd *cobra.Command) (deps *cli.Deps) {
	defer func() {
		if recover() != nil {
			deps = nil
		}
	}()

	return cli.GetDeps(cmd)
}

// writeCAFile writes the certificate of a throwaway TLS listener as a PEM
// file and returns its path, so a context or an override can name a CA
// bundle the kit loads.
func writeCAFile(t *testing.T, name string) string {
	t.Helper()

	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()

	path := filepath.Join(t.TempDir(), name)
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	require.NoError(t, os.WriteFile(path, block, 0o600))

	return path
}

// precedenceContext is the stored context the precedence rows start from.
func precedenceContext() *config.Context {
	return &config.Context{
		Host: "ctx-host.invalid", Protocol: "https", Product: config.ProductPVE,
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "literal-secret"},
	}
}

// TestAPIConnectionFlagPrecedence runs a client-building command through the
// real root for every connection parameter at each of the four tiers, and
// reads the answer off Deps.Route, the Connection the root's client dials.
// At each tier every lower source is also set, so the tier wins only by
// precedence: the flag beats the environment, the environment beats the
// context, and the context beats the built-in default.
func TestAPIConnectionFlagPrecedence(t *testing.T) {
	defaults := apiclient.DefaultTimeoutSpec()
	caFlag, caEnv, caCtx := writeCAFile(t, "flag.pem"), writeCAFile(t, "env.pem"), writeCAFile(t, "ctx.pem")
	pinFlag := testPinA
	pinEnv := testPinB
	pinCtx := strings.TrimSuffix(strings.Repeat("CC:", 32), ":")

	type tier struct {
		flag     []string
		env      string
		ctx      func(*config.Context)
		expected func(t *testing.T, conn cli.Connection)
	}

	type row struct {
		name   string
		envVar string
		// tiers lists the flag, environment, context, and default tiers in
		// that order; each tier's sources are applied together with every
		// tier below it.
		tiers [4]tier
	}

	timeoutRow := func(name, flag, envVar, ctxKey string, def time.Duration,
		get func(cli.Connection) time.Duration) row {
		setCtx := func(c *config.Context) {
			switch ctxKey {
			case "connect":
				c.Timeout.Connect = "9s"
			case "tls-handshake":
				c.Timeout.TLSHandshake = "9s"
			case "request":
				c.Timeout.Request = "9s"
			}
		}
		want := func(d time.Duration) func(*testing.T, cli.Connection) {
			return func(t *testing.T, conn cli.Connection) {
				t.Helper()
				require.Equal(t, d, get(conn))
			}
		}

		return row{name: name, envVar: envVar, tiers: [4]tier{
			{flag: []string{flag, "7s"}, expected: want(7 * time.Second)},
			{env: "8s", expected: want(8 * time.Second)},
			{ctx: setCtx, expected: want(9 * time.Second)},
			{expected: want(def)},
		}}
	}

	rows := []row{
		{name: "endpoint", envVar: "PMX_API_ENDPOINT", tiers: [4]tier{
			{flag: []string{"--api-endpoint", "flag-host.invalid:1111"},
				expected: func(t *testing.T, conn cli.Connection) {
					require.Equal(t, "flag-host.invalid", conn.Host)
					require.Equal(t, 1111, conn.Port)
					require.Equal(t, "--api-endpoint", conn.EndpointSource)
				}},
			{env: "env-host.invalid:2222",
				expected: func(t *testing.T, conn cli.Connection) {
					require.Equal(t, "env-host.invalid", conn.Host)
					require.Equal(t, 2222, conn.Port)
					require.Equal(t, "$PMX_API_ENDPOINT", conn.EndpointSource)
				}},
			{ctx: func(c *config.Context) { c.Port = 3333 },
				expected: func(t *testing.T, conn cli.Connection) {
					require.Equal(t, "ctx-host.invalid", conn.Host)
					require.Equal(t, 3333, conn.Port)
					require.Empty(t, conn.EndpointSource)
				}},
			{expected: func(t *testing.T, conn cli.Connection) {
				require.Equal(t, "ctx-host.invalid", conn.Host)
				require.Equal(t, 8006, conn.Port, "the product default port")
			}},
		}},
		{name: "jump", envVar: "PMX_API_JUMP", tiers: [4]tier{
			{flag: []string{"--api-jump", "flag-bastion.invalid"},
				expected: func(t *testing.T, conn cli.Connection) {
					require.Equal(t, "flag-bastion.invalid", conn.Jump.Chain)
				}},
			{env: "env-bastion.invalid",
				expected: func(t *testing.T, conn cli.Connection) {
					require.Equal(t, "env-bastion.invalid", conn.Jump.Chain)
				}},
			{ctx: func(c *config.Context) { c.SSH.Jump = "ctx-bastion.invalid" },
				expected: func(t *testing.T, conn cli.Connection) {
					require.Equal(t, "ctx-bastion.invalid", conn.Jump.Chain)
				}},
			{expected: func(t *testing.T, conn cli.Connection) {
				require.Empty(t, conn.Jump.Chain, "no bastion by default")
			}},
		}},
		{name: "proxy", envVar: "PMX_API_PROXY", tiers: [4]tier{
			{flag: []string{"--api-proxy", "socks5h://flag-proxy.invalid:1080"},
				expected: func(t *testing.T, conn cli.Connection) {
					require.NotNil(t, conn.Proxy.URL)
					require.Equal(t, "socks5h://flag-proxy.invalid:1080", conn.Proxy.URL.String())
				}},
			{env: "socks5h://env-proxy.invalid:1080",
				expected: func(t *testing.T, conn cli.Connection) {
					require.NotNil(t, conn.Proxy.URL)
					require.Equal(t, "socks5h://env-proxy.invalid:1080", conn.Proxy.URL.String())
				}},
			{ctx: func(c *config.Context) { c.Proxy.URL = "socks5h://ctx-proxy.invalid:1080" },
				expected: func(t *testing.T, conn cli.Connection) {
					require.NotNil(t, conn.Proxy.URL)
					require.Equal(t, "socks5h://ctx-proxy.invalid:1080", conn.Proxy.URL.String())
				}},
			{expected: func(t *testing.T, conn cli.Connection) {
				require.Nil(t, conn.Proxy.URL, "no proxy by default")
				require.False(t, conn.Proxy.FromEnv)
			}},
		}},
		{name: "ca-cert", envVar: "PMX_API_CA_CERT", tiers: [4]tier{
			{flag: []string{"--api-ca-cert", caFlag},
				expected: func(t *testing.T, conn cli.Connection) { require.Equal(t, caFlag, conn.CACert) }},
			{env: caEnv,
				expected: func(t *testing.T, conn cli.Connection) { require.Equal(t, caEnv, conn.CACert) }},
			{ctx: func(c *config.Context) { c.TLS.CACert = caCtx },
				expected: func(t *testing.T, conn cli.Connection) { require.Equal(t, caCtx, conn.CACert) }},
			{expected: func(t *testing.T, conn cli.Connection) { require.Empty(t, conn.CACert) }},
		}},
		{name: "fingerprint", envVar: "PMX_API_FINGERPRINT", tiers: [4]tier{
			{flag: []string{"--api-fingerprint", pinFlag},
				expected: func(t *testing.T, conn cli.Connection) {
					require.Equal(t, pinFlag, conn.Fingerprint)
					require.Equal(t, "--api-fingerprint", conn.FingerprintSource)
				}},
			{env: pinEnv,
				expected: func(t *testing.T, conn cli.Connection) {
					require.Equal(t, pinEnv, conn.Fingerprint)
					require.Equal(t, "$PMX_API_FINGERPRINT", conn.FingerprintSource)
				}},
			{ctx: func(c *config.Context) { c.TLS.Fingerprint = pinCtx },
				expected: func(t *testing.T, conn cli.Connection) {
					require.Equal(t, pinCtx, conn.Fingerprint)
					require.Equal(t, "tls.fingerprint", conn.FingerprintSource)
				}},
			{expected: func(t *testing.T, conn cli.Connection) {
				require.Empty(t, conn.Fingerprint)
				require.Empty(t, conn.FingerprintSource)
			}},
		}},
		timeoutRow("connect timeout", "--api-connect-timeout", "PMX_API_CONNECT_TIMEOUT", "connect",
			defaults.Connect, func(c cli.Connection) time.Duration { return c.Timeouts.Connect }),
		timeoutRow("tls-handshake timeout", "--api-tls-handshake-timeout", "PMX_API_TLS_HANDSHAKE_TIMEOUT",
			"tls-handshake", defaults.TLSHandshake,
			func(c cli.Connection) time.Duration { return c.Timeouts.TLSHandshake }),
		timeoutRow("request timeout", "--api-request-timeout", "PMX_API_REQUEST_TIMEOUT", "request",
			defaults.Request, func(c cli.Connection) time.Duration { return c.Timeouts.Request }),
	}

	tierNames := [4]string{"flag", "environment", "context", "default"}

	for _, r := range rows {
		for i, name := range tierNames {
			t.Run(r.name+" from "+name, func(t *testing.T) {
				ctx := precedenceContext()
				var flags []string

				// Apply this tier's sources and every lower tier's, so the
				// tier under test wins only because it outranks them.
				for j := i; j < len(r.tiers); j++ {
					below := r.tiers[j]
					flags = append(flags, below.flag...)
					if below.env != "" {
						t.Setenv(r.envVar, below.env)
					}
					if below.ctx != nil {
						below.ctx(ctx)
					}
				}

				cfgPath := filepath.Join(t.TempDir(), "config.yml")
				require.NoError(t, config.SaveForce(cfgPath, &config.Config{
					CurrentContext: "lab",
					Contexts:       map[string]*config.Context{"lab": ctx},
				}))

				res := runThroughRoot(t, nil, nil, append([]string{"--config", cfgPath, "probe"}, flags...)...)
				require.NoError(t, res.err, "stderr: %s", res.stderr)
				require.True(t, res.ran)
				require.NotNil(t, res.deps.API, "the root must build the client")
				require.Equal(t, "lab", res.deps.Route.ContextName, "Deps.Route must carry the resolved connection")

				r.tiers[i].expected(t, res.deps.Route)
			})
		}
	}
}

// TestAPIEndpointOverride_PartialFallsBackToContext proves that an endpoint
// override naming only a host keeps the context's product default port and
// protocol, so --api-endpoint pve2 against a Proxmox Backup Server context
// dials https://pve2:8007.
func TestAPIEndpointOverride_PartialFallsBackToContext(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(cfgPath, &config.Config{
		CurrentContext: "backup",
		Contexts: map[string]*config.Context{"backup": {
			Host: "pbs1.invalid", Product: config.ProductPBS,
			Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "literal-secret"},
		}},
	}))

	res := runThroughRoot(t, map[string]string{cli.ProductAnnotation: config.ProductPBS}, nil,
		"--config", cfgPath, "probe", "--api-endpoint", "pve2")
	require.NoError(t, res.err, "stderr: %s", res.stderr)
	require.NotNil(t, res.deps.PBS)
	require.Equal(t, "pve2", res.deps.Route.Host)
	require.Equal(t, 8007, res.deps.Route.Port)
	require.Equal(t, "https", res.deps.Route.Protocol)
	require.Equal(t, "pbs1.invalid", res.deps.Ctx.Host, "the stored host stays on Deps.Ctx")
}

// TestAPIConnectionFlags_MalformedFailInvocation proves that each malformed
// override fails a client-building command naming its flag, before any
// context is resolved or any client is built, against a context that would
// otherwise build one.
func TestAPIConnectionFlags_MalformedFailInvocation(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(cfgPath, &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": precedenceContext()},
	}))

	control := runThroughRoot(t, nil, nil, "--config", cfgPath, "probe")
	require.NoError(t, control.err, "the context must build a client when no override is malformed")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"endpoint", []string{"--api-endpoint", "ftp://pve1"},
			`invalid --api-endpoint "ftp://pve1": scheme must be https or http`},
		{"zero connect timeout", []string{"--api-connect-timeout", "0s"},
			"--api-connect-timeout must be greater than zero"},
		{"negative connect timeout", []string{"--api-connect-timeout=-1s"},
			"--api-connect-timeout must be greater than zero"},
		{"connect timeout without a unit", []string{"--api-connect-timeout", "5"},
			`--api-connect-timeout "5" is not a duration (e.g. 5s, 500ms)`},
		{"fingerprint", []string{"--api-fingerprint", "garbage"},
			`--api-fingerprint "garbage" must be a colon-separated hex SHA-256 (e.g. AA:BB:..., 32 pairs)`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runThroughRoot(t, nil, nil, append([]string{"--config", cfgPath, "probe"}, tc.args...)...)

			require.EqualError(t, res.err, tc.want)
			require.False(t, res.ran, "the command must not run")
			require.NotNil(t, res.deps, "persistentPreRunE stashes Deps before it parses the overrides")
			require.Nil(t, res.deps.API, "no client may be built")
			require.Nil(t, res.deps.Ctx, "no context may be resolved")
			require.Zero(t, res.deps.Route.Host, "no connection may be published")
		})
	}
}

// TestAPIEndpointEnv_ReachesListener proves that $PMX_API_ENDPOINT reaches
// the wire: the context points at a closed port, the variable points at a
// listener on a dynamic port, and the command's request arrives there.
func TestAPIEndpointEnv_ReachesListener(t *testing.T) {
	var hits atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"version":"8.2.4","release":"8.2","repoid":"faa83925"}}`))
	}))
	t.Cleanup(srv.Close)

	listenerPort := srv.Listener.Addr().(*net.TCPAddr).Port
	t.Setenv("PMX_API_ENDPOINT", "127.0.0.1:"+strconv.Itoa(listenerPort))

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(cfgPath, &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{"lab": {
			Host: "127.0.0.1", Port: closedPort(t), Protocol: "http", Product: config.ProductPVE,
			Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "literal-secret"},
		}},
	}))

	res := runThroughRoot(t, nil, func(cmd *cobra.Command, deps *cli.Deps) error {
		_, err := deps.API.Version.Get(pve.WithRetries(cmd.Context(), 0))
		return err
	}, "--config", cfgPath, "probe")

	require.NoError(t, res.err, "stderr: %s", res.stderr)
	require.Equal(t, int64(1), hits.Load(), "the request must reach the listener $PMX_API_ENDPOINT names")
	require.Equal(t, listenerPort, res.deps.Route.Port)
	require.Equal(t, "$PMX_API_ENDPOINT", res.deps.Route.EndpointSource)
	require.Equal(t, 1, countLines(res.stderr, "note: $PMX_API_ENDPOINT"), "stderr: %s", res.stderr)
}

// closedPort returns a loopback port nothing listens on, so a dial to it is
// refused at once.
func closedPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	return port
}

// executeProbe runs cli.Execute with os.Args set to args and one factory,
// and returns what reached the process's standard error and the error.
func executeProbe(t *testing.T, factory cli.GroupFactory, args ...string) (string, error) {
	t.Helper()
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	oldArgs := os.Args
	os.Args = append([]string{"pmx"}, args...)
	defer func() { os.Args = oldArgs }()

	var execErr error
	stderr := captureStderr(t, func() {
		execErr = cli.Execute("pmx", []cli.GroupFactory{factory})
	})

	return stderr, execErr
}

// probeFactory returns a group factory for one client-building command,
// "probe", whose RunE calls run.
func probeFactory(run func(*cobra.Command, *cli.Deps) error) cli.GroupFactory {
	return func(*cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use: "probe",
			RunE: func(cmd *cobra.Command, _ []string) error {
				return run(cmd, cli.GetDeps(cmd))
			},
		}
	}
}

// TestExecute_ClosesKitClientsOnExit proves that Execute closes the client
// the root built, on the success path and on the error path alike, so the
// keep-alive connection a command left idle in the pool is closed with the
// command rather than lingering until the kit's idle timeout, which is far
// longer than the bound here. It also proves that the clients close before
// the ssh jump children are reaped, so no idle connection is still parked on
// a child when its pipes are closed.
func TestExecute_ClosesKitClientsOnExit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
	}{
		{"success", nil},
		{"error", errors.New("the command failed after its request")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu     sync.Mutex
				states = map[net.Conn]http.ConnState{}
				events []string
			)

			record := func(event string) {
				mu.Lock()
				defer mu.Unlock()
				events = append(events, event)
			}

			restore := cli.SetShutdownJumps(func(bound time.Duration) {
				record("shutdown jumps")
				apiclient.ShutdownJumps(bound)
			})
			t.Cleanup(restore)

			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"version":"8.2.4","release":"8.2","repoid":"faa83925"}}`))
			}))
			srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
				mu.Lock()
				defer mu.Unlock()
				states[c] = s
			}
			srv.Start()
			t.Cleanup(srv.Close)

			port := srv.Listener.Addr().(*net.TCPAddr).Port
			cfgPath := filepath.Join(t.TempDir(), "config.yml")
			require.NoError(t, config.SaveForce(cfgPath, &config.Config{
				CurrentContext: "lab",
				Contexts: map[string]*config.Context{"lab": {
					Host: "127.0.0.1", Port: port, Protocol: "http", Product: config.ProductPVE,
					Auth: config.AuthBlock{
						Type: "token", Username: "root@pam", TokenID: "tok", Secret: "literal-secret",
					},
				}},
			}))

			var opened int

			stderr, err := executeProbe(t, probeFactory(func(cmd *cobra.Command, deps *cli.Deps) error {
				if _, err := deps.API.Version.Get(pve.WithRetries(cmd.Context(), 0)); err != nil {
					return err
				}

				deps.API.Raw = closeRecorder{Client: deps.API.Raw, record: record}

				mu.Lock()
				opened = len(states)
				mu.Unlock()

				return tc.failure
			}), "--config", cfgPath, "--no-log", "probe")

			if tc.failure == nil {
				require.NoError(t, err, "stderr: %s", stderr)
			} else {
				require.ErrorIs(t, err, tc.failure)
			}

			require.Equal(t, 1, opened, "the command must have opened exactly one connection")

			// The client closes its end inside Execute; the server's own
			// goroutine sees the end of file a moment later and records
			// the closed state.
			require.Eventually(t, func() bool {
				mu.Lock()
				defer mu.Unlock()

				for _, s := range states {
					if s != http.StateClosed {
						return false
					}
				}

				return len(states) == 1
			}, 2*time.Second, 10*time.Millisecond,
				"the connection the command opened must be closed when Execute returns")

			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, []string{"close client", "shutdown jumps"}, events,
				"the clients must close before the jump children are reaped")
		})
	}
}

// closeRecorder wraps a kit client and records its Close before passing it
// on, so a test can see when Execute closed the client.
type closeRecorder struct {
	pve.Client
	record func(string)
}

func (c closeRecorder) Close() error {
	c.record("close client")
	return c.Client.Close()
}

// TestExecute_InterruptSuppressesConnectionHints proves that a connection
// failure reaching the root after the command's context was cancelled is
// reported as an interruption rather than diagnosed as an unreachable host,
// and that the same failure without the cancellation still gets its hint.
func TestExecute_InterruptSuppressesConnectionHints(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(cfgPath, &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": precedenceContext()},
	}))

	dialFailure := func() error {
		return fmt.Errorf("GET /version: %w",
			&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")})
	}

	t.Run("an interrupted command", func(t *testing.T) {
		stderr, err := executeProbe(t, probeFactory(func(cmd *cobra.Command, _ *cli.Deps) error {
			ctx, cancel := context.WithCancel(cmd.Context())
			cancel()
			cmd.SetContext(ctx)

			return dialFailure()
		}), "--config", cfgPath, "--no-log", "probe")

		require.Error(t, err)
		require.Equal(t, "GET /version: dial tcp: connect: connection refused\ninterrupted\n", stderr)
	})

	t.Run("the same failure uninterrupted", func(t *testing.T) {
		stderr, err := executeProbe(t, probeFactory(func(*cobra.Command, *cli.Deps) error {
			return dialFailure()
		}), "--config", cfgPath, "--no-log", "probe")

		require.Error(t, err)
		require.Contains(t, stderr, "hint: could not reach ctx-host.invalid:8006")
		require.NotContains(t, stderr, "interrupted")
	})
}

// TestExecute_PinMismatchUnderEndpointOverrideNamesSource proves that a
// context that pins a certificate, reached through --api-endpoint at a TLS
// listener presenting another certificate, fails with the explanation that
// names the override, and that a context which also enables trust on first
// use gets the same pin explanation rather than the read-only one.
func TestExecute_PinMismatchUnderEndpointOverrideNamesSource(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"version":"8.2.4","release":"8.2","repoid":"faa83925"}}`))
	}))
	t.Cleanup(srv.Close)

	endpoint := srv.Listener.Addr().String()
	want := fmt.Sprintf(`context "lab" pins a certificate that %s (from --api-endpoint) does not present; `+
		"pass --api-fingerprint for that host\n", endpoint)

	for _, tofu := range []bool{false, true} {
		t.Run(fmt.Sprintf("tofu %v", tofu), func(t *testing.T) {
			ctx := precedenceContext()
			ctx.Host = "pve-stored.invalid"
			ctx.TLS = config.TLSBlock{Fingerprint: testPinA, Tofu: tofu}

			cfgPath := filepath.Join(t.TempDir(), "config.yml")
			require.NoError(t, config.SaveForce(cfgPath, &config.Config{
				CurrentContext: "lab",
				Contexts:       map[string]*config.Context{"lab": ctx},
			}))

			stderr, err := executeProbe(t, probeFactory(func(cmd *cobra.Command, deps *cli.Deps) error {
				_, err := deps.API.Version.Get(pve.WithRetries(cmd.Context(), 0))
				return err
			}), "--config", cfgPath, "--no-log", "--api-endpoint", endpoint, "probe")

			require.Error(t, err)
			firstLine, _, _ := strings.Cut(stderr, "\n")
			require.Equal(t, want, firstLine+"\n", "stderr: %s", stderr)
			require.NotContains(t, stderr, "no trusted certificate", "the pin explanation outranks the read-only one")
		})
	}
}

// auditConfig writes a config holding one context, "lab", and returns its
// path.
func auditConfig(t *testing.T, ctx *config.Context) string {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(cfgPath, &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}))

	return cfgPath
}

// auditRun runs a noClient command that consumes the connection overrides
// through cli.Execute with args, under a fresh HOME so the log lands in a
// temporary directory, and returns every log record written, the Deps the
// command saw, and the error.
func auditRun(t *testing.T, cfgPath string, args ...string) ([]map[string]any, *cli.Deps, error) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PMX_LOG_LAYOUT", "")
	t.Setenv("PMX_LOG_LEVEL", "")

	var seen *cli.Deps

	factory := func(*cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true", cli.AnnotationUsesConnection: "true"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				seen = cli.GetDeps(cmd)
				return nil
			},
		}
	}

	_, err := executeProbe(t, factory, append([]string{"--config", cfgPath}, args...)...)

	return readLogRecords(t, filepath.Join(home, ".pmx", "logs")), seen, err
}

// TestInvocationTargetAttrs_NamesProxyHostAndJump proves that the invocation
// record names the effective target: the connection's host, port, bastion,
// and proxy host after the overrides, with the stored context's product and
// user, and the sources of the overrides but never their values. It also
// proves that building the record reads no secret and writes nothing back
// into the stored context, which is what calling config.ResolveContext did.
func TestInvocationTargetAttrs_NamesProxyHostAndJump(t *testing.T) {
	t.Run("the function resolves the connection, not the context", func(t *testing.T) {
		file, err := parser.ParseFile(token.NewFileSet(), "root.go", nil, parser.SkipObjectResolution)
		require.NoError(t, err)

		calls := map[string]bool{}
		found := false

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "invocationTargetAttrs" {
				continue
			}

			found = true
			require.Len(t, fn.Type.Params.List, 3, "invocationTargetAttrs(cmd, cfg, name)")

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					switch fun := call.Fun.(type) {
					case *ast.Ident:
						calls[fun.Name] = true
					case *ast.SelectorExpr:
						calls[fun.Sel.Name] = true
					}
				}

				return true
			})
		}

		require.True(t, found, "root.go must define invocationTargetAttrs")
		require.True(t, calls["OverridesFromCommand"], "the record must read the invocation's overrides")
		require.True(t, calls["ResolveConnection"], "the record must resolve the connection")
		require.False(t, calls["ResolveContext"], "the record must not call config.ResolveContext")
	})

	stored := func() *config.Context {
		return &config.Context{
			Host: "pve1.example.test", Product: config.ProductPVE,
			Auth: config.AuthBlock{Type: "token", Username: "auditor@pve", TokenID: "tok", Secret: "tok-secret"},
			SSH:  config.SSHBlock{Jump: "admin@bastion.example.test"},
		}
	}

	t.Run("an endpoint flag", func(t *testing.T) {
		records, seen, err := auditRun(t, auditConfig(t, stored()), "--api-endpoint", "pve9:9999", "probe")
		require.NoError(t, err)

		inv := findRecord(records, "invocation")
		require.NotNil(t, inv)
		require.Equal(t, "pve9", inv["host"])
		require.Equal(t, float64(9999), inv["port"])
		require.Equal(t, "pve", inv["product"])
		require.Equal(t, "auditor@pve", inv["user"])
		require.Equal(t, "admin@bastion.example.test", inv["jump"])
		require.NotContains(t, inv, "proxy_host")
		require.Equal(t, []any{"api-endpoint=flag"}, inv["overrides"])

		require.NotNil(t, seen)
		require.Zero(t, seen.Cfg.Contexts["lab"].Port, "the record must not write defaults into the stored context")
		require.Empty(t, seen.Cfg.Contexts["lab"].Realm, "the record must not write defaults into the stored context")
	})

	t.Run("a jump disabled from the environment", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "none")

		records, _, err := auditRun(t, auditConfig(t, stored()), "probe")
		require.NoError(t, err)

		inv := findRecord(records, "invocation")
		require.NotNil(t, inv)
		require.Equal(t, "pve1.example.test", inv["host"])
		require.Equal(t, float64(8006), inv["port"])
		require.Equal(t, "none", inv["jump"])
		require.Equal(t, []any{"api-jump=env"}, inv["overrides"])
	})

	t.Run("a proxy with a keychain password", func(t *testing.T) {
		ctx := stored()
		ctx.Proxy = config.ProxyBlock{
			URL: "socks5h://proxy.example.test:1080", Username: "proxyuser",
			Password: "keychain:pmx-audit-test-never-stored/proxyuser",
		}

		records, _, err := auditRun(t, auditConfig(t, ctx), "probe")
		require.NoError(t, err, "the audit path must never read the keychain")

		inv := findRecord(records, "invocation")
		require.NotNil(t, inv)
		require.Equal(t, "pve1.example.test", inv["host"])
		require.Equal(t, float64(8006), inv["port"])
		require.Equal(t, "pve", inv["product"])
		require.Equal(t, "auditor@pve", inv["user"])
		require.Equal(t, "proxy.example.test:1080", inv["proxy_host"])
		require.NotContains(t, inv, "overrides", "no override was set")
		require.NotNil(t, findRecord(records, "exit"))
	})

	t.Run("an environment proxy carrying credentials", func(t *testing.T) {
		t.Setenv("PMX_API_PROXY", "socks5h://envuser:env-s3cret@env-proxy.example.test:1080")

		records, _, err := auditRun(t, auditConfig(t, stored()), "probe")
		require.NoError(t, err)

		inv := findRecord(records, "invocation")
		require.NotNil(t, inv)
		require.Equal(t, "env-proxy.example.test:1080", inv["proxy_host"])
		require.Equal(t, []any{"api-proxy=env"}, inv["overrides"])

		for _, rec := range records {
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "env-s3cret", "the proxy password must never reach the log")
			require.NotContains(t, string(raw), "envuser", "the overrides attribute must carry no proxy value")
		}
	})

	t.Run("a stored proxy that does not parse", func(t *testing.T) {
		ctx := stored()
		ctx.Proxy = config.ProxyBlock{URL: "socks5://pmx:s3cr3t/x@proxy:1080"}

		records, _, err := auditRun(t, auditConfig(t, ctx), "probe")
		require.NoError(t, err)

		inv := findRecord(records, "invocation")
		require.NotNil(t, inv)
		require.Equal(t, "pve1.example.test", inv["host"], "a connection that does not resolve falls back")
		require.NotContains(t, inv, "proxy_host", "an unparseable proxy has no host to record")

		raw, err := json.Marshal(inv)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "s3cr3t")
	})

	// url.Parse accepts each of these, reading "pmx:4711" as the host and
	// pushing the rest of the password past a "/", "?", or "#", so a record
	// that trusted u.Host would carry the user and the password's digits.
	numericPasswordForms := []string{
		"socks5://pmx:4711/x@proxy.example.test:1080",
		"socks5://pmx:4711?x@proxy.example.test:1080",
		"socks5://pmx:4711#x@proxy.example.test:1080",
	}

	requireNoUserinfo := func(t *testing.T, records []map[string]any) {
		t.Helper()

		inv := findRecord(records, "invocation")
		require.NotNil(t, inv)
		require.NotContains(t, inv, "proxy_host", "a proxy URL with a stray \"@\" has no host to record")

		for _, rec := range records {
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "4711", "record: %s", raw)
			require.NotContains(t, string(raw), "pmx:", "record: %s", raw)
		}
	}

	for _, form := range numericPasswordForms {
		t.Run("stored "+form, func(t *testing.T) {
			ctx := stored()
			ctx.Proxy = config.ProxyBlock{URL: form}

			records, _, err := auditRun(t, auditConfig(t, ctx), "probe")
			require.NoError(t, err)
			requireNoUserinfo(t, records)
		})

		t.Run("environment "+form, func(t *testing.T) {
			t.Setenv("PMX_API_PROXY", form)

			records, _, err := auditRun(t, auditConfig(t, stored()), "probe")
			require.NoError(t, err)
			requireNoUserinfo(t, records)
		})
	}
}

// TestLogInvocationExit_MasksURLUserinfo proves that the exit record and the
// terminal both mask a URL's userinfo as well as its query parameters, and
// that none of the three proxy URL forms url.Parse rejects leaves its
// password in the invocation record or the exit record, whether it comes
// from the stored context, from $PMX_API_PROXY, or quoted whole in an error.
func TestLogInvocationExit_MasksURLUserinfo(t *testing.T) {
	forms := []struct{ url, password string }{
		{"socks5://pmx:s3cr3t/x@proxy:1080", "s3cr3t"},
		{"socks5://pmx:s3%zzt@proxy:1080", "s3%zzt"},
		{"socks5://u:s3cret@[::1", "s3cret"},
		{"socks5://pmx:4711/x@proxy:1080", "4711"},
		{"socks5://pmx:4711?x@proxy:1080", "4711"},
		{"socks5://pmx:4711#x@proxy:1080", "4711"},
	}

	requireAbsent := func(t *testing.T, records []map[string]any, password string) {
		t.Helper()

		require.NotNil(t, findRecord(records, "invocation"))
		require.NotNil(t, findRecord(records, "exit"))

		for _, rec := range records {
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			require.NotContains(t, string(raw), password, "record: %s", raw)
		}
	}

	runLogged := func(t *testing.T, cfgPath string, run func(*cobra.Command, *cli.Deps) error, args ...string) (
		[]map[string]any, string,
	) {
		t.Helper()

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("PMX_LOG_LAYOUT", "")
		t.Setenv("PMX_LOG_LEVEL", "")

		// A stored or exported form url.Parse rejects fails the client
		// build, while a form it misreads as host "pmx:4711" builds a
		// client and lets the command succeed, so only the records and
		// the terminal are checked here.
		stderr, _ := executeProbe(t, probeFactory(run), append([]string{"--config", cfgPath}, args...)...)

		return readLogRecords(t, filepath.Join(home, ".pmx", "logs")), stderr
	}

	noop := func(*cobra.Command, *cli.Deps) error { return nil }

	for _, form := range forms {
		t.Run(form.url, func(t *testing.T) {
			t.Run("stored", func(t *testing.T) {
				ctx := precedenceContext()
				ctx.Proxy.URL = form.url

				records, stderr := runLogged(t, auditConfig(t, ctx), noop, "probe")
				requireAbsent(t, records, form.password)
				require.NotContains(t, stderr, form.password)
			})

			t.Run("environment", func(t *testing.T) {
				t.Setenv("PMX_API_PROXY", form.url)

				records, stderr := runLogged(t, auditConfig(t, precedenceContext()), noop, "probe")
				requireAbsent(t, records, form.password)
				require.NotContains(t, stderr, form.password)
			})

			t.Run("quoted in an error", func(t *testing.T) {
				quoting := func(*cobra.Command, *cli.Deps) error {
					return fmt.Errorf("proxyconnect tcp: dial %s: connection refused (retry with ?password=hunter2)",
						form.url)
				}

				records, stderr := runLogged(t, auditConfig(t, precedenceContext()), quoting, "probe")
				requireAbsent(t, records, form.password)
				require.NotContains(t, stderr, form.password)

				exit := findRecord(records, "exit")
				require.NotContains(t, exit["error"], "hunter2", "query parameters stay masked")
				require.Contains(t, exit["error"], "<redacted>@")
			})
		})
	}
}

// TestExecute_ErrorTextKeepsAPIURLs proves that an API URL quoted in an
// error reaches the terminal and the exit record exactly as written when its
// path or query holds a user ID, whose "@" must not be read as the end of a
// URL password.
func TestExecute_ErrorTextKeepsAPIURLs(t *testing.T) {
	for _, text := range []string{
		`Get "https://127.0.0.1:9/api2/json/access/users/alice@pve": dial tcp 127.0.0.1:9: connect: connection refused`,
		`Delete "https://h:8006/api2/json/access/users/root@pam/token/x": EOF`,
		`Get "https://h:8006/api2/json/access/acl?userid=alice@pve": EOF`,
	} {
		t.Run(text, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PMX_LOG_LAYOUT", "")
			t.Setenv("PMX_LOG_LEVEL", "")

			stderr, err := executeProbe(t, noClientProbe(errors.New(text)),
				"--config", auditConfig(t, precedenceContext()), "probe")
			require.Error(t, err)

			firstLine, _, _ := strings.Cut(stderr, "\n")
			require.Equal(t, text, firstLine)

			exit := findRecord(readLogRecords(t, filepath.Join(home, ".pmx", "logs")), "exit")
			require.NotNil(t, exit)
			require.Equal(t, text, exit["error"])
		})
	}
}

// TestExecute_ErrorTextMasksConfiguredProxyURL proves that the proxy URL
// this invocation was configured with is masked wherever an error quotes it,
// even in the http form whose numeric password followed by "/" has the
// shape of an API URL and so escapes the free-text userinfo mask.
func TestExecute_ErrorTextMasksConfiguredProxyURL(t *testing.T) {
	const proxyURL = "http://pmx:4711/x@proxy.example.test:3128"

	quoting := noClientProbe(fmt.Errorf("proxyconnect tcp: dial %s: connection refused", proxyURL))

	run := func(t *testing.T, cfgPath string) {
		t.Helper()

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("PMX_LOG_LAYOUT", "")
		t.Setenv("PMX_LOG_LEVEL", "")

		stderr, err := executeProbe(t, quoting, "--config", cfgPath, "probe")
		require.Error(t, err)
		require.NotContains(t, stderr, "4711")
		require.Contains(t, stderr, "http://<redacted>")

		for _, rec := range readLogRecords(t, filepath.Join(home, ".pmx", "logs")) {
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "4711", "record: %s", raw)
		}
	}

	t.Run("stored", func(t *testing.T) {
		ctx := precedenceContext()
		ctx.Proxy.URL = proxyURL

		run(t, auditConfig(t, ctx))
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv("PMX_API_PROXY", proxyURL)

		run(t, auditConfig(t, precedenceContext()))
	})
}

// noClientProbe returns a group factory for "probe", a noClient command that
// consumes the connection overrides and fails with err, so a test can hand
// the root an error text without building a client first.
func noClientProbe(err error) cli.GroupFactory {
	return func(*cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true", cli.AnnotationUsesConnection: "true"},
			RunE: func(*cobra.Command, []string) error {
				return err
			},
		}
	}
}
