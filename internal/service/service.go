// Package service runs PowerPilot's background service at login: a LaunchAgent
// on macOS, a systemd user unit on Linux, and a Run-key entry on Windows.
package service

import (
	"os"
	"path/filepath"
	"runtime"
)

// ConfigDir holds settings and the service's PID file.
func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "PowerPilot"), nil
}

// LogDir holds the event and reading logs: ~/.local/state on Linux,
// %LocalAppData% on Windows (not the roaming profile), next to the settings
// on macOS.
func LogDir() (string, error) {
	switch runtime.GOOS {
	case "linux":
		state := os.Getenv("XDG_STATE_HOME")
		if state == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			state = filepath.Join(home, ".local", "state")
		}
		return filepath.Join(state, "PowerPilot", "logs"), nil
	case "windows":
		local, err := os.UserCacheDir() // %LocalAppData%
		if err != nil {
			return "", err
		}
		return filepath.Join(local, "PowerPilot", "logs"), nil
	default:
		dir, err := ConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "logs"), nil
	}
}

// WindowFile keeps the dashboard window's size between launches.
func WindowFile() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "window.json")
}

// PIDFile is where the running service records its process ID.
func PIDFile() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "service.pid")
}

// trayFile is where the process showing the tray icon records its ID.
func trayFile() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "tray.pid")
}
