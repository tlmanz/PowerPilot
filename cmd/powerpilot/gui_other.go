//go:build !darwin

package main

// On Windows and Linux the tray icon is a process of its own: the dashboard
// window is the browser's.

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/tlmanz/PowerPilot/internal/app"
	"github.com/tlmanz/PowerPilot/internal/service"
	"github.com/tlmanz/PowerPilot/internal/system"
	"github.com/tlmanz/PowerPilot/internal/tray"
)

// claimTray is for macOS, where the window's process shows the icon.
func claimTray() bool { return false }

// dashboard opens the window, and the tray icon if it was quit.
func dashboard(url string, _ bool) {
	app.Run(url)
	if exe, err := executable(); err == nil {
		service.StartTray(exe, "-url", url) // it quits if the icon is showing
	}
}

func runTray(url string) {
	if err := tray.Available(); err != nil {
		fmt.Fprintln(os.Stderr, "powerpilot tray:", err)
		return
	}
	if !service.ClaimTray(trayWait()) {
		return
	}
	tray.Run(tray.Options{
		URL:     url,
		Version: version,
		Open:    func() { app.Run(url) },
		Notify:  system.Notify,
		Restart: func() error {
			exe, err := executable()
			if err != nil {
				return err
			}
			return service.RestartTray(exe, []string{"tray", "-url", url})
		},
		Log: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
}
