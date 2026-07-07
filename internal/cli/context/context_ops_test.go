package context

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// ---- test helpers -----------------------------------------------------------

// runOpsCmd builds the context group with injected deps, sets output to buf,
// and executes args.
func runOpsCmd(cfg *config.Config, tmpPath string, buf *bytes.Buffer, args ...string) error {
	deps := &cli.Deps{
		Cfg:        cfg,
		ConfigPath: tmpPath,
		Out:        output.New(),
		Format:     output.FormatJSON,
	}
	cmd := Group(nil)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// scratchConfig writes cfg to a temp file and returns the path.
func scratchConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yml")
	require.NoError(t, config.Save(p, cfg))
	return p
}

// labContext returns a fully-valid token-auth Context for use in tests.
func labContext() *config.Context {
	return &config.Context{
		Host:     "10.0.0.1",
		Port:     8006,
		Protocol: "https",
		Realm:    "pam",
		Auth: config.AuthBlock{
			Type:     "token",
			Username: "root@pam",
			TokenID:  "mytoken",
			Secret:   "s3cr3t",
		},
	}
}

// ---- copy tests -------------------------------------------------------------

func TestContextCopy_Happy(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging"))

	// Reload and verify dst exists and is a deep copy.
	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Contains(t, loaded.Contexts, "staging")
	require.Contains(t, loaded.Contexts, "lab", "src must still exist")

	src := loaded.Contexts["lab"]
	dst := loaded.Contexts["staging"]
	require.Equal(t, src.Host, dst.Host)
	require.Equal(t, src.Auth.TokenID, dst.Auth.TokenID)
	require.Equal(t, src.Auth.Secret, dst.Auth.Secret)

	// Verify independence: mutating dst pointer does not affect src pointer.
	dst.Host = "mutated"
	require.NotEqual(t, loaded.Contexts["lab"].Host, dst.Host, "copy must be independent")
}

// TestContextCopy_PreservesTLSFields guards against a copy verb silently
// dropping a TLSBlock field added after deepCopyContext was first written
// (regression class: IMP-02b added Tofu and it must survive copy).
func TestContextCopy_PreservesTLSFields(t *testing.T) {
	src := labContext()
	src.TLS = config.TLSBlock{
		Insecure:    true,
		Fingerprint: "AA:BB:CC",
		CACert:      "/etc/ssl/certs/ca.pem",
		Tofu:        true,
	}
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": src,
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	dst := loaded.Contexts["staging"]
	require.NotNil(t, dst)

	require.Equal(t, src.TLS.Insecure, dst.TLS.Insecure)
	require.Equal(t, src.TLS.Fingerprint, dst.TLS.Fingerprint)
	require.Equal(t, src.TLS.CACert, dst.TLS.CACert)
	require.Equal(t, src.TLS.Tofu, dst.TLS.Tofu)
}

// TestContextCopy_PreservesProduct guards against the copy verb silently
// dropping Product, mirroring TestContextCopy_PreservesTLSFields's intent for
// the pve/pbs product selector.
func TestContextCopy_PreservesProduct(t *testing.T) {
	src := labContext()
	src.Product = config.ProductPBS
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": src,
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	dst := loaded.Contexts["staging"]
	require.NotNil(t, dst)
	require.Equal(t, config.ProductPBS, dst.Product)
}

func TestContextCopy_MissingSrc(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"lab": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "copy", "nonexistent", "dst")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestContextCopy_ExistingDstNoForce(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"lab":     labContext(),
			"staging": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "copy", "lab", "staging")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")
}

func TestContextCopy_ExistingDstForce(t *testing.T) {
	orig := labContext()
	orig.Host = "original-host"
	over := labContext()
	over.Host = "old-staging-host"

	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"lab":     orig,
			"staging": over,
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging", "--force"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, "original-host", loaded.Contexts["staging"].Host)
}

func TestContextCopy_Select(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging", "--select"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, "staging", loaded.CurrentContext)
	require.Equal(t, "lab", loaded.PreviousContext)
}

func TestContextCopy_SameSrcDst(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"lab": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "copy", "lab", "lab")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must differ")
}

// ---- edit tests -------------------------------------------------------------

// TestContextEdit_SuccessfulEdit verifies that a well-behaved fake editor
// that appends a known YAML line causes the change to be persisted.
func TestContextEdit_SuccessfulEdit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext()
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Write a fake editor that appends default-node to the temp file.
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "fake-editor.sh")
	script := `#!/bin/sh
# Append default-node line to the file passed as $1 and exit 0.
printf '\ndefault-node: edited-node\n' >> "$1"
`
	require.NoError(t, os.WriteFile(editorPath, []byte(script), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "edit", "lab"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, "edited-node", loaded.Contexts["lab"].DefaultNode)
}

// TestContextEdit_EditorFailingExitAbortsChange verifies that a non-zero
// editor exit code leaves the config unchanged.
func TestContextEdit_EditorFailingExitAbortsChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext()
	origNode := "original-node"
	ctx.DefaultNode = origNode
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Fake editor exits non-zero without modifying the file.
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "fail-editor.sh")
	require.NoError(t, os.WriteFile(editorPath, []byte("#!/bin/sh\nexit 1\n"), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab")
	require.Error(t, err)
	require.Contains(t, err.Error(), "editor exited with error")

	// Config must be unchanged.
	loaded, err2 := config.Load(p)
	require.NoError(t, err2)
	require.Equal(t, origNode, loaded.Contexts["lab"].DefaultNode)
}

// TestContextEdit_InvalidYAMLRejected verifies that a fake editor that writes
// invalid YAML causes an error and preserves the temp file.
func TestContextEdit_InvalidYAMLRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext()
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Fake editor overwrites the file with broken YAML.
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "bad-yaml-editor.sh")
	script := "#!/bin/sh\nprintf 'host: [\nbad yaml' > \"$1\"\nexit 0\n"
	require.NoError(t, os.WriteFile(editorPath, []byte(script), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab")
	require.Error(t, err)
	// Error must contain a temp file path for recovery.
	require.Contains(t, err.Error(), "pve-context-")
}

// TestContextEdit_NoEditorEnvReturnsError verifies that an unset $EDITOR (and
// $VISUAL) produces a helpful error message.
func TestContextEdit_NoEditorEnvReturnsError(t *testing.T) {
	ctx := labContext()
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab")
	require.Error(t, err)
	require.Contains(t, err.Error(), "$EDITOR is not set")
}

// TestContextEdit_DefaultsToCurrentContext verifies that omitting the name arg
// uses the current context.
func TestContextEdit_DefaultsToCurrentContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext()
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Fake editor: no-op (exit 0 without modifying file).
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "noop.sh")
	require.NoError(t, os.WriteFile(editorPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	// No name arg — should use current context "lab".
	require.NoError(t, runOpsCmd(cfg, p, &buf, "edit"))
}

// TestContextEdit_NoCurrentContextErrors verifies that with no arg and no
// current-context set, an error is returned.
func TestContextEdit_NoCurrentContextErrors(t *testing.T) {
	ctx := labContext()
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	t.Setenv("EDITOR", "true") // valid editor path
	t.Setenv("VISUAL", "")

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no current-context")
}

// ---- validate tests ---------------------------------------------------------

func TestContextValidate_ValidContext(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate", "lab"))
	require.Contains(t, buf.String(), "OK")
}

func TestContextValidate_MissingHost(t *testing.T) {
	ctx := labContext()
	ctx.Host = ""
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "host is required")
}

func TestContextValidate_TokenAuthMissingTokenID(t *testing.T) {
	ctx := labContext()
	ctx.Auth.TokenID = ""
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "token-id")
}

func TestContextValidate_PasswordAuthMissingUsername(t *testing.T) {
	ctx := labContext()
	ctx.Auth.Type = "password"
	ctx.Auth.TokenID = ""
	ctx.Auth.Username = ""
	ctx.Auth.Secret = "pass"
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "username")
}

func TestContextValidate_BadDefaultOutput(t *testing.T) {
	ctx := labContext()
	ctx.DefaultOutput = "xml"
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "default-output")
}

func TestContextValidate_BadFingerprint(t *testing.T) {
	ctx := labContext()
	ctx.TLS.Fingerprint = "not-a-fingerprint"
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "fingerprint")
}

func TestContextValidate_AllMixed(t *testing.T) {
	goodCtx := labContext()
	badCtx := labContext()
	badCtx.Host = ""

	cfg := &config.Config{
		CurrentContext: "good",
		Contexts: map[string]*config.Context{
			"good": goodCtx,
			"bad":  badCtx,
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "--all")
	require.Error(t, err, "--all with any invalid context must exit non-zero")
	out := buf.String()
	require.Contains(t, out, "OK")
	require.Contains(t, out, "INVALID")
}

func TestContextValidate_AllValid(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "a",
		Contexts: map[string]*config.Context{
			"a": labContext(),
			"b": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate", "--all"))
}

// TestContextValidate_AllSortedOrder verifies that `validate --all` emits rows
// in deterministic alphabetical order regardless of map iteration order (F-W6-02).
func TestContextValidate_AllSortedOrder(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "alpha",
		Contexts: map[string]*config.Context{
			"gamma": labContext(),
			"alpha": labContext(),
			"beta":  labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate", "--all"))

	out := buf.String()
	idxAlpha := strings.Index(out, "alpha")
	idxBeta := strings.Index(out, "beta")
	idxGamma := strings.Index(out, "gamma")

	require.True(t, idxAlpha >= 0, "alpha must appear in output")
	require.True(t, idxBeta >= 0, "beta must appear in output")
	require.True(t, idxGamma >= 0, "gamma must appear in output")
	require.True(t, idxAlpha < idxBeta, "alpha must precede beta in sorted output")
	require.True(t, idxBeta < idxGamma, "beta must precede gamma in sorted output")
}

func TestContextValidate_DefaultsToCurrentContext(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate"))
	require.Contains(t, buf.String(), "OK")
}

func TestContextValidate_NoCurrentContextErrors(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no current-context")
}

func TestContextValidate_UnknownContextErrors(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "ghost")
	require.Error(t, err)
}

func TestContextValidate_ValidFingerprint(t *testing.T) {
	ctx := labContext()
	// 32 colon-separated uppercase hex pairs.
	ctx.TLS.Fingerprint = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"ctx": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate", "ctx"))
}

// ---- validation-drift regression (F2 remediation) --------------------------

// TestContextEdit_TokenMissingTokenIDRejected pins the write-time rule that
// token auth requires token-id.  A fake editor removes the token-id line; the
// edit must be rejected and the config must not be modified.
func TestContextEdit_TokenMissingTokenIDRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext() // token auth with TokenID set
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Fake editor rewrites the file with a valid token context but no token-id.
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "strip-tokenid.sh")
	script := `#!/bin/sh
# Write a token context missing token-id.
cat > "$1" <<'YAML'
host: 10.0.0.1
port: 8006
protocol: https
realm: pam
auth:
  type: token
  secret: s3cr3t
YAML
`
	require.NoError(t, os.WriteFile(editorPath, []byte(script), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab")
	require.Error(t, err, "edit must be rejected when token-id is missing")
	require.Contains(t, err.Error(), "token-id")

	// Config must be unchanged — original token-id still present.
	loaded, err2 := config.Load(p)
	require.NoError(t, err2)
	require.Equal(t, "mytoken", loaded.Contexts["lab"].Auth.TokenID,
		"config must not be modified after rejected edit")
}

// TestContextValidate_TokenMissingTokenIDInvalid pins that validate catches a
// token context with no token-id, ensuring add/edit/validate share the same rule.
func TestContextValidate_TokenMissingTokenIDInvalid(t *testing.T) {
	ctx := labContext()
	ctx.Auth.TokenID = "" // missing token-id
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "token-id")
}

// ---- noClient annotation regression for copy/edit/validate -----------------

func TestContextCopyEditValidateAreNoClient(t *testing.T) {
	for _, name := range []string{"copy", "edit", "validate"} {
		t.Run(name, func(t *testing.T) {
			cmd := Group(&cli.Deps{})
			found := false
			for _, sub := range cmd.Commands() {
				if sub.Name() == name {
					found = true
					ann := sub.Annotations["noClient"]
					require.Equal(t, "true", ann,
						fmt.Sprintf("command %q must have Annotations[\"noClient\"]==\"true\"", name))
				}
			}
			require.True(t, found, "command %q must be registered under context group", name)
		})
	}
}
