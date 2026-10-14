package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const unitName = "powerpilot.service"

type paths struct{ unit, desktop, icon string }

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
		desktop: filepath.Join(data, "applications", "powerpilot.desktop"),
		icon:    filepath.Join(data, "icons", "hicolor", "256x256", "apps", "powerpilot.png"),
	}, nil
}

// Install writes a systemd user unit running "exe serve", plus an app-menu
// entry with the logo, then enables and (re)starts the unit.
func Install(exe string, icon []byte) error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	files := map[string][]byte{
		p.unit:    []byte(fmt.Sprintf(unitTemplate, exe)),
		p.desktop: []byte(fmt.Sprintf(desktopTemplate, exe)),
	}
	if len(icon) > 0 {
		files[p.icon] = icon
	}
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unitName}, {"restart", unitName}} {
		if err := systemctl(args...); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall stops and disables the unit and removes the files Install wrote.
func Uninstall() error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	systemctl("disable", "--now", unitName)
	for _, path := range []string{p.unit, p.desktop, p.icon} {
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

const desktopTemplate = `[Desktop Entry]
Type=Application
Name=PowerPilot
Comment=UPS monitoring and control
Exec="%s" app
Icon=powerpilot
Terminal=false
Categories=Utility;System;Monitor;
`
