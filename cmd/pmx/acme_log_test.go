package main_test

import (
	"encoding/base64"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// runPMXHome runs the built binary with HOME pinned to home, so the caller
// can read the log tree the invocation wrote, and with stdin and extra
// environment supplied.
func runPMXHome(t *testing.T, home, stdin string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cmd := exec.Command(pmxBinary(t), args...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+t.TempDir(),
		"PMX_CONTEXT=", "PMX_NODE=", "PMX_OUTPUT=", "PMX_LOG_LEVEL=", "PMX_LOG_LAYOUT=",
	)
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = strings.NewReader(stdin)

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errorAs(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		require.NoError(t, err, "running the binary failed for a reason other than its exit code")
	}

	return outBuf.String(), errBuf.String(), code
}

// logTreeText concatenates every file under home's log directory.
func logTreeText(t *testing.T, home string) string {
	t.Helper()

	var b strings.Builder
	root := filepath.Join(home, ".pmx", "logs")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // test-owned temp tree
		if err != nil {
			return err
		}
		b.Write(data)
		return nil
	})
	require.NoError(t, err)

	return b.String()
}

// TestMain_ACMEPluginDataNeverLogged runs every way of handing pmx DNS plugin
// credentials, at trace level so the log is as detailed as it gets, and
// checks that neither the plaintext nor its base64 form reaches any log file
// or the terminal. The value travels as a flag or on stdin, and the invocation
// record carries only positional arguments while the HTTP logger never writes
// request bodies, so this pins both properties at once.
func TestMain_ACMEPluginDataNeverLogged(t *testing.T) {
	const plaintext = "CF_Token=never-log-this-cf-token"
	encoded := base64.StdEncoding.EncodeToString([]byte(plaintext))

	f := testhelper.NewFakePVE(t)
	var gotData []string
	record := func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotData = append(gotData, r.PostFormValue("data"))
		testhelper.WriteData(w, nil)
	}
	f.HandleFunc("POST /api2/json/cluster/acme/plugins", record)
	f.HandleFunc("PUT /api2/json/cluster/acme/plugins/cf-dns", record)
	cfg := fakeConfig(t, f)

	cases := []struct {
		name  string
		stdin string
		env   []string
		args  []string
	}{
		{
			name:  "create --data-file -",
			stdin: plaintext + "\n",
			args:  []string{"pve", "cluster", "acme", "plugin", "create", "cf-dns", "--type", "dns", "--api", "cf", "--data-file", "-"},
		},
		{
			name: "set --data env reference",
			env:  []string{"PMX_TEST_ACME_DNS_DATA=" + encoded},
			args: []string{"pve", "cluster", "acme", "plugin", "set", "cf-dns", "--data", "${PMX_TEST_ACME_DNS_DATA}"},
		},
		{
			name: "set --data literal",
			args: []string{"pve", "cluster", "acme", "plugin", "set", "cf-dns", "--data", encoded},
		},
		{
			name: "api post -d data=",
			args: []string{"api", "post", "/cluster/acme/plugins", "-d", "id=cf-dns", "-d", "type=dns", "-d", "data=" + encoded},
		},
		{
			name:  "api post -d @-",
			stdin: "data=" + encoded + "\n",
			args:  []string{"api", "post", "/cluster/acme/plugins", "-d", "id=cf-dns", "-d", "type=dns", "-d", "@-"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			gotData = nil

			args := append([]string{"--config", cfg, "--trace"}, tc.args...)
			stdout, stderr, code := runPMXHome(t, home, tc.stdin, tc.env, args...)
			require.Equal(t, 0, code, stderr)
			require.Equal(t, []string{encoded}, gotData, "the API receives the encoded block")

			logs := logTreeText(t, home)
			require.Contains(t, logs, `"msg":"invocation"`, "the log tree must hold this invocation")
			for _, secret := range []string{plaintext, "never-log-this-cf-token", encoded} {
				require.NotContains(t, logs, secret, "plugin data reached the log")
				require.NotContains(t, stdout, secret)
				require.NotContains(t, stderr, secret)
			}
		})
	}
}
