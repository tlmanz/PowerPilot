//go:build !windows

package service

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// trayLock stays open, and so locked, while this process shows the tray icon.
var trayLock *os.File

// ClaimTray makes this process the one that shows PowerPilot's tray icon
// until it exits, waiting up to wait for another to let go. It reports false
// while another process shows the icon.
func ClaimTray(wait time.Duration) bool {
	f, ok := claim(trayFile(), wait)
	if f != nil {
		trayLock = f
	}
	return ok
}

// claim locks path and writes this process's ID in it. A nil file with ok
// means locking isn't possible there; better two icons than none.
func claim(path string, wait time.Duration) (f *os.File, ok bool) {
	os.MkdirAll(filepath.Dir(path), 0o700)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, true
	}
	deadline := time.Now().Add(wait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	switch {
	case errors.Is(err, syscall.EWOULDBLOCK):
		f.Close()
		return nil, false
	case err != nil:
		f.Close()
		return nil, true
	}
	f.Truncate(0)
	f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return f, true
}

// trayPID is the process showing the tray icon, if any.
func trayPID(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) == nil {
		return 0, false // nobody holds it: the ID inside is stale
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid, err == nil && pid > 0
}

// stopTray ends the process showing the tray icon, if any.
func stopTray() {
	if pid, ok := trayPID(trayFile()); ok && pid != os.Getpid() {
		syscall.Kill(pid, syscall.SIGTERM)
	}
}

// RestartTray runs exe with args in place of this process, the one showing
// the tray icon; the lock goes with the old program. It returns only on failure.
func RestartTray(exe string, args []string) error {
	return syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
}
