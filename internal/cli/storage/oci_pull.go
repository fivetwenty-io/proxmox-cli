package storage

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newOciPullCmd builds `pmx pve storage oci-pull <storage>` — instruct the resolved
// node to pull an OCI image from a registry and store it on the given storage
// (POST /nodes/{node}/storage/{storage}/oci-registry-pull). The pull runs as an
// asynchronous task; the command blocks until it finishes unless --async is set.
func newOciPullCmd() *cobra.Command {
	var (
		reference string
		filename  string
	)
	cmd := &cobra.Command{
		Use:   "oci-pull <storage>",
		Short: "Pull an OCI image from a registry to a storage",
		Long: "Instruct the resolved node to pull the OCI image at --reference from a container " +
			"registry and store it on the given storage " +
			"(POST /nodes/{node}/storage/{storage}/oci-registry-pull). " +
			"The pull runs as an asynchronous task and the command blocks until it finishes " +
			"unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			storage := args[0]
			fl := cmd.Flags()

			params := &nodes.CreateStorageOciRegistryPullParams{
				Reference: reference,
			}
			if fl.Changed("filename") {
				params.Filename = &filename
			}

			resp, err := deps.API.Nodes.CreateStorageOciRegistryPull(cmd.Context(), deps.Node, storage, params)
			if err != nil {
				return fmt.Errorf("OCI pull %q to storage %q on node %q: %w", reference, storage, deps.Node, err)
			}

			var raw json.RawMessage
			if resp != nil {
				raw = json.RawMessage(*resp)
			}
			upid, err := apiclient.UPIDFromRaw(raw)
			if err != nil {
				return fmt.Errorf("OCI pull %q to storage %q on node %q: %w", reference, storage, deps.Node, err)
			}

			return renderStorageTask(cmd, deps, upid,
				fmt.Sprintf("OCI pull %q to storage %q on node %q complete.", reference, storage, deps.Node))
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&reference, "reference", "", "OCI image reference to pull, e.g. docker.io/library/alpine:latest (required)")
	fl.StringVar(&filename, "filename", "", "custom destination file name on the storage (defaults to the normalized image reference)")
	cli.MustMarkRequired(cmd, "reference")
	return cmd
}
