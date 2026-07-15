package storage

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newDownloadURLCmd builds `pmx pve storage download-url <storage>` — instruct the
// resolved node to fetch a file from a URL directly onto a storage
// (POST /nodes/{node}/storage/{storage}/download-url). The download runs as an
// asynchronous task; by default the command blocks until it completes, or with
// --async it prints the task UPID and returns immediately.
func newDownloadURLCmd() *cobra.Command {
	var (
		url          string
		filename     string
		content      string
		checksum     string
		checksumAlgo string
		compression  string
		verifyCerts  bool
	)
	cmd := &cobra.Command{
		Use:   "download-url <storage>",
		Short: "Download a file from a URL onto a storage",
		Long: "Have the resolved node fetch the file at --url and store it on the given storage " +
			"under --filename. Optionally verify the download against a --checksum, decompress it " +
			"with --compression, or skip TLS verification with --no-verify-certificates. The download " +
			"runs as an asynchronous task and the command blocks until it finishes unless --async is set.",
		Example: `  pmx pve storage download-url local-lvm --url https://example.com/image.iso --filename image.iso
  pmx pve storage download-url local-lvm --url https://example.com/image.iso --filename image.iso --async`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			storage := args[0]
			fl := cmd.Flags()

			params := &nodes.CreateStorageDownloadUrlParams{
				Url:      url,
				Filename: filename,
				Content:  content,
			}
			if fl.Changed("checksum") {
				params.Checksum = &checksum
			}
			if fl.Changed("checksum-algorithm") {
				params.ChecksumAlgorithm = &checksumAlgo
			}
			if fl.Changed("compression") {
				params.Compression = &compression
			}
			if fl.Changed("verify-certificates") {
				params.VerifyCertificates = &verifyCerts
			}

			resp, err := deps.API.Nodes.CreateStorageDownloadUrl(cmd.Context(), deps.Node, storage, params)
			if err != nil {
				return fmt.Errorf("download %q to storage %q on node %q: %w", filename, storage, deps.Node, err)
			}

			var raw json.RawMessage
			if resp != nil {
				raw = json.RawMessage(*resp)
			}
			upid, err := apiclient.UPIDFromRaw(raw)
			if err != nil {
				return fmt.Errorf("download %q to storage %q on node %q: %w", filename, storage, deps.Node, err)
			}

			return renderStorageTask(cmd, deps, upid,
				fmt.Sprintf("Downloaded %q to storage %q on node %q.", filename, storage, deps.Node))
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&url, "url", "", "URL to download the file from (required)")
	fl.StringVar(&filename, "filename", "", "destination file name on the storage (required)")
	fl.StringVar(&content, "content", "iso", "content type of the download: iso|vztmpl|import")
	fl.StringVar(&checksum, "checksum", "", "expected checksum of the file")
	fl.StringVar(&checksumAlgo, "checksum-algorithm", "", "checksum algorithm: md5|sha1|sha224|sha256|sha384|sha512")
	fl.StringVar(&compression, "compression", "", "decompress the download with this algorithm")
	fl.BoolVar(&verifyCerts, "verify-certificates", true, "verify the server's TLS certificate (use --verify-certificates=false to skip)")
	cli.MustMarkRequired(cmd, "url")
	cli.MustMarkRequired(cmd, "filename")
	return cmd
}
