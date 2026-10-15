package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const unitName = "powerpilot.service"

// desktopID is the app ID Chrome gives the dashboard window (app.Run's own
// profile is "Default"). Naming the app-menu entry after it lets KWin and
// GNOME show the logo even when the window doesn't send an icon itself.
const desktopID = "chrome-127.0.0.1__-Default"

type paths struct{ unit, desktop, oldDesktop, icon string }

func userPaths() (paths, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return paths{}, err
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return paths{}, err
		}
		data = filepath.Join(home, ".local", "share")
	}
	return paths{
		unit:    filepath.Join(cfg, "systemd", "user", unitName),
		desktop: filepath.Join(data, "applications", desktopID+".desktop"),
		// v1.0.2 and earlier.
		oldDesktop: filepath.Join(data, "applications", "powerpilot.desktop"),
		icon:       filepath.Join(data, "icons", "hicolor", "256x256", "apps", "powerpilot.png"),
	}, nil
}

// Install writes a systemd user unit running "exe serve", plus an app-menu
// entry with the logo, then enables and (re)starts the unit.
func Install(exe string, icon []byte) error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	if err := writeFile(p.unit, []byte(fmt.Sprintf(unitTemplate, exe))); err != nil {
		return err
	}
	if err := writeMenuEntry(p, exe, icon); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unitName}, {"restart", unitName}} {
		if err := systemctl(args...); err != nil {
			return err
		}
	}
	return nil
}

// Refresh rewrites an installed service's app-menu entry and icon, which may
// have changed in an update, without touching the running service.
func Refresh(exe string, icon []byte) error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.unit); err != nil {
		return nil // not installed
	}
	return writeMenuEntry(p, exe, icon)
}

func writeMenuEntry(p paths, exe string, icon []byte) error {
	if err := writeFile(p.desktop, []byte(fmt.Sprintf(desktopTemplate, exe, desktopID))); err != nil {
		return err
	}
	if len(icon) > 0 {
		if err := writeFile(p.icon, icon); err != nil {
			return err
		}
	}
	if err := os.Remove(p.oldDesktop); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Uninstall stops and disables the unit and removes the files Install wrote.
func Uninstall() error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	systemctl("disable", "--now", unitName)
	for _, path := range []string{p.unit, p.desktop, p.oldDesktop, p.icon} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return systemctl("daemon-reload")
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

const unitTemplate = `[Unit]
Description=PowerPilot UPS monitor
After=default.target

[Service]
ExecStart="%s" serve
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`

// StartupWMClass repeats desktopID for task managers that match on it.
const desktopTemplate = `[Desktop Entry]
Type=Application
Name=PowerPilot
Comment=UPS monitoring and control
Exec="%s" app
Icon=powerpilot
Terminal=false
Categories=Utility;System;Monitor;
StartupWMClass=%s
`
