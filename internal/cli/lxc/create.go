package lxc

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
)

// newCreateCmd builds `pve lxc create <vmid>`. The container is created as an
// asynchronous task; the command blocks until it completes unless --async is
// set. --ostemplate is required and names a template volume such as
// "local:vztmpl/alpine-3.22-default_20250617_amd64.tar.xz".
func newCreateCmd() *cobra.Command {
	var (
		async         bool
		ostemplate    string
		hostname      string
		storage       string
		rootfs        string
		memory        int64
		swap          int64
		cores         int64
		net0          string
		password      string
		sshPublicKeys string
		pool          string
		tags          string
		unprivileged  bool
		start         bool
	)
	cmd := &cobra.Command{
		Use:   "create <vmid>",
		Short: "Create an LXC container",
		Long: "Create an LXC container on a node from a template volume. The root " +
			"filesystem is given as a PVE option string, e.g. --rootfs " +
			"\"local-lvm:8\", and the network as --net0 " +
			"\"name=eth0,bridge=vmbr0,ip=dhcp\".",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := getDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid vmid %q: %w", args[0], err)
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateLxcParams{Vmid: vmid, Ostemplate: ostemplate}
			fl := cmd.Flags()
			if fl.Changed("hostname") {
				params.Hostname = &hostname
			}
			if fl.Changed("storage") {
				params.Storage = &storage
			}
			if fl.Changed("rootfs") {
				params.Rootfs = &rootfs
			}
			if fl.Changed("memory") {
				params.Memory = &memory
			}
			if fl.Changed("swap") {
				params.Swap = &swap
			}
			if fl.Changed("cores") {
				params.Cores = &cores
			}
			if fl.Changed("password") {
				params.Password = &password
			}
			if fl.Changed("ssh-public-keys") {
				params.SshPublicKeys = &sshPublicKeys
			}
			if fl.Changed("pool") {
				params.Pool = &pool
			}
			if fl.Changed("tags") {
				params.Tags = &tags
			}
			if fl.Changed("unprivileged") {
				params.Unprivileged = &unprivileged
			}
			if fl.Changed("start") {
				params.Start = &start
			}
			if fl.Changed("net0") {
				params.Net = map[int]string{0: net0}
			}

			resp, err := deps.API.Nodes.CreateLxc(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("create container %d on node %q: %w", vmid, node, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %d created.", vmid))
		},
	}
	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&ostemplate, "ostemplate", "", "template volume (required), e.g. local:vztmpl/...tar.zst")
	cmd.Flags().StringVar(&hostname, "hostname", "", "container hostname")
	cmd.Flags().StringVar(&storage, "storage", "", "default storage for the container")
	cmd.Flags().StringVar(&rootfs, "rootfs", "", "root filesystem spec, e.g. local-lvm:8")
	cmd.Flags().Int64Var(&memory, "memory", 0, "RAM in MiB")
	cmd.Flags().Int64Var(&swap, "swap", 0, "swap in MiB")
	cmd.Flags().Int64Var(&cores, "cores", 0, "CPU cores")
	cmd.Flags().StringVar(&net0, "net0", "", "network device 0, e.g. name=eth0,bridge=vmbr0,ip=dhcp")
	cmd.Flags().StringVar(&password, "password", "", "root password for the container")
	cmd.Flags().StringVar(&sshPublicKeys, "ssh-public-keys", "", "SSH public keys to install for root")
	cmd.Flags().StringVar(&pool, "pool", "", "resource pool to place the container in")
	cmd.Flags().StringVar(&tags, "tags", "", "comma- or semicolon-separated tags")
	cmd.Flags().BoolVar(&unprivileged, "unprivileged", false, "create an unprivileged container")
	cmd.Flags().BoolVar(&start, "start", false, "start the container immediately after creation")
	_ = cmd.MarkFlagRequired("ostemplate")
	return cmd
}
