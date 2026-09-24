package testhelper

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// SSHStandInMode selects what an SSHStandIn script does once it has started.
type SSHStandInMode int

const (
	// SSHForward connects to the -W host:port target through bash's
	// /dev/tcp and copies bytes both ways, as `ssh -W` does. When its
	// standard input reaches end-of-file it writes MarkerFile, kills its
	// reader, and exits zero, unless IgnoreEOF is set. When the target closes
	// the connection it writes Stderr and exits with ExitStatus.
	SSHForward SSHStandInMode = iota

	// SSHHang never forwards a byte. It exits zero once its standard input
	// reaches end-of-file, unless IgnoreEOF is set, in which case it runs
	// until it is signalled.
	SSHHang

	// SSHFail writes Stdout, then Stderr, and exits with ExitStatus, or ends
	// itself with SIGKILL when KillSelf is set, without forwarding anything
	// from a target.
	SSHFail
)

// SSHStandInOptions configures an SSHStandIn script.
type SSHStandInOptions struct {
	Mode SSHStandInMode

	// StartDelay delays everything after the argv record, including the pid
	// file.
	StartDelay time.Duration

	// IgnoreEOF keeps the script running after its standard input reaches
	// end-of-file, as an ssh still authenticating does.
	IgnoreEOF bool

	// Descendant spawns a child process that keeps standard error open and
	// records its pid in DescendantPIDFile. It never exits on its own, so
	// only a signal to the stand-in's process group ends it.
	Descendant bool

	// Stderr is written to standard error, each line ending in "\r\n" as
	// ssh's own lines do.
	Stderr []string

	// ExitStatus is the status SSHFail exits with, and the status SSHForward
	// exits with when the target closes the connection.
	ExitStatus int

	// KillSelf makes SSHFail end itself with SIGKILL after writing Stderr.
	KillSelf bool

	// Stdout is written to standard output by SSHFail before Stderr, with no
	// newline added, as bytes ssh forwarded just before it failed.
	Stdout string

	// TermExitStatus, when not zero, makes the script exit with this status
	// on SIGTERM. ssh's client loop does the same with 255, so a SIGTERM that
	// pmx sends reads as ssh's own failure.
	TermExitStatus int
}

// SSHScript is a written stand-in and the files it records into.
type SSHScript struct {
	// Program is the script's absolute path, for JumpSpec.Program.
	Program string

	// ArgvFile holds one line per invocation, each argument followed by the
	// unit separator (0x1f).
	ArgvFile string

	// MarkerFile is written when SSHForward's standard input reaches
	// end-of-file.
	MarkerFile string

	// PIDFile holds the most recent invocation's own pid.
	PIDFile string

	// DescendantPIDFile holds the most recent descendant's pid.
	DescendantPIDFile string
}

// SSHStandIn writes a bash script that stands in for ssh: it finds its
// -W host:port argument, appends its argv to ArgvFile, optionally delays,
// records its pid, optionally spawns a descendant, and then behaves as
// opts.Mode says. It skips the test where bash is unavailable, which
// includes Windows. A cleanup kills any invocation still alive when the test
// ends, so a failed test cannot leave a process group behind.
func SSHStandIn(t testing.TB, opts SSHStandInOptions) SSHScript {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the ssh stand-in needs a POSIX shell")
	}

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	dir := t.TempDir()
	s := SSHScript{
		Program:           filepath.Join(dir, "ssh-standin"),
		ArgvFile:          filepath.Join(dir, "argv"),
		MarkerFile:        filepath.Join(dir, "eof-marker"),
		PIDFile:           filepath.Join(dir, "pid"),
		DescendantPIDFile: filepath.Join(dir, "descendant-pid"),
	}

	script := []byte(standInScript(bash, s, opts))
	if err := os.WriteFile(s.Program, script, 0o700); err != nil { //nolint:gosec // an executable test script
		t.Fatalf("write ssh stand-in: %v", err)
	}

	t.Cleanup(func() { killScriptProcesses(s.Program) })

	return s
}

// standInScript renders the script. It sticks to bash 3.2, the version macOS
// ships as /bin/bash.
func standInScript(bash string, s SSHScript, opts SSHStandInOptions) string {
	var b strings.Builder

	fmt.Fprintf(&b, "#!%s\n", bash)

	if opts.TermExitStatus != 0 {
		// A foreground sleep defers the trap, but the group signal ends the
		// sleep too, so the trap runs at once.
		fmt.Fprintf(&b, "trap 'exit %d' TERM\n", opts.TermExitStatus)
	}

	b.WriteString("target=''\nprev=''\nline=''\n")
	b.WriteString("for a in \"$@\"; do\n")
	b.WriteString("  if [ \"$prev\" = '-W' ]; then target=\"$a\"; fi\n")
	b.WriteString("  prev=\"$a\"\n")
	b.WriteString("  line=\"$line$a\"$'\\037'\n")
	b.WriteString("done\n")
	// One write per invocation, so concurrent invocations never interleave.
	fmt.Fprintf(&b, "printf '%%s\\n' \"$line\" >> %s\n", shQuote(s.ArgvFile))

	if opts.StartDelay > 0 {
		fmt.Fprintf(&b, "sleep %.3f\n", opts.StartDelay.Seconds())
	}

	writeAtomically(&b, "$$", s.PIDFile)

	if opts.Descendant {
		// The subshell keeps standard error and nothing else, so it cannot
		// hold the forwarded stream open, and it stays in the stand-in's
		// process group.
		b.WriteString("( while :; do sleep 1; done ) </dev/null >/dev/null &\n")
		writeAtomically(&b, "$!", s.DescendantPIDFile)
	}

	var stderr strings.Builder
	for _, l := range opts.Stderr {
		fmt.Fprintf(&stderr, "printf '%%s\\r\\n' %s >&2\n", shQuote(l))
	}

	switch opts.Mode {
	case SSHFail:
		if opts.Stdout != "" {
			fmt.Fprintf(&b, "printf '%%s' %s\n", shQuote(opts.Stdout))
		}

		b.WriteString(stderr.String())

		if opts.KillSelf {
			b.WriteString("kill -KILL $$\n")
		}

		fmt.Fprintf(&b, "exit %d\n", opts.ExitStatus)

	case SSHHang:
		if opts.IgnoreEOF {
			b.WriteString("while :; do sleep 1; done\n")
		} else {
			b.WriteString("cat >/dev/null\nexit 0\n")
		}

	default:
		b.WriteString("host=\"${target%:*}\"\nport=\"${target##*:}\"\n")
		b.WriteString("host=\"${host#[}\"\nhost=\"${host%]}\"\n")
		b.WriteString("exec 3<>\"/dev/tcp/$host/$port\" || exit 255\n")
		b.WriteString("cat <&0 >&3 &\nwriter=$!\n")
		b.WriteString("cat <&3 &\nreader=$!\n")
		b.WriteString("eof=0\n")
		b.WriteString("while :; do\n")
		b.WriteString("  if ! kill -0 \"$reader\" 2>/dev/null; then\n")
		b.WriteString("    kill \"$writer\" 2>/dev/null\n")
		b.WriteString(indent(stderr.String(), "    "))
		fmt.Fprintf(&b, "    exit %d\n", opts.ExitStatus)
		b.WriteString("  fi\n")
		b.WriteString("  if [ \"$eof\" = 0 ] && ! kill -0 \"$writer\" 2>/dev/null; then\n")

		if opts.IgnoreEOF {
			b.WriteString("    eof=1\n")
		} else {
			fmt.Fprintf(&b, "    : > %s\n", shQuote(s.MarkerFile))
			b.WriteString("    kill \"$reader\" 2>/dev/null\n")
			b.WriteString("    exit 0\n")
		}

		b.WriteString("  fi\n")
		b.WriteString("  sleep 0.05\n")
		b.WriteString("done\n")
	}

	return b.String()
}

// writeAtomically renders a write of value to path through a rename, so a
// reader never sees an empty pid file.
func writeAtomically(b *strings.Builder, value, path string) {
	fmt.Fprintf(b, "printf '%%s\\n' \"%s\" > %s && mv -f %s %s\n",
		value, shQuote(path+".tmp"), shQuote(path+".tmp"), shQuote(path))
}

func indent(s, prefix string) string {
	if s == "" {
		return ""
	}

	lines := strings.SplitAfter(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}

	return strings.Join(lines, "")
}

// shQuote single-quotes s for bash.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Invocations returns the argv of every invocation so far, in order.
func (s SSHScript) Invocations(t testing.TB) [][]string {
	t.Helper()

	data, err := os.ReadFile(s.ArgvFile)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		t.Fatalf("read ssh stand-in argv record: %v", err)
	}

	var out [][]string

	for line := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}

		out = append(out, strings.Split(strings.TrimSuffix(line, "\x1f"), "\x1f"))
	}

	return out
}

// WaitPID polls path until it holds a pid, for up to bound, and fails the
// test when it never does.
func WaitPID(t testing.TB, path string, bound time.Duration) int {
	t.Helper()

	deadline := time.Now().Add(bound)

	for {
		if data, err := os.ReadFile(path); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid > 0 {
				return pid
			}
		}

		if time.Now().After(deadline) {
			t.Fatalf("no pid in %s within %s", path, bound)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// ProcessGone reports whether pid no longer runs. A zombie counts as gone,
// because a reparented descendant stays a zombie until init or launchd reaps
// it, while kill(pid, 0) still reports it alive.
func ProcessGone(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	stat := strings.TrimSpace(string(out))

	return err != nil || stat == "" || strings.HasPrefix(stat, "Z")
}

// WaitProcessGone polls ProcessGone for up to bound and reports whether pid
// went away.
func WaitProcessGone(pid int, bound time.Duration) bool {
	deadline := time.Now().Add(bound)

	for {
		if ProcessGone(pid) {
			return true
		}

		if time.Now().After(deadline) {
			return false
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// LivePIDs returns every process that is not a zombie and whose command line
// names the script, which covers each invocation and its descendant
// subshell.
func (s SSHScript) LivePIDs(t testing.TB) []int {
	t.Helper()

	pids, err := scriptPIDs(s.Program)
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}

	return pids
}

func scriptPIDs(program string) ([]int, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,stat=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}

	var pids []int

	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || strings.HasPrefix(fields[1], "Z") || !strings.Contains(line, program) {
			continue
		}

		if pid, perr := strconv.Atoi(fields[0]); perr == nil {
			pids = append(pids, pid)
		}
	}

	return pids, nil
}

// killScriptProcesses kills every live process running the script.
func killScriptProcesses(program string) {
	pids, err := scriptPIDs(program)
	if err != nil {
		return
	}

	for _, pid := range pids {
		if p, ferr := os.FindProcess(pid); ferr == nil {
			_ = p.Kill()
		}
	}
}
