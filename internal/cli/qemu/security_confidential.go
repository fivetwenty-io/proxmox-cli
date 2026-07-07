package qemu

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/propstr"
)

// newSecurityConfidentialCmd builds `pve qemu security confidential` and its
// show/set/clear sub-commands.
func newSecurityConfidentialCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "confidential",
		Short: "Manage confidential-computing memory encryption (AMD SEV / Intel TDX)",
		Long: "Configure AMD Secure Encrypted Virtualization (amd-sev) or Intel Trusted Domain " +
			"Extensions (intel-tdx). These encrypt guest memory against the hypervisor. They " +
			"need matching host CPU/firmware support and restrict live migration, snapshots " +
			"with RAM state, and some hotplug operations. A VM uses at most one platform.",
	}
	cmd.AddCommand(newSecurityConfidentialShowCmd(), newSecurityConfidentialSetCmd(), newSecurityConfidentialClearCmd())
	return cmd
}

// newSecurityConfidentialShowCmd builds `pve qemu security confidential show <vmid|name>`.
func newSecurityConfidentialShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show confidential-computing configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			m, _, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			cp := parseConfidentialPosture(m)

			single := map[string]string{"vmid": vmid, "node": node, "platform": cp.Platform}
			prefix := "sev."
			if cp.Platform == "intel-tdx" {
				prefix = "tdx."
			}
			if cp.Platform != "none" {
				for k, v := range cp.Fields {
					single[prefix+strings.ReplaceAll(k, "-", "_")] = v
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: cp}, deps.Format)
		},
	}
}

// newSecurityConfidentialSetCmd builds `pve qemu security confidential set <vmid|name>`.
func newSecurityConfidentialSetCmd() *cobra.Command {
	var (
		sevType         string
		sevAllowSMT     bool
		sevKernelHashes bool
		sevNoDebug      bool
		sevNoKeySharing bool

		tdxType        string
		tdxAttestation bool
		tdxVsockCID    int64
		tdxVsockPort   int64

		digest  string
		restart bool
	)

	cmd := &cobra.Command{
		Use:   "set <vmid|name>",
		Short: "Configure AMD SEV or Intel TDX for the VM",
		Long: "Configure one confidential-computing platform. --sev / --tdx select the platform " +
			"and its type; the per-platform sub-flags merge with the current value. Type values " +
			"are passed to PVE unvalidated (upstream SEV types include std, es, and snp; the " +
			"accepted set depends on the PVE version and host hardware). If the VM currently " +
			"has the other platform configured, clear it first with 'confidential clear'.\n\n" +
			"Example: pve qemu security confidential set 100 --sev snp --sev-no-debug",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			sevChanged := fl.Changed("sev") || fl.Changed("sev-allow-smt") || fl.Changed("sev-kernel-hashes") ||
				fl.Changed("sev-no-debug") || fl.Changed("sev-no-key-sharing")
			tdxChanged := fl.Changed("tdx") || fl.Changed("tdx-attestation") || fl.Changed("tdx-vsock-cid") ||
				fl.Changed("tdx-vsock-port")

			if sevChanged && tdxChanged {
				return fmt.Errorf(
					"--sev* and --tdx* flags are mutually exclusive: a VM uses at most one confidential-computing platform")
			}
			if !sevChanged && !tdxChanged {
				return fmt.Errorf("no confidential-computing flags given: specify at least one of --sev/--sev-*/--tdx/--tdx-*")
			}

			m, autoDigest, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			cp := parseConfidentialPosture(m)

			params := &nodes.UpdateQemuConfigParams{}
			var msg string

			if sevChanged {
				if !fl.Changed("sev") && cp.Platform != "amd-sev" {
					return fmt.Errorf("--sev-* sub-flags require --sev (VM %s has no amd-sev configured)", vmid)
				}
				if fl.Changed("sev") && cp.Platform == "intel-tdx" {
					return fmt.Errorf(
						"VM %s currently has intel-tdx configured; run 'pve qemu security confidential clear' "+
							"first before switching to amd-sev", vmid)
				}
				raw, _ := rawStr(m, "amd-sev")
				list := propstr.Parse(raw, "type")
				if fl.Changed("sev") {
					list.Set("type", sevType)
				}
				if fl.Changed("sev-allow-smt") {
					list.Set("allow-smt", boolToStr(sevAllowSMT))
				}
				if fl.Changed("sev-kernel-hashes") {
					list.Set("kernel-hashes", boolToStr(sevKernelHashes))
				}
				if fl.Changed("sev-no-debug") {
					list.Set("no-debug", boolToStr(sevNoDebug))
				}
				if fl.Changed("sev-no-key-sharing") {
					list.Set("no-key-sharing", boolToStr(sevNoKeySharing))
				}
				params.AmdSev = strPtr(list.String())
				t, _ := list.Get("type")
				msg = fmt.Sprintf("VM %s amd-sev configured (type=%s).", vmid, t)
			} else {
				if !fl.Changed("tdx") && cp.Platform != "intel-tdx" {
					return fmt.Errorf("--tdx-* sub-flags require --tdx (VM %s has no intel-tdx configured)", vmid)
				}
				if fl.Changed("tdx") && cp.Platform == "amd-sev" {
					return fmt.Errorf(
						"VM %s currently has amd-sev configured; run 'pve qemu security confidential clear' "+
							"first before switching to intel-tdx", vmid)
				}
				raw, _ := rawStr(m, "intel-tdx")
				list := propstr.Parse(raw, "type")
				if fl.Changed("tdx") {
					list.Set("type", tdxType)
				}
				if fl.Changed("tdx-attestation") {
					list.Set("attestation", boolToStr(tdxAttestation))
				}
				if fl.Changed("tdx-vsock-cid") {
					list.Set("vsock-cid", strconv.FormatInt(tdxVsockCID, 10))
				}
				if fl.Changed("tdx-vsock-port") {
					list.Set("vsock-port", strconv.FormatInt(tdxVsockPort, 10))
				}
				// attestation is mandatory in the API typetext: never emit a
				// value PVE will reject by leaving it unset on a fresh value.
				attestationDefaulted := false
				if _, ok := list.Get("attestation"); !ok {
					list.Set("attestation", "0")
					attestationDefaulted = true
				}
				params.IntelTdx = strPtr(list.String())
				t, _ := list.Get("type")
				msg = fmt.Sprintf("VM %s intel-tdx configured (type=%s).", vmid, t)
				if attestationDefaulted {
					msg += " attestation defaulted to 0 (not passed)."
				}
			}

			applyDigest(params, fl, digest, autoDigest)

			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"WARNING: confidential computing restricts live migration, snapshots with RAM state, "+
					"and hotplug; the host must have matching CPU/firmware support")

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("configure confidential computing for VM %s on node %q: %w", vmid, node, err)
			}

			suffix, err := mutationSuffix(cmd, deps, vmid, node, restart)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: msg + suffix, Raw: map[string]any{"vmid": vmid, "node": node}}, deps.Format)
		},
	}

	f := cmd.Flags()
	f.StringVar(&sevType, "sev", "", "AMD SEV type (e.g. std, es, snp)")
	f.BoolVar(&sevAllowSMT, "sev-allow-smt", false, "allow the guest to run on SMT siblings (allow-smt=1)")
	f.BoolVar(&sevKernelHashes, "sev-kernel-hashes", false,
		"add kernel hashes to the measured launch (kernel-hashes=1)")
	f.BoolVar(&sevNoDebug, "sev-no-debug", false, "deny hypervisor debug access to guest memory (no-debug=1)")
	f.BoolVar(&sevNoKeySharing, "sev-no-key-sharing", false,
		"do not share the encryption key with other guests (no-key-sharing=1)")
	f.StringVar(&tdxType, "tdx", "", "Intel TDX type")
	f.BoolVar(&tdxAttestation, "tdx-attestation", false, "enable TDX attestation (the API requires this field)")
	f.Int64Var(&tdxVsockCID, "tdx-vsock-cid", 0, "vsock CID for the attestation quote service")
	f.Int64Var(&tdxVsockPort, "tdx-vsock-port", 0, "vsock port for the attestation quote service")
	f.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	f.BoolVar(&restart, "restart", false, "reboot the VM after a successful change (applies pending config)")
	return cmd
}

// newSecurityConfidentialClearCmd builds `pve qemu security confidential clear <vmid|name>`.
func newSecurityConfidentialClearCmd() *cobra.Command {
	var (
		digest  string
		restart bool
	)

	cmd := &cobra.Command{
		Use:   "clear <vmid|name>",
		Short: "Remove the confidential-computing configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			m, autoDigest, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			cp := parseConfidentialPosture(m)
			if cp.Platform == "none" {
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{Message: fmt.Sprintf("VM %s has no confidential-computing configuration; no change.", vmid)},
					deps.Format)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"WARNING: clearing %s on VM %s — the guest will boot with unencrypted memory and "+
					"existing attestation workflows will break\n",
				cp.Platform, vmid)

			params := &nodes.UpdateQemuConfigParams{Delete: strPtr(cp.Platform)}
			applyDigest(params, fl, digest, autoDigest)

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("clear confidential computing for VM %s on node %q: %w", vmid, node, err)
			}

			suffix, err := mutationSuffix(cmd, deps, vmid, node, restart)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Message: fmt.Sprintf("VM %s confidential-computing configuration cleared.", vmid) + suffix,
					Raw:     map[string]any{"vmid": vmid, "node": node},
				}, deps.Format)
		},
	}

	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	f.BoolVar(&restart, "restart", false, "reboot the VM after a successful change (applies pending config)")
	return cmd
}
