package lab

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/nodeaddr"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// sshJumpNone is the literal an operator writes to ssh.jump to say "no jump,
// dial direct". The API side and `pmx ssh` both read it that way, so an ssh
// call built here must not hand ssh a "-J none" that it would take for a
// jump host literally named none.
const sshJumpNone = "none"

// contextSSHJump returns the ssh -J destination list every lab ssh call uses,
// or "" for a direct dial. When the invocation built a client, that is the
// bastion the API connection itself dials through (deps.Route.Jump), so
// --api-jump and $PMX_API_JUMP move the ssh calls together with the API
// calls, and "none" from any source dials direct. Without a client it is the
// context's own ssh.jump, with the "none" literal read as no jump.
// deps.Ctx must be non-nil.
func contextSSHJump(deps *cli.Deps) string {
	if deps.Route.Host != "" {
		return deps.Route.Jump.Chain
	}

	jump := deps.Ctx.SSH.Jump
	if jump == sshJumpNone {
		return ""
	}
	return jump
}

// labDatasetNodePolicy says what a verb does when the lab has no VM to show
// which node hosts its dataset.
type labDatasetNodePolicy int

const (
	// labDatasetNodeAmbient falls back to the invocation's node: --node,
	// $PMX_NODE, or the context's default node. It is for a verb that sets a
	// property and can be re-run.
	labDatasetNodeAmbient labDatasetNodePolicy = iota

	// labDatasetNodeExplicit requires --node on the command line. It is for a
	// verb that destroys the dataset, where an ambient node could be the
	// wrong machine.
	labDatasetNodeExplicit
)

// labDatasetNode picks the outer PVE node that hosts a lab's ZFS dataset for
// a verb that has to reach it over ssh.
//
// When the lab has VMs, the node they live on decides, because that is where
// create put the dataset. An ambient node ($PMX_NODE or the context's default
// node) never overrides them. VMs on more than one node are refused, since
// there is no one node to act on, and so is an explicit --node that names a
// different node than the VMs, since one of the two is wrong and the verb
// cannot tell which.
//
// When the lab has no VMs the policy decides: labDatasetNodeAmbient takes the
// invocation's node, and labDatasetNodeExplicit refuses unless --node was
// passed. verb names the command in the errors.
func labDatasetNode(deps *cli.Deps, verb string, vmNodes []string, policy labDatasetNodePolicy) (string, error) {
	distinct := distinctNodes(vmNodes)

	switch len(distinct) {
	case 0:
		switch {
		case policy == labDatasetNodeExplicit && (!deps.NodeExplicit || deps.Node == ""):
			return "", fmt.Errorf(
				"%s found no VM of the lab, so nothing shows which node hosts its ZFS dataset; "+
					"pass --node naming that node", verb)
		case deps.Node == "":
			return "", fmt.Errorf(
				"%s needs the outer PVE node that hosts the lab's ZFS dataset; "+
					"pass --node, set $PMX_NODE, or configure a context default node", verb)
		}
		return deps.Node, nil

	case 1:
		if deps.NodeExplicit && deps.Node != "" && deps.Node != distinct[0] {
			return "", fmt.Errorf(
				"%s: --node %q disagrees with node %q, where the lab's VMs live and where its ZFS dataset is; "+
					"drop --node or pass the node the lab lives on", verb, deps.Node, distinct[0])
		}
		return distinct[0], nil

	default:
		return "", fmt.Errorf(
			"%s: the lab's VMs live on more than one node (%s), so there is no single node whose ZFS dataset "+
				"to act on; move the VMs to one node or act on the dataset by hand",
			verb, strings.Join(distinct, ", "))
	}
}

// distinctNodes returns the sorted, non-empty, de-duplicated names in nodes.
func distinctNodes(nodes []string) []string {
	seen := make(map[string]struct{}, len(nodes))
	var out []string
	for _, n := range nodes {
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// labPoolVMNodes returns the node of every live VM in pool, so a dataset verb
// can ask where the lab actually lives. Unlike findLabVMs it does not refuse a
// member whose name matches no lab convention: such a VM is still a guest on
// some node, and that node still counts.
func labPoolVMNodes(vms []labVM, pool string) []string {
	var nodes []string
	for _, vm := range vms {
		if vm.Pool == pool {
			nodes = append(nodes, vm.Node)
		}
	}
	return nodes
}

// resolveLabDatasetSSH resolves where and how to reach the lab's ZFS dataset
// over ssh: the connection flags from the active context, with the ssh jump
// of the route the API itself takes (createDatasetSSHFlags), and the node's
// own address from cluster status (createDatasetSSHHost), exactly as the
// create verb's dataset step does. It is the single resolution every lab verb
// that runs zfs on the outer node shares, so none of them can fall back to the
// context's own host, which on a multi-node cluster is only the API
// entrypoint and may not be reachable by ssh at all.
//
// With strict set, the address must come from cluster status. A node that the
// API's cluster does not list, an empty list, or a failed lookup is an error,
// where the lenient lookup would hand ssh the bare node name to resolve
// through DNS or ~/.ssh/config, possibly to a machine in a different cluster.
// A verb that destroys data passes strict.
func resolveLabDatasetSSH(
	ctx context.Context, deps *cli.Deps, verb, node string, strict bool,
) (sshcmd.Flags, string, error) {
	if deps.Ctx == nil {
		return sshcmd.Flags{}, "", fmt.Errorf(
			"%s requires an active pmx context to resolve an ssh target; select one with --context/-c", verb)
	}
	if deps.API == nil {
		return sshcmd.Flags{}, "", fmt.Errorf("%s requires an API client to resolve the address of node %q", verb, node)
	}

	var (
		host string
		err  error
	)
	if strict {
		host, err = nodeaddr.ResolveStrict(ctx, deps.API.Cluster, node)
		if err != nil {
			return sshcmd.Flags{}, "", fmt.Errorf(
				"%s will not guess the address of node %q: %w; "+
					"nothing was changed, so fix the node name or the cluster connection and retry", verb, node, err)
		}
	} else {
		host, err = createDatasetSSHHost(ctx, deps.API, node, deps.Log)
		if err != nil {
			return sshcmd.Flags{}, "", err
		}
	}

	return createDatasetSSHFlags(deps), host, nil
}
