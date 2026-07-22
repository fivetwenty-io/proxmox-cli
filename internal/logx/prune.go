package logx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EmptyMinAge is the minimum age a 0-byte log file must reach before
// PruneOptions.Empty may remove it. The floor exists so a file just created
// by a concurrently running command (in particular an older pmx build, which
// writes no record until its first API call) is never removed out from
// under the open fd.
const EmptyMinAge = time.Hour

// PruneSentinel is the marker file under the log directory whose mtime
// records the last automatic prune; AutoPrune skips its work while the
// sentinel is younger than 24 hours.
const PruneSentinel = ".last-prune"

// PruneOptions selects what Prune removes from a log directory tree.
type PruneOptions struct {
	// Dir is the log root to prune (typically DefaultDir()). Required.
	Dir string

	// OlderThan, when positive, removes every .jsonl file whose mtime is
	// before Now-OlderThan.
	OlderThan time.Duration

	// Empty additionally removes 0-byte .jsonl files older than EmptyMinAge
	// regardless of OlderThan.
	Empty bool

	// DryRun reports what would be removed without deleting anything.
	DryRun bool

	// Now overrides the reference time for cutoff math; zero means
	// time.Now(). Exposed for tests.
	Now time.Time
}

// PruneStats reports what Prune removed (or, under DryRun, would remove).
type PruneStats struct {
	// Files is the count of aged-out non-empty .jsonl files removed.
	Files int

	// Empty is the count of 0-byte .jsonl files removed.
	Empty int

	// Bytes is the total size of removed files.
	Bytes int64

	// Dirs is the count of directories left empty by the removals (and
	// themselves removed); the log root itself is never removed.
	Dirs int
}

// Prune removes log files under opts.Dir per opts and then removes any
// directories the deletions left empty, deepest first. Only files with the
// .jsonl extension are considered, so the prune sentinel and any foreign
// files survive. Per-file removal errors do not stop the walk; they are
// joined into the returned error alongside the stats for what did succeed.
func Prune(opts PruneOptions) (PruneStats, error) {
	var stats PruneStats

	if opts.Dir == "" {
		return stats, errors.New("logx: prune: empty dir")
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	var (
		errs       []error
		removed    = map[string]bool{}
		dirParents = map[string][]string{} // dir → child entries (files and subdirs)
	)

	walkErr := filepath.WalkDir(opts.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, err)
			return nil //nolint:nilerr // best-effort: skip unreadable entries, keep walking
		}
		if path == opts.Dir {
			return nil
		}
		dirParents[filepath.Dir(path)] = append(dirParents[filepath.Dir(path)], path)
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			errs = append(errs, err)
			return nil //nolint:nilerr // best-effort
		}

		aged := opts.OlderThan > 0 && info.ModTime().Before(now.Add(-opts.OlderThan))
		empty := opts.Empty && info.Size() == 0 && info.ModTime().Before(now.Add(-EmptyMinAge))
		if !aged && !empty {
			return nil
		}

		if !opts.DryRun {
			// A concurrent prune (two invocations past the sentinel gate at
			// once) may have removed the file first; that is success, not an
			// error worth surfacing.
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
				return nil //nolint:nilerr // best-effort
			}
		}
		removed[path] = true
		stats.Bytes += info.Size()
		if info.Size() == 0 {
			stats.Empty++
		} else {
			stats.Files++
		}
		return nil
	})
	if walkErr != nil {
		errs = append(errs, walkErr)
	}

	stats.Dirs = pruneEmptyDirs(opts.Dir, dirParents, removed, opts.DryRun, &errs)

	return stats, errors.Join(errs...)
}

// pruneEmptyDirs removes (or, under dryRun, counts) every directory under
// root left with no surviving entries after the removals recorded in
// removed. dirParents maps each directory to the child paths seen during
// the walk. Directories are processed deepest first so a chain of
// now-empty parents collapses in one pass. The root itself is never
// removed. Returns the count of directories removed.
func pruneEmptyDirs(root string, dirParents map[string][]string, removed map[string]bool, dryRun bool, errs *[]error) int {
	var dirs []string
	for d := range dirParents {
		if d != root && strings.HasPrefix(d, root+string(filepath.Separator)) {
			dirs = append(dirs, d)
		}
	}
	// Deepest first: longer paths (more separators) sort before their parents.
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], string(filepath.Separator)) > strings.Count(dirs[j], string(filepath.Separator))
	})

	count := 0
	for _, d := range dirs {
		emptyAfter := true
		for _, child := range dirParents[d] {
			if !removed[child] {
				emptyAfter = false
				break
			}
		}
		if !emptyAfter {
			continue
		}
		if !dryRun {
			if err := os.Remove(d); err != nil && !errors.Is(err, fs.ErrNotExist) {
				*errs = append(*errs, err)
				continue
			}
		}
		removed[d] = true
		count++
	}
	return count
}

// AutoPrune runs Prune over dir with a cutoff of retentionDays days (empty
// files included) at most once per 24 hours, gated by the PruneSentinel
// file's mtime. It returns the stats, whether a prune actually ran, and any
// error. A non-positive retentionDays disables it. The sentinel is touched
// before pruning so concurrent invocations and prune failures both still
// delay the next attempt by a full day.
func AutoPrune(dir string, retentionDays int) (PruneStats, bool, error) {
	if retentionDays <= 0 || dir == "" {
		return PruneStats{}, false, nil
	}

	sentinel := filepath.Join(dir, PruneSentinel)
	if info, err := os.Stat(sentinel); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		return PruneStats{}, false, nil
	}

	if err := touchFile(sentinel); err != nil {
		return PruneStats{}, false, err
	}

	stats, err := Prune(PruneOptions{
		Dir:       dir,
		OlderThan: time.Duration(retentionDays) * 24 * time.Hour,
		Empty:     true,
	})
	return stats, true, err
}

// touchFile creates path if absent and sets its mtime to now.
func touchFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: fixed sentinel name under the log dir
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	now := time.Now()
	return os.Chtimes(path, now, now)
}

// DefaultDir returns the default log directory (~/.pmx/logs), the same
// resolution Init uses when Config.LogDir is empty.
func DefaultDir() (string, error) {
	return resolveLogDir("")
}
