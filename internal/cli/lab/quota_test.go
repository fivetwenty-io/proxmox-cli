package lab

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// quotaTestNodeIP is the address the testhelper's default GET /cluster/status
// route reports for node "pve1". The quota tests assert it is the ssh
// destination, and that the context's own host never is.
const quotaTestNodeIP = "192.168.1.10"

// quotaTestCtxHost is the context's own configured host in the quota tests:
// the cluster's API entrypoint, which must never be the ssh destination.
const quotaTestCtxHost = "sm-0.lab.internal"

// quotaTestCmd builds a bare cobra.Command carrying a *cli.Deps loaded from
// configPath (via newCmdWithDeps, the shared package helper), plus the same
// --refquota-gb/--dry-run/--yes flags newQuotaSetCmd registers so
// cmd.Flags().Changed("refquota-gb") reflects real flag-parsing semantics
// rather than a hardcoded bool. flagArgs is parsed against those flags
// before returning, so e.g. quotaTestCmd(t, path, "--refquota-gb=999")
// leaves Changed("refquota-gb") true exactly as real invocation would.
func quotaTestCmd(t *testing.T, configPath string, flagArgs ...string) *cobra.Command {
	t.Helper()

	cmd := newCmdWithDeps(t, configPath)
	cmd.Flags().Int("refquota-gb", 0, "")
	cmd.Flags().Bool("dry-run", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	require.NoError(t, cmd.Flags().Parse(flagArgs))

	return cmd
}

// quotaFlagValues reads back the three flags quotaTestCmd registers, in the
// shape runQuotaSet's positional parameters expect.
func quotaFlagValues(t *testing.T, cmd *cobra.Command) (refquotaGB int, dryRun, yes bool) {
	t.Helper()

	var err error
	refquotaGB, err = cmd.Flags().GetInt("refquota-gb")
	require.NoError(t, err)
	dryRun, err = cmd.Flags().GetBool("dry-run")
	require.NoError(t, err)
	yes, err = cmd.Flags().GetBool("yes")
	require.NoError(t, err)
	return refquotaGB, dryRun, yes
}

// wireQuotaDeps attaches a fake ssh context (host + connection settings), a
// *exec.FakeRunner, a fake PVE API client reporting node "pve1" at
// quotaTestNodeIP, and an output.Renderer to cmd's already-loaded *cli.Deps
// (mutating the same pointer newCmdWithDeps stashed in cmd's context), and
// wires cmd's in/out/err streams to the given buffers. Returns the
// FakeRunner so tests can inspect its recorded Calls.
func wireQuotaDeps(t *testing.T, cmd *cobra.Command, stdin string, stdout, stderr *bytes.Buffer) *exec.FakeRunner {
	t.Helper()

	f := testhelper.NewFakePVE(t)
	registerNodeCertificates(f)
	return wireQuotaDepsOn(t, cmd, f, stdin, stdout, stderr)
}

// wireQuotaDepsOn is wireQuotaDeps against a FakePVE the test has already
// given the routes it needs, such as the lab's VMs.
func wireQuotaDepsOn(
	t *testing.T, cmd *cobra.Command, f *testhelper.FakePVE, stdin string, stdout, stderr *bytes.Buffer,
) *exec.FakeRunner {
	t.Helper()

	deps := cli.GetDeps(cmd)
	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	deps.API = api
	deps.Node = "pve1"
	deps.Ctx = &config.Context{
		Host: quotaTestCtxHost,
		SSH: config.SSHBlock{
			User:     "root",
			Port:     2222,
			Identity: "/home/wayne/.ssh/lab_ed25519",
		},
	}
	fake := exec.Fake()
	deps.Runner = newIdentityRunner(fake)
	deps.Out = output.New()
	deps.Format = output.FormatPlain

	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	return fake
}

// TestLabQuota_TargetsNodeAddressNotContextHost is the regression test for
// quota set ssh-ing to the context's own host. On a multi-node cluster that
// host is only the API entrypoint and may not accept ssh at all, so the verb
// must reach the node's own address, resolved from the cluster the same way
// the create verb's dataset step resolves it.
func TestLabQuota_TargetsNodeAddressNotContextHost(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)

	t.Run("the node address is the ssh destination", func(t *testing.T) {
		cmd := quotaTestCmd(t, path, "--yes")
		var stdout, stderr bytes.Buffer
		fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)

		refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
		require.NoError(t, runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes))
		require.Len(t, fake.Calls, 1)
		assert.Contains(t, fake.Calls[0].Args, "root@"+quotaTestNodeIP)
		assert.NotContains(t, strings.Join(fake.Calls[0].Args, " "), quotaTestCtxHost)
	})

	t.Run("an endpoint override no longer makes the target ambiguous", func(t *testing.T) {
		cmd := quotaTestCmd(t, path, "--yes")
		var stdout, stderr bytes.Buffer
		fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)
		deps := cli.GetDeps(cmd)
		deps.CtxName = "sm"
		conn, err := cli.ResolveConnection("sm", deps.Ctx, cli.ConnectionOverrides{
			Host: "pve9.lab.internal", Port: 8443, EndpointSource: "$PMX_API_ENDPOINT",
		})
		require.NoError(t, err)
		deps.Route = conn

		refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
		require.NoError(t, runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes))
		require.Len(t, fake.Calls, 1)
		assert.Contains(t, fake.Calls[0].Args, "root@"+quotaTestNodeIP,
			"the node address comes from the cluster the API reached, so there is nothing to refuse")
	})

	t.Run("no node anywhere is a clear error before any ssh", func(t *testing.T) {
		cmd := quotaTestCmd(t, path, "--yes")
		var stdout, stderr bytes.Buffer
		fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)
		cli.GetDeps(cmd).Node = ""

		refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
		err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
		require.Error(t, err)
		assert.ErrorContains(t, err, "pass --node")
		assert.Empty(t, fake.Calls)
	})

	t.Run("no context is a clear error", func(t *testing.T) {
		cmd := quotaTestCmd(t, path, "--yes")
		var stdout, stderr bytes.Buffer
		fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)
		cli.GetDeps(cmd).Ctx = nil

		refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
		err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
		require.Error(t, err)
		assert.ErrorContains(t, err, "active pmx context")
		assert.Empty(t, fake.Calls)
	})
}

// TestLabQuota_UsesContextSSHJump covers the jump half of the same bug: a
// context reached through a bastion lists its bastion in ssh.jump, and the
// quota ssh call must tunnel through it, as the create verb's dataset step
// does. The "none" literal means dial direct and must not become "-J none".
func TestLabQuota_UsesContextSSHJump(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)

	run := func(t *testing.T, jump string) []string {
		t.Helper()

		cmd := quotaTestCmd(t, path, "--yes")
		var stdout, stderr bytes.Buffer
		fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)
		cli.GetDeps(cmd).Ctx.SSH.Jump = jump

		refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
		require.NoError(t, runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes))
		require.Len(t, fake.Calls, 1)
		return fake.Calls[0].Args
	}

	t.Run("a stored jump chain is passed as -J", func(t *testing.T) {
		args := run(t, "ops@bastion.example:2200")
		assert.Equal(t, []string{
			"-p", "2222",
			"-J", "ops@bastion.example:2200",
			"-i", "/home/wayne/.ssh/lab_ed25519",
			"root@" + quotaTestNodeIP,
			"zfs", "set", "quota=50G", "refquota=50G", "tank/labs/alpha",
		}, args)
	})

	t.Run("none dials direct", func(t *testing.T) {
		assert.NotContains(t, run(t, "none"), "-J")
	})

	t.Run("an empty jump dials direct", func(t *testing.T) {
		assert.NotContains(t, run(t, ""), "-J")
	})
}

// TestLabQuota_NodeFollowsTheVMs covers which node quota set acts on. The node
// the lab's VMs live on decides and an ambient node never overrides it, VMs on
// several nodes and an explicit --node that disagrees with them are refused,
// and a lab with no VMs keeps using the ambient node.
func TestLabQuota_NodeFollowsTheVMs(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)

	vm := func(vmid int, node, pool string) map[string]any {
		return map[string]any{
			"vmid": vmid, "node": node, "pool": pool, "name": "lab-alpha", "status": "running", "type": "qemu",
		}
	}
	twoNodes := []any{
		map[string]any{"type": "node", "name": "pve1", "ip": quotaTestNodeIP, "online": 1},
		map[string]any{"type": "node", "name": "pve2", "ip": "192.168.1.20", "online": 1},
	}

	run := func(
		t *testing.T, resources []any, explicit bool, ambient string,
	) (*exec.FakeRunner, error) {
		t.Helper()

		f := testhelper.NewFakePVE(t)
		registerNodeCertificates(f)
		f.HandleJSON("GET /api2/json/cluster/status", twoNodes)
		f.HandleJSON("GET /api2/json/cluster/resources", resources)

		cmd := quotaTestCmd(t, path, "--yes")
		var stdout, stderr bytes.Buffer
		fake := wireQuotaDepsOn(t, cmd, f, "", &stdout, &stderr)
		deps := cli.GetDeps(cmd)
		deps.Node, deps.NodeExplicit = ambient, explicit

		refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
		return fake, runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	}

	t.Run("the VMs' node beats an ambient node", func(t *testing.T) {
		fake, err := run(t, []any{vm(100, "pve2", "lab-alpha")}, false, "pve1")
		require.NoError(t, err)
		require.Len(t, fake.Calls, 1)
		assert.Contains(t, fake.Calls[0].Args, "root@192.168.1.20")
		assert.NotContains(t, fake.Calls[0].Args, "root@"+quotaTestNodeIP)
	})

	t.Run("VMs of another pool do not count", func(t *testing.T) {
		fake, err := run(t, []any{vm(100, "pve2", "lab-other")}, false, "pve1")
		require.NoError(t, err)
		require.Len(t, fake.Calls, 1)
		assert.Contains(t, fake.Calls[0].Args, "root@"+quotaTestNodeIP, "no VM of this lab, so the ambient node stands")
	})

	t.Run("an explicit node that agrees with the VMs is accepted", func(t *testing.T) {
		fake, err := run(t, []any{vm(100, "pve2", "lab-alpha")}, true, "pve2")
		require.NoError(t, err)
		require.Len(t, fake.Calls, 1)
		assert.Contains(t, fake.Calls[0].Args, "root@192.168.1.20")
	})

	t.Run("an explicit node that disagrees with the VMs is refused", func(t *testing.T) {
		fake, err := run(t, []any{vm(100, "pve2", "lab-alpha")}, true, "pve1")
		require.Error(t, err)
		assert.ErrorContains(t, err, `--node "pve1" disagrees with node "pve2"`)
		assert.Empty(t, fake.Calls)
	})

	t.Run("VMs on more than one node are refused", func(t *testing.T) {
		fake, err := run(t, []any{vm(100, "pve1", "lab-alpha"), vm(101, "pve2", "lab-alpha")}, false, "pve1")
		require.Error(t, err)
		assert.ErrorContains(t, err, "more than one node (pve1, pve2)")
		assert.Empty(t, fake.Calls)
	})
}

// TestLabQuota_JumpOverrideReplacesStoredJump covers the quota ssh call under
// --api-jump or $PMX_API_JUMP: the jump of the route the API dials wins over
// the context's stored ssh.jump, and an override of "none" dials direct.
func TestLabQuota_JumpOverrideReplacesStoredJump(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)

	run := func(t *testing.T, override string) []string {
		t.Helper()

		cmd := quotaTestCmd(t, path, "--yes")
		var stdout, stderr bytes.Buffer
		fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)
		deps := cli.GetDeps(cmd)
		deps.CtxName = "sm"
		deps.Ctx.SSH.Jump = "ops@stored.example:2200"
		conn, err := cli.ResolveConnection("sm", deps.Ctx, cli.ConnectionOverrides{
			Jump: override, JumpSource: "$PMX_API_JUMP",
		})
		require.NoError(t, err)
		deps.Route = conn

		refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
		require.NoError(t, runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes))
		require.Len(t, fake.Calls, 1)
		return fake.Calls[0].Args
	}

	args := run(t, "ops@override.example:2201")
	assert.Contains(t, args, "ops@override.example:2201")
	assert.NotContains(t, args, "ops@stored.example:2200")

	assert.NotContains(t, run(t, "none"), "-J", "an override of none beats a stored jump")
}

func TestQuotaSet_HappyPathWithYes_UsesConfigRefquota(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"), // Storage.RefquotaGB: 50
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	require.Len(t, fake.Calls, 1)
	call := fake.Calls[0]
	assert.Equal(t, "ssh", call.Name)
	assert.False(t, call.Interactive)
	assert.Equal(t, []string{
		"-p", "2222", "-o", "ProxyJump=none",
		"-i", "/home/wayne/.ssh/lab_ed25519",
		"root@" + quotaTestNodeIP,
		"zfs", "set", "quota=50G", "refquota=50G", "tank/labs/alpha",
	}, call.Args)
}

// TestQuotaSet_UsesConfiguredStoragePoolForDataset covers quota set's
// dataset targeting: it must derive the dataset from the lab's own
// storage.pool, the same base pool create.go allocates disks on, not a
// hardcoded "tank/labs/<name>".
func TestQuotaSet_UsesConfiguredStoragePoolForDataset(t *testing.T) {
	lab := cleanLab("alpha") // Storage.RefquotaGB: 50
	lab.Storage.Pool = "othertank"
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": lab,
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	require.Len(t, fake.Calls, 1)
	assert.Contains(t, fake.Calls[0].Args, "othertank/labs/alpha")
}

func TestQuotaSet_RefquotaFlagOverridesConfig(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"), // Storage.RefquotaGB: 50
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes", "--refquota-gb=999")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	require.Len(t, fake.Calls, 1)
	assert.Contains(t, fake.Calls[0].Args, "refquota=999G")
	assert.Contains(t, fake.Calls[0].Args, "quota=999G")
	assert.NotContains(t, fake.Calls[0].Args, "refquota=50G")
	assert.NotContains(t, fake.Calls[0].Args, "quota=50G")
}

func TestQuotaSet_DryRun_RecordsNoCallAndPrintsCommand(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--dry-run")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	assert.Empty(t, fake.Calls, "dry-run must never invoke the runner")
	out := stdout.String()
	assert.Contains(t, out, "ssh")
	assert.Contains(t, out, "-p 2222")
	assert.Contains(t, out, "root@"+quotaTestNodeIP)
	assert.NotContains(t, out, quotaTestCtxHost)
	assert.Contains(t, out, "zfs set quota=50G refquota=50G tank/labs/alpha")
	assert.Contains(t, out, "A real run first probes the ssh target over ssh")
	assert.Contains(t, out, "and only then runs this zfs command")
}

// TestQuotaSetHelp_SaysARealRunProbesTheTargetFirst keeps the help in step with
// the dry run's note about the identity probe.
func TestQuotaSetHelp_SaysARealRunProbesTheTargetFirst(t *testing.T) {
	cmd := newQuotaSetCmd()
	assert.Contains(t, cmd.Long, "a real run first probes the target's identity")
	assert.Contains(t, cmd.Long, "over ssh and only then runs the zfs command")
	assert.Contains(t, cmd.Flags().Lookup("dry-run").Usage, "a real run first probes the target's identity over ssh")
}

func TestQuotaSet_RefusesWithoutYesNonInteractively(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path)
	var stdout, stderr bytes.Buffer
	// Empty stdin simulates a non-interactive invocation: the confirmation
	// read hits EOF immediately and must be treated as "no", not a hang or
	// a panic.
	fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	assert.Empty(t, fake.Calls, "must refuse to run without confirmation")
	assert.Contains(t, stdout.String(), "Aborted")
}

func TestQuotaSet_MissingRefquotaEverywhere_HelpfulError(t *testing.T) {
	lab := cleanLab("alpha")
	lab.Storage.RefquotaGB = 0
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": lab,
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no positive refquota")
	assert.ErrorContains(t, err, `"alpha"`)
	assert.Empty(t, fake.Calls)
}

func TestQuotaSet_PeppiGuardRefusesBeforeAnyRunnerCall(t *testing.T) {
	dirty := cleanLab("dirty")
	dirty.Network.VnetID = "peppivn0"
	dirty.Storage.RefquotaGB = 50 // valid, isolates the failure to the guard

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"dirty": dirty,
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(t, cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "dirty", refquotaGB, dryRun, yes)
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls, "peppi refusal must happen before any ssh call")
}
