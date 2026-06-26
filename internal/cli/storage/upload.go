package storage

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newUploadCmd builds `pve storage upload <storage>` — push a local file to a
// storage on the resolved node (POST /nodes/{node}/storage/{storage}/upload).
// The file is streamed as a multipart part; the operation is asynchronous, so by
// default the command blocks until the import task completes, or with --async it
// prints the task UPID and returns immediately.
func newUploadCmd() *cobra.Command {
	var (
		file         string
		content      string
		filename     string
		checksum     string
		checksumAlgo string
	)
	cmd := &cobra.Command{
		Use:   "upload <storage>",
		Short: "Upload a local file to a storage",
		Long: "Stream a local file to the resolved node's storage as an ISO image or container " +
			"template. The destination name defaults to the source file's base name; override it " +
			"with --filename. Optionally verify the upload with --checksum and --checksum-algorithm. " +
			"The upload runs as an asynchronous task and the command blocks until " +
			"it finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PVE_NODE, or configure a default node")
			}
			storage := args[0]
			fl := cmd.Flags()

			dest := filename
			if dest == "" {
				dest = filepath.Base(file)
			}

			f, err := os.Open(file) //nolint:gosec // G304: file is a CLI --file flag value supplied by the operator, not untrusted remote input
			if err != nil {
				return fmt.Errorf("open file %q: %w", file, err)
			}
			defer func() { _ = f.Close() }()

			// Build the multipart form fields. The PVE upload endpoint expects
			// "content" as a plain form field; the file is sent as the "filename"
			// file part (its filename attribute carries the destination name). Do
			// NOT also pass "filename" as a plain form field — PVE rejects (HTTP
			// 400) when the same multipart part name appears twice.
			fields := map[string]string{"content": content}
			if fl.Changed("checksum") {
				fields["checksum"] = checksum
			}
			if fl.Changed("checksum-algorithm") {
				fields["checksum-algorithm"] = checksumAlgo
			}

			path := fmt.Sprintf("/nodes/%s/storage/%s/upload",
				url.PathEscape(deps.Node), url.PathEscape(storage))

			resp, err := deps.API.Raw.UploadCtx(cmd.Context(), path, fields, "filename", dest, f)
			if err != nil {
				return fmt.Errorf("upload %q to storage %q on node %q: %w", dest, storage, deps.Node, err)
			}

			var upid string
			if resp != nil {
				if s, ok := resp.Data.(string); ok {
					upid = s
				} else if m, ok := resp.Data.(map[string]interface{}); ok {
					if v, ok := m["upid"].(string); ok {
						upid = v
					}
				}
			}

			return renderStorageTask(cmd, deps, upid,
				fmt.Sprintf("Uploaded %q to storage %q on node %q.", dest, storage, deps.Node))
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&file, "file", "", "path to the local file to upload (required)")
	fl.StringVar(&content, "content", "iso", "content type of the upload: iso|vztmpl|import")
	fl.StringVar(&filename, "filename", "", "destination file name (defaults to the source base name)")
	fl.StringVar(&checksum, "checksum", "", "expected checksum of the uploaded file")
	fl.StringVar(&checksumAlgo, "checksum-algorithm", "", "checksum algorithm: md5|sha1|sha224|sha256|sha384|sha512")
	cli.MustMarkRequired(cmd, "file")
	return cmd
}

// renderStorageTask blocks on an asynchronous storage task and renders the
// outcome, or — when --async is set, or the response carried no UPID — prints the
// UPID (or the supplied done message) without waiting. An empty UPID means the
// storage plugin completed the operation synchronously.
func renderStorageTask(cmd *cobra.Command, deps *cli.Deps, upid, doneMsg string) error {
	if upid == "" {
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
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
}
