package storage

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newFileRestoreCmd builds `pmx pve storage file-restore` — browse and extract
// individual files from a backup snapshot without a full restore. The backing
// endpoints currently support Proxmox Backup Server snapshots only.
func newFileRestoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file-restore",
		Short: "Browse and download single files from a backup snapshot",
		Long: "List directory entries inside a backup snapshot and download individual files " +
			"or directories from it. Currently only Proxmox Backup Server snapshots are supported.",
	}
	cmd.AddCommand(newFileRestoreListCmd(), newFileRestoreDownloadCmd())
	return cmd
}

// encodeFilepath encodes a path within a backup for the file-restore API, which
// expects a base64-encoded path. The archive root "/" is passed through
// verbatim, matching the endpoint's special case.
func encodeFilepath(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	return base64.StdEncoding.EncodeToString([]byte(p))
}

// decodeFilepaths decodes the base64 `filepath` each listing entry carries, so
// what the listing prints is what --filepath and `download` accept. The API
// answers in base64 and takes base64, and encoding only one of the two ends
// leaves the caller holding a value it cannot pass back. An entry whose
// filepath is not valid base64 is left untouched.
func decodeFilepaths(raws []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(raws))
	for _, raw := range raws {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			out = append(out, raw)
			continue
		}
		s, ok := m["filepath"].(string)
		if !ok || s == "" {
			out = append(out, raw)
			continue
		}
		dec, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			out = append(out, raw)
			continue
		}
		m["filepath"] = string(dec)
		next, err := json.Marshal(m)
		if err != nil {
			out = append(out, raw)
			continue
		}
		out = append(out, next)
	}
	return out
}

// newFileRestoreListCmd builds `pmx pve storage file-restore list <storage>`.
func newFileRestoreListCmd() *cobra.Command {
	var (
		volume   string
		filepath string
	)
	cmd := &cobra.Command{
		Use:   "list <storage>",
		Short: "List directory entries inside a backup snapshot",
		Long: "List the directory entries inside a backup snapshot at a given path.\n\n" +
			"Two things are required: a node, via --node or PMX_NODE or the active context's " +
			"default, and the backup to browse, via --volume.\n\n" +
			"--filepath picks the directory within the backup, and defaults to the archive " +
			"root. The filepath each entry reports can be passed straight back to " +
			"--filepath, or to `file-restore download`.\n\n" +
			"Only Proxmox Backup Server snapshots are supported so far.",
		Example: `  pmx pve storage file-restore list pbs --node pve1 --volume pbs:backup/vm/100/2026-01-01T00:00:00Z
  pmx pve storage file-restore list pbs --node pve1 --volume pbs:backup/vm/100/2026-01-01T00:00:00Z --filepath /etc`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			storage := args[0]

			params := &nodes.ListStorageFileRestoreListParams{
				Volume:   volume,
				Filepath: encodeFilepath(filepath),
			}
			resp, err := deps.API.Nodes.ListStorageFileRestoreList(cmd.Context(), deps.Node, storage, params)
			if err != nil {
				return fmt.Errorf("list files in %q on storage %q (node %q): %w",
					volume, storage, deps.Node, err)
			}

			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			return deps.Out.Render(cmd.OutOrStdout(), rawObjectListResult(decodeFilepaths(raws)), deps.Format)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&volume, "volume", "", "backup volume ID or snapshot to browse (required)")
	fl.StringVar(&filepath, "filepath", "/", "path within the backup to list (base64-encoded for the API)")
	cli.MustMarkRequired(cmd, "volume")
	return cmd
}

// newFileRestoreDownloadCmd builds `pmx pve storage file-restore download <storage>`.
func newFileRestoreDownloadCmd() *cobra.Command {
	var (
		volume     string
		filepath   string
		tar        bool
		outputFile string
	)
	cmd := &cobra.Command{
		Use:   "download <storage>",
		Short: "Download a single file or directory from a backup snapshot",
		Long: "Download a file (or, with --tar, a directory) from inside a backup snapshot. " +
			"The raw bytes are written to --output-file, or to standard output when it is omitted.",
		Example: `  pmx pve storage file-restore download pbs --node pve1 \
  --volume pbs:backup/vm/100/2026-01-01T00:00:00Z --filepath /etc/hosts --output-file hosts`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			storage := args[0]
			fl := cmd.Flags()

			// The response is the restored file's own bytes, so it is fetched
			// through the client's undecoded path: the typed call parses every
			// body as the API's JSON envelope, which a file never is.
			params := map[string]any{
				"volume":   volume,
				"filepath": encodeFilepath(filepath),
			}
			if fl.Changed("tar") {
				params["tar"] = tar
			}

			path := fmt.Sprintf("/nodes/%s/storage/%s/file-restore/download",
				url.PathEscape(deps.Node), url.PathEscape(storage))
			data, err := deps.API.Raw.GetBytesCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("download %q from %q on storage %q (node %q): %w",
					filepath, volume, storage, deps.Node, err)
			}
			if outputFile != "" {
				if err := os.WriteFile(outputFile, data, 0o600); err != nil {
					return fmt.Errorf("write %q: %w", outputFile, err)
				}
				res := output.Result{Message: fmt.Sprintf("Wrote %d bytes to %q.", len(data), outputFile)}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&volume, "volume", "", "backup volume ID or snapshot to read from (required)")
	fl.StringVar(&filepath, "filepath", "", "path within the backup to download (required)")
	fl.BoolVar(&tar, "tar", false, "download a directory as tar.zst instead of zip")
	fl.StringVar(&outputFile, "output-file", "", "write the download to this file instead of standard output")
	cli.MustMarkRequired(cmd, "volume")
	cli.MustMarkRequired(cmd, "filepath")
	return cmd
}

// newImportMetadataCmd builds `pmx pve storage import-metadata <storage>` — inspect
// the create parameters a foreign guest archive (e.g. an OVA or ESXi import)
// would map to before actually importing it.
func newImportMetadataCmd() *cobra.Command {
	var volume string
	cmd := &cobra.Command{
		Use:   "import-metadata <storage>",
		Short: "Show the import parameters detected for a guest archive",
		Long: "Inspect the guest-creation parameters Proxmox VE detects for a foreign guest " +
			"archive, such as an OVA or an ESXi import, without importing it. Requires a node " +
			"via --node, PMX_NODE, or the active context's default, and the archive to " +
			"inspect via --volume. The detected settings are rendered as a single object.",
		Example: `  pmx pve storage import-metadata esxi-store --node pve1 --volume esxi-store:import/vm.ova`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			storage := args[0]

			resp, err := deps.API.Nodes.ListStorageImportMetadata(cmd.Context(), deps.Node, storage,
				&nodes.ListStorageImportMetadataParams{Volume: volume})
			if err != nil {
				return fmt.Errorf("read import metadata for %q on storage %q (node %q): %w",
					volume, storage, deps.Node, err)
			}

			fields := map[string]any{}
			raw, mErr := json.Marshal(resp)
			if mErr == nil {
				_ = json.Unmarshal(raw, &fields)
			}
			single := make(map[string]string, len(fields))
			for k, v := range fields {
				single[k] = scalarString(v)
			}
			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&volume, "volume", "", "guest archive volume to inspect (required)")
	cli.MustMarkRequired(cmd, "volume")
	return cmd
}

// rawObjectListResult renders a slice of untyped JSON objects as a table whose
// columns are the sorted union of the objects' keys, with upper-cased headers.
// The raw slice is preserved for JSON and YAML output.
func rawObjectListResult(raws []json.RawMessage) output.Result {
	objs := make([]map[string]any, 0, len(raws))
	keySet := map[string]struct{}{}
	for _, raw := range raws {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			continue
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
			row[i] = scalarString(obj[k])
		}
		rows = append(rows, row)
	}

	return output.Result{Headers: headers, Rows: rows, Raw: objs}
}
