package storage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/clusterstorage"
)

func init() {
	cli.RegisterGroup(newGroupCmd)
}

// newGroupCmd builds the `pve storage` command and all of its sub-commands.
// The supplied *cli.Deps is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained per-invocation via cli.GetDeps.
func newGroupCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Manage cluster storage configuration",
		Long:  "List, inspect, create, update, and delete Proxmox VE cluster storage definitions.",
	}
	cmd.AddCommand(
		newListCmd(),
		newGetCmd(),
		newContentCmd(),
		newCreateCmd(),
		newSetCmd(),
		newDeleteCmd(),
		newPruneCmd(),
		newUploadCmd(),
		newDownloadURLCmd(),
	)
	return cmd
}

// storageEntry is the subset of a storage definition rendered in list output.
// Storage definitions are returned by the API as untyped objects, so fields are
// decoded individually and absent fields render as empty cells.
type storageEntry struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Path    string `json:"path"`
	Server  string `json:"server"`
	Export  string `json:"export"`
	Nodes   string `json:"nodes"`
	Shared  int    `json:"shared"`
	Disable int    `json:"disable"`
}

// pathOrServer returns the most descriptive location field for a storage entry:
// the filesystem path if present, otherwise the server (optionally with export).
func (e storageEntry) pathOrServer() string {
	if e.Path != "" {
		return e.Path
	}
	if e.Server != "" && e.Export != "" {
		return e.Server + ":" + e.Export
	}
	return e.Server
}

// newListCmd builds `pve storage list`.
func newListCmd() *cobra.Command {
	var typeFilter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured cluster storage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &clusterstorage.ListStorageParams{}
			if typeFilter != "" {
				params.Type = &typeFilter
			}
			resp, err := deps.API.ClusterStorage.ListStorage(cmd.Context(), params)
			if err != nil {
				return err
			}

			entries := make([]storageEntry, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e storageEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode storage entry: %w", err)
					}
					entries = append(entries, e)
				}
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Storage < entries[j].Storage })

			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Storage,
					e.Type,
					e.Content,
					e.pathOrServer(),
					e.Nodes,
					boolCell(e.Shared == 1),
					boolCell(e.Disable == 0),
				})
			}

			res := output.Result{
				Headers: []string{"STORAGE", "TYPE", "CONTENT", "PATH/SERVER", "NODES", "SHARED", "ENABLED"},
				Rows:    rows,
				Raw:     rawList(resp),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&typeFilter, "type", "", "only list storage of the given type")
	return cmd
}

// newGetCmd builds `pve storage get <storage>`.
func newGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <storage>",
		Short: "Show a single storage definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.ClusterStorage.GetStorage(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			fields := map[string]any{}
			if resp != nil {
				if err := json.Unmarshal(*resp, &fields); err != nil {
					return fmt.Errorf("decode storage: %w", err)
				}
			}
			// Defensively strip credentials in case the backend echoes a stored
			// value; they are write-only inputs and must never appear in output.
			for _, k := range storageSecretKeys {
				delete(fields, k)
			}

			single := make(map[string]string, len(fields))
			for k, v := range fields {
				single[k] = scalarString(v)
			}

			res := output.Result{Single: single, Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// storageSecretKeys are the credential fields that must never be echoed. They
// are forwarded to the API on create/set but stripped from `get` output in case
// the backend ever returns a stored value.
var storageSecretKeys = []string{"password", "keyring", "encryption-key", "master-pubkey"}

// storageFlags collects the storage attributes shared by create and set. The
// create-only identity fields (path, export, share, vgname, thinpool, datastore,
// portal, target) select where the backend lives and have no update parameter,
// so they are bound only on create; everything else applies to both verbs.
type storageFlags struct {
	// create-only identity fields
	path        string
	export      string
	share       string
	vgname      string
	thinpool    string
	datastore   string
	portal      string
	iscsiTarget string

	// shared tunables
	server         string
	content        string
	contentDirs    string
	nodes          string
	shared         bool
	enabled        bool
	username       string
	password       string
	domain         string
	smbversion     string
	options        string
	subdir         string
	mountpoint     string
	pool           string
	dataPool       string
	monhost        string
	fsName         string
	namespace      string
	krbd           bool
	fuse           bool
	keyring        string
	fingerprint    string
	encryptionKey  string
	masterPubkey   string
	port           int64
	format         string
	preallocation  string
	bwlimit        string
	pruneBackups   string
	maxProtected   int64
	mkdir          bool
	createBasePath bool
	createSubdirs  bool
	sparse         bool
	isMountpoint   string
	skipCertVerify bool

	// update-only
	del    string
	digest string
}

// registerCreate binds every storage attribute flag, including the create-only
// identity fields, onto cmd.
func (sf *storageFlags) registerCreate(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&sf.path, "path", "", "file system path (dir)")
	f.StringVar(&sf.export, "export", "", "NFS export path (nfs)")
	f.StringVar(&sf.share, "share", "", "CIFS share name (cifs)")
	f.StringVar(&sf.vgname, "vgname", "", "LVM volume group name (lvm, lvmthin)")
	f.StringVar(&sf.thinpool, "thinpool", "", "LVM thin pool LV name (lvmthin)")
	f.StringVar(&sf.datastore, "datastore", "", "Proxmox Backup Server datastore name (pbs)")
	f.StringVar(&sf.portal, "portal", "", "iSCSI portal, IP or DNS name with optional port (iscsi)")
	f.StringVar(&sf.iscsiTarget, "iscsi-target", "", "iSCSI target (iscsi)")
	sf.registerCommon(cmd)
}

// registerSet binds the storage attribute flags that the update endpoint accepts.
func (sf *storageFlags) registerSet(cmd *cobra.Command) {
	sf.registerCommon(cmd)
	f := cmd.Flags()
	f.StringVar(&sf.del, "delete", "", "comma-separated list of settings to reset to default")
	f.StringVar(&sf.digest, "digest", "", "prevent changes if the config digest differs")
}

// registerCommon binds the attribute flags accepted by both create and update.
func (sf *storageFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&sf.server, "server", "", "server IP or DNS name (nfs, cifs, pbs)")
	f.StringVar(&sf.content, "content", "", "allowed content types (comma-separated)")
	f.StringVar(&sf.contentDirs, "content-dirs", "", "overrides for default content type directories")
	f.StringVar(&sf.nodes, "nodes", "", "nodes the storage applies to (comma-separated)")
	f.BoolVar(&sf.shared, "shared", false, "mark the storage as shared")
	f.BoolVar(&sf.enabled, "enabled", true, "enable the storage")
	f.StringVar(&sf.username, "username", "", "user name for the share/datastore")
	f.StringVar(&sf.password, "password", "", "password for the share/datastore (never echoed)")
	f.StringVar(&sf.domain, "domain", "", "CIFS domain")
	f.StringVar(&sf.smbversion, "smbversion", "", "SMB protocol version (cifs)")
	f.StringVar(&sf.options, "options", "", "NFS/CIFS mount options")
	f.StringVar(&sf.subdir, "subdir", "", "subdirectory to mount (cifs, nfs)")
	f.StringVar(&sf.mountpoint, "mountpoint", "", "mount point (zfspool)")
	f.StringVar(&sf.pool, "pool", "", "pool name (rbd, cephfs, zfspool)")
	f.StringVar(&sf.dataPool, "data-pool", "", "data pool, for erasure coding (rbd)")
	f.StringVar(&sf.monhost, "monhost", "", "monitor IP addresses for external Ceph clusters")
	f.StringVar(&sf.fsName, "fs-name", "", "Ceph filesystem name (cephfs)")
	f.StringVar(&sf.namespace, "namespace", "", "namespace (rbd, pbs)")
	f.BoolVar(&sf.krbd, "krbd", false, "always access rbd through the krbd kernel module")
	f.BoolVar(&sf.fuse, "fuse", false, "mount CephFS through FUSE (cephfs)")
	f.StringVar(&sf.keyring, "keyring", "", "client keyring contents for external Ceph clusters (never echoed)")
	f.StringVar(&sf.fingerprint, "fingerprint", "", "certificate SHA-256 fingerprint (pbs)")
	f.StringVar(&sf.encryptionKey, "encryption-key", "", "encryption key, or 'autogen' (pbs; never echoed)")
	f.StringVar(&sf.masterPubkey, "master-pubkey", "", "base64 PEM public RSA key for backup encryption (pbs; never echoed)")
	f.Int64Var(&sf.port, "port", 0, "alternate port for the storage (pbs, esxi)")
	f.StringVar(&sf.format, "format", "", "default image format (raw, qcow2, vmdk)")
	f.StringVar(&sf.preallocation, "preallocation", "", "preallocation mode for raw/qcow2 images")
	f.StringVar(&sf.bwlimit, "bwlimit", "", "I/O bandwidth limit in KiB/s")
	f.StringVar(&sf.pruneBackups, "prune-backups", "", "backup retention options, e.g. keep-last=3,keep-daily=7")
	f.Int64Var(&sf.maxProtected, "max-protected-backups", 0, "max protected backups per guest (-1 for unlimited)")
	f.BoolVar(&sf.mkdir, "mkdir", false, "create the directory if missing (deprecated; use create-base-path/create-subdirs)")
	f.BoolVar(&sf.createBasePath, "create-base-path", false, "create the base directory if it doesn't exist (dir)")
	f.BoolVar(&sf.createSubdirs, "create-subdirs", false, "populate the directory with the default structure (dir)")
	f.BoolVar(&sf.sparse, "sparse", false, "use sparse volumes (zfspool)")
	f.StringVar(&sf.isMountpoint, "is-mountpoint", "", "treat the path as an externally managed mountpoint (dir)")
	f.BoolVar(&sf.skipCertVerify, "skip-cert-verification", false, "disable TLS certificate verification (trusted networks only)")
}

// applyCreate builds the create payload, forwarding optional flags only when set.
func (sf *storageFlags) applyCreate(cmd *cobra.Command, p *clusterstorage.CreateStorageParams) {
	fl := cmd.Flags()
	str := func(name string, v *string, dst **string) {
		if fl.Changed(name) {
			*dst = v
		}
	}
	bl := func(name string, v *bool, dst **bool) {
		if fl.Changed(name) {
			*dst = v
		}
	}
	i64 := func(name string, v *int64, dst **int64) {
		if fl.Changed(name) {
			*dst = v
		}
	}
	str("path", &sf.path, &p.Path)
	str("export", &sf.export, &p.Export)
	str("share", &sf.share, &p.Share)
	str("vgname", &sf.vgname, &p.Vgname)
	str("thinpool", &sf.thinpool, &p.Thinpool)
	str("datastore", &sf.datastore, &p.Datastore)
	str("portal", &sf.portal, &p.Portal)
	str("iscsi-target", &sf.iscsiTarget, &p.Target)
	str("server", &sf.server, &p.Server)
	str("content", &sf.content, &p.Content)
	str("content-dirs", &sf.contentDirs, &p.ContentDirs)
	str("nodes", &sf.nodes, &p.Nodes)
	bl("shared", &sf.shared, &p.Shared)
	if fl.Changed("enabled") {
		p.Disable = boolptr(!sf.enabled)
	}
	str("username", &sf.username, &p.Username)
	str("password", &sf.password, &p.Password)
	str("domain", &sf.domain, &p.Domain)
	str("smbversion", &sf.smbversion, &p.Smbversion)
	str("options", &sf.options, &p.Options)
	str("subdir", &sf.subdir, &p.Subdir)
	str("mountpoint", &sf.mountpoint, &p.Mountpoint)
	str("pool", &sf.pool, &p.Pool)
	str("data-pool", &sf.dataPool, &p.DataPool)
	str("monhost", &sf.monhost, &p.Monhost)
	str("fs-name", &sf.fsName, &p.FsName)
	str("namespace", &sf.namespace, &p.Namespace)
	bl("krbd", &sf.krbd, &p.Krbd)
	bl("fuse", &sf.fuse, &p.Fuse)
	str("keyring", &sf.keyring, &p.Keyring)
	str("fingerprint", &sf.fingerprint, &p.Fingerprint)
	str("encryption-key", &sf.encryptionKey, &p.EncryptionKey)
	str("master-pubkey", &sf.masterPubkey, &p.MasterPubkey)
	i64("port", &sf.port, &p.Port)
	str("format", &sf.format, &p.Format)
	str("preallocation", &sf.preallocation, &p.Preallocation)
	str("bwlimit", &sf.bwlimit, &p.Bwlimit)
	str("prune-backups", &sf.pruneBackups, &p.PruneBackups)
	i64("max-protected-backups", &sf.maxProtected, &p.MaxProtectedBackups)
	bl("mkdir", &sf.mkdir, &p.Mkdir)
	bl("create-base-path", &sf.createBasePath, &p.CreateBasePath)
	bl("create-subdirs", &sf.createSubdirs, &p.CreateSubdirs)
	bl("sparse", &sf.sparse, &p.Sparse)
	str("is-mountpoint", &sf.isMountpoint, &p.IsMountpoint)
	bl("skip-cert-verification", &sf.skipCertVerify, &p.SkipCertVerification)
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (sf *storageFlags) applyUpdate(cmd *cobra.Command, p *clusterstorage.UpdateStorageParams) {
	fl := cmd.Flags()
	str := func(name string, v *string, dst **string) {
		if fl.Changed(name) {
			*dst = v
		}
	}
	bl := func(name string, v *bool, dst **bool) {
		if fl.Changed(name) {
			*dst = v
		}
	}
	i64 := func(name string, v *int64, dst **int64) {
		if fl.Changed(name) {
			*dst = v
		}
	}
	str("server", &sf.server, &p.Server)
	str("content", &sf.content, &p.Content)
	str("content-dirs", &sf.contentDirs, &p.ContentDirs)
	str("nodes", &sf.nodes, &p.Nodes)
	bl("shared", &sf.shared, &p.Shared)
	if fl.Changed("enabled") {
		p.Disable = boolptr(!sf.enabled)
	}
	str("username", &sf.username, &p.Username)
	str("password", &sf.password, &p.Password)
	str("domain", &sf.domain, &p.Domain)
	str("smbversion", &sf.smbversion, &p.Smbversion)
	str("options", &sf.options, &p.Options)
	str("subdir", &sf.subdir, &p.Subdir)
	str("mountpoint", &sf.mountpoint, &p.Mountpoint)
	str("pool", &sf.pool, &p.Pool)
	str("data-pool", &sf.dataPool, &p.DataPool)
	str("monhost", &sf.monhost, &p.Monhost)
	str("fs-name", &sf.fsName, &p.FsName)
	str("namespace", &sf.namespace, &p.Namespace)
	bl("krbd", &sf.krbd, &p.Krbd)
	bl("fuse", &sf.fuse, &p.Fuse)
	str("keyring", &sf.keyring, &p.Keyring)
	str("fingerprint", &sf.fingerprint, &p.Fingerprint)
	str("encryption-key", &sf.encryptionKey, &p.EncryptionKey)
	str("master-pubkey", &sf.masterPubkey, &p.MasterPubkey)
	i64("port", &sf.port, &p.Port)
	str("format", &sf.format, &p.Format)
	str("preallocation", &sf.preallocation, &p.Preallocation)
	str("bwlimit", &sf.bwlimit, &p.Bwlimit)
	str("prune-backups", &sf.pruneBackups, &p.PruneBackups)
	i64("max-protected-backups", &sf.maxProtected, &p.MaxProtectedBackups)
	bl("mkdir", &sf.mkdir, &p.Mkdir)
	bl("create-base-path", &sf.createBasePath, &p.CreateBasePath)
	bl("create-subdirs", &sf.createSubdirs, &p.CreateSubdirs)
	bl("sparse", &sf.sparse, &p.Sparse)
	str("is-mountpoint", &sf.isMountpoint, &p.IsMountpoint)
	bl("skip-cert-verification", &sf.skipCertVerify, &p.SkipCertVerification)
	str("delete", &sf.del, &p.Delete)
	str("digest", &sf.digest, &p.Digest)
}

// newCreateCmd builds `pve storage create`.
func newCreateCmd() *cobra.Command {
	var (
		storageID string
		stType    string
		sf        storageFlags
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new storage definition",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &clusterstorage.CreateStorageParams{
				Storage: storageID,
				Type:    stType,
			}
			sf.applyCreate(cmd, params)

			if _, err := deps.API.ClusterStorage.CreateStorage(cmd.Context(), params); err != nil {
				return err
			}
			res := output.Result{Message: fmt.Sprintf("Storage %q created.", storageID)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&storageID, "storage", "", "storage identifier (required)")
	cmd.Flags().StringVar(&stType, "type", "",
		"storage type: dir|nfs|cifs|rbd|lvm|lvmthin|zfspool|btrfs|pbs (required)")
	sf.registerCreate(cmd)
	_ = cmd.MarkFlagRequired("storage")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

// newSetCmd builds `pve storage set <storage>`.
func newSetCmd() *cobra.Command {
	var sf storageFlags
	cmd := &cobra.Command{
		Use:   "set <storage>",
		Short: "Update an existing storage definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			storageID := args[0]
			params := &clusterstorage.UpdateStorageParams{}
			sf.applyUpdate(cmd, params)

			if _, err := deps.API.ClusterStorage.UpdateStorage(cmd.Context(), storageID, params); err != nil {
				return err
			}
			res := output.Result{Message: fmt.Sprintf("Storage %q updated.", storageID)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	sf.registerSet(cmd)
	return cmd
}

// newDeleteCmd builds `pve storage delete <storage>`.
func newDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <storage>",
		Short: "Delete a storage definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			storageID := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete storage %q without --yes", storageID)
			}
			if err := deps.API.ClusterStorage.DeleteStorage(cmd.Context(), storageID); err != nil {
				return err
			}
			res := output.Result{Message: fmt.Sprintf("Storage %q deleted.", storageID)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// --- helpers ---

// boolptr returns a pointer to b.
func boolptr(b bool) *bool { return &b }

// boolCell renders a boolean as the conventional table cell text.
func boolCell(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// rawList converts a list response into a slice of decoded objects for JSON and
// YAML output, preserving every field returned by the API. A nil response
// yields an empty slice.
func rawList(resp *clusterstorage.ListStorageResponse) any {
	out := make([]map[string]any, 0)
	if resp == nil {
		return out
	}
	for _, raw := range *resp {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err == nil {
			out = append(out, obj)
		}
	}
	return out
}

// scalarString renders an arbitrary JSON scalar as a display string. Numbers
// decoded as float64 with no fractional part render without a trailing ".0".
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return strings.TrimSpace(fmt.Sprintf("%v", t))
		}
		return string(b)
	}
}
