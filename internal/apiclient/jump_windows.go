//go:build windows

package apiclient

import (
	"os"
	"os/exec"
	"syscall"
)

// createNoWindow is the documented Win32 CREATE_NO_WINDOW process creation
// flag, which the syscall package does not export.
const createNoWindow = 0x08000000

// jumpEntryAwaitsStderr is false on Windows: Process.Kill cannot reach a
// descendant once ssh.exe is gone, so an entry ends as soon as Wait returns.
const jumpEntryAwaitsStderr = false

// isolateJumpCommand starts the child in a new process group without a
// console window, so it neither receives the console's Ctrl+C nor shares
// pmx's console.
func isolateJumpCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow}
}

// terminateJumpChild kills the child, because Process.Signal on Windows
// supports nothing but a kill.
func terminateJumpChild(p *os.Process) {
	if p != nil {
		_ = p.Kill()
	}
}

// killJumpChild kills the child. It ends only ssh.exe itself, not an
// intermediate ssh.exe that a -J chain started.
func killJumpChild(p *os.Process) {
	if p != nil {
		_ = p.Kill()
	}
}
