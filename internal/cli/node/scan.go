package node

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newScanCmd builds the `pve node scan` sub-tree: storage discovery probes that
// enumerate the LVM volume groups, LVM-thin pools, ZFS pools, and remote NFS,
// CIFS, iSCSI, and Proxmox Backup Server exports reachable from the node.
func newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Discover local and remote storage from a node",
		Long: "Probe for storage reachable from the resolved node. The lvm, lvmthin, and " +
			"zfs probes enumerate local volume groups and pools; the nfs, cifs, iscsi, " +
			"and pbs probes query a remote server for its exports. All probes are " +
			"read-only.",
	}
	cmd.AddCommand(
		newScanLvmCmd(),
		newScanLvmthinCmd(),
		newScanZfsCmd(),
		newScanNfsCmd(),
		newScanCifsCmd(),
		newScanIscsiCmd(),
		newScanPbsCmd(),
	)
	return cmd
}

// derefRaws converts a pointer to any named []json.RawMessage response type
// into a plain slice, tolerating a nil pointer.
func derefRaws[T ~[]json.RawMessage](resp *T) []json.RawMessage {
	if resp == nil {
		return nil
	}
	return []json.RawMessage(*resp)
}

// rawObjectListResult builds a union-key table over a list of JSON objects so a
// scan or hardware response of varying shape renders as a stable table. The Raw
// field is left for the caller to set to the original lossless response.
func rawObjectListResult(raws []json.RawMessage) (output.Result, error) {
	keySet := make(map[string]struct{})
	objs := make([]map[string]any, 0, len(raws))
	for _, raw := range raws {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return output.Result{}, err
		}
		objs = append(objs, obj)
		for k := range obj {
			keySet[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	headers := make([]string, len(keys))
	for i, k := range keys {
		headers[i] = strings.ToUpper(k)
	}
	rows := make([][]string, 0, len(objs))
	for _, obj := range objs {
		row := make([]string, len(keys))
		for i, k := range keys {
			row[i] = anyCell(obj[k])
		}
		rows = append(rows, row)
	}
	return output.Result{Headers: headers, Rows: rows}, nil
}

// renderScan runs a scan that returned a list of JSON objects and renders it.
func renderScan(cmd *cobra.Command, deps *cli.Deps, raws []json.RawMessage, raw any) error {
	res, err := rawObjectListResult(raws)
	if err != nil {
		return fmt.Errorf("decode scan result: %w", err)
	}
	res.Raw = raw
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// ---- local probes (no arguments) -------------------------------------------

func newScanLvmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lvm",
		Short: "List local LVM volume groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListScanLvm(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("scan LVM on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newScanZfsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "zfs",
		Short: "List local ZFS pools",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListScanZfs(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("scan ZFS on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newScanLvmthinCmd() *cobra.Command {
	var vg string
	cmd := &cobra.Command{
		Use:   "lvmthin",
		Short: "List LVM-thin pools within a volume group",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListScanLvmthin(cmd.Context(), deps.Node,
				&nodes.ListScanLvmthinParams{Vg: vg})
			if err != nil {
				return fmt.Errorf("scan LVM-thin on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().StringVar(&vg, "vg", "", "volume group to scan for thin pools (required)")
	cli.MustMarkRequired(cmd, "vg")
	return cmd
}

// ---- remote probes (require a server/target) -------------------------------

func newScanNfsCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "nfs",
		Short: "List NFS exports on a remote server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListScanNfs(cmd.Context(), deps.Node,
				&nodes.ListScanNfsParams{Server: server})
			if err != nil {
				return fmt.Errorf("scan NFS server %q from node %q: %w", server, deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "NFS server address (name or IP) (required)")
	cli.MustMarkRequired(cmd, "server")
	return cmd
}

func newScanCifsCmd() *cobra.Command {
	var (
		server   string
		domain   string
		username string
		password string
	)
	cmd := &cobra.Command{
		Use:   "cifs",
		Short: "List CIFS/SMB shares on a remote server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListScanCifsParams{Server: server}
			fl := cmd.Flags()
			if fl.Changed("domain") {
				params.Domain = &domain
			}
			if fl.Changed("username") {
				params.Username = &username
			}
			if fl.Changed("password") {
				params.Password = &password
			}
			resp, err := deps.API.Nodes.ListScanCifs(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("scan CIFS server %q from node %q: %w", server, deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	f := cmd.Flags()
	f.StringVar(&server, "server", "", "CIFS/SMB server address (name or IP) (required)")
	f.StringVar(&domain, "domain", "", "SMB domain (workgroup)")
	f.StringVar(&username, "username", "", "user name for authenticated shares")
	f.StringVar(&password, "password", "", "user password for authenticated shares")
	cli.MustMarkRequired(cmd, "server")
	return cmd
}

func newScanIscsiCmd() *cobra.Command {
	var portal string
	cmd := &cobra.Command{
		Use:   "iscsi",
		Short: "List iSCSI targets on a portal",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListScanIscsi(cmd.Context(), deps.Node,
				&nodes.ListScanIscsiParams{Portal: portal})
			if err != nil {
				return fmt.Errorf("scan iSCSI portal %q from node %q: %w", portal, deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().StringVar(&portal, "portal", "", "iSCSI portal (IP or DNS name, optional :port) (required)")
	cli.MustMarkRequired(cmd, "portal")
	return cmd
}

func newScanPbsCmd() *cobra.Command {
	var (
		server      string
		username    string
		password    string
		port        int64
		fingerprint string
	)
	cmd := &cobra.Command{
		Use:   "pbs",
		Short: "List datastores on a Proxmox Backup Server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListScanPbsParams{Server: server, Username: username, Password: password}
			fl := cmd.Flags()
			if fl.Changed("port") {
				params.Port = &port
			}
			if fl.Changed("fingerprint") {
				params.Fingerprint = &fingerprint
			}
			resp, err := deps.API.Nodes.ListScanPbs(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("scan Proxmox Backup Server %q from node %q: %w", server, deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	f := cmd.Flags()
	f.StringVar(&server, "server", "", "PBS server address (name or IP) (required)")
	f.StringVar(&username, "username", "", "user name or API token ID (required)")
	f.StringVar(&password, "password", "", "user password or API token secret (required)")
	f.Int64Var(&port, "port", 0, "optional PBS port")
	f.StringVar(&fingerprint, "fingerprint", "", "certificate SHA-256 fingerprint")
	cli.MustMarkRequired(cmd, "server")
	cli.MustMarkRequired(cmd, "username")
	cli.MustMarkRequired(cmd, "password")
	return cmd
}
