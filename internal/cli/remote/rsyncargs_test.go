package remote

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// --- extractPMXFlags --------------------------------------------------------

func TestExtractPMXFlags_Empty(t *testing.T) {
	vals, rest, err := extractPMXFlags(nil)
	require.NoError(t, err)
	require.Empty(t, rest)
	require.False(t, vals.Help)
	require.Empty(t, vals.Root)
	require.Empty(t, vals.SSH)
}

func TestExtractPMXFlags_ShortContextSeparateToken(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"-c", "prod", "-avz", "src", "dst"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"context": "prod"}, vals.Root)
	require.Equal(t, []string{"-avz", "src", "dst"}, rest)
}

func TestExtractPMXFlags_LongContextEquals(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"--context=prod", "src", "dst"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"context": "prod"}, vals.Root)
	require.Equal(t, []string{"src", "dst"}, rest)
}

func TestExtractPMXFlags_LongContextSeparateToken(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"--context", "prod", "src", "dst"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"context": "prod"}, vals.Root)
	require.Equal(t, []string{"src", "dst"}, rest)
}

func TestExtractPMXFlags_ConfigInsecureDebugAllForms(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{
		"--config", "/tmp/c.yml", "--insecure", "--debug", "src", "dst",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"config":   "/tmp/c.yml",
		"insecure": "true",
		"debug":    "true",
	}, vals.Root)
	require.Equal(t, []string{"src", "dst"}, rest)
}

// Every root persistent flag must be extractable here, and each row has to
// agree with the flag about whether it takes a value. One that is missing is
// handed to rsync(1) verbatim — `pmx --no-log rsync src dst` then dies with
// "rsync: --no-log: unknown option" and nothing points back at pmx. One whose
// takesValue is wrong fails later and more obscurely, either by feeding the
// literal "true" to a numeric flag or by swallowing the next argv token. The
// short forms are deliberately excluded: rsync owns -o, -v, and -t.
func TestExtractPMXFlags_CoversEveryRootPersistentFlag(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()

	extractable := map[string]*pmxFlagSpec{}
	for i := range pmxFlagTable {
		if pmxFlagTable[i].target != "root" {
			continue
		}
		for _, n := range pmxFlagTable[i].names {
			extractable[strings.TrimLeft(n, "-")] = &pmxFlagTable[i]
		}
	}

	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		// help and version are cobra's own and never reach a sub-command.
		if f.Name == "help" || f.Name == "version" {
			return
		}
		spec := extractable[f.Name]
		require.NotNil(t, spec,
			"root persistent flag --%s is not in pmxFlagTable; `pmx --%s rsync ...` "+
				"would pass it to rsync(1)", f.Name, f.Name)

		// Only a boolean flag may be extracted without consuming the token
		// after it; everything else needs its value.
		require.Equal(t, f.Value.Type() != "bool", spec.takesValue,
			"pmxFlagTable row for --%s has takesValue %t, but the flag is a %s",
			f.Name, spec.takesValue, f.Value.Type())
	})
}

// The extracted names have to be settable on the root's flag set, or applying
// them panics or silently no-ops at run time.
func TestExtractPMXFlags_RootDestinationsExistOnRoot(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()

	for _, spec := range pmxFlagTable {
		if spec.target != "root" {
			continue
		}
		require.NotNil(t, root.PersistentFlags().Lookup(spec.dest),
			"pmxFlagTable dest %q is not a root persistent flag", spec.dest)
	}
}

func TestExtractPMXFlags_SSHFlagsLongOnly(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{
		"--ssh-user", "admin", "--ssh-port=2222", "--ssh-identity", "/k",
		"--ssh-agent", "--no-strict", "-avz", "src", "dst",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"ssh-user":     "admin",
		"ssh-port":     "2222",
		"ssh-identity": "/k",
		"ssh-agent":    "true",
		"no-strict":    "true",
	}, vals.SSH)
	require.Equal(t, []string{"-avz", "src", "dst"}, rest)
}

func TestExtractPMXFlags_StopsAtFirstUnrecognizedToken(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"-avz", "--context", "prod", "src", "dst"})
	require.NoError(t, err)
	require.Empty(t, vals.Root, "extraction is front-only; -avz ends it before --context is seen")
	require.Equal(t, []string{"-avz", "--context", "prod", "src", "dst"}, rest)
}

func TestExtractPMXFlags_StopsAtBareDoubleDash(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"--context", "prod", "--", "src", "dst"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"context": "prod"}, vals.Root)
	require.Equal(t, []string{"--", "src", "dst"}, rest)
}

func TestExtractPMXFlags_HelpShortAbortsImmediately(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"-h", "src", "dst"})
	require.NoError(t, err)
	require.True(t, vals.Help)
	require.Equal(t, []string{"-h", "src", "dst"}, rest)
}

func TestExtractPMXFlags_HelpLongAfterOtherFlags(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"-c", "prod", "--help"})
	require.NoError(t, err)
	require.True(t, vals.Help)
	require.Equal(t, []string{"--help"}, rest)
}

func TestExtractPMXFlags_MissingValueErrors(t *testing.T) {
	_, _, err := extractPMXFlags([]string{"--ssh-port"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--ssh-port")
}

func TestExtractPMXFlags_BoolFlagAcceptsInlineTrue(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"--insecure=true", "src", "dst"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"insecure": "true"}, vals.Root)
	require.Equal(t, []string{"src", "dst"}, rest)
}

func TestExtractPMXFlags_BoolFlagAcceptsInlineFalse(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"--debug=false", "src", "dst"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"debug": "false"}, vals.Root)
	require.Equal(t, []string{"src", "dst"}, rest)
}

func TestExtractPMXFlags_BoolFlagRejectsInlineGarbage(t *testing.T) {
	_, _, err := extractPMXFlags([]string{"--insecure=garbage"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--insecure")
	require.Contains(t, err.Error(), "garbage")
}

// --- M-1: value-taking flag whose extracted value looks like another flag --

func TestExtractPMXFlags_ContextValueLooksLikeFlagGetsTargetedError(t *testing.T) {
	_, _, err := extractPMXFlags([]string{"-c", "-av", "src", "dst"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `"-av"`)
	require.Contains(t, err.Error(), "--checksum")
}

func TestExtractPMXFlags_LongContextValueLooksLikeFlagGetsTargetedError(t *testing.T) {
	_, _, err := extractPMXFlags([]string{"--context", "-av"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `"-av"`)
	require.Contains(t, err.Error(), "--checksum")
}

func TestExtractPMXFlags_ContextEqualsValueLooksLikeFlagGetsTargetedError(t *testing.T) {
	_, _, err := extractPMXFlags([]string{"--context=-av"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `"-av"`)
}

func TestExtractPMXFlags_NonContextValueLooksLikeFlagGetsGenericError(t *testing.T) {
	_, _, err := extractPMXFlags([]string{"--ssh-user", "-x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--ssh-user")
	require.Contains(t, err.Error(), `"-x"`)
	require.NotContains(t, err.Error(), "--checksum")
}

func TestExtractPMXFlags_ConfigValueLooksLikeFlagGetsGenericError(t *testing.T) {
	_, _, err := extractPMXFlags([]string{"--config", "--insecure"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--config")
	require.Contains(t, err.Error(), `"--insecure"`)
}

// TestExtractPMXFlags_APIConnectionFlags proves that `pmx --api-endpoint h
// rsync src dst` hands the flag to pmx rather than to rsync(1), and that
// every one of the nine connection flags is extracted in both of its forms,
// with the boolean one consuming no value.
func TestExtractPMXFlags_APIConnectionFlags(t *testing.T) {
	vals, rest, err := extractPMXFlags([]string{"--api-endpoint", "h", "src", "dst"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"api-endpoint": "h"}, vals.Root)
	require.Empty(t, vals.SSH)
	require.Equal(t, []string{"src", "dst"}, rest, "rsync must receive neither the flag nor its value")

	values := map[string]string{
		"api-endpoint":              "https://pve2:8006",
		"api-jump":                  "admin@bastion:2222",
		"api-proxy":                 "socks5h://proxy.test:1080",
		"api-ca-cert":               "/etc/pmx/ca.pem",
		"api-fingerprint":           "AA:BB",
		"api-connect-timeout":       "5s",
		"api-tls-handshake-timeout": "10s",
		"api-request-timeout":       "30s",
	}

	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			for _, args := range [][]string{
				{"--" + name, value, "-avz", "src", "dst"},
				{"--" + name + "=" + value, "-avz", "src", "dst"},
			} {
				vals, rest, err := extractPMXFlags(args)
				require.NoError(t, err, "%v", args)
				require.Equal(t, map[string]string{name: value}, vals.Root, "%v", args)
				require.Equal(t, []string{"-avz", "src", "dst"}, rest, "%v", args)
			}
		})
	}

	t.Run("api-proxy-from-env", func(t *testing.T) {
		vals, rest, err := extractPMXFlags([]string{"--api-proxy-from-env", "src", "dst"})
		require.NoError(t, err)
		require.Equal(t, map[string]string{"api-proxy-from-env": "true"}, vals.Root)
		require.Equal(t, []string{"src", "dst"}, rest, "the boolean flag must not swallow the next operand")

		vals, rest, err = extractPMXFlags([]string{"--api-proxy-from-env=false", "src", "dst"})
		require.NoError(t, err)
		require.Equal(t, map[string]string{"api-proxy-from-env": "false"}, vals.Root)
		require.Equal(t, []string{"src", "dst"}, rest)
	})

	t.Run("a value that looks like an rsync flag is refused", func(t *testing.T) {
		_, _, err := extractPMXFlags([]string{"--api-jump", "-avz", "src", "dst"})
		require.EqualError(t, err, `--api-jump expects a value, got "-avz"`)
	})
}

// TestRsync_APIEndpointFlagReachesTheRootNotRsync runs `pmx --api-endpoint
// <addr> rsync ...` through the real root and proves that the value lands on
// the root's persistent flag, where Deps.ConnectionOverrides reads it as
// typed on the command line, and that rsync(1) never sees it. The endpoint
// is the fake server's own address, so the invocation reaches the same
// server whether or not the root's client honours the override.
func TestRsync_APIEndpointFlagReachesTheRootNotRsync(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	var deps *cli.Deps
	std := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := std(cmd, args); err != nil {
			return err
		}
		deps = cli.GetDeps(cmd)
		return nil
	}

	endpoint := f.Server.Listener.Addr().String()
	root.SetArgs(append(prefix, "--api-endpoint", endpoint, "rsync", "-avz", "pve1:/etc", "./dst"))
	require.NoError(t, root.Execute())

	require.Len(t, runner.Calls, 1)
	for _, arg := range runner.Calls[0].Args {
		require.NotEqual(t, "--api-endpoint", arg, "rsync(1) must not receive pmx's flag")
		require.NotEqual(t, endpoint, arg, "rsync(1) must not receive the flag's value")
	}

	require.NotNil(t, deps)
	ov, err := deps.ConnectionOverrides()
	require.NoError(t, err)
	require.Equal(t, "--api-endpoint", ov.EndpointSource)

	host, port, err := net.SplitHostPort(endpoint)
	require.NoError(t, err)
	require.Equal(t, host, ov.Host)
	require.Equal(t, port, strconv.Itoa(ov.Port))
}

// --- classifyRsyncArgs / classifyOperand -----------------------------------

func TestClassifyRsyncArgs_SimpleNodePath(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"-avz", "./site/", "pve1:/var/www/"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 2)
	require.False(t, ops[0].Remote)
	require.True(t, ops[1].Remote)
	require.Equal(t, "", ops[1].User)
	require.Equal(t, "/var/www/", ops[1].Path)
}

func TestClassifyRsyncArgs_ExplicitUser(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"admin@pve1:/etc", "./dst"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Equal(t, "admin", ops[0].User)
	require.Equal(t, "/etc", ops[0].Path)
}

func TestClassifyRsyncArgs_LocalPathWithColonAfterSlashIsNotRemote(t *testing.T) {
	ops, _, err := classifyRsyncArgs([]string{"./x:y", "pve1:/dst"})
	require.NoError(t, err)
	require.False(t, ops[0].Remote, "colon after the first slash is part of the path, not a host separator")
	require.Equal(t, "./x:y", ops[0].Path)
}

func TestClassifyRsyncArgs_RsyncDaemonURLIsSkipped(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"rsync://otherhost/mod/path", "pve1:/dst"})
	require.NoError(t, err)
	require.False(t, ops[0].Remote)
	require.Equal(t, "rsync://otherhost/mod/path", ops[0].Path)
	require.Equal(t, "pve1", node)
}

func TestClassifyRsyncArgs_BracketedIPv6Literal(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"admin@[fe80::1]:/etc", "./dst"})
	require.NoError(t, err)
	require.Equal(t, "fe80::1", node)
	require.True(t, ops[0].Remote)
	require.Equal(t, "admin", ops[0].User)
	require.Equal(t, "fe80::1", ops[0].Node)
	require.Equal(t, "/etc", ops[0].Path)
}

func TestClassifyRsyncArgs_MultipleSourcesSameNodeOK(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"pve1:/etc", "pve1:/var/log", "./dst/"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 3)
	require.True(t, ops[0].Remote)
	require.True(t, ops[1].Remote)
	require.False(t, ops[2].Remote)
}

func TestClassifyRsyncArgs_CrossNodeRejected(t *testing.T) {
	_, _, err := classifyRsyncArgs([]string{"pve1:/etc", "pve2:/etc", "./dst"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pve1")
	require.Contains(t, err.Error(), "pve2")
}

func TestClassifyRsyncArgs_NoRemoteOperandErrors(t *testing.T) {
	_, _, err := classifyRsyncArgs([]string{"-avz", "./src/", "./dst/"})
	require.Error(t, err)
}

func TestClassifyRsyncArgs_UserSuppliedDashERejected(t *testing.T) {
	_, _, err := classifyRsyncArgs([]string{"-avz", "-e", "ssh -p 2200", "pve1:/etc", "./dst"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reserved")
}

func TestClassifyRsyncArgs_UserSuppliedDashERejected_Attached(t *testing.T) {
	_, _, err := classifyRsyncArgs([]string{"-essh", "pve1:/etc", "./dst"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reserved")
}

func TestClassifyRsyncArgs_UserSuppliedLongRshRejected(t *testing.T) {
	_, _, err := classifyRsyncArgs([]string{"--rsh=ssh", "pve1:/etc", "./dst"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reserved")
}

func TestClassifyRsyncArgs_ClusteredDashERejected(t *testing.T) {
	_, _, err := classifyRsyncArgs([]string{"-ae", "ssh", "pve1:/etc", "./dst"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reserved")
}

// --- H-1: value-taking rsync option arity ------------------------------------

// TestClassifyRsyncArgs_ChownSpaceFormValueNotMisreadAsOperand is the
// regression test for the first H-1 bug: "--chown a:b" used to have its
// value scanned as a second operand, producing a bogus "different nodes"
// error since "www-data:www-data" doesn't match pve1's node.
func TestClassifyRsyncArgs_ChownSpaceFormValueNotMisreadAsOperand(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"-a", "--chown", "www-data:www-data", "./src", "pve1:/dst"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 2, "the --chown value must not be scanned as a third operand")
	require.False(t, ops[0].Remote)
	require.Equal(t, "./src", ops[0].Path)
	require.True(t, ops[1].Remote)
}

// TestClassifyRsyncArgs_ExcludeSpaceFormValueNotRewritten is the regression
// test for the second H-1 bug: "--exclude pve1:secret" used to have its
// value classified as a plain (non-node-matching) operand, or — worse, if it
// happened to match the agreed node — silently rewritten into a
// "root@<resolved-ip>:secret" rsync operand.
func TestClassifyRsyncArgs_ExcludeSpaceFormValueNotRewritten(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"--exclude", "pve1:secret", "./src", "pve1:/dst"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 2, "the --exclude value must not be scanned as an operand at all")
	require.False(t, ops[0].Remote)
	require.True(t, ops[1].Remote)
}

// TestClassifyRsyncArgs_ExcludeEqualsFormValueNotRewritten covers the
// "--exclude=pve1:secret" inline form: it is never rewritten too, but for a
// different reason — the whole "--opt=value" token is a single "-"-prefixed
// token that classifyRsyncArgs skips outright, regardless of the arity
// table, so it never reaches classifyOperand in the first place.
func TestClassifyRsyncArgs_ExcludeEqualsFormValueNotRewritten(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"--exclude=pve1:secret", "./src", "pve1:/dst"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 2)
}

// TestClassifyRsyncArgs_ClusteredBlockSizeConsumesSeparateValue covers
// "-avB 1024": -B is the last (value-taking) letter in the cluster, so the
// following "1024" is its separate-token value and must not be classified as
// an operand.
func TestClassifyRsyncArgs_ClusteredBlockSizeConsumesSeparateValue(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"-avB", "1024", "./src", "pve1:/dst"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 2, "\"1024\" (-B's separate value) must not become a third operand")
}

// TestClassifyRsyncArgs_ClusteredAttachedValueDoesNotConsumeNextToken covers
// "-aB1024": the value is attached directly after -B in the same token, so
// nothing extra is consumed and the token following it is a real operand.
func TestClassifyRsyncArgs_ClusteredAttachedValueDoesNotConsumeNextToken(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"-aB1024", "./src", "pve1:/dst"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 2)
}

// TestClassifyRsyncArgs_TokensAfterDoubleDashAreForcedOperands covers rsync's
// (popt's) own "--" convention: every token after a bare "--" is a literal
// filename, even one that starts with "-" and would otherwise look like a
// flag (or even the reserved -e).
func TestClassifyRsyncArgs_TokensAfterDoubleDashAreForcedOperands(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"-avz", "--", "-oddname", "pve1:/dst"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 2)
	require.False(t, ops[0].Remote)
	require.Equal(t, "-oddname", ops[0].Path)
}

// TestClassifyRsyncArgs_ReservedDashEAfterDoubleDashIsALiteralFilename proves
// the reserved-flag check itself is suspended once "--" has been seen: "-e"
// here is a filename, not rsync's -e/--rsh option.
func TestClassifyRsyncArgs_ReservedDashEAfterDoubleDashIsALiteralFilename(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"--", "-e", "pve1:/dst"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 2)
	require.False(t, ops[0].Remote)
	require.Equal(t, "-e", ops[0].Path)
}

// TestClassifyRsyncArgs_UnknownLongOptionValueStillMisclassifiable pins the
// documented limitation: an unrecognised long option's separate-token value
// is still scanned as a standalone operand on the next iteration, and gets
// misclassified as remote if it happens to match the host:path shape.
// --opt=value avoids this; see rsyncLongValueOptsWithSeparateForm's doc.
func TestClassifyRsyncArgs_UnknownLongOptionValueStillMisclassifiable(t *testing.T) {
	ops, node, err := classifyRsyncArgs([]string{"--some-unlisted-opt", "pve1:oops", "./src", "pve1:/dst"})
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.Len(t, ops, 3, "the unlisted option's value is (mis)scanned as a third operand")
	require.True(t, ops[0].Remote, "known limitation: it matches the host:path shape and is treated as remote")
	require.Equal(t, "oops", ops[0].Path)
}

// --- findHostSplit -----------------------------------------------------------

func TestFindHostSplit(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		wantOK bool
		wantAt int
	}{
		{"no colon", "localonly", false, 0},
		{"colon before slash", "pve1:/etc", true, 4},
		{"slash before colon is local", "./x:y", false, 0},
		{"bracketed ipv6", "[fe80::1]:/etc", true, 9},
		{"bracket without trailing colon is local", "[fe80::1]", false, 0},
		{"empty", "", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx, ok := findHostSplit(tc.in)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.Equal(t, tc.wantAt, idx)
			}
		})
	}
}

// A negative number after a value-taking flag is a value, not a misplaced
// rsync flag: no rsync option looks like "-5", and the root's own validation
// of --wait-timeout is the message the operator should see for it.
func TestExtractPMXFlags_NegativeNumberIsAValueNotAFlag(t *testing.T) {
	for _, args := range [][]string{{"--wait-timeout=-5"}, {"--wait-timeout", "-5"}} {
		vals, rest, err := extractPMXFlags(args)
		require.NoError(t, err, "%v", args)
		require.Equal(t, "-5", vals.Root["wait-timeout"])
		require.Empty(t, rest)
	}
}

func TestExtractPMXFlags_NonIntegerValueStillLooksLikeAFlag(t *testing.T) {
	for _, value := range []string{"-av", "-5.5", "-1e3", "-Inf"} {
		_, _, err := extractPMXFlags([]string{"--wait-timeout", value})
		require.Error(t, err, value)
		require.Contains(t, err.Error(), "--wait-timeout")
		require.Contains(t, err.Error(), `"`+value+`"`)
	}
}

// The exception belongs to --wait-timeout alone. rsync really has a -8
// option, and a negative port is not something ssh should be the one to
// reject, so the other value-taking flags keep the misplaced-flag guard.
func TestExtractPMXFlags_NegativeNumberIsStillAFlagForOtherFlags(t *testing.T) {
	for _, args := range [][]string{{"-c", "-8"}, {"--ssh-port", "-5"}, {"--node", "-1"}} {
		_, _, err := extractPMXFlags(args)
		require.Error(t, err, "%v", args)
		require.Contains(t, err.Error(), args[0])
	}
}
