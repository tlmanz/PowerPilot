package service

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// A per-user Run entry starts the service at sign-in without admin rights.
const (
	runKey  = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	runName = "PowerPilot"

	createNoWindow  = 0x08000000
	detachedProcess = 0x00000008
	newProcessGroup = 0x00000200
)

// Install registers "exe serve" to run at sign-in and starts it now.
func Install(exe string, _ []byte) error {
	value := fmt.Sprintf(`"%s" serve`, exe)
	if err := reg("add", runKey, "/v", runName, "/t", "REG_SZ", "/d", value, "/f"); err != nil {
		return err
	}
	stopRunning()
	cmd := exec.Command(exe, "serve")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | newProcessGroup}
	return cmd.Start()
}

// Refresh has nothing to do on Windows: the Run entry points at the same exe.
func Refresh(string, []byte) error { return nil }

// Uninstall removes the Run entry and stops the running service.
func Uninstall() error {
	if err := reg("delete", runKey, "/v", runName, "/f"); err != nil && !strings.Contains(err.Error(), "unable to find") {
		return err
	}
	stopRunning()
	return nil
}

// stopRunning ends the service process recorded in the PID file.
func stopRunning() {
	data, err := os.ReadFile(PIDFile())
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid == os.Getpid() {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
	os.Remove(PIDFile())
}

func reg(args ...string) error {
	cmd := exec.Command("reg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg %s: %s", args[0], strings.TrimSpace(strings.ToLower(string(out))))
	}
	return nil
}
