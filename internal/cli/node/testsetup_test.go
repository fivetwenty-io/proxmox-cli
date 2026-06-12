package node_test

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/cli/node"
)

// addNodeGroup wires the node command group into root for tests that need the
// full node sub-tree. It replaces the former global-registry approach.
func addNodeGroup(root *cobra.Command) {
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{node.Group})
}
