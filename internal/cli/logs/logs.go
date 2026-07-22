// Package logs implements the `pmx logs` command group for managing the
// per-invocation JSONL logs written under ~/.pmx/logs.
package logs

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/logx"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// Group builds the `pmx logs` command group. Everything under it operates
// on the local log directory only, so the whole group is noClient.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "logs",
		Short:       "Manage pmx invocation logs",
		Long:        "Manage the per-invocation JSONL logs written under ~/.pmx/logs.",
		Annotations: map[string]string{"noClient": "true"},
	}
	cmd.AddCommand(newPruneCmd())
	return cmd
}

// newPruneCmd builds `pmx logs prune`.
func newPruneCmd() *cobra.Command {
	var (
		olderThan int
		empty     bool
		dryRun    bool
	)

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete old or empty log files",
		Long: `Delete JSONL log files under ~/.pmx/logs.

Files older than the cutoff are removed. The cutoff comes from --older-than
(days), falling back to the log.retention config key. With --empty, 0-byte
log files older than one hour are removed regardless of age cutoff (empty
files were produced by pmx releases that logged only API activity).
Directories left empty by the removals are removed too.

With a positive log.retention configured, an equivalent prune also runs
automatically at most once per 24 hours after any command completes.`,
		Example: `  pmx logs prune --older-than 30
  pmx logs prune --empty
  pmx logs prune --older-than 90 --empty --dry-run
  pmx logs prune`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if olderThan < 0 {
				return errors.New("--older-than must be a positive number of days")
			}

			days := olderThan
			if days == 0 {
				days = config.EffectiveLogRetention(deps.Cfg)
			}
			if days <= 0 && !empty {
				return errors.New("nothing to prune: pass --older-than <days> or --empty, or set log.retention in the config")
			}

			dir, err := logx.DefaultDir()
			if err != nil {
				return fmt.Errorf("resolve log directory: %w", err)
			}
			if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{Message: fmt.Sprintf("log directory %s does not exist; nothing to prune", dir)},
					deps.Format)
			}

			stats, err := logx.Prune(logx.PruneOptions{
				Dir:       dir,
				OlderThan: time.Duration(days) * 24 * time.Hour,
				Empty:     empty,
				DryRun:    dryRun,
			})
			if err != nil {
				return fmt.Errorf("prune logs: %w", err)
			}

			return deps.Out.Render(cmd.OutOrStdout(), pruneResult(stats, days, dryRun), deps.Format)
		},
	}

	cmd.Flags().IntVar(&olderThan, "older-than", 0,
		"delete log files older than this many days (default: log.retention config key)")
	cmd.Flags().BoolVar(&empty, "empty", false,
		"also delete 0-byte log files older than one hour")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"report what would be deleted without deleting anything")

	return cmd
}

// pruneResult shapes logx.PruneStats for the output renderer. days is the
// effective age cutoff (0 when pruning empties only).
func pruneResult(stats logx.PruneStats, days int, dryRun bool) output.Result {
	cutoff := "none"
	if days > 0 {
		cutoff = strconv.Itoa(days) + "d"
	}
	single := map[string]string{
		"files":   strconv.Itoa(stats.Files),
		"empty":   strconv.Itoa(stats.Empty),
		"dirs":    strconv.Itoa(stats.Dirs),
		"bytes":   strconv.FormatInt(stats.Bytes, 10),
		"cutoff":  cutoff,
		"dry-run": strconv.FormatBool(dryRun),
	}
	return output.Result{
		Single: single,
		Raw: map[string]any{
			"files":   stats.Files,
			"empty":   stats.Empty,
			"dirs":    stats.Dirs,
			"bytes":   stats.Bytes,
			"cutoff":  cutoff,
			"dry_run": dryRun,
		},
	}
}
