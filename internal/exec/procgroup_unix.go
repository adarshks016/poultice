//go:build unix

package exec

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the child in its own process group and arranges
// for the whole group to be signalled on cancellation, so that build tools
// which fork (mvn spawning a JVM, npm spawning node) do not survive a timeout.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID signals the entire process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
