package qemu

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/propstr"
)

// newSecuritySecurebootCmd builds `pmx pve qemu security secureboot` and its
// show/enable sub-commands. There is deliberately no `disable`: the real
// Secure Boot on/off switch lives in the EFI vars themselves.
func newSecuritySecurebootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secureboot",
		Short: "Inspect and enable UEFI Secure Boot (OVMF + EFI vars disk)",
		Long: "Secure Boot on PVE means bios=ovmf plus an EFI vars disk (efidisk0) created with " +
			"efitype=4m and pre-enrolled-keys=1 (distribution + Microsoft keys). The " +
			"pre-enrolled template enables Secure Boot by default; the guest can still toggle " +
			"it from the OVMF firmware menu. There is no 'disable' command: the Secure Boot " +
			"on/off switch lives in the EFI variables themselves — flip it in the firmware " +
			"setup menu, or recreate the vars disk without pre-enrolled keys.",
	}
	cmd.AddCommand(newSecuritySecurebootShowCmd(), newSecuritySecurebootEnableCmd())
	return cmd
}

// newSecuritySecurebootShowCmd builds `pmx pve qemu security secureboot show <vmid|name>`.
func newSecuritySecurebootShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show BIOS / EFI / Secure Boot configuration",
		Long: "Show the VM's bios= setting and, when present, its EFI vars disk (efidisk0): " +
			"volume, efitype, whether keys are pre-enrolled, and Microsoft certificate era. " +
			"Warns on stderr in table/plain output when bios=ovmf has no efidisk0, since EFI " +
			"state (including Secure Boot) will not persist across restarts in that case.",
		Example: `  pmx pve qemu security secureboot show 100
  pmx pve qemu security secureboot show win11`,
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
			bp := parseBootPosture(m)

			if (deps.Format == output.FormatTable || deps.Format == output.FormatPlain) && bp.Posture == "ovmf-no-efidisk" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"note: bios=ovmf without an efidisk0 — EFI variables (including Secure Boot "+
						"state) will not persist across restarts")
			}

			single := map[string]string{
				"vmid":    vmid,
				"node":    node,
				"bios":    bp.Bios,
				"posture": bp.Posture,
			}
			if bp.EfidiskVolume != "" {
				single["efidisk.volume"] = bp.EfidiskVolume
				single["efidisk.efitype"] = bp.Efitype
				single["efidisk.pre_enrolled_keys"] = strconv.FormatBool(bp.PreEnrolledKeys)
				if bp.MsCert != "" {
					single["efidisk.ms_cert"] = bp.MsCert
				}
				if bp.Size != "" {
					single["efidisk.size"] = bp.Size
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: bp}, deps.Format)
		},
	}
}

// newSecuritySecurebootEnableCmd builds `pmx pve qemu security secureboot enable <vmid|name>`.
func newSecuritySecurebootEnableCmd() *cobra.Command {
	var (
		storage           string
		msCert            string
		noPreEnrolledKeys bool
		recreate          bool
		digest            string
		restart           bool
	)

	cmd := &cobra.Command{
		Use:   "enable <vmid|name>",
		Short: "Enable Secure Boot (set bios=ovmf and create a pre-enrolled EFI vars disk)",
		Long: "Switch the VM to OVMF firmware and allocate an EFI vars disk with efitype=4m and " +
			"pre-enrolled-keys=1, which enables Secure Boot by default. Pass " +
			"--no-pre-enrolled-keys for an empty vars store (UEFI without Secure Boot keys).\n\n" +
			"Switching an installed guest from SeaBIOS to OVMF usually makes it unbootable " +
			"until the OS is converted to UEFI booting — do this on new VMs or after preparing " +
			"the guest. Replacing an existing efidisk0 discards all stored EFI variables " +
			"(enrolled keys, boot entries) and requires --recreate.\n\n" +
			"Example: pmx pve qemu security secureboot enable win11 --storage local-lvm --ms-cert 2023",
		Args: cobra.ExactArgs(1),
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
			bp := parseBootPosture(m)

			// ms-cert-only in-place edit: the one legitimate in-place efidisk0
			// edit, preserving file=/size=/format= verbatim. Only applies when
			// no structural change (storage, key policy, recreate) was asked for.
			if bp.EfidiskVolume != "" && fl.Changed("ms-cert") &&
				!fl.Changed("storage") && !recreate && !fl.Changed("no-pre-enrolled-keys") {
				raw, _ := rawStr(m, "efidisk0")
				list := propstr.Parse(raw, "file")
				list.Set("ms-cert", msCert)

				params := &nodes.UpdateQemuConfigParams{Efidisk0: strPtr(list.String())}
				applyDigest(params, fl, digest, autoDigest)
				if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
					return fmt.Errorf("update efidisk0 for VM %s on node %q: %w", vmid, node, err)
				}
				suffix, err := mutationSuffix(cmd, deps, vmid, node, restart)
				if err != nil {
					return err
				}
				msg := fmt.Sprintf("VM %s EFI vars ms-cert set to %s.", vmid, msCert)
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{Message: msg + suffix, Raw: map[string]any{"vmid": vmid, "node": node}}, deps.Format)
			}

			preEnrolled := !noPreEnrolledKeys

			// An existing efidisk0 without --recreate: refuse to touch it unless
			// the VM is already at exactly the requested posture AND the caller
			// asked for no posture-changing flag (--storage/--no-pre-enrolled-keys).
			// Checking the refusal first keeps --storage from being silently
			// ignored by a "no change" no-op when it disagrees with the current
			// volume's storage.
			if bp.EfidiskVolume != "" && !recreate {
				postureFlagsPassed := fl.Changed("storage") || fl.Changed("no-pre-enrolled-keys")
				atRequestedPosture := bp.Bios == "ovmf" && bp.Efitype == "4m" && bp.PreEnrolledKeys == preEnrolled
				if atRequestedPosture && !postureFlagsPassed {
					msg := fmt.Sprintf("VM %s already has Secure Boot configured (ovmf, efitype=4m); no change.", vmid)
					return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
				}
				return fmt.Errorf(
					"refusing to replace the existing EFI vars disk of VM %s without --recreate: "+
						"enrolled keys and boot entries stored in it will be lost", vmid)
			}

			if !fl.Changed("storage") {
				if bp.EfidiskVolume == "" {
					return fmt.Errorf("--storage is required to allocate a new EFI vars disk")
				}
				// --recreate without an explicit --storage reuses the existing
				// volume's storage.
				i := strings.Index(bp.EfidiskVolume, ":")
				if i < 0 {
					return fmt.Errorf(
						"could not determine storage from existing efidisk0 volume %q; pass --storage explicitly",
						bp.EfidiskVolume)
				}
				storage = bp.EfidiskVolume[:i]
			}

			if bp.Bios != "ovmf" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"WARNING: switching VM %s from SeaBIOS to OVMF — an already-installed guest OS may "+
						"fail to boot until converted to UEFI\n", vmid)
			}

			efidiskParts := []string{storage + ":1", "efitype=4m"}
			if preEnrolled {
				efidiskParts = append(efidiskParts, "pre-enrolled-keys=1")
			} else {
				efidiskParts = append(efidiskParts, "pre-enrolled-keys=0")
			}
			if fl.Changed("ms-cert") {
				efidiskParts = append(efidiskParts, "ms-cert="+msCert)
			}
			newEfidisk := strings.Join(efidiskParts, ",")

			params := &nodes.UpdateQemuConfigParams{Efidisk0: strPtr(newEfidisk)}
			if bp.Bios != "ovmf" {
				params.Bios = strPtr("ovmf")
			}
			applyDigest(params, fl, digest, autoDigest)

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("configure Secure Boot for VM %s on node %q: %w", vmid, node, err)
			}

			suffix, err := mutationSuffix(cmd, deps, vmid, node, restart)
			if err != nil {
				return err
			}

			var msg string
			if bp.EfidiskVolume != "" && recreate {
				msg = fmt.Sprintf(
					"VM %s Secure Boot configured (ovmf, efitype=4m, pre-enrolled keys); "+
						"the previous EFI vars disk was moved to unused[n] (clean up with "+
						"'pmx pve qemu config set %s --delete unusedN --force').", vmid, vmid)
			} else {
				keyNote := "pre-enrolled keys"
				if !preEnrolled {
					keyNote = "no pre-enrolled keys"
				}
				msg = fmt.Sprintf("VM %s Secure Boot configured (ovmf, efitype=4m, %s).", vmid, keyNote)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Message: msg + suffix,
					Raw:     map[string]any{"vmid": vmid, "node": node, "efidisk0": newEfidisk},
				}, deps.Format)
		},
	}

	f := cmd.Flags()
	f.StringVar(&storage, "storage", "", "storage for the EFI vars disk, e.g. local-lvm (required on fresh enable)")
	f.StringVar(&msCert, "ms-cert", "", "Microsoft certificate era marker: 2011, 2023, 2023w, or 2023k")
	f.BoolVar(&noPreEnrolledKeys, "no-pre-enrolled-keys", false,
		"allocate an empty vars store (UEFI on, Secure Boot keys not enrolled)")
	f.BoolVar(&recreate, "recreate", false, "replace an existing efidisk0 (discards stored EFI variables)")
	f.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	f.BoolVar(&restart, "restart", false, "reboot the VM after a successful change (applies pending config)")
	return cmd
}
