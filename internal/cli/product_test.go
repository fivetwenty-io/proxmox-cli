package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
)

// makeProductConfig writes a config file with one PVE context ("pve1", the
// current context), one PBS context ("pbs1"), and one PDM context ("pdm1"),
// all token-auth with a literal secret so client construction never needs
// env/keychain resolution.
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
			"pdm1": {
				Host: "10.0.0.3", Port: 8443, Protocol: "https", Realm: "pam",
				Product: config.ProductPDM,
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
	t.Setenv("PMX_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	ac, ctx, err := cli.BuildContextClient(productTestCmd(), cfg, cfgPath, "pbs1", false, neverTTY)

	require.Error(t, err, "a PVE command must reject a PBS context")
	require.Contains(t, err.Error(), "pbs1", "error must name the offending context")
	require.Contains(t, err.Error(), "Proxmox Backup Server")
	require.Contains(t, err.Error(), "pmx pbs", "error must point at the pbs command group")
	require.Nil(t, ac)
	require.Nil(t, ctx)
}

func TestBuildContextClient_AcceptsPVEContext(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
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
	t.Setenv("PMX_CONTEXT", "")
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
	t.Setenv("PMX_CONTEXT", "")
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

func TestBuildContextClient_RejectsPDMContext(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	ac, ctx, err := cli.BuildContextClient(productTestCmd(), cfg, cfgPath, "pdm1", false, neverTTY)

	require.Error(t, err, "a PVE command must reject a PDM context")
	require.Contains(t, err.Error(), "pdm1", "error must name the offending context")
	require.Contains(t, err.Error(), "Datacenter Manager")
	require.Contains(t, err.Error(), "pmx pdm", "error must point at the pdm command group")
	require.Nil(t, ac)
	require.Nil(t, ctx)
}

func TestBuildContextPBSClient_RejectsPDMContext(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	pc, ctx, err := cli.BuildContextPBSClient(productTestCmd(), cfg, cfgPath, "pdm1", false, neverTTY)

	require.Error(t, err, "a PBS command must reject a PDM context")
	require.Contains(t, err.Error(), "pdm1", "error must name the offending context")
	require.Contains(t, err.Error(), "Datacenter Manager")
	require.Contains(t, err.Error(), "--product pbs", "error must say how to create a PBS context")
	require.Nil(t, pc)
	require.Nil(t, ctx)
}

// TestBuildContextPDMClient_RejectsNonPDMContext verifies that
// BuildContextPDMClient rejects both a PVE and a PBS context, each with an
// error naming the offending context and its actual product.
func TestBuildContextPDMClient_RejectsNonPDMContext(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	cases := []struct {
		contextName string
		wantProduct string
	}{
		{"pve1", "Proxmox VE"},
		{"pbs1", "Proxmox Backup Server"},
	}

	for _, tc := range cases {
		t.Run(tc.contextName, func(t *testing.T) {
			dc, ctx, err := cli.BuildContextPDMClient(productTestCmd(), cfg, cfgPath, tc.contextName, false, neverTTY)

			require.Error(t, err, "a PDM command must reject a %s context", tc.wantProduct)
			require.Contains(t, err.Error(), tc.contextName, "error must name the offending context")
			require.Contains(t, err.Error(), tc.wantProduct)
			require.Contains(t, err.Error(), "--product pdm", "error must say how to create a PDM context")
			require.Nil(t, dc)
			require.Nil(t, ctx)
		})
	}
}

func TestBuildContextPDMClient_AcceptsPDMContext(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	dc, ctx, err := cli.BuildContextPDMClient(productTestCmd(), cfg, cfgPath, "pdm1", false, neverTTY)

	require.NoError(t, err, "client construction is lazy; a stub host must succeed")
	require.NotNil(t, dc)
	require.NotNil(t, dc.Raw, "PDM raw client handle must be wired")
	require.NotNil(t, ctx)
	require.Equal(t, config.ProductPDM, ctx.Product)
}

// personaTestCmd returns a command mounted under a root named persona, with
// stdin/stderr wired the way the client builders expect.
func personaTestCmd(persona string) *cobra.Command {
	root := &cobra.Command{Use: persona}
	child := &cobra.Command{Use: "test"}
	root.AddCommand(child)
	child.SetIn(strings.NewReader(""))
	child.SetErr(&bytes.Buffer{})
	return child
}

func TestBuildContextClient_PersonaAwareAdvice(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	_, _, err = cli.BuildContextClient(personaTestCmd("pve"), cfg, cfgPath, "pbs1", false, neverTTY)

	require.Error(t, err)
	require.Contains(t, err.Error(), "'pve context select <name>'",
		"advice under the pve binary must use the pve prefix")
	require.NotContains(t, err.Error(), "pmx context select")
	require.Contains(t, err.Error(), "'pbs' binary",
		"advice must point at the product binary for the context's own product")
}

func TestBuildContextPDMClient_PmxAdvice(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	cfgPath := makeProductConfig(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	_, _, err = cli.BuildContextPDMClient(productTestCmd(), cfg, cfgPath, "pve1", false, neverTTY)

	require.Error(t, err)
	require.Contains(t, err.Error(), "'pmx context select <name>'")
	require.Contains(t, err.Error(), "--product pdm")
	require.Contains(t, err.Error(), "'pmx pve'",
		"under plain pmx, advice points at the pmx product group for the context's product")
}

func TestBuildContextOptions_NoContext_PersonaPrefix(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(cfgPath, &config.Config{}))
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	_, _, err = cli.BuildContextClient(personaTestCmd("pbs"), cfg, cfgPath, "", false, neverTTY)

	require.Error(t, err)
	require.Contains(t, err.Error(), "'pbs context select'")
}

// ---------------------------------------------------------------------------
// PersistentPreRunE product dispatch (ProductAnnotation)
// ---------------------------------------------------------------------------

// buildPBSInspectCmd is buildInspectCmd with the PBS product annotation, as a
// 'pmx pbs' group verb would carry (via its group parent).
func buildPBSInspectCmd(deps **cli.Deps) *cobra.Command {
	cmd := buildInspectCmd(deps)
	cmd.Use = "pbsinspect"
	cmd.Annotations = map[string]string{cli.ProductAnnotation: config.ProductPBS}

	return cmd
}

// buildPDMInspectCmd is buildInspectCmd with the PDM product annotation, as a
// 'pmx pdm' group verb would carry (via its group parent).
func buildPDMInspectCmd(deps **cli.Deps) *cobra.Command {
	cmd := buildInspectCmd(deps)
	cmd.Use = "pdminspect"
	cmd.Annotations = map[string]string{cli.ProductAnnotation: config.ProductPDM}

	return cmd
}

// buildAnyInspectCmd is buildInspectCmd with the ProductFromContext
// annotation, as shared commands (version, api, ssh, rsync) carry.
func buildAnyInspectCmd(deps **cli.Deps) *cobra.Command {
	cmd := buildInspectCmd(deps)
	cmd.Use = "anyinspect"
	cmd.Annotations = map[string]string{cli.ProductAnnotation: cli.ProductFromContext}

	return cmd
}

func TestPersistentPreRunE_ProductDispatch(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "table")
	cfgPath := makeProductConfig(t)

	newRoot := func(t *testing.T) *cobra.Command {
		t.Helper()

		root, cleanup := cli.NewRootCmd("pmx")
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

	t.Run("pbs-annotated command with pmx context fails the product guard", func(t *testing.T) {
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

	t.Run("unannotated command with pmx context populates Deps.API not Deps.PBS", func(t *testing.T) {
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

	t.Run("pdm-annotated command with pdm context populates Deps.PDM not Deps.API or Deps.PBS", func(t *testing.T) {
		root := newRoot(t)

		var deps *cli.Deps
		root.AddCommand(buildPDMInspectCmd(&deps))
		root.SetArgs([]string{"--config", cfgPath, "--context", "pdm1", "pdminspect"})

		require.NoError(t, root.Execute())
		require.NotNil(t, deps, "inspect command must have run")
		require.NotNil(t, deps.PDM, "PDM product commands must get Deps.PDM")
		require.Nil(t, deps.API, "PDM product commands must not get a PVE client")
		require.Nil(t, deps.PBS, "PDM product commands must not get a PBS client")
		require.Equal(t, config.ProductPDM, deps.Ctx.Product)
	})

	t.Run("pdm-annotated command with pve context fails the product guard", func(t *testing.T) {
		root := newRoot(t)

		var deps *cli.Deps
		root.AddCommand(buildPDMInspectCmd(&deps))
		root.SetArgs([]string{"--config", cfgPath, "--context", "pve1", "pdminspect"})

		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "Proxmox VE")
		require.Nil(t, deps, "the command must not run against the wrong product")
	})

	t.Run("any-client command with pdm context populates Deps.PDM", func(t *testing.T) {
		root := newRoot(t)

		var deps *cli.Deps
		root.AddCommand(buildAnyInspectCmd(&deps))
		root.SetArgs([]string{"--config", cfgPath, "--context", "pdm1", "anyinspect"})

		require.NoError(t, root.Execute())
		require.NotNil(t, deps, "inspect command must have run")
		require.NotNil(t, deps.PDM, "a pdm context must populate Deps.PDM via the any-client path")
		require.Nil(t, deps.API)
		require.Nil(t, deps.PBS)
	})

	t.Run("any-client command with pbs context populates Deps.PBS", func(t *testing.T) {
		root := newRoot(t)

		var deps *cli.Deps
		root.AddCommand(buildAnyInspectCmd(&deps))
		root.SetArgs([]string{"--config", cfgPath, "--context", "pbs1", "anyinspect"})

		require.NoError(t, root.Execute())
		require.NotNil(t, deps, "inspect command must have run")
		require.NotNil(t, deps.PBS, "a pbs context must populate Deps.PBS via the any-client path")
		require.Nil(t, deps.API)
		require.Nil(t, deps.PDM)
	})
}

// TestDispatch_UnknownProductFailsLoudly verifies that a command reaching the
// any-client dispatch path against a context whose product is not one of the
// three known products fails loudly instead of silently defaulting to a PVE
// client (see the Global Constraints "no silent PVE fallthrough" rule).
func TestDispatch_UnknownProductFailsLoudly(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "table")

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	cfg := &config.Config{
		CurrentContext: "bogus1",
		Contexts: map[string]*config.Context{
			"bogus1": {
				Host: "10.0.0.9", Port: 1, Protocol: "https", Realm: "pam",
				Product: "bogus",
				Auth: config.AuthBlock{
					Type: "token", Username: "root@pam", TokenID: "tok", Secret: "sekrit",
				},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	var deps *cli.Deps
	root.AddCommand(buildAnyInspectCmd(&deps))
	root.SetArgs([]string{"--config", cfgPath, "--context", "bogus1", "anyinspect"})

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), `unsupported product "bogus"`)
	require.Nil(t, deps, "the command must not run against an unrecognized product")
}
