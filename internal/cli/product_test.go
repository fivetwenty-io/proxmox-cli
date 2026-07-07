package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/config"
)

// makeProductConfig writes a config file with one PVE context ("pve1", the
// current context) and one PBS context ("pbs1"), both token-auth with a
// literal secret so client construction never needs env/keychain resolution.
func makeProductConfig(t *testing.T) string {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	cfg := &config.Config{
		CurrentContext: "pve1",
		Contexts: map[string]*config.Context{
			"pve1": {
				Host: "10.0.0.1", Port: 8006, Protocol: "https", Realm: "pam",
				Auth: config.AuthBlock{
					Type: "token", Username: "root@pam", TokenID: "tok", Secret: "sekrit",
				},
			},
			"pbs1": {
				Host: "10.0.0.2", Port: 8007, Protocol: "https", Realm: "pam",
				Product: config.ProductPBS,
				Auth: config.AuthBlock{
					Type: "token", Username: "root@pam", TokenID: "tok", Secret: "sekrit",
				},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	return cfgPath
}

// productTestCmd returns a bare command with stdin/stderr wired the way
// BuildContextClient/BuildContextPBSClient expect from a real invocation.
func productTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetIn(strings.NewReader(""))
	cmd.SetErr(&bytes.Buffer{})

	return cmd
}

func neverTTY() bool { return false }

// ---------------------------------------------------------------------------
// Product guards on the exported client builders
// ---------------------------------------------------------------------------

func TestBuildContextClient_RejectsPBSContext(t *testing.T) {
	t.Setenv("PVE_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	ac, ctx, err := cli.BuildContextClient(productTestCmd(), cfg, cfgPath, "pbs1", false, neverTTY)

	require.Error(t, err, "a PVE command must reject a PBS context")
	require.Contains(t, err.Error(), "pbs1", "error must name the offending context")
	require.Contains(t, err.Error(), "Proxmox Backup Server")
	require.Contains(t, err.Error(), "pve pbs", "error must point at the pbs command group")
	require.Nil(t, ac)
	require.Nil(t, ctx)
}

func TestBuildContextClient_AcceptsPVEContext(t *testing.T) {
	t.Setenv("PVE_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	ac, ctx, err := cli.BuildContextClient(productTestCmd(), cfg, cfgPath, "pve1", false, neverTTY)

	require.NoError(t, err, "client construction is lazy; a stub host must succeed")
	require.NotNil(t, ac)
	require.NotNil(t, ctx)
	require.False(t, ctx.IsPBS())
}

func TestBuildContextPBSClient_RejectsPVEContext(t *testing.T) {
	t.Setenv("PVE_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	pc, ctx, err := cli.BuildContextPBSClient(productTestCmd(), cfg, cfgPath, "pve1", false, neverTTY)

	require.Error(t, err, "a PBS command must reject a PVE context")
	require.Contains(t, err.Error(), "pve1", "error must name the offending context")
	require.Contains(t, err.Error(), "Proxmox VE")
	require.Contains(t, err.Error(), "--product pbs", "error must say how to create a PBS context")
	require.Nil(t, pc)
	require.Nil(t, ctx)
}

func TestBuildContextPBSClient_AcceptsPBSContext(t *testing.T) {
	t.Setenv("PVE_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	pc, ctx, err := cli.BuildContextPBSClient(productTestCmd(), cfg, cfgPath, "pbs1", false, neverTTY)

	require.NoError(t, err, "client construction is lazy; a stub host must succeed")
	require.NotNil(t, pc)
	require.NotNil(t, pc.Raw, "PBS raw client handle must be wired")
	require.NotNil(t, ctx)
	require.True(t, ctx.IsPBS())
}

// ---------------------------------------------------------------------------
// PersistentPreRunE product dispatch (ProductAnnotation)
// ---------------------------------------------------------------------------

// buildPBSInspectCmd is buildInspectCmd with the PBS product annotation, as a
// 'pve pbs' group verb would carry (via its group parent).
func buildPBSInspectCmd(deps **cli.Deps) *cobra.Command {
	cmd := buildInspectCmd(deps)
	cmd.Use = "pbsinspect"
	cmd.Annotations = map[string]string{cli.ProductAnnotation: config.ProductPBS}

	return cmd
}

func TestPersistentPreRunE_ProductDispatch(t *testing.T) {
	t.Setenv("PVE_CONTEXT", "")
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_OUTPUT", "table")
	cfgPath := makeProductConfig(t)

	newRoot := func(t *testing.T) *cobra.Command {
		t.Helper()

		root, cleanup := cli.NewRootCmd()
		t.Cleanup(cleanup)
		root.SetContext(context.Background())

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)

		return root
	}

	t.Run("pbs-annotated command with pbs context populates Deps.PBS not Deps.API", func(t *testing.T) {
		root := newRoot(t)

		var deps *cli.Deps
		root.AddCommand(buildPBSInspectCmd(&deps))
		root.SetArgs([]string{"--config", cfgPath, "--context", "pbs1", "pbsinspect"})

		require.NoError(t, root.Execute())
		require.NotNil(t, deps, "inspect command must have run")
		require.NotNil(t, deps.PBS, "PBS product commands must get Deps.PBS")
		require.Nil(t, deps.API, "PBS product commands must not get a PVE client")
		require.True(t, deps.Ctx.IsPBS())
	})

	t.Run("pbs-annotated command with pve context fails the product guard", func(t *testing.T) {
		root := newRoot(t)

		var deps *cli.Deps
		root.AddCommand(buildPBSInspectCmd(&deps))
		root.SetArgs([]string{"--config", cfgPath, "--context", "pve1", "pbsinspect"})

		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "Proxmox VE")
		require.Nil(t, deps, "the command must not run against the wrong product")
	})

	t.Run("unannotated command defaults to PVE and rejects a pbs context", func(t *testing.T) {
		root := newRoot(t)

		var deps *cli.Deps
		root.AddCommand(buildInspectCmd(&deps))
		root.SetArgs([]string{"--config", cfgPath, "--context", "pbs1", "inspect"})

		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "Proxmox Backup Server")
		require.Nil(t, deps, "the command must not run against the wrong product")
	})

	t.Run("unannotated command with pve context populates Deps.API not Deps.PBS", func(t *testing.T) {
		root := newRoot(t)

		var deps *cli.Deps
		root.AddCommand(buildInspectCmd(&deps))
		root.SetArgs([]string{"--config", cfgPath, "--context", "pve1", "inspect"})

		require.NoError(t, root.Execute())
		require.NotNil(t, deps, "inspect command must have run")
		require.NotNil(t, deps.API)
		require.Nil(t, deps.PBS)
	})

	t.Run("child of a pbs-annotated group inherits the product requirement", func(t *testing.T) {
		root := newRoot(t)

		var deps *cli.Deps
		group := &cobra.Command{
			Use:         "pbsgroup",
			Annotations: map[string]string{cli.ProductAnnotation: config.ProductPBS},
		}
		group.AddCommand(buildInspectCmd(&deps))
		root.AddCommand(group)
		root.SetArgs([]string{"--config", cfgPath, "--context", "pbs1", "pbsgroup", "inspect"})

		require.NoError(t, root.Execute())
		require.NotNil(t, deps, "inspect command must have run")
		require.NotNil(t, deps.PBS, "annotation on the group parent must apply to the child")
		require.Nil(t, deps.API)
	})
}
