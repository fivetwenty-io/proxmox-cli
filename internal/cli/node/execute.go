package node

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newNodeExecuteCmd builds `pmx pve node execute`. It sends a JSON-encoded array of
// commands to the PVE API's /nodes/{node}/execute endpoint and renders the
// result array. This is distinct from `pmx pve node exec` (SSH-based remote
// command execution).
func newNodeExecuteCmd() *cobra.Command {
	var commandsJSON string
	cmd := &cobra.Command{
		Use:   "execute",
		Short: "Execute commands on the node via the PVE API",
		Long: "Send a JSON-encoded array of commands to the Proxmox VE API for execution on the " +
			"resolved node. The value of --commands must be a valid JSON array, for example: " +
			`'["uname -a","hostname"]'. Each element becomes one command.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			// Validate that --commands parses as a JSON array before sending to the API.
			var arr []json.RawMessage
			if err := json.Unmarshal([]byte(commandsJSON), &arr); err != nil {
				return fmt.Errorf("--commands must be a valid JSON array: %w", err)
			}
			resp, err := deps.API.Nodes.CreateExecute(cmd.Context(), deps.Node,
				&nodes.CreateExecuteParams{Commands: commandsJSON})
			if err != nil {
				return fmt.Errorf("execute commands on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().StringVar(&commandsJSON, "commands", "",
		"JSON-encoded array of commands to execute, e.g. '[\"uname -a\",\"hostname\"]' (required)")
	cli.MustMarkRequired(cmd, "commands")
	return cmd
}
