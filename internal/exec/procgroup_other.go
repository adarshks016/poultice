//go:build !unix

package exec

import "os/exec"

// configureProcessGroup is a no-op on platforms without POSIX process groups.
// The context timeout still kills the direct child.
func configureProcessGroup(cmd *exec.Cmd) {}
