//go:build !windows

package update

import (
	"os"
	"syscall"
)

// restart replaces this process with exe. It keeps the PID, so systemd and
// launchd carry on supervising the same service.
func restart(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
