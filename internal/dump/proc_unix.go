//go:build !windows

package dump

import (
	"os/exec"
	"syscall"
)

// setProcAttr puts the child in a process group of its own, so cancelling
// can kill it and everything it forked in one call. Without it, killing
// pg_dump would leave its worker processes writing into the output file.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup signals the whole process group. The negative pid is what
// makes kill(2) address the group rather than the single process; the
// group id equals the child's pid because Setpgid made the child a group
// leader.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	// SIGTERM first so a tool that cleans up after itself gets the
	// chance; the process is reaped by Wait either way, and SIGKILL
	// follows for anything that ignores the first signal.
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
