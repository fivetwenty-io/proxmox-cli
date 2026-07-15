package lab

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// newQuotaCmd builds the `pmx lab quota` parent command. ZFS refquota
// management has no Proxmox VE API endpoint at all (confirmed absent from
// the generated API client's storage-parameter struct, not merely
// unwrapped), so its lone subcommand shells out to the lab host over ssh
// instead of calling deps.API like every other pmx lab verb.
func newQuotaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quota",
		Short: "Manage a lab's ZFS refquota",
	}
	cmd.AddCommand(newQuotaSetCmd())
	return cmd
}

// newQuotaSetCmd builds `pmx lab quota set <name>`.
func newQuotaSetCmd() *cobra.Command {
	var (
		refquotaGB int
		dryRun     bool
		yes        bool
	)

	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Set a lab's ZFS dataset refquota over ssh",
		Long: `Set a lab's ZFS dataset refquota over ssh.

There is no Proxmox VE API for ZFS dataset properties, so this verb runs
"zfs set refquota" with the requested size (for example refquota=480G) on the lab host directly over ssh, targeting the
active context's host and the SSH connection settings configured on it
(ssh.user/ssh.port/ssh.identity). The effective refquota is --refquota-gb
when given, else the lab's storage.refquota_gb; at least one of the two
must yield a positive value.

This runs a real command against the lab host by default; pass --dry-run
to print the ssh command that would run without executing it, or --yes to
skip the confirmation prompt.`,
		Example: `  pmx lab quota set wayne
  pmx lab quota set wayne --refquota-gb 600 --yes
  pmx lab quota set wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQuotaSet(cmd, args[0], refquotaGB, dryRun, yes)
		},
	}

	cmd.Flags().IntVar(&refquotaGB, "refquota-gb", 0,
		"refquota in GB; overrides the lab's storage.refquota_gb config value")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"print the ssh command that would run, without executing it")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

// runQuotaSet resolves and peppi-guards name, determines the effective
// refquota (--refquota-gb over the lab's storage.refquota_gb when the flag was passed),
// builds the ssh argv the same way internal/cli/remote and
// internal/cli/qemu do (sshcmd.BaseArgs against the active context's host
// and SSH block), and either previews or executes it via deps.Runner.
func runQuotaSet(cmd *cobra.Command, name string, refquotaFlagGB int, dryRun, yes bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	refquotaGB := lab.Storage.RefquotaGB
	if cmd.Flags().Changed("refquota-gb") {
		refquotaGB = refquotaFlagGB
	}
	if refquotaGB <= 0 {
		return fmt.Errorf(
			"lab %q has no positive refquota: pass --refquota-gb or set storage.refquota_gb in its config", name)
	}

	if deps.Ctx == nil {
		return fmt.Errorf(
			"quota set requires an active pmx context to resolve an ssh target; select one with --context/-c")
	}

	f := sshcmd.Flags{User: "root", Port: 22}
	if deps.Ctx.SSH.User != "" {
		f.User = deps.Ctx.SSH.User
	}
	if deps.Ctx.SSH.Port != 0 {
		f.Port = deps.Ctx.SSH.Port
	}
	if deps.Ctx.SSH.Identity != "" {
		f.Identity = deps.Ctx.SSH.Identity
	}

	dataset := zfsDatasetPath(lab)
	remoteCmd := []string{"zfs", "set", fmt.Sprintf("refquota=%dG", refquotaGB), dataset}

	argv := sshcmd.BaseArgs(&f, deps.Ctx.Host)
	argv = append(argv, remoteCmd...)

	if dryRun {
		display := append([]string{"ssh"}, argv...)
		res := output.Result{Message: strings.Join(display, " ")}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	if !yes {
		prompt := fmt.Sprintf("Set refquota=%dG on lab %q (%s)?", refquotaGB, name, dataset)
		ok, err := confirmYesNo(cmd, prompt)
		if err != nil {
			return err
		}
		if !ok {
			res := output.Result{Message: "Aborted."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		}
	}

	// stdin is nil: "zfs set" reads no input of its own, unlike the
	// arbitrary user commands `pmx pve node exec`/`pmx ssh` pass through.
	if err := deps.Runner.Run("ssh", argv, nil, nil, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("set refquota on lab %q via ssh %s@%s: %w", name, f.User, deps.Ctx.Host, err)
	}

	res := output.Result{Message: fmt.Sprintf("Lab %q refquota set to %dG.", name, refquotaGB)}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}
