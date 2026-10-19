package service

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex = kernel32.NewProc("CreateMutexW")
	procOpenMutex   = kernel32.NewProc("OpenMutexW")

	// trayMutex stays open while this process shows the tray icon.
	trayMutex uintptr
)

const trayMutexName = `Local\PowerPilotTray`

// ClaimTray makes this process the one that shows PowerPilot's tray icon
// until it exits, waiting up to wait for another to let go. It reports false
// while another process shows the icon.
func ClaimTray(wait time.Duration) bool {
	name, _ := syscall.UTF16PtrFromString(trayMutexName)
	deadline := time.Now().Add(wait)
	for {
		h, _, err := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
		if h == 0 {
			return true // can't coordinate: better two icons than none
		}
		if err != syscall.ERROR_ALREADY_EXISTS {
			trayMutex = h
			os.WriteFile(trayFile(), []byte(strconv.Itoa(os.Getpid())), 0o644)
			return true
		}
		syscall.CloseHandle(syscall.Handle(h))
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// trayPID is the process showing the tray icon, if any.
func trayPID() (int, bool) {
	const synchronize = 0x00100000
	name, _ := syscall.UTF16PtrFromString(trayMutexName)
	h, _, _ := procOpenMutex.Call(synchronize, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return 0, false // nobody holds it: the ID on file is stale
	}
	syscall.CloseHandle(syscall.Handle(h))
	data, err := os.ReadFile(trayFile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid, err == nil && pid > 0
}

// stopTray ends the process showing the tray icon, if any.
func stopTray() {
	pid, ok := trayPID()
	if !ok || pid == os.Getpid() {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
}

// StartTray starts "exe tray args...", which quits if the icon is showing already.
func StartTray(exe string, args ...string) error {
	cmd := exec.Command(exe, append([]string{"tray"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | newProcessGroup}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// RestartTray starts exe with args and exits, which hands the tray icon to
// it: Windows can't replace a running process's program. It returns only on failure.
func RestartTray(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | newProcessGroup}
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
