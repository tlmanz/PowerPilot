package update

import (
	"os"
	"os/exec"
	"syscall"
)

const (
	detachedProcess = 0x00000008
	newProcessGroup = 0x00000200
)

// restart starts exe with this process's arguments, then exits; Windows can't
// replace a running process's program. The new one waits for the port.
func restart(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | newProcessGroup}
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
