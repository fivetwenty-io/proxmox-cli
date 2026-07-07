// Package nodefile reads and atomically rewrites files on a PVE node over
// non-interactive ssh. It knows nothing about cobra, cli.Deps, or config:
// callers hand it an already-resolved host and finished ssh flags. Its only
// dependencies are internal/exec (the shell-out Runner) and internal/sshcmd
// (the argv builder), keeping it a leaf package free of the cli/config web.
package nodefile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/sshcmd"
)

// MaxFileSize bounds the bytes Read will return and Write will send. It sits
// far below the pmxcfs per-file limit yet far above any sane guest config, and
// keeps a runaway remote file from being slurped wholesale into memory.
const MaxFileSize = 256 * 1024

// lockWaitSec is how long the remote write waits for the PVE config lock before
// giving up with ErrLockTimeout.
const lockWaitSec = 10

// Remote exit codes reserved by the write script to signal distinguishable
// failures. They sit well clear of ordinary tool exits (0-2) and ssh's own 255.
const (
	exitLockTimeout = 90
	exitConflict    = 91
	exitNotFound    = 92
)

// Sentinel errors, matched with errors.Is by the command layer.
var (
	// ErrNotFound reports that the remote file does not exist.
	ErrNotFound = errors.New("remote file does not exist")
	// ErrConflict reports that the remote file changed since it was read, so
	// the optimistic sha256 guard rejected the write.
	ErrConflict = errors.New("remote file changed since it was read")
	// ErrLockTimeout reports that the PVE config lock could not be acquired
	// within the bounded wait.
	ErrLockTimeout = errors.New("timed out waiting for the PVE config lock")
	// ErrTooLarge reports that the content exceeds MaxFileSize.
	ErrTooLarge = errors.New("remote file exceeds the size limit")
)

// Conn describes one PVE node reachable over ssh. Runner is the shell-out
// interface (deps.Runner), Flags are the connection flags after context
// defaults have been applied, and Host is the resolved management address.
type Conn struct {
	Runner exec.Runner
	Flags  *sshcmd.Flags
	Host   string
}

// Read returns the exact bytes of path and the hex sha256 of those bytes, which
// is the guard token a later Write passes as expectSHA. The remote invocation
// is a plain `cat` of the ShellQuoted path; stdout is captured client-side and
// refused if it exceeds MaxFileSize. A non-zero exit yields a generic error
// carrying the remote stderr (cat cannot distinguish a missing file from other
// failures by exit code, so no sentinel is mapped here).
func (c Conn) Read(path string) (content, sha string, err error) {
	argv := append(sshcmd.BatchOptionArgs(c.Flags), sshcmd.Dest(c.Flags, c.Host),
		"cat", sshcmd.ShellQuote(path))

	var out, errBuf bytes.Buffer
	runErr := c.Runner.Run("ssh", argv, nil, nil, &out, &errBuf)
	if runErr != nil {
		return "", "", fmt.Errorf("read %s over ssh: %w%s", path, runErr, stderrSuffix(errBuf.String()))
	}
	if out.Len() > MaxFileSize {
		return "", "", fmt.Errorf("read %s over ssh: %w (%d bytes)", path, ErrTooLarge, out.Len())
	}

	sum := sha256.Sum256(out.Bytes())
	return out.String(), hex.EncodeToString(sum[:]), nil
}

// Write replaces path with content, but only if the remote file's current
// sha256 still equals expectSHA (optimistic lock; ErrConflict otherwise). The
// whole check-and-write runs as one remote critical section under flock(1) on
// lockPath, and the replacement is a tmp-file-plus-rename in the same directory
// so readers never observe a partial file. content is piped via stdin, never
// embedded in the argv, so it faces no quoting hazard. Content larger than
// MaxFileSize is refused client-side before any ssh call. The distinguishable
// remote exits map to ErrLockTimeout, ErrConflict, and ErrNotFound; any other
// non-zero exit (including ssh's own 255 transport failure) becomes a wrapped
// error carrying the remote stderr.
func (c Conn) Write(path, content, expectSHA, lockPath string) error {
	if len(content) > MaxFileSize {
		return fmt.Errorf("write %s over ssh: %w (%d bytes)", path, ErrTooLarge, len(content))
	}

	script := buildWriteScript(path, expectSHA, lockPath)
	argv := append(sshcmd.BatchOptionArgs(c.Flags), sshcmd.Dest(c.Flags, c.Host),
		"sh", "-ec", sshcmd.ShellQuote(script))

	var out, errBuf bytes.Buffer
	runErr := c.Runner.Run("ssh", argv, nil, strings.NewReader(content), &out, &errBuf)
	if runErr == nil {
		return nil
	}

	switch exec.ExitCodeOf(runErr) {
	case exitLockTimeout:
		return ErrLockTimeout
	case exitConflict:
		return ErrConflict
	case exitNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("write %s over ssh: %w%s", path, runErr, stderrSuffix(errBuf.String()))
	}
}

// Exec runs script under `sh -ec` on the node and returns captured stdout and
// stderr. Callers use it for post-write validation (e.g. `pct config <vmid>`)
// and for read-only probes. A non-zero exit is returned as the error with both
// captured streams available for the caller to fold into its own message.
func (c Conn) Exec(script string) (stdout, stderr string, err error) {
	argv := append(sshcmd.BatchOptionArgs(c.Flags), sshcmd.Dest(c.Flags, c.Host),
		"sh", "-ec", sshcmd.ShellQuote(script))

	var out, errBuf bytes.Buffer
	runErr := c.Runner.Run("ssh", argv, nil, nil, &out, &errBuf)
	if runErr != nil {
		return out.String(), errBuf.String(),
			fmt.Errorf("exec over ssh: %w%s", runErr, stderrSuffix(errBuf.String()))
	}
	return out.String(), errBuf.String(), nil
}

// buildWriteScript renders the guarded atomic write script with path, expectSHA,
// and lockPath baked in as ShellQuoted shell values so nothing is ever
// interpolated unquoted. The script:
//
//   - opens lockPath on fd 9 and flocks it (distinct exit 90 on timeout, since
//     `flock -w N fd` reports the timeout separately from any child failure);
//   - refuses to proceed if path has vanished (exit 92);
//   - guards on the file's current sha256 matching expectSHA (exit 91) to close
//     the read-write TOCTOU window;
//   - writes stdin to a sibling tmp file and rename(2)s it into place.
//
// The lock is held until the script's shell exits, so the guard and the write
// are one critical section. tmp+rename mirrors PVE::Tools::file_set_contents;
// no chmod/chown is attempted because pmxcfs synthesizes ownership itself.
func buildWriteScript(path, expectSHA, lockPath string) string {
	return fmt.Sprintf(`p=%s
l=%s
s=%s
d=${l%%/*}
mkdir -p -- "$d"
exec 9>>"$l"
flock -w %d 9 || exit %d
[ -e "$p" ] || exit %d
a=$(sha256sum -- "$p"); a=${a%%%% *}
[ "$a" = "$s" ] || exit %d
t="$p.tmp.$$"
cat > "$t" || { rm -f -- "$t"; exit 1; }
mv -- "$t" "$p"`,
		sshcmd.ShellQuote(path),
		sshcmd.ShellQuote(lockPath),
		sshcmd.ShellQuote(expectSHA),
		lockWaitSec, exitLockTimeout,
		exitNotFound,
		exitConflict,
	)
}

// stderrSuffix formats captured remote stderr as a trailing clause for an error
// message, or the empty string when there was none.
func stderrSuffix(stderr string) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		return ""
	}
	return ": " + s
}
