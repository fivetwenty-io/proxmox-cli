package lab

import (
	"context"
	"fmt"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// sshJumpNone is the literal an operator writes to ssh.jump to say "no jump,
// dial direct". The API side and `pmx ssh` both read it that way, so an ssh
// call built here must not hand ssh a "-J none" that it would take for a
// jump host literally named none.
const sshJumpNone = "none"

// contextSSHJump returns the ssh -J destination list configured on the active
// context's ssh.jump, or "" when the context sets none or sets the "none"
// literal. deps.Ctx must be non-nil.
func contextSSHJump(deps *cli.Deps) string {
	jump := deps.Ctx.SSH.Jump
	if jump == sshJumpNone {
		return ""
	}
	return jump
}

// labDatasetNode picks the outer PVE node that hosts a lab's ZFS dataset for
// a verb that has to reach it over ssh: the node from --node, $PMX_NODE, or
// the context's default node. When none of those is set and fallback holds
// exactly one distinct node (for example the one node every VM of the lab
// lives on), that node is used. verb names the command in the error.
func labDatasetNode(deps *cli.Deps, verb string, fallback ...string) (string, error) {
	if deps.Node != "" {
		return deps.Node, nil
	}

	distinct := make(map[string]struct{}, len(fallback))
	var only string
	for _, n := range fallback {
		if n == "" {
			continue
		}
		distinct[n] = struct{}{}
		only = n
	}
	if len(distinct) == 1 {
		return only, nil
	}

	return "", fmt.Errorf(
		"%s needs the outer PVE node that hosts the lab's ZFS dataset; "+
			"pass --node, set $PMX_NODE, or configure a context default node", verb)
}

// resolveLabDatasetSSH resolves where and how to reach the lab's ZFS dataset
// over ssh: the connection flags from the active context, including its
// ssh.jump (createDatasetSSHFlags), and the node's own address
// (createDatasetSSHHost), exactly as the create verb's dataset step does. It
// is the single resolution every lab verb that runs zfs on the outer node
// shares, so none of them can fall back to the context's own host, which on
// a multi-node cluster is only the API entrypoint and may not be reachable by
// ssh at all.
func resolveLabDatasetSSH(ctx context.Context, deps *cli.Deps, verb, node string) (sshcmd.Flags, string, error) {
	if deps.Ctx == nil {
		return sshcmd.Flags{}, "", fmt.Errorf(
			"%s requires an active pmx context to resolve an ssh target; select one with --context/-c", verb)
	}
	if deps.API == nil {
		return sshcmd.Flags{}, "", fmt.Errorf("%s requires an API client to resolve the address of node %q", verb, node)
	}

	host, err := createDatasetSSHHost(ctx, deps.API, node, deps.Log)
	if err != nil {
		return sshcmd.Flags{}, "", err
	}

	return createDatasetSSHFlags(deps), host, nil
}
