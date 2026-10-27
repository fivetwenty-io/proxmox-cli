package cli_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// acmeDataSecret is the credential every test feeds in. No error or warning
// may ever quote it.
const acmeDataSecret = "s3cr3t-cf-token-value"

// resolveACMEData runs ResolveACMEPluginData on a throwaway command built with
// the shared flags, fed args and stdin, and returns what it resolved along
// with everything written to stderr.
func resolveACMEData(t *testing.T, stdin string, required bool, args ...string) (string, bool, string, error) {
	t.Helper()

	var (
		data, dataFile string
		got            string
		have           bool
		resolveErr     error
	)
	cmd := &cobra.Command{
		Use:  "probe",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, have, resolveErr = cli.ResolveACMEPluginData(cmd, data, dataFile)
			return nil
		},
	}
	cli.RegisterACMEPluginDataFlags(cmd, &data, &dataFile, required)

	var stderr bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		return "", false, stderr.String(), err
	}

	return got, have, stderr.String(), resolveErr
}

func decodeACMEData(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	return string(raw)
}

func TestResolveACMEPluginData_FileIsEncoded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cf.env")
	content := "# Cloudflare\r\nCF_Token=" + acmeDataSecret + "\r\n\r\nCF_Account_ID=abc123\r\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	got, have, stderr, err := resolveACMEData(t, "", false, "--data-file", path)
	require.NoError(t, err)
	require.True(t, have)
	require.Empty(t, stderr, "a file never warns")
	require.Equal(t, "CF_Token="+acmeDataSecret+"\nCF_Account_ID=abc123", decodeACMEData(t, got),
		"comments and blank lines dropped, CRLF normalised, lines joined with newlines")
}

func TestResolveACMEPluginData_StdinIsEncoded(t *testing.T) {
	got, have, _, err := resolveACMEData(t, "CF_Token="+acmeDataSecret+"\n", false, "--data-file", "-")
	require.NoError(t, err)
	require.True(t, have)
	require.Equal(t, "CF_Token="+acmeDataSecret, decodeACMEData(t, got))
}

func TestResolveACMEPluginData_EnvReference(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("CF_Token=" + acmeDataSecret))
	t.Setenv("PMX_TEST_ACME_DATA", encoded)

	for _, ref := range []string{"${PMX_TEST_ACME_DATA}", "$PMX_TEST_ACME_DATA"} {
		got, have, stderr, err := resolveACMEData(t, "", false, "--data", ref)
		require.NoError(t, err, ref)
		require.True(t, have)
		require.Equal(t, encoded, got, ref)
		require.Empty(t, stderr, "a reference is not an inline literal: %s", ref)
	}
}

func TestResolveACMEPluginData_UnsetBareEnvIsAnError(t *testing.T) {
	_, _, _, err := resolveACMEData(t, "", false, "--data", "$PMX_TEST_ACME_UNSET_VAR")
	require.Error(t, err)
	require.Contains(t, err.Error(), "PMX_TEST_ACME_UNSET_VAR")
	require.Contains(t, err.Error(), "not set")
}

func TestResolveACMEPluginData_KeychainReference(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("CF_Token=" + acmeDataSecret))
	var asked string
	restore := cli.SetACMESecretResolver(func(ref string) (string, error) {
		asked = ref
		return encoded + "\n", nil
	})
	defer restore()

	got, have, _, err := resolveACMEData(t, "", false, "--data", "keychain:pmx-acme/cf-dns")
	require.NoError(t, err)
	require.True(t, have)
	require.Equal(t, "keychain:pmx-acme/cf-dns", asked, "the keychain reference goes to the shared resolver")
	require.Equal(t, encoded, got, "surrounding whitespace is trimmed")
}

func TestResolveACMEPluginData_KeychainFailureSurfaces(t *testing.T) {
	restore := cli.SetACMESecretResolver(func(string) (string, error) {
		return "", errors.New("keychain lookup for \"pmx-acme\" failed: item not found")
	})
	defer restore()

	_, _, _, err := resolveACMEData(t, "", false, "--data", "keychain:pmx-acme")
	require.Error(t, err)
	require.Contains(t, err.Error(), "item not found")
}

func TestResolveACMEPluginData_LiteralWarns(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("CF_Token=" + acmeDataSecret))
	got, have, stderr, err := resolveACMEData(t, "", false, "--data", encoded)
	require.NoError(t, err)
	require.True(t, have)
	require.Equal(t, encoded, got)
	require.Contains(t, stderr, "WARN: --data carries the plugin credentials inline")
	require.NotContains(t, stderr, encoded, "the warning must not quote the value")
}

func TestResolveACMEPluginData_DataIsNormalized(t *testing.T) {
	// Long enough that GNU base64 wraps it, and with bytes that encode to
	// "+" and "/" in the standard alphabet and "-" and "_" in the URL-safe one.
	plain := "CF_Token=" + acmeDataSecret + "\nCF_Account_ID=" + strings.Repeat("0123456789abcdef", 4) +
		"\nCF_Zone_ID=\xfb\xff\xfe>?"
	want := base64.StdEncoding.EncodeToString([]byte(plain))
	require.True(t, strings.ContainsAny(want, "+/"), "the fixture must exercise the alphabet difference")
	require.Greater(t, len(want), 76, "the fixture must be long enough to wrap")

	wrap := func(s, eol string) string {
		var b strings.Builder
		for len(s) > 76 {
			b.WriteString(s[:76] + eol)
			s = s[76:]
		}
		b.WriteString(s + eol)
		return b.String()
	}

	for name, value := range map[string]string{
		"standard":           want,
		"wrapped at 76 (LF)": wrap(want, "\n"),
		"wrapped at 76 CRLF": wrap(want, "\r\n"),
		"inner spaces, tabs": want[:20] + " \t " + want[20:],
		"unpadded":           base64.RawStdEncoding.EncodeToString([]byte(plain)),
		"URL-safe":           base64.URLEncoding.EncodeToString([]byte(plain)),
		"URL-safe, unpadded": base64.RawURLEncoding.EncodeToString([]byte(plain)),
		"URL-safe, wrapped":  wrap(base64.URLEncoding.EncodeToString([]byte(plain)), "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			got, _, _, err := resolveACMEData(t, "", false, "--data", value)
			require.NoError(t, err)
			require.Equal(t, want, got, "every accepted form is sent as standard padded base64")
		})

		t.Run(name+" via reference", func(t *testing.T) {
			t.Setenv("PMX_TEST_ACME_DATA", value)
			got, _, _, err := resolveACMEData(t, "", false, "--data", "${PMX_TEST_ACME_DATA}")
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestResolveACMEPluginData_DataRejectsMixedAlphabets(t *testing.T) {
	for name, value := range map[string]string{
		"both alphabets":   "Q0ZfVG9r+W4-YWJj",
		"whitespace only":  " \n\t ",
		"stray characters": "Q0ZfVG9rZW4*YWJj",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := resolveACMEData(t, "", false, "--data", value)
			require.Error(t, err)
		})
	}
}

func TestResolveACMEPluginData_PlaintextDataIsRejected(t *testing.T) {
	_, _, stderr, err := resolveACMEData(t, "", false, "--data", "CF_Token="+acmeDataSecret)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not base64-encoded")
	require.Contains(t, err.Error(), "--data-file")
	require.NotContains(t, err.Error(), acmeDataSecret)
	require.NotContains(t, stderr, acmeDataSecret)
}

func TestResolveACMEPluginData_NeitherFlag(t *testing.T) {
	got, have, _, err := resolveACMEData(t, "", false)
	require.NoError(t, err)
	require.False(t, have)
	require.Empty(t, got)
}

func TestResolveACMEPluginData_FlagRules(t *testing.T) {
	_, _, _, err := resolveACMEData(t, "", false, "--data", "Zm9v", "--data-file", "-")
	require.Error(t, err, "the two flags are mutually exclusive")

	_, _, _, err = resolveACMEData(t, "", true)
	require.Error(t, err, "required means one of the two")
	require.Contains(t, err.Error(), "data-file")
}

func TestResolveACMEPluginData_FileErrors(t *testing.T) {
	dir := t.TempDir()

	_, _, _, err := resolveACMEData(t, "", false, "--data-file", filepath.Join(dir, "missing"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "--data-file")

	big := filepath.Join(dir, "big")
	require.NoError(t, os.WriteFile(big, bytes.Repeat([]byte("A"), 70<<10), 0o600))
	_, _, _, err = resolveACMEData(t, "", false, "--data-file", big)
	require.Error(t, err)
	require.Contains(t, err.Error(), "larger than")

	_, _, _, err = resolveACMEData(t, "", false, "--data-file", "")
	require.Error(t, err)
}

func TestEncodeACMEPluginDataLines(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "line without equals", in: "CF_Token " + acmeDataSecret, wantErr: "line 1 is not KEY=VALUE"},
		{name: "bad key", in: "OK=1\n1BAD=" + acmeDataSecret, wantErr: "line 2: the key is not a variable name"},
		{name: "empty value", in: "CF_Token=", wantErr: "line 1: the value is empty"},
		{name: "nothing but comments", in: "# none\n\n", wantErr: "no KEY=VALUE lines"},
		{
			name:    "already encoded",
			in:      base64.StdEncoding.EncodeToString([]byte("CF_Token="+acmeDataSecret)) + "\n",
			wantErr: "already base64-encoded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cli.EncodeACMEPluginDataLines(tc.in)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
			require.NotContains(t, err.Error(), acmeDataSecret, "errors must never quote a line")
		})
	}

	got, err := cli.EncodeACMEPluginDataLines("CF_Token=a=b==\n")
	require.NoError(t, err, "a value may itself contain '='")
	require.Equal(t, "CF_Token=a=b==", decodeACMEData(t, got))
}
