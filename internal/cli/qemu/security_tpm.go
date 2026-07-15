package qemu

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newSecurityTpmCmd builds `pmx pve qemu security tpm` and its show/add/remove
// sub-commands.
func newSecurityTpmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tpm",
		Short: "Manage the VM's TPM state device (tpmstate0)",
		Long: "A virtual TPM stores its state in the tpmstate0 disk. The interface version " +
			"(v1.2 / v2.0) is fixed at creation and CANNOT be changed later; changing versions " +
			"means removing the state device (destroying all keys sealed in it) and adding a " +
			"new one. Windows 11 requires TPM 2.0.",
	}
	cmd.AddCommand(newSecurityTpmShowCmd(), newSecurityTpmAddCmd(), newSecurityTpmRemoveCmd())
	return cmd
}

// newSecurityTpmShowCmd builds `pmx pve qemu security tpm show <vmid|name>`.
func newSecurityTpmShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show TPM state device configuration",
		Long: "Show whether the VM has a TPM state device (tpmstate0) and, if so, its volume, " +
			"interface version (v1.2 or v2.0), and disk size.",
		Example: `  pmx pve qemu security tpm show 100
  pmx pve qemu security tpm show win11`,
		Args: cobra.ExactArgs(1),
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
			tp := parseTPMPosture(m)

			single := map[string]string{
				"vmid":    vmid,
				"node":    node,
				"present": strconv.FormatBool(tp.Present),
			}
			if tp.Present {
				single["volume"] = tp.Volume
				single["version"] = tp.Version
				if tp.Size != "" {
					single["size"] = tp.Size
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: tp}, deps.Format)
		},
	}
}

// newSecurityTpmAddCmd builds `pmx pve qemu security tpm add <vmid|name>`.
func newSecurityTpmAddCmd() *cobra.Command {
	var (
		storage string
		version string
		digest  string
		restart bool
	)

	cmd := &cobra.Command{
		Use:   "add <vmid|name>",
		Short: "Add a TPM state device",
		Long: "Allocate a TPM state disk (tpmstate0). The version defaults to v2.0 — the current, " +
			"recommended interface (note: the PVE API's own default is the legacy v1.2; this " +
			"command overrides that). The version is immutable after creation.\n\n" +
			"Example: pmx pve qemu security tpm add win11 --storage local-lvm",
		Example: `  pmx pve qemu security tpm add win11 --storage local-lvm`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if !fl.Changed("storage") {
				return fmt.Errorf("--storage is required to allocate a TPM state disk")
			}
			if version != "v1.2" && version != "v2.0" {
				return fmt.Errorf("--version must be one of v2.0, v1.2 (got %q)", version)
			}

			m, autoDigest, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			tp := parseTPMPosture(m)
			if tp.Present {
				if tp.Version == version {
					return deps.Out.Render(cmd.OutOrStdout(),
						output.Result{Message: fmt.Sprintf("VM %s already has a TPM %s state device; no change.", vmid, version)},
						deps.Format)
				}
				return fmt.Errorf(
					"VM %s already has a TPM state device (version %s); the version cannot be changed "+
						"in place — remove it first with 'pmx pve qemu security tpm remove %s --force' "+
						"(this destroys all keys sealed in the TPM)", vmid, tp.Version, vmid)
			}

			newTpm := fmt.Sprintf("%s:1,version=%s", storage, version)
			params := &nodes.UpdateQemuConfigParams{Tpmstate0: strPtr(newTpm)}
			applyDigest(params, fl, digest, autoDigest)

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("add TPM state device for VM %s on node %q: %w", vmid, node, err)
			}

			suffix, err := mutationSuffix(cmd, deps, vmid, node, restart)
			if err != nil {
				return err
			}
			msg := fmt.Sprintf("VM %s TPM %s state device added on %s.", vmid, version, storage)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: msg + suffix, Raw: map[string]any{"vmid": vmid, "node": node, "tpmstate0": newTpm}},
				deps.Format)
		},
	}

	f := cmd.Flags()
	f.StringVar(&storage, "storage", "", "storage for the TPM state disk (required)")
	f.StringVar(&version, "version", "v2.0", "TPM interface version: v2.0 or v1.2")
	f.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	f.BoolVar(&restart, "restart", false, "reboot the VM after a successful change (applies pending config)")
	return cmd
}

// newSecurityTpmRemoveCmd builds `pmx pve qemu security tpm remove <vmid|name>`.
func newSecurityTpmRemoveCmd() *cobra.Command {
	var (
		force   bool
		digest  string
		restart bool
	)

	cmd := &cobra.Command{
		Use:   "remove <vmid|name>",
		Short: "Remove the TPM state device (destroys all sealed keys)",
		Long: "Detach and delete the tpmstate0 disk. Everything sealed in the TPM — BitLocker " +
			"keys, Windows Hello credentials, measured-boot state — is destroyed and cannot be " +
			"recovered. A guest relying on TPM-bound disk encryption may become unbootable.",
		Example: `  pmx pve qemu security tpm remove win11 --force`,
		Args:    cobra.ExactArgs(1),
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
			tp := parseTPMPosture(m)
			if !tp.Present {
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{Message: fmt.Sprintf("VM %s has no TPM state device; no change.", vmid)}, deps.Format)
			}
			if !force {
				return fmt.Errorf(
					"refusing to remove the TPM state device of VM %s without --force: all keys sealed "+
						"in it (e.g. BitLocker) will be permanently destroyed", vmid)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"WARNING: destroying TPM state of VM %s — keys sealed in the TPM are unrecoverable\n", vmid)

			params := &nodes.UpdateQemuConfigParams{Delete: strPtr("tpmstate0")}
			applyDigest(params, fl, digest, autoDigest)

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("remove TPM state device for VM %s on node %q: %w", vmid, node, err)
			}

			suffix, err := mutationSuffix(cmd, deps, vmid, node, restart)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Message: fmt.Sprintf("VM %s TPM state device removed.", vmid) + suffix,
					Raw:     map[string]any{"vmid": vmid, "node": node},
				}, deps.Format)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "required; confirms destruction of the TPM state")
	f.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	f.BoolVar(&restart, "restart", false, "reboot the VM after a successful change (applies pending config)")
	return cmd
}
