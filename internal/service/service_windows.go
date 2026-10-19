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
	runKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	runName  = "PowerPilot"
	trayName = "PowerPilot Tray"

	createNoWindow  = 0x08000000
	detachedProcess = 0x00000008
	newProcessGroup = 0x00000200
)

// Install registers "exe serve" and the tray icon to run at sign-in and
// starts them now.
func Install(exe string, _ []byte) error {
	value := fmt.Sprintf(`"%s" serve`, exe)
	if err := reg("add", runKey, "/v", runName, "/t", "REG_SZ", "/d", value, "/f"); err != nil {
		return err
	}
	stopRunning()
	cmd := exec.Command(exe, "serve")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | newProcessGroup}
	if err := cmd.Start(); err != nil {
		return err
	}
	stopTray()
	addTray(exe)
	return nil
}

// Refresh adds the tray icon's Run entry, which builds before it lacked,
// after an update, and starts the icon.
func Refresh(exe string, _ []byte) error {
	addTray(exe)
	return nil
}

// addTray runs the tray icon at sign-in and starts it now. It's an extra:
// failures leave the service as it is.
func addTray(exe string) {
	if reg("add", runKey, "/v", trayName, "/t", "REG_SZ", "/d", fmt.Sprintf(`"%s" tray`, exe), "/f") == nil {
		StartTray(exe)
	}
}

// Uninstall removes the Run entries and stops the service and the tray icon.
func Uninstall() error {
	for _, name := range []string{runName, trayName} {
		if err := reg("delete", runKey, "/v", name, "/f"); err != nil && !strings.Contains(err.Error(), "unable to find") {
			return err
		}
	}
	stopRunning()
	stopTray()
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
