package storage

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// resolveStorageNode picks the node used to address a storage-scoped endpoint:
// deps.Node when --node was passed explicitly, otherwise a node carrying the
// storage according to the cluster resource inventory (see
// cli.ResolveStorageNode), with a stderr note when the choice differs from the
// ambient default node.
func resolveStorageNode(cmd *cobra.Command, deps *cli.Deps, storage string) (string, error) {
	node, err := cli.ResolveStorageNode(cmd.Context(), deps, storage)
	if err != nil {
		return "", err
	}
	cli.NoteResolvedStorageNode(cmd.ErrOrStderr(), deps, storage, node)
	return node, nil
}
