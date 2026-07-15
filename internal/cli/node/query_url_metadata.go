package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newQueryUrlMetadataCmd builds `pmx node query-url-metadata` — proxies a URL
// metadata request through the resolved node. Useful for resolving metadata for
// URLs that are only reachable from a node's network (for example an ISO on an
// internal HTTP server). The --url flag is required.
func newQueryUrlMetadataCmd() *cobra.Command {
	var (
		url                string
		verifyCertificates bool
	)
	cmd := &cobra.Command{
		Use:   "query-url-metadata",
		Short: "Query URL metadata through the node",
		Long: "Resolve URL metadata (filename, MIME type, size) for the given URL by " +
			"proxying the request through the resolved node. Useful for URLs that are " +
			"only reachable from the node's network. --url is required.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListQueryUrlMetadataParams{Url: url}
			if cmd.Flags().Changed("verify-certificates") {
				params.VerifyCertificates = &verifyCertificates
			}
			resp, err := deps.API.Nodes.ListQueryUrlMetadata(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("query URL metadata on node %q: %w", deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "URL to query metadata for (required)")
	cmd.Flags().BoolVar(&verifyCertificates, "verify-certificates", true,
		"verify SSL/TLS certificates when fetching the URL")
	cli.MustMarkRequired(cmd, "url")
	return cmd
}
