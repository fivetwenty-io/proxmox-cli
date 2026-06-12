package cluster

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newNextIDCmd builds `pve cluster next-id`.
func newNextIDCmd() *cobra.Command {
	var vmidHint int64

	cmd := &cobra.Command{
		Use:   "next-id",
		Short: "Return the next free VM/CT ID",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pvecluster.ListNextidParams{}
			if vmidHint != 0 {
				params.Vmid = &vmidHint
			}

			resp, err := deps.API.Cluster.ListNextid(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get next id: %w", err)
			}
			if resp == nil {
				return fmt.Errorf("get next id: empty response")
			}

			id, err := decodeNextID(*resp)
			if err != nil {
				return err
			}

			result := output.Result{
				Headers: []string{"VMID"},
				Rows:    [][]string{{id}},
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}

	cmd.Flags().Int64Var(&vmidHint, "vmid", 0, "preferred VM/CT ID to validate as available")
	return cmd
}

// decodeNextID extracts the next-id value from the raw response. PVE returns the
// ID as a JSON string (e.g. "100"); a bare JSON number is accepted as well.
func decodeNextID(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", fmt.Errorf("get next id: empty value in response")
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return "", fmt.Errorf("get next id: empty value in response")
		}
		return s, nil
	}

	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String(), nil
	}

	return "", fmt.Errorf("get next id: cannot decode value %q", trimmed)
}
