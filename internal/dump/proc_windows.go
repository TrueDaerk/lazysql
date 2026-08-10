//go:build windows

package dump

import (
	"os/exec"
	"syscall"
)

// setProcAttr gives the child its own console process group. Windows has
// no process-group kill that matches Unix's, but the flag at least keeps
// a ctrl+c in the parent's console from reaching the tool.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killGroup terminates the child. TerminateProcess — which os.Process.Kill
// calls — does not walk the tree, so a tool that spawned helpers may leave
// them behind; that is the platform's limit, not a missed case.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
