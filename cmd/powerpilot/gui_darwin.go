package main

// On macOS one process shows both the dashboard window and the menu-bar icon
// with its panel. Closing the window leaves the icon, and opening the app
// again (Finder, the Dock) reaches the running process, even one the icon's
// login item started.

import (
	"log/slog"
	"os"

	"github.com/tlmanz/PowerPilot/internal/app"
	"github.com/tlmanz/PowerPilot/internal/service"
	"github.com/tlmanz/PowerPilot/internal/system"
	"github.com/tlmanz/PowerPilot/internal/tray"
)

// claimTray claims the menu-bar icon for the window's process. It runs before
// the service is installed, as the icon's login item would claim it otherwise.
func claimTray() bool { return service.ClaimTray(0) }

// dashboard shows the window, and the menu-bar icon if claimed. Otherwise
// another process shows the icon and this one just the window, as before.
func dashboard(url string, claimed bool) {
	if !claimed {
		app.Run(url)
		return
	}
	app.RunWithTray(url, true, menuBar(url))
}

// runTray shows the menu-bar icon alone, until the dashboard is opened.
func runTray(url string) {
	if !service.ClaimTray(trayWait()) {
		return
	}
	app.RunWithTray(url, false, menuBar(url))
}

// menuBar follows the service for the menu-bar icon, which the app draws.
func menuBar(url string) app.Tray {
	var t *tray.Tray
	opt := tray.Options{
		URL:     url,
		Version: version,
		Notify:  system.Notify,
		Restart: func() error {
			if app.InUse() {
				return tray.ErrNotNow // not under the user's hands: once it's closed
			}
			exe, err := executable()
			if err != nil {
				return err
			}
			return service.RestartTray(exe, []string{"tray", "-url", url})
		},
		Log: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	return app.Tray{
		Start: func() {
			t = tray.Follow(opt, func(s tray.Status) { app.SetTray(s.Icon, s.Title, s.Tooltip) })
		},
		Summary: func() { t.Summary() },
	}
}
