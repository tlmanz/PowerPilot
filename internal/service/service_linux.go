package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const unitName = "powerpilot.service"

// desktopID is the app ID Chrome gives the dashboard window (app.Run's own
// profile is "Default"). Naming the app-menu entry after it lets KWin and
// GNOME show the logo even when the window doesn't send an icon itself.
const desktopID = "chrome-127.0.0.1__-Default"

type paths struct{ unit, desktop, oldDesktop, icon, autostart string }

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
		// The tray icon, started with the desktop session: it needs the
		// session's D-Bus, and systemd would stop it with the service.
		autostart: filepath.Join(cfg, "autostart", "powerpilot-tray.desktop"),
	}, nil
}

// Install writes a systemd user unit running "exe serve", plus an app-menu
// entry with the logo and the tray icon's autostart entry, then enables and
// (re)starts the unit and starts the tray icon.
func Install(exe string, icon []byte) error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	if err := writeFile(p.unit, []byte(fmt.Sprintf(unitTemplate, exe))); err != nil {
		return err
	}
	if err := writeEntries(p, exe, icon); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unitName}, {"restart", unitName}} {
		if err := systemctl(args...); err != nil {
			return err
		}
	}
	stopTray()
	StartTray(exe) // an extra: without a desktop session it just quits
	return nil
}

// Refresh rewrites an installed service's app-menu entry, icon and tray
// autostart entry, which may have changed in an update, without touching the
// running service. The tray icon itself restarts when it sees the update.
func Refresh(exe string, icon []byte) error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.unit); err != nil {
		return nil // not installed
	}
	return writeEntries(p, exe, icon)
}

// writeEntries writes the app-menu entry and its icon, and the tray icon's
// autostart entry.
func writeEntries(p paths, exe string, icon []byte) error {
	if err := writeFile(p.desktop, []byte(fmt.Sprintf(desktopTemplate, exe, desktopID))); err != nil {
		return err
	}
	if err := writeFile(p.autostart, []byte(fmt.Sprintf(autostartTemplate, exe))); err != nil {
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
	stopTray()
	for _, path := range []string{p.unit, p.desktop, p.oldDesktop, p.icon, p.autostart} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return systemctl("daemon-reload")
}

// StartTray starts "exe tray args..." in a session of its own, so it outlives
// this process; it quits if the icon is showing already.
func StartTray(exe string, args ...string) error {
	cmd := exec.Command(exe, append([]string{"tray"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
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

// The tray icon, at login; NoDisplay keeps it out of the app menu.
const autostartTemplate = `[Desktop Entry]
Type=Application
Name=PowerPilot tray icon
Comment=UPS status in the system tray
Exec="%s" tray
Icon=powerpilot
Terminal=false
NoDisplay=true
X-GNOME-Autostart-enabled=true
`
