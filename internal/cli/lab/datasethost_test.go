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

// TestLabDatasetNode covers where a dataset verb finds its node: the
// resolved --node/default node wins, a single distinct fallback node is
// taken, and anything ambiguous is an error that says how to fix it.
func TestLabDatasetNode(t *testing.T) {
	got, err := labDatasetNode(&cli.Deps{Node: "pve2"}, "verb", "pve1")
	require.NoError(t, err)
	assert.Equal(t, "pve2", got)

	got, err = labDatasetNode(&cli.Deps{}, "verb", "pve1", "pve1", "")
	require.NoError(t, err)
	assert.Equal(t, "pve1", got)

	_, err = labDatasetNode(&cli.Deps{}, "verb", "pve1", "pve2")
	require.ErrorContains(t, err, "pass --node")

	_, err = labDatasetNode(&cli.Deps{}, "the verb")
	require.ErrorContains(t, err, "the verb needs the outer PVE node")
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
