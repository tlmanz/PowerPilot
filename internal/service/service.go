// Package service runs PowerPilot's background service at login: a LaunchAgent
// on macOS, a systemd user unit on Linux, and a Run-key entry on Windows.
package service

import (
	"os"
	"path/filepath"
)

// ConfigDir holds settings and the service's PID file.
func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "PowerPilot"), nil
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
