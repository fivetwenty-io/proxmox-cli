package lab

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestContextSSHJump covers the ssh.jump a lab ssh call inherits: a stored
// chain is used as written, and both an unset value and the "none" literal
// mean no jump, so ssh never sees "-J none".
func TestContextSSHJump(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{
		{"", ""},
		{"none", ""},
		{"ops@bastion.example", "ops@bastion.example"},
		{"a@one:2200,b@two", "a@one:2200,b@two"},
	} {
		deps := &cli.Deps{Ctx: &config.Context{SSH: config.SSHBlock{Jump: tc.stored}}}
		assert.Equal(t, tc.want, contextSSHJump(deps), "stored %q", tc.stored)
	}
}

// TestSSHFlags_ShareTheContextJump pins that the dataset ssh calls (create,
// nfs, quota set, destroy --purge-dataset) and the guest ssh calls resolve
// the context's jump the same way.
func TestSSHFlags_ShareTheContextJump(t *testing.T) {
	deps := &cli.Deps{Ctx: &config.Context{SSH: config.SSHBlock{User: "ops", Port: 2200, Jump: "none"}}}

	dataset := createDatasetSSHFlags(deps)
	guest, err := labGuestSSHFlags(deps)
	require.NoError(t, err)
	assert.Empty(t, dataset.Jump)
	assert.Empty(t, guest.Jump)

	deps.Ctx.SSH.Jump = "bastion.example"
	dataset = createDatasetSSHFlags(deps)
	guest, err = labGuestSSHFlags(deps)
	require.NoError(t, err)
	assert.Equal(t, "bastion.example", dataset.Jump)
	assert.Equal(t, "bastion.example", guest.Jump)
	assert.Equal(t, "ops", dataset.User)
	assert.Equal(t, 2200, dataset.Port)
}

// TestLabDatasetNode covers where a dataset verb finds its node. The node the
// lab's VMs live on decides and an ambient node never overrides it; VMs on
// several nodes, and an explicit --node that disagrees with them, are refused;
// and a lab with no VMs falls back according to the verb's policy.
func TestLabDatasetNode(t *testing.T) {
	ambient := &cli.Deps{Node: "pve-0"}
	explicit := &cli.Deps{Node: "pve-0", NodeExplicit: true}

	t.Run("the VMs' node beats an ambient node", func(t *testing.T) {
		for _, policy := range []labDatasetNodePolicy{labDatasetNodeAmbient, labDatasetNodeExplicit} {
			got, err := labDatasetNode(ambient, "verb", []string{"pve-2", "pve-2"}, policy)
			require.NoError(t, err)
			assert.Equal(t, "pve-2", got, "ambient pve-0 must not win over the node the VMs are on")
		}
	})

	t.Run("an explicit node that agrees with the VMs is accepted", func(t *testing.T) {
		got, err := labDatasetNode(&cli.Deps{Node: "pve-2", NodeExplicit: true}, "verb", []string{"pve-2"},
			labDatasetNodeExplicit)
		require.NoError(t, err)
		assert.Equal(t, "pve-2", got)
	})

	t.Run("an explicit node that disagrees with the VMs is refused", func(t *testing.T) {
		_, err := labDatasetNode(explicit, "verb", []string{"pve-2"}, labDatasetNodeAmbient)
		require.Error(t, err)
		assert.ErrorContains(t, err, `--node "pve-0" disagrees with node "pve-2"`)
	})

	t.Run("VMs on more than one node are refused", func(t *testing.T) {
		_, err := labDatasetNode(explicit, "verb", []string{"pve-2", "pve-1", "pve-2"}, labDatasetNodeAmbient)
		require.Error(t, err)
		assert.ErrorContains(t, err, "more than one node (pve-1, pve-2)")
	})

	t.Run("no VMs and the ambient policy uses the invocation's node", func(t *testing.T) {
		got, err := labDatasetNode(ambient, "verb", nil, labDatasetNodeAmbient)
		require.NoError(t, err)
		assert.Equal(t, "pve-0", got)

		_, err = labDatasetNode(&cli.Deps{}, "the verb", nil, labDatasetNodeAmbient)
		require.ErrorContains(t, err, "the verb needs the outer PVE node")
	})

	t.Run("no VMs and the explicit policy needs --node", func(t *testing.T) {
		_, err := labDatasetNode(ambient, "the verb", []string{""}, labDatasetNodeExplicit)
		require.Error(t, err)
		assert.ErrorContains(t, err, "found no VM of the lab")
		assert.ErrorContains(t, err, "pass --node")

		got, err := labDatasetNode(explicit, "the verb", nil, labDatasetNodeExplicit)
		require.NoError(t, err)
		assert.Equal(t, "pve-0", got)
	})
}

// TestContextSSHJump_FollowsTheAPIRoute covers a lab ssh call under a jump
// override. The jump of the route the API dials wins over the stored ssh.jump,
// so --api-jump and $PMX_API_JUMP move the ssh calls with the API calls, and
// an override of "none" dials direct even when a jump is stored.
func TestContextSSHJump_FollowsTheAPIRoute(t *testing.T) {
	stored := &config.Context{Host: "pve-0", SSH: config.SSHBlock{Jump: "ops@stored-bastion"}}

	route := func(t *testing.T, jump string) cli.Connection {
		t.Helper()

		conn, err := cli.ResolveConnection("sm", stored, cli.ConnectionOverrides{
			Jump: jump, JumpSource: "--api-jump",
		})
		require.NoError(t, err)
		return conn
	}

	t.Run("no override keeps the stored jump", func(t *testing.T) {
		deps := &cli.Deps{Ctx: stored, Route: route(t, "")}
		assert.Equal(t, "ops@stored-bastion", contextSSHJump(deps))
	})

	t.Run("an override replaces the stored jump", func(t *testing.T) {
		deps := &cli.Deps{Ctx: stored, Route: route(t, "ops@other-bastion:2200")}
		assert.Equal(t, "ops@other-bastion:2200", contextSSHJump(deps))
	})

	t.Run("none dials direct", func(t *testing.T) {
		deps := &cli.Deps{Ctx: stored, Route: route(t, "none")}
		assert.Empty(t, contextSSHJump(deps))
	})
}

// TestCreate_ZfsDatasetEnsure_UsesContextSSHJump covers create's own dataset
// probe: with ssh.jump on the context the probe tunnels through it, aimed at
// the node's resolved address.
func TestCreate_ZfsDatasetEnsure_UsesContextSSHJump(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	poolID := lab.Access.Pool

	createSharedResourcesExist(f, t, lab, "wayne", poolID)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9500")
	f.HandleJSON("POST /api2/json/nodes/node1/qemu", createTestUPID)
	createHandleTaskStatus(f)

	fake := exec.Fake(
		exec.FakeResponse{ExitCode: 1}, // remote "zfs list": dataset absent
		exec.FakeResponse{},            // zfs create
	)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmdWithZFSFake(t, path, f, "node1", fake)
	cli.GetDeps(cmd).Ctx.SSH.Jump = "ops@bastion.example:2200"

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)

	require.Len(t, fake.Calls, 2)
	for _, call := range fake.Calls {
		assert.Equal(t, []string{"-p", "22", "-J", "ops@bastion.example:2200", "root@" + createDatasetTestNodeIP},
			call.Args[:5], "every dataset ssh call tunnels through the context's jump")
	}
}
