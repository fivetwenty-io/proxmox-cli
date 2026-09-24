//go:build unix

package apiclient

import (
	"os"
	"os/exec"
	"syscall"
)

// jumpEntryAwaitsStderr keeps a registry entry alive until standard error
// reaches end-of-file as well as until Wait returns. On Unix the group
// signal still reaches a descendant after its leader has been reaped, so an
// entry whose descendant holds the pipe is worth keeping.
const jumpEntryAwaitsStderr = true

// isolateJumpCommand puts the child in a session of its own. It then has no
// controlling terminal, so no hop can prompt on pmx's terminal and a
// terminal ^C reaches pmx rather than killing ssh mid-dial, and it leads its
// own process group, which is what makes a group signal valid.
func isolateJumpCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// terminateJumpChild sends SIGTERM to the child's whole process group, so a
// descendant such as a ProxyCommand hop is signalled too.
func terminateJumpChild(p *os.Process) {
	signalJumpGroup(p, syscall.SIGTERM)
}

// killJumpChild sends SIGKILL to the child's whole process group.
func killJumpChild(p *os.Process) {
	signalJumpGroup(p, syscall.SIGKILL)
}

// signalJumpGroup signals the group the child leads. The group still answers
// after its leader has been reaped as long as any member lives. A group that
// is already gone reports ESRCH, which is the outcome the caller wanted.
func signalJumpGroup(p *os.Process, sig syscall.Signal) {
	// A pid of 0 or 1 would turn -pid into pmx's own group or every process
	// the user may signal, so refuse anything that is not a real child.
	if p == nil || p.Pid <= 1 {
		return
	}

	_ = syscall.Kill(-p.Pid, sig)
}
