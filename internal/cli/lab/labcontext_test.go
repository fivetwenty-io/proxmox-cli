package lab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestLabContextName verifies derived context name formation.
func TestLabContextName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"alpha", "lab-alpha"},
		{"beta", "lab-beta"},
		{"test-lab", "lab-test-lab"},
		{"x", "lab-x"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := labContextName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestLabKeychainService verifies keychain service name formation.
func TestLabKeychainService(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"alpha", "pmx-lab-alpha"},
		{"beta", "pmx-lab-beta"},
		{"test-lab", "pmx-lab-test-lab"},
		{"x", "pmx-lab-x"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := labKeychainService(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestLabCtxConstants verifies lab context user and token name constants.
func TestLabCtxConstants(t *testing.T) {
	assert.Equal(t, "pmx@pve", labCtxUser)
	assert.Equal(t, "pmx", labCtxTokenName)
}

// TestLabCtxAccount verifies account identifier formation.
func TestLabCtxAccount(t *testing.T) {
	result := labCtxAccount()
	assert.Equal(t, "pmx@pve!pmx", result)
}

// TestParseTokenAddValue_Success verifies extraction of value from JSON
// output: {"value":"<secret>"}.
func TestParseTokenAddValue_Success(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"simple value",
			`{"value":"secret123"}`,
			"secret123",
		},
		{
			"value with special chars",
			`{"value":"abc!@#$%^&*()_+-=[]{}|;:,.<>?"}`,
			"abc!@#$%^&*()_+-=[]{}|;:,.<>?",
		},
		{
			"value with whitespace",
			`{"value":"sec ret 123"}`,
			"sec ret 123",
		},
		{
			"long value",
			`{"value":"` + strings.Repeat("a", 256) + `"}`,
			strings.Repeat("a", 256),
		},
		{
			"JSON with extra whitespace",
			`{ "value" : "secret123" }`,
			"secret123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTokenAddValue(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestParseTokenAddValue_InvalidJSON verifies errors on malformed JSON.
func TestParseTokenAddValue_InvalidJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"not JSON", "not json"},
		{"unclosed brace", `{"value":"secret`},
		{"empty string", ""},
		{"just whitespace", "   "},
		{"incomplete object", "{"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTokenAddValue(tt.input)
			require.Error(t, err)
		})
	}
}

// TestParseTokenAddValue_MissingValue verifies errors when JSON lacks value
// key or field is null.
func TestParseTokenAddValue_MissingValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"missing value key", `{"data":"secret"}`},
		{"null value", `{"value":null}`},
		{"empty value", `{"value":""}`},
		{"empty object", `{}`},
		{"value is object", `{"value":{}}`},
		{"value is array", `{"value":[]}`},
		{"value is number", `{"value":123}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTokenAddValue(tt.input)
			require.Error(t, err)
		})
	}
}

// TestNormalizeFingerprint_Success verifies stripping of the sha256
// Fingerprint= prefix and validation of colon-hex SHA-256 format.
func TestNormalizeFingerprint_Success(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"with sha256 Fingerprint prefix",
			"sha256 Fingerprint=ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
			"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		},
		{
			"SHA256 uppercase",
			"SHA256 Fingerprint=AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90",
			"AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90",
		},
		{
			"just the hash without prefix",
			"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
			"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		},
		{
			"mixed case hex",
			"sha256 Fingerprint=aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90",
			"aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := normalizeFingerprint(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNormalizeFingerprint_InvalidFormat verifies errors on invalid
// fingerprint format: non-colon-separated hex, invalid hex chars, wrong
// length, missing or extra colons.
func TestNormalizeFingerprint_InvalidFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no colons", "abcdef123456"},
		{
			"invalid hex chars",
			"ab:cd:ef:12:34:56:78:90:xz:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		},
		{
			"too many colons",
			"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab",
		},
		{"too few colons", "ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef"},
		{
			"space instead of colon",
			"ab cd ef 12 34 56 78 90 ab cd ef 12 34 56 78 90 ab cd ef 12 34 56 78 90 ab cd ef 12 34 56 78 90",
		},
		{"single digit parts", "a:b:c:d:e:f:1:2:3:4:5:6:7:8:9:0:a:b:c:d:e:f:1:2:3:4:5:6:7:8:9:0"},
		{
			"three digit parts",
			"abc:def:123:456:789:012:345:678:90a:bcd:ef1:234:567:890:abc:def:123:456:789:012:345:678:90a:" +
				"bcd:ef1:234:567:890:abc",
		},
		{"missing prefix but wrong format", "sha256 Fingerprint=not-hex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeFingerprint(tt.input)
			require.Error(t, err, "expected error for input: %q", tt.input)
		})
	}
}

// TestFingerprintRE_Matches verifies the regexp matches valid SHA-256
// fingerprints in colon-hex format.
func TestFingerprintRE_Matches(t *testing.T) {
	tests := []string{
		"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		"AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90",
		"00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00",
		"ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			assert.True(t, fingerprintRE.MatchString(tt),
				"expected fingerprintRE to match %q", tt)
		})
	}
}

// TestFingerprintRE_Rejects verifies the regexp rejects invalid formats.
func TestFingerprintRE_Rejects(t *testing.T) {
	tests := []string{
		"",
		"ab",
		"ab:cd",
		"ab:cd:ef:12:34:56:78:90",
		"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78",
		"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:99",
		"xb:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		"ab-cd-ef-12-34-56-78-90-ab-cd-ef-12-34-56-78-90-ab-cd-ef-12-34-56-78-90-ab-cd-ef-12-34-56-78-90",
		" ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90 ",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			assert.False(t, fingerprintRE.MatchString(tt),
				"expected fingerprintRE to reject %q", tt)
		})
	}
}

// TestLabEnsureUser verifies that labEnsureUser tolerates an existing user
// but aborts on transport failure.
func TestLabEnsureUser(t *testing.T) {
	tests := []struct {
		name     string
		response exec.FakeResponse
		wantErr  bool
	}{
		{
			"success - user created",
			exec.FakeResponse{Stdout: "", ExitCode: 0},
			false,
		},
		{
			"user already exists (exit code 1)",
			exec.FakeResponse{Stdout: "", ExitCode: 1},
			false,
		},
		{
			"user already exists (exit code 2)",
			exec.FakeResponse{Stdout: "", ExitCode: 2},
			false,
		},
		{
			"transport failure",
			exec.FakeResponse{Err: fmt.Errorf("ssh: command not found")},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := exec.Fake(tt.response)
			deps := &cli.Deps{
				Runner: fake,
				Ctx:    &config.Context{SSH: config.SSHBlock{}},
			}
			err := labEnsureUser(deps, "192.0.2.1")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NotEmpty(t, fake.Calls)
			assert.Contains(t, fake.Calls[0].Args, "pveum user add pmx@pve")
		})
	}
}

// TestLabEnsureACL verifies that labEnsureACL applies the Administrator ACL on
// / for pmx@pve. `pveum acl modify` is idempotent (exits 0 on re-apply), so any
// non-zero exit or transport failure is a real error.
func TestLabEnsureACL(t *testing.T) {
	tests := []struct {
		name     string
		response exec.FakeResponse
		wantErr  bool
	}{
		{
			"success - ACL applied",
			exec.FakeResponse{Stdout: "", ExitCode: 0},
			false,
		},
		{
			"non-zero exit is an error",
			exec.FakeResponse{Stdout: "", ExitCode: 1},
			true,
		},
		{
			"transport failure",
			exec.FakeResponse{Err: fmt.Errorf("connection refused")},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := exec.Fake(tt.response)
			deps := &cli.Deps{
				Runner: fake,
				Ctx:    &config.Context{SSH: config.SSHBlock{}},
			}
			err := labEnsureACL(deps, "192.0.2.1")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NotEmpty(t, fake.Calls)
			assert.Contains(t, fake.Calls[0].Args,
				"pveum acl modify / --users pmx@pve --roles Administrator")
		})
	}
}

// TestLabMintToken verifies token removal and addition, including removal
// tolerance for non-existent tokens and parsing of the token secret.
func TestLabMintToken(t *testing.T) {
	tests := []struct {
		name       string
		responses  []exec.FakeResponse
		wantSecret string
		wantErr    bool
	}{
		{
			"success - token removed and added",
			[]exec.FakeResponse{
				{Stdout: "", ExitCode: 0},
				{Stdout: `{"value":"test-secret-123"}`, ExitCode: 0},
			},
			"test-secret-123",
			false,
		},
		{
			"success - token not found, then added",
			[]exec.FakeResponse{
				{Stdout: "", ExitCode: 1},
				{Stdout: `{"value":"new-secret-456"}`, ExitCode: 0},
			},
			"new-secret-456",
			false,
		},
		{
			"failure - remove transport error",
			[]exec.FakeResponse{
				{Err: fmt.Errorf("connection refused")},
			},
			"",
			true,
		},
		{
			"failure - add fails with non-zero exit",
			[]exec.FakeResponse{
				{Stdout: "", ExitCode: 0},
				{Stdout: "", ExitCode: 1},
			},
			"",
			true,
		},
		{
			"failure - add returns invalid JSON",
			[]exec.FakeResponse{
				{Stdout: "", ExitCode: 0},
				{Stdout: "not json", ExitCode: 0},
			},
			"",
			true,
		},
		{
			"failure - add returns empty secret",
			[]exec.FakeResponse{
				{Stdout: "", ExitCode: 0},
				{Stdout: `{"value":""}`, ExitCode: 0},
			},
			"",
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := exec.Fake(tt.responses...)
			deps := &cli.Deps{
				Runner: fake,
				Ctx:    &config.Context{SSH: config.SSHBlock{}},
			}
			secret, err := labMintToken(deps, "192.0.2.1")

			require.NotEmpty(t, fake.Calls)
			assert.Contains(t, fake.Calls[0].Args, "pveum user token remove pmx@pve pmx")
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantSecret, secret)
				require.Len(t, fake.Calls, 2)
				assert.Contains(t, fake.Calls[1].Args,
					"pveum user token add pmx@pve pmx --privsep 0 --output-format json")
			}
		})
	}
}

// TestLabFetchFingerprint verifies that the fingerprint is fetched and
// normalized correctly.
func TestLabFetchFingerprint(t *testing.T) {
	testFP := "ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90"
	tests := []struct {
		name            string
		response        exec.FakeResponse
		wantFingerprint string
		wantErr         bool
	}{
		{
			"success - with prefix",
			exec.FakeResponse{Stdout: "sha256 Fingerprint=" + testFP, ExitCode: 0},
			testFP,
			false,
		},
		{
			"success - without prefix",
			exec.FakeResponse{Stdout: testFP, ExitCode: 0},
			testFP,
			false,
		},
		{
			"failure - invalid fingerprint format",
			exec.FakeResponse{Stdout: "not-a-fingerprint", ExitCode: 0},
			"",
			true,
		},
		{
			"failure - command fails",
			exec.FakeResponse{Stdout: "", ExitCode: 1},
			"",
			true,
		},
		{
			"failure - transport error",
			exec.FakeResponse{Err: fmt.Errorf("ssh failed")},
			"",
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := exec.Fake(tt.response)
			deps := &cli.Deps{
				Runner: fake,
				Ctx:    &config.Context{SSH: config.SSHBlock{}},
			}
			fp, err := labFetchFingerprint(deps, "192.0.2.1")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantFingerprint, fp)
			}
			require.NotEmpty(t, fake.Calls)
			assert.Contains(t, fake.Calls[0].Args,
				"openssl x509 -noout -fingerprint -sha256 -in /etc/pve/local/pve-ssl.pem")
		})
	}
}

// TestLabFetchHostname verifies that the hostname is fetched correctly.
func TestLabFetchHostname(t *testing.T) {
	tests := []struct {
		name         string
		response     exec.FakeResponse
		wantHostname string
		wantErr      bool
	}{
		{
			"success - simple hostname",
			exec.FakeResponse{Stdout: "pve-lab-node1\n", ExitCode: 0},
			"pve-lab-node1",
			false,
		},
		{
			"success - hostname with spaces (trimmed)",
			exec.FakeResponse{Stdout: "  pve-lab-node1  \n", ExitCode: 0},
			"pve-lab-node1",
			false,
		},
		{
			"failure - command fails",
			exec.FakeResponse{Stdout: "", ExitCode: 1},
			"",
			true,
		},
		{
			"failure - transport error",
			exec.FakeResponse{Err: fmt.Errorf("ssh failed")},
			"",
			true,
		},
		{
			"failure - empty hostname",
			exec.FakeResponse{Stdout: "\n", ExitCode: 0},
			"",
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := &cli.Deps{
				Runner: exec.Fake(tt.response),
				Ctx:    &config.Context{SSH: config.SSHBlock{}},
			}
			hostname, err := labFetchHostname(deps, "192.0.2.1")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantHostname, hostname)
			}
		})
	}
}

// assertAnErr is a non-ExitError so guestCommandTransportFailed treats it as a
// transport failure (ssh could not connect), the shape labWaitForSSH retries.
type assertAnErr struct{}

func (assertAnErr) Error() string { return "dial tcp: connection refused" }

// waitDeps builds a *cli.Deps whose runner replays the given fake responses.
func waitDeps(responses ...exec.FakeResponse) *cli.Deps {
	return &cli.Deps{
		Runner: exec.Fake(responses...),
		Ctx:    &config.Context{SSH: config.SSHBlock{}},
	}
}

// TestLabWaitForSSH_SucceedsWhenProbeOK verifies the loop returns nil as soon
// as the hostname probe connects.
func TestLabWaitForSSH_SucceedsWhenProbeOK(t *testing.T) {
	deps := waitDeps(exec.FakeResponse{Stdout: "node0\n"})
	require.NoError(t, labWaitForSSH(context.Background(), deps, "10.10.1.10"))
}

// TestLabWaitForSSH_RetriesTransportThenSucceeds verifies the loop retries on
// transport failures and returns success once a probe connects.
func TestLabWaitForSSH_RetriesTransportThenSucceeds(t *testing.T) {
	deps := waitDeps(
		exec.FakeResponse{Err: assertAnErr{}},
		exec.FakeResponse{Err: assertAnErr{}},
		exec.FakeResponse{Stdout: "node0\n"},
	)
	require.NoError(t, labWaitForSSH(context.Background(), deps, "10.10.1.10"))
}

// TestLabWaitForSSH_HonorsCancelledContext verifies the loop stops on a
// cancelled context rather than exhausting its attempt budget. Every probe
// fails as a transport error, so the loop must rely on ctx.Err(), not on a
// probe eventually succeeding.
func TestLabWaitForSSH_HonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deps := waitDeps(exec.FakeResponse{Err: assertAnErr{}})
	require.Error(t, labWaitForSSH(ctx, deps, "10.10.1.10"))
}

// init disables the actual sleep in labSSHPollSleep during tests so the wait
// loop runs at full speed without blocking.
func init() {
	labSSHPollSleep = func(time.Duration) {}
}

// syncTestDeps builds a *cli.Deps with a temp config path, a fake ssh runner,
// and an active context (for guest-ssh defaults). It overrides the network
// probe and keychain-store seams so no live API call or real keychain write
// happens, restoring them via t.Cleanup.
func syncTestDeps(t *testing.T, fake *exec.FakeRunner) (*cobra.Command, *cli.Deps) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("contexts: {}\n"), 0o600))
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	deps := &cli.Deps{
		Cfg:        cfg,
		ConfigPath: cfgPath,
		Out:        output.New(),
		Format:     output.FormatPlain,
		Runner:     fake,
		Ctx:        &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}

	origProbe, origStore := labProbeContextVersion, labStoreSecretFn
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error { return nil }
	labStoreSecretFn = func(_ *cobra.Command, _ *cli.Deps, service, account, _ string) (string, error) {
		return "keychain:" + service + "/" + account, nil
	}
	t.Cleanup(func() { labProbeContextVersion, labStoreSecretFn = origProbe, origStore })

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return cmd, deps
}

func TestSyncLabContext_FreshMintsAndWritesContext(t *testing.T) {
	fp := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	fake := exec.Fake(
		exec.FakeResponse{}, // ensure user
		exec.FakeResponse{}, // ensure ACL
		exec.FakeResponse{}, // token remove
		exec.FakeResponse{Stdout: `{"value":"the-secret"}`},          // token add
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + fp + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                    // hostname
	)
	cmd, deps := syncTestDeps(t, fake)
	lab := multiNodeTestLab("demo", 1, "never")
	// multiNodeTestLab (via cleanLab) does not set Name; syncLabContext is
	// called directly here (no config-load round trip through the loader's
	// map-key defaulting), so Name must be set explicitly, matching the
	// pattern used elsewhere in this package (resolve_test.go, scale_test.go).
	lab.Name = "demo"

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.NoError(t, err)
	assert.Equal(t, "lab-demo", res.ContextName)
	assert.True(t, res.Rotated)

	ctx := deps.Cfg.Contexts["lab-demo"]
	require.NotNil(t, ctx)
	assert.Equal(t, "10.10.1.10", ctx.Host)
	assert.Equal(t, "keychain:pmx-lab-demo/pmx@pve!pmx", ctx.Auth.Secret)
	assert.Equal(t, fp, ctx.TLS.Fingerprint)
	assert.Equal(t, "lab-demo-0", ctx.DefaultNode)

	// Persisted to disk.
	reloaded, err := config.Load(deps.ConfigPath)
	require.NoError(t, err)
	assert.NotNil(t, reloaded.Contexts["lab-demo"])
}

func TestSyncLabContext_ReusesValidStoredSecret(t *testing.T) {
	// Pre-seed a working context; the version probe returns nil (valid), so
	// the token must NOT be rotated (no token add call).
	fp := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	fake := exec.Fake(
		exec.FakeResponse{}, // ensure user
		exec.FakeResponse{}, // ensure ACL
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + fp + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                    // hostname
	)
	cmd, deps := syncTestDeps(t, fake)
	deps.Cfg.Contexts["lab-demo"] = &config.Context{
		Host: "10.10.1.10", Port: 8006, Protocol: "https", Product: config.ProductPVE,
		Auth: config.AuthBlock{Type: "token", Username: "pmx@pve", TokenID: "pmx",
			Secret: "keychain:pmx-lab-demo/pmx@pve!pmx"},
	}
	lab := multiNodeTestLab("demo", 1, "never")
	// multiNodeTestLab (via cleanLab) does not set Name; syncLabContext is
	// called directly here (no config-load round trip through the loader's
	// map-key defaulting), so Name must be set explicitly, matching the
	// pattern used elsewhere in this package (resolve_test.go, scale_test.go).
	lab.Name = "demo"

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.NoError(t, err)
	assert.False(t, res.Rotated, "a valid stored secret must not be rotated")
	for _, c := range fake.Calls {
		for _, a := range c.Args {
			assert.NotContains(t, a, "token add", "must not mint when the stored secret is valid")
		}
	}
}

func mintedAToken(fake *exec.FakeRunner) bool {
	for _, c := range fake.Calls {
		for _, a := range c.Args {
			if strings.Contains(a, "token add") {
				return true
			}
		}
	}
	return false
}

func TestSyncLabContext_RefusesReuseWhenIdentityDiffers(t *testing.T) {
	// A same-named context whose stored identity is NOT this lab's pmx@pve!pmx
	// must never have its secret reused: UpsertLabContext would rewrite the
	// identity to pmx@pve!pmx and pair it with the foreign secret. The probe
	// would pass (syncTestDeps stubs it to nil), so only the identity guard
	// prevents the mistaken reuse — the token must be freshly minted.
	fp := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	fake := exec.Fake(
		exec.FakeResponse{}, // ensure user
		exec.FakeResponse{}, // ensure ACL
		exec.FakeResponse{}, // token remove
		exec.FakeResponse{Stdout: `{"value":"the-secret"}`},          // token add
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + fp + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                    // hostname
	)
	cmd, deps := syncTestDeps(t, fake)
	deps.Cfg.Contexts["lab-demo"] = &config.Context{
		Host: "10.10.1.10", Port: 8006, Protocol: "https", Product: config.ProductPVE,
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "pmx",
			Secret: "keychain:pmx-lab-demo/root@pam!pmx"},
	}
	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.NoError(t, err)
	assert.True(t, res.Rotated, "a foreign identity must force a fresh mint, not reuse")
	assert.True(t, mintedAToken(fake), "must mint when the stored context is not the lab's own identity")
	assert.Equal(t, "keychain:pmx-lab-demo/pmx@pve!pmx", deps.Cfg.Contexts["lab-demo"].Auth.Secret)
}

func TestSyncLabContext_ReusesOnTransportProbeFailure(t *testing.T) {
	// The reuse probe failing for a transport reason (node briefly unreachable,
	// or a stale pinned fingerprint about to be refreshed) is not evidence the
	// stored token is bad, so it must NOT be rotated. The end-to-end probe that
	// runs after the fingerprint refresh then succeeds.
	fp := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	fake := exec.Fake(
		exec.FakeResponse{}, // ensure user
		exec.FakeResponse{}, // ensure ACL
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + fp + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                    // hostname
	)
	cmd, deps := syncTestDeps(t, fake)
	deps.Cfg.Contexts["lab-demo"] = &config.Context{
		Host: "10.10.1.10", Port: 8006, Protocol: "https", Product: config.ProductPVE,
		Auth: config.AuthBlock{Type: "token", Username: "pmx@pve", TokenID: "pmx",
			Secret: "keychain:pmx-lab-demo/pmx@pve!pmx"},
	}
	// First probe (reuse check) fails at the transport layer; the mandatory
	// end-to-end probe after the refresh succeeds.
	probeCalls := 0
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error {
		probeCalls++
		if probeCalls == 1 {
			return &pveerrors.ConnectionError{Host: "10.10.1.10", Port: 8006, Message: "connection refused"}
		}
		return nil
	}
	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.NoError(t, err)
	assert.False(t, res.Rotated, "a transport-level probe failure must not rotate a possibly-valid secret")
	assert.False(t, mintedAToken(fake), "must not mint when the probe failed only at the transport layer")
	assert.Equal(t, 2, probeCalls, "reuse probe then the mandatory end-to-end probe")
}

func TestSyncLabContext_RotatesOnAuthProbeFailure(t *testing.T) {
	// A reuse probe that fails with an authentication rejection (not a
	// transport error) IS evidence the stored secret is bad, so it must rotate.
	fp := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	fake := exec.Fake(
		exec.FakeResponse{}, // ensure user
		exec.FakeResponse{}, // ensure ACL
		exec.FakeResponse{}, // token remove
		exec.FakeResponse{Stdout: `{"value":"the-secret"}`},          // token add
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + fp + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                    // hostname
	)
	cmd, deps := syncTestDeps(t, fake)
	deps.Cfg.Contexts["lab-demo"] = &config.Context{
		Host: "10.10.1.10", Port: 8006, Protocol: "https", Product: config.ProductPVE,
		Auth: config.AuthBlock{Type: "token", Username: "pmx@pve", TokenID: "pmx",
			Secret: "keychain:pmx-lab-demo/pmx@pve!pmx"},
	}
	probeCalls := 0
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error {
		probeCalls++
		if probeCalls == 1 {
			return pveerrors.ErrUnauthorized
		}
		return nil
	}
	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.NoError(t, err)
	assert.True(t, res.Rotated, "an auth rejection must rotate the stored secret")
	assert.True(t, mintedAToken(fake), "must mint a fresh token when the stored secret no longer authenticates")
}

func TestSyncLabContext_ProbeFailureIsFatal(t *testing.T) {
	fp := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	fake := exec.Fake(
		exec.FakeResponse{}, exec.FakeResponse{}, exec.FakeResponse{},
		exec.FakeResponse{Stdout: `{"value":"the-secret"}`},
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + fp + "\n"},
		exec.FakeResponse{Stdout: "lab-demo-0\n"},
	)
	cmd, deps := syncTestDeps(t, fake)
	// Force the end-to-end probe (after upsert) to fail.
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error {
		return assertAnErr{}
	}
	lab := multiNodeTestLab("demo", 1, "never")
	// multiNodeTestLab (via cleanLab) does not set Name; syncLabContext is
	// called directly here (no config-load round trip through the loader's
	// map-key defaulting), so Name must be set explicitly, matching the
	// pattern used elsewhere in this package (resolve_test.go, scale_test.go).
	lab.Name = "demo"

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.Error(t, err)

	// The probe runs after the context is written and saved, so a probe
	// failure must still leave the context recorded (and name it), not roll
	// it back — the error reports "written but GET /version failed".
	assert.Equal(t, "lab-demo", res.ContextName)
	assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
	reloaded, lerr := config.Load(deps.ConfigPath)
	require.NoError(t, lerr)
	assert.NotNil(t, reloaded.Contexts["lab-demo"])
}

func TestContextSyncCommand_WritesContextAndRenders(t *testing.T) {
	fp := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	fake := exec.Fake(
		exec.FakeResponse{}, exec.FakeResponse{}, exec.FakeResponse{},
		exec.FakeResponse{Stdout: `{"value":"the-secret"}`},
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + fp + "\n"},
		exec.FakeResponse{Stdout: "lab-demo-0\n"},
	)
	_, deps := syncTestDeps(t, fake)
	// Write a lab config the command can resolve.
	writeLabConfig(t, deps.ConfigPath, "demo")
	// Reload cfg so resolveLabForMutate sees the lab.
	reloaded, err := config.Load(deps.ConfigPath)
	require.NoError(t, err)
	deps.Cfg = reloaded

	root := newContextCmd()
	root.SetContext(cli.WithDeps(context.Background(), deps))
	out, err := runGuestCmd(t, root, "sync", "demo")
	require.NoError(t, err)
	assert.Contains(t, out, "lab-demo")

	assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
}

// contextRmDeps builds a *cli.Deps carrying a persisted config.yml. When
// withContext is true it pre-seeds a lab-<name> pmx token context whose secret
// is a keychain reference, mirroring what auto-registration writes; when false
// the config has no context, exercising the orphaned-secret cleanup path.
func contextRmDeps(t *testing.T, name string, withContext bool) *cli.Deps {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("contexts: {}\n"), 0o600))
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	if withContext {
		cfg.Contexts["lab-"+name] = &config.Context{
			Host:     "10.10.1.10",
			Port:     8006,
			Protocol: "https",
			Product:  config.ProductPVE,
			Auth: config.AuthBlock{
				Type:     "token",
				Username: "pmx@pve",
				TokenID:  "pmx",
				Secret:   "keychain:pmx-lab-" + name + "/pmx@pve!pmx",
			},
		}
	}
	return &cli.Deps{Cfg: cfg, ConfigPath: cfgPath, Out: output.New(), Format: output.FormatPlain}
}

func TestContextRm_RemovesContextAndSecret(t *testing.T) {
	var svc, acct string
	orig := labDeleteSecretFn
	labDeleteSecretFn = func(service, account string) error {
		svc, acct = service, account
		return nil
	}
	t.Cleanup(func() { labDeleteSecretFn = orig })

	deps := contextRmDeps(t, "demo", true)
	root := newContextCmd()
	root.SetContext(cli.WithDeps(context.Background(), deps))

	out, err := runGuestCmd(t, root, "rm", "demo")
	require.NoError(t, err)

	assert.Nil(t, deps.Cfg.Contexts["lab-demo"], "context must be removed from config")
	assert.Equal(t, "pmx-lab-demo", svc)
	assert.Equal(t, "pmx@pve!pmx", acct)
	assert.Contains(t, out, `removed context "lab-demo" and its keychain secret`)

	// Persisted to disk.
	reloaded, err := config.Load(deps.ConfigPath)
	require.NoError(t, err)
	assert.Nil(t, reloaded.Contexts["lab-demo"], "removal must be saved")
}

func TestContextRm_ClearsActiveContext(t *testing.T) {
	orig := labDeleteSecretFn
	labDeleteSecretFn = func(string, string) error { return nil }
	t.Cleanup(func() { labDeleteSecretFn = orig })

	deps := contextRmDeps(t, "demo", true)
	deps.Cfg.CurrentContext = "lab-demo"
	root := newContextCmd()
	root.SetContext(cli.WithDeps(context.Background(), deps))

	_, err := runGuestCmd(t, root, "rm", "demo")
	require.NoError(t, err)
	assert.Equal(t, "", deps.Cfg.CurrentContext,
		"removing the active lab context must unset CurrentContext")

	reloaded, err := config.Load(deps.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, "", reloaded.CurrentContext, "unset CurrentContext must be persisted")
}

// TestContextRm_KeychainFailureLeavesContext covers the error path: a real
// keychain-delete failure (not ErrKeychainUnsupported) must propagate as a
// wrapped error with the context left intact, matching cleanupLabContext's
// keychain-first ordering — on darwin the secret lives in the keychain, so the
// config reference is kept as the trail to a secret that could not be deleted.
func TestContextRm_KeychainFailureLeavesContext(t *testing.T) {
	orig := labDeleteSecretFn
	labDeleteSecretFn = func(string, string) error { return fmt.Errorf("keychain locked") }
	t.Cleanup(func() { labDeleteSecretFn = orig })

	deps := contextRmDeps(t, "demo", true)
	root := newContextCmd()
	root.SetContext(cli.WithDeps(context.Background(), deps))

	_, err := runGuestCmd(t, root, "rm", "demo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `remove context for lab "demo"`)
	assert.NotNil(t, deps.Cfg.Contexts["lab-demo"],
		"a real keychain failure must not remove the context")
}

// TestContextRm_RejectsInvalidName covers the charset guard: rm validates its
// argument directly (it never routes through resolveLabForMutate) so a name
// that could reach a remote command line is refused before any mutation.
func TestContextRm_RejectsInvalidName(t *testing.T) {
	called := false
	orig := labDeleteSecretFn
	labDeleteSecretFn = func(string, string) error { called = true; return nil }
	t.Cleanup(func() { labDeleteSecretFn = orig })

	deps := contextRmDeps(t, "demo", false)
	root := newContextCmd()
	root.SetContext(cli.WithDeps(context.Background(), deps))

	_, err := runGuestCmd(t, root, "rm", "bad name")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "charset")
	assert.False(t, called, "an invalid name must be rejected before any keychain call")
}

// TestContextRm_AbsentContext covers the orphaned-secret path: no lab-<name>
// context exists in config, but rm must still attempt the keychain delete
// (the secret may outlive its context) and report the absence without error.
func TestContextRm_AbsentContext(t *testing.T) {
	called := false
	orig := labDeleteSecretFn
	labDeleteSecretFn = func(string, string) error { called = true; return nil }
	t.Cleanup(func() { labDeleteSecretFn = orig })

	deps := contextRmDeps(t, "demo", false)
	root := newContextCmd()
	root.SetContext(cli.WithDeps(context.Background(), deps))

	out, err := runGuestCmd(t, root, "rm", "demo")
	require.NoError(t, err)
	assert.True(t, called, "rm must still attempt to clear a possibly-stray keychain secret")
	assert.Contains(t, out, `context "lab-demo" not found`)
}

// writeLabConfig writes a minimal config.yml at path containing one inline lab
// named name with a resolvable mgmt /24, so resolveLabForMutate succeeds.
func writeLabConfig(t *testing.T, path, name string) {
	t.Helper()
	body := "contexts: {}\nlabs:\n  " + name + ":\n" +
		"    name: " + name + "\n" +
		"    network:\n      mgmt:\n        subnet: 10.10.1.0/24\n        gateway: 10.10.1.1\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}
