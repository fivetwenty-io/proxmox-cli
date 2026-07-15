package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/remote"
	"github.com/fivetwenty-io/proxmox-cli/internal/nodeaddr"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// newUploadCmd builds `pmx pve storage upload <storage>` — push a local file to a
// storage on the resolved node (POST /nodes/{node}/storage/{storage}/upload).
// The file is streamed as a multipart part; the operation is asynchronous, so by
// default the command blocks until the import task completes, or with --async it
// prints the task UPID and returns immediately.
//
// --content snippets takes a different transport entirely: the PVE upload API
// does not accept snippets (its content enum is iso|vztmpl|import; Proxmox
// Bugzilla #2208), so the file is streamed over SSH into the storage's
// snippets/ directory instead. See uploadSnippetSSH.
func newUploadCmd() *cobra.Command {
	var (
		file         string
		content      string
		filename     string
		checksum     string
		checksumAlgo string
		sshF         sshcmd.Flags
	)
	cmd := &cobra.Command{
		Use:   "upload <storage>",
		Short: "Upload a local file to a storage",
		Long: "Stream a local file to the resolved node's storage as an ISO image, container " +
			"template, or snippet. The destination name defaults to the source file's base name; " +
			"override it with --filename. Optionally verify the upload with --checksum and " +
			"--checksum-algorithm. The upload runs as an asynchronous task and the command blocks " +
			"until it finishes unless --async is set.\n\n" +
			"--content snippets is a workaround, not an API upload: the PVE upload endpoint " +
			"only accepts iso, vztmpl, and import (snippets upload is a long-standing gap, " +
			"Proxmox Bugzilla #2208 — https://bugzilla.proxmox.com/show_bug.cgi?id=2208), so the " +
			"file is streamed over SSH into the storage's snippets/ directory instead. This " +
			"requires a path-backed storage (dir, nfs, cifs, ...) with the snippets content type " +
			"enabled, and SSH access to the node (root by default; -l/-i/-p and the context ssh " +
			"block apply). --checksum is not supported in this mode, and no task is created.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
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

			if content == "snippets" {
				return uploadSnippetSSH(cmd, deps, &sshF, storage, dest, f)
			}

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
				} else if m, ok := resp.Data.(map[string]any); ok {
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
	fl.StringVar(&content, "content", "iso",
		"content type of the upload: iso|vztmpl|import|snippets (snippets streamed over SSH; see --help)")
	fl.StringVar(&filename, "filename", "", "destination file name (defaults to the source base name)")
	fl.StringVar(&checksum, "checksum", "", "expected checksum of the uploaded file")
	fl.StringVar(&checksumAlgo, "checksum-algorithm", "", "checksum algorithm: md5|sha1|sha224|sha256|sha384|sha512")
	sshcmd.RegisterFlags(cmd, &sshF)
	cli.MustMarkRequired(cmd, "file")
	return cmd
}

// snippetStorageConfig is the subset of GET /storage/{storage} needed to locate
// a storage's snippets directory on the node's filesystem.
type snippetStorageConfig struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// uploadSnippetSSH streams src into <storage-path>/snippets/<dest> on the
// resolved node over SSH. The PVE API has no snippets upload (the upload
// endpoint's content enum is iso|vztmpl|import — Proxmox Bugzilla #2208), so
// this is the only remote path onto snippet storage the CLI can offer. It
// requires a path-backed storage with the snippets content type enabled, and
// runs ssh in batch mode so a missing key or unknown host fails instead of
// prompting.
func uploadSnippetSSH(
	cmd *cobra.Command, deps *cli.Deps, f *sshcmd.Flags, storage, dest string, src *os.File,
) error {
	fl := cmd.Flags()
	if fl.Changed("checksum") || fl.Changed("checksum-algorithm") {
		return fmt.Errorf("--checksum is not supported with --content snippets: " +
			"the file is streamed over SSH, not through the PVE upload API")
	}
	if strings.Contains(dest, "/") || dest == "" || dest == "." || dest == ".." {
		return fmt.Errorf("invalid snippet file name %q: must be a plain file name without path separators", dest)
	}

	resp, err := deps.API.ClusterStorage.GetStorage(cmd.Context(), storage)
	if err != nil {
		return fmt.Errorf("get storage %q: %w", storage, err)
	}
	var scfg snippetStorageConfig
	if resp != nil {
		if err := json.Unmarshal(*resp, &scfg); err != nil {
			return fmt.Errorf("decode storage %q: %w", storage, err)
		}
	}
	if scfg.Path == "" {
		return fmt.Errorf("storage %q (type %q) has no filesystem path: the SSH snippets workaround "+
			"needs a path-backed storage (dir, nfs, cifs, ...)", storage, scfg.Type)
	}
	if !contentListHas(scfg.Content, "snippets") {
		return fmt.Errorf("snippets content is not enabled on storage %q: enable it first, e.g. "+
			"`pmx pve storage set %s --content %s`", storage, storage,
			strings.Join(append(splitContentList(scfg.Content), "snippets"), ","))
	}

	remote.ApplyContextSSHDefaults(cmd, deps, f, "user", "port", "identity")

	host, err := nodeaddr.Resolve(cmd.Context(), deps.API.Cluster, deps.Node)
	if err != nil {
		return fmt.Errorf("resolve address for node %q: %w", deps.Node, err)
	}

	snippetDir := path.Join(scfg.Path, "snippets")
	destPath := path.Join(snippetDir, dest)
	remoteCmd := fmt.Sprintf("mkdir -p %s && cat > %s",
		sshcmd.ShellQuote(snippetDir), sshcmd.ShellQuote(destPath))

	argv := append(sshcmd.BatchOptionArgs(f), sshcmd.Dest(f, host), remoteCmd)
	if err := deps.Runner.Run("ssh", argv, nil, src, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("upload snippet %q to storage %q on node %q over SSH: %w", dest, storage, deps.Node, err)
	}

	msg := fmt.Sprintf("Uploaded snippet %q to %s on node %q via SSH (PVE API has no snippets upload; Bugzilla #2208).",
		dest, destPath, deps.Node)
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// splitContentList splits a PVE content list ("iso,vztmpl,snippets") into its
// entries, dropping empty segments.
func splitContentList(list string) []string {
	parts := strings.Split(list, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// contentListHas reports whether the PVE content list contains the given type.
func contentListHas(list, want string) bool {
	return slices.Contains(splitContentList(list), want)
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
