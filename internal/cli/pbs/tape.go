package pbs

import (
	"github.com/spf13/cobra"
)

// newTapeCmd builds `pve pbs tape` and its sub-groups: tape drives and
// changers (configuration plus runtime operations), media and media pools,
// tape encryption keys, tape backup jobs, and one-shot backup/restore runs
// (the /config/{drive,changer,media-pool,tape-encryption-keys,tape-backup-job}
// and /tape/* API subtrees).
func newTapeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tape",
		Short: "Manage tape backup",
		Long: "Manage tape backup: drives, changers, media and media pools, encryption " +
			"keys, tape backup jobs, and one-shot tape backup and restore runs.",
	}

	cmd.AddCommand(
		newTapeDriveCmd(),
		newTapeChangerCmd(),
		newTapeMediaCmd(),
		newTapePoolCmd(),
		newTapeKeyCmd(),
		newTapeJobCmd(),
		newTapeBackupCmd(),
		newTapeRestoreCmd(),
	)

	return cmd
}
