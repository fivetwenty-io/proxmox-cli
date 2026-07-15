package node

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newOciCmd builds the `pmx node oci` sub-tree for working with OCI container
// images: querying the tags published for a repository and pulling an image
// into a node storage.
func newOciCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "oci",
		Short: "Query and pull OCI container images",
		Long: "Query the tags published for an OCI image repository and pull an OCI " +
			"image from a registry into a node storage. Pulling writes a new image " +
			"artifact to the storage and requires --yes.",
	}
	cmd.AddCommand(newOciTagsCmd(), newOciPullCmd())
	return cmd
}

// renderOciTask renders the asynchronous task started by an OCI pull. The
// endpoint returns a worker UPID; honour --async and otherwise block on the
// task, but tolerate a non-UPID or empty body by falling back to a plain
// success message.
func renderOciTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, doneMsg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
	}
	if deps.Async {
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{
				Single:  map[string]string{"upid": upid},
				Raw:     map[string]string{"upid": upid},
				Message: upid,
			}, deps.Format)
	}
	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return fmt.Errorf("OCI image pull on node %q: %w", deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
}

func newOciTagsCmd() *cobra.Command {
	var reference string
	cmd := &cobra.Command{
		Use:   "tags",
		Short: "List published tags for an OCI image repository",
		Long: "List the tags published for an OCI image repository as seen from the " +
			"resolved node. This is a read-only query.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListQueryOciRepoTags(cmd.Context(), deps.Node,
				&nodes.ListQueryOciRepoTagsParams{Reference: reference})
			if err != nil {
				return fmt.Errorf("query OCI repo tags for %q on node %q: %w", reference, deps.Node, err)
			}
			var tags []string
			if resp != nil {
				tags = []string(*resp)
			}
			rows := make([][]string, len(tags))
			for i, tg := range tags {
				rows[i] = []string{tg}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: []string{"TAG"}, Rows: rows, Raw: resp}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&reference, "reference", "",
		"OCI repository reference to query, for example docker.io/library/alpine (required)")
	cli.MustMarkRequired(cmd, "reference")
	return cmd
}

func newOciPullCmd() *cobra.Command {
	var (
		reference string
		filename  string
		yes       bool
	)
	cmd := &cobra.Command{
		Use:   "pull <storage>",
		Short: "Pull an OCI image into a node storage",
		Long: "Download an OCI container image from a registry into the given storage " +
			"on the resolved node. This writes a new image artifact to the storage " +
			"and requires --yes. The operation runs as a background task; use --async " +
			"to return the task UPID immediately.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			storage := args[0]
			if err := requireSystemYes(deps.Node, yes,
				fmt.Sprintf("pull OCI image %q into storage %q", reference, storage)); err != nil {
				return err
			}
			params := &nodes.CreateStorageOciRegistryPullParams{Reference: reference}
			if cmd.Flags().Changed("filename") {
				params.Filename = &filename
			}
			resp, err := deps.API.Nodes.CreateStorageOciRegistryPull(cmd.Context(), deps.Node, storage, params)
			if err != nil {
				return fmt.Errorf("pull OCI image %q into storage %q on node %q: %w",
					reference, storage, deps.Node, err)
			}
			return renderOciTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("OCI image %s pulled into %s.", reference, storage))
		},
	}
	f := cmd.Flags()
	f.StringVar(&reference, "reference", "",
		"OCI image reference to download, for example docker.io/library/alpine:latest (required)")
	f.StringVar(&filename, "filename", "", "custom destination file name (will be normalized)")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the pull without prompting")
	cli.MustMarkRequired(cmd, "reference")
	return cmd
}
